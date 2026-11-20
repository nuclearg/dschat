package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"dschat/app/core/model"
)

const DefaultBaseURL = "https://api.deepseek.com"
const filesBeta = "files-api-2025-04-14"

const SystemPrompt = "该助手为DeepSeek，由深度求索公司创造。"

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 0},
	}
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("deepseek api: %d %s", e.Status, e.Message)
	}
	return "deepseek api: " + e.Message
}

type Request struct {
	Model       string
	System      string
	Messages    []any
	Thinking    bool
	Search      bool
	SafetyCheck bool
	ReadURL     bool
	Title       bool
	HasFiles    bool
	MaxTokens   int
}

type Event struct {
	Type       string // block_start | text | thinking | signature | citation | block_stop | message_delta | message_stop | usage | error
	Index      int
	Block      map[string]any
	Text       string
	Thinking   string
	Signature  string
	Citation   map[string]any
	StopReason string
	Usage      *model.Usage
	Title      string
	Err        error
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	ServerToolUse            *struct {
		WebSearchRequests int `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

func (c *Client) buildBody(req Request) ([]byte, error) {
	tools := []any{}
	if req.ReadURL || req.Search {
		tools = append(tools, map[string]any{"type": "deepseek_read_url", "name": "deepseek_read_url"})
	}
	if req.Search {
		tools = append(tools, map[string]any{"type": "deepseek_web_search", "name": "deepseek_web_search"})
	}
	thinking := "disabled"
	if req.Thinking {
		thinking = "enabled"
	}
	body := map[string]any{
		"model":    req.Model,
		"system":   req.System,
		"messages": req.Messages,
		"thinking": map[string]any{"type": thinking},
		"tools":    tools,
		"stream":   true,
	}
	// safety_check 仅在设置中开启时下发；关闭时整个字段都不出现，交回服务端默认处理。
	if req.SafetyCheck {
		body["safety_check"] = map[string]any{"level": "high"}
	}
	if req.Title {
		body["title"] = map[string]any{"enabled": true}
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	return json.Marshal(body)
}

// Stream starts a streaming Messages request. The returned channel is closed
// after message_stop or an error event.
func (c *Client) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := c.buildBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/anthropic/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.APIKey)
	if req.HasFiles {
		httpReq.Header.Set("anthropic-beta", filesBeta)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &APIError{Status: resp.StatusCode, Message: extractError(b, resp.Status)}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &APIError{Status: resp.StatusCode, Message: "unexpected content type: " + ct + " " + extractError(b, "")}
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		emit := func(ev Event) bool {
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var usage *model.Usage
		reader := newSSEReader(resp.Body)
		stopped := false
		for {
			data, err := reader.Next()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if errors.Is(err, io.EOF) && !stopped {
					emit(Event{Type: "error", Err: errors.New("stream interrupted before message_stop")})
				}
				return
			}
			if len(data) == 0 {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				continue
			}
			typ, _ := payload["type"].(string)
			switch typ {
			case "message_start":
				if msg, ok := payload["message"].(map[string]any); ok {
					if u, ok := msg["usage"].(map[string]any); ok {
						usage = parseUsage(u)
					}
				}
			case "content_block_start":
				idx := int(number(payload["index"]))
				block, _ := payload["content_block"].(map[string]any)
				if block != nil {
					if !emit(Event{Type: "block_start", Index: idx, Block: block}) {
						return
					}
				}
			case "content_block_delta":
				idx := int(number(payload["index"]))
				delta, _ := payload["delta"].(map[string]any)
				if delta == nil {
					continue
				}
				dt, _ := delta["type"].(string)
				switch dt {
				case "text_delta":
					if !emit(Event{Type: "text", Index: idx, Text: str(delta["text"])}) {
						return
					}
				case "thinking_delta":
					if !emit(Event{Type: "thinking", Index: idx, Text: str(delta["thinking"])}) {
						return
					}
				case "signature_delta":
					if !emit(Event{Type: "signature", Index: idx, Signature: str(delta["signature"])}) {
						return
					}
				case "citations_delta":
					if cit, ok := delta["citation"].(map[string]any); ok {
						if !emit(Event{Type: "citation", Index: idx, Citation: cit}) {
							return
						}
					}
				}
			case "content_block_stop":
				emit(Event{Type: "block_stop", Index: int(number(payload["index"]))})
			case "message_delta":
				delta, _ := payload["delta"].(map[string]any)
				if u, ok := payload["usage"].(map[string]any); ok {
					usage = parseUsage(u)
				}
				ev := Event{Type: "message_delta", Usage: usage}
				if delta != nil {
					ev.StopReason = str(delta["stop_reason"])
					ev.Title = str(delta["title"])
				}
				emit(ev)
			case "message_stop":
				stopped = true
				emit(Event{Type: "message_stop", Usage: usage})
				return
			case "error":
				msg := "stream error"
				if e, ok := payload["error"].(map[string]any); ok {
					if m := str(e["message"]); m != "" {
						msg = m
					}
				}
				emit(Event{Type: "error", Err: &APIError{Message: msg}})
				return
			}
		}
	}()
	return ch, nil
}

func parseUsage(m map[string]any) *model.Usage {
	u := &model.Usage{
		InputTokens:              int(number(m["input_tokens"])),
		OutputTokens:             int(number(m["output_tokens"])),
		CacheCreationInputTokens: int(number(m["cache_creation_input_tokens"])),
		CacheReadInputTokens:     int(number(m["cache_read_input_tokens"])),
	}
	if st, ok := m["server_tool_use"].(map[string]any); ok {
		u.WebSearchRequests = int(number(st["web_search_requests"]))
	}
	return u
}

// UploadFile uploads an attachment to the Anthropic-compatible Files API.
// purpose must be "" for images.
func (c *Client) UploadFile(ctx context.Context, filename, mime string, r io.Reader, purpose string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if purpose != "" {
		if err := w.WriteField("purpose", purpose); err != nil {
			return "", err
		}
	}
	// 7 天后过期，由 chatsvr 本地 6 天 TTL 提前重传；字段必须在 file 之前。
	if err := w.WriteField("expires_after[anchor]", "created_at"); err != nil {
		return "", err
	}
	if err := w.WriteField("expires_after[seconds]", "604800"); err != nil {
		return "", err
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, r); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/anthropic/v1/files", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-beta", filesBeta)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", &APIError{Status: resp.StatusCode, Message: extractError(body, resp.Status)}
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("files api returned empty id")
	}
	return out.ID, nil
}

// VerifyKey performs a cheap GET /models to validate a key.
func (c *Client) VerifyKey(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Message: extractError(body, resp.Status)}
	}
	return nil
}

func extractError(body []byte, fallback string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return fallback
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// sseReader yields one SSE data payload at a time.
type sseReader struct {
	r *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(r, 64*1024)}
}

func (s *sseReader) Next() ([]byte, error) {
	var data [][]byte
	for {
		line, err := s.r.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) == 0 {
				if len(data) > 0 {
					return bytes.Join(data, []byte("\n")), nil
				}
			} else if bytes.HasPrefix(line, []byte("data:")) {
				v := bytes.TrimPrefix(line, []byte("data:"))
				v = bytes.TrimPrefix(v, []byte(" "))
				data = append(data, v)
			}
			// event:, id:, retry: and comments are ignored; the JSON carries its type.
		}
		if err != nil {
			if len(data) > 0 {
				return bytes.Join(data, []byte("\n")), nil
			}
			return nil, err
		}
	}
}
