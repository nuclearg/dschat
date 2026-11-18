package chatsvr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dschat/app/core/model"
)

type RemoteError struct {
	Status  int
	Code    string
	Message string
	TurnID  string
}

func (e *RemoteError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *RemoteError) IsOpenTurn() bool { return e.Code == "OPEN_TURN" }

type RemoteSession struct {
	model.Session
	HasOpenTurn bool `json:"hasOpenTurn"`
}

type RemoteEvent struct {
	Type          string `json:"type"`
	Protocol      int    `json:"protocol"`
	ServerID      string `json:"serverId"`
	SessionID     string `json:"sessionId"`
	OwnerDeviceID string `json:"ownerDeviceId"`
	TurnID        string `json:"turnId"`
	ParentTurnID  string `json:"parentTurnId"`
}

type Sync struct {
	BaseURL  string
	Token    string
	DeviceID string
	HTTP     *http.Client

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewSync(baseURL, token, deviceID string) *Sync {
	return &Sync{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		DeviceID: deviceID,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *Sync) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

func Pair(ctx context.Context, baseURL, code, name, platform, localDeviceID string) (token, deviceID, serverID string, err error) {
	body, _ := json.Marshal(map[string]string{"code": code, "name": name, "platform": platform, "deviceId": localDeviceID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/pair", bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", "", "", parseRemoteError(resp.StatusCode, data)
	}
	var out struct {
		Token    string `json:"token"`
		DeviceID string `json:"deviceId"`
		ServerID string `json:"serverId"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", "", err
	}
	return out.Token, out.DeviceID, out.ServerID, nil
}

func (s *Sync) do(ctx context.Context, method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		return parseRemoteError(resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func parseRemoteError(status int, body []byte) error {
	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		TurnID string `json:"turnId"`
	}
	if json.Unmarshal(body, &wrapper) == nil && wrapper.Error.Code != "" {
		return &RemoteError{Status: status, Code: wrapper.Error.Code, Message: wrapper.Error.Message, TurnID: wrapper.TurnID}
	}
	return &RemoteError{Status: status, Code: "HTTP_ERROR", Message: strings.TrimSpace(string(body))}
}

func (s *Sync) CreateSession(ctx context.Context, id, title string) (*model.Session, error) {
	var out model.Session
	err := s.do(ctx, http.MethodPost, "/v1/sessions", map[string]string{"id": id, "title": title}, &out)
	if err != nil {
		if re, ok := err.(*RemoteError); ok && re.Status == 409 {
			return s.GetSessionMeta(ctx, id)
		}
		return nil, err
	}
	return &out, nil
}

func (s *Sync) GetSessionMeta(ctx context.Context, id string) (*model.Session, error) {
	_, sess, err := s.GetSession(ctx, id)
	return sess, err
}

func (s *Sync) GetSession(ctx context.Context, id string) ([]model.Turn, *model.Session, error) {
	var out struct {
		Session model.Session `json:"session"`
		Turns   []model.Turn  `json:"turns"`
	}
	if err := s.do(ctx, http.MethodGet, "/v1/sessions/"+id, nil, &out); err != nil {
		return nil, nil, err
	}
	return out.Turns, &out.Session, nil
}

func (s *Sync) ListSessions(ctx context.Context) ([]RemoteSession, error) {
	var out struct {
		Sessions []RemoteSession `json:"sessions"`
	}
	if err := s.do(ctx, http.MethodGet, "/v1/sessions", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (s *Sync) AppendTurn(ctx context.Context, sessionID string, t model.Turn) error {
	return s.do(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/turns", map[string]any{"turn": t}, nil)
}

func (s *Sync) AppendAnswer(ctx context.Context, sessionID, turnID string, a model.Answer) error {
	return s.do(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/turns/"+turnID+"/answer", map[string]any{"answer": a}, nil)
}

func (s *Sync) Claim(ctx context.Context, sessionID string, force bool) (*model.Session, error) {
	var out model.Session
	if err := s.do(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/claim", map[string]any{"force": force}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Sync) Rename(ctx context.Context, sessionID, title string) error {
	return s.do(ctx, http.MethodPatch, "/v1/sessions/"+sessionID, map[string]string{"title": title}, nil)
}

func (s *Sync) DeleteSession(ctx context.Context, sessionID string) error {
	return s.do(ctx, http.MethodDelete, "/v1/sessions/"+sessionID, nil, nil)
}

// Run consumes the notification stream until ctx is canceled. It reconnects
// with exponential backoff; every reconnect triggers a full resync callback.
func (s *Sync) Run(ctx context.Context, onEvent func(RemoteEvent), onReconnect func()) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		hello, err := s.stream(ctx, onEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("sessionsvr stream: %v", err)
		}
		if hello && onReconnect != nil {
			onReconnect()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *Sync) stream(ctx context.Context, onEvent func(RemoteEvent)) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/v1/stream", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, parseRemoteError(resp.StatusCode, data)
	}
	hello := false
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var data [][]byte
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if len(data) > 0 {
					var ev RemoteEvent
					if json.Unmarshal(bytes.Join(data, []byte("\n")), &ev) == nil {
						if ev.Type == "hello" {
							hello = true
						}
						onEvent(ev)
					}
					data = nil
				}
			case bytes.HasPrefix(line, []byte("data:")):
				data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return hello, ctx.Err()
			}
			if err == io.EOF {
				return hello, io.ErrUnexpectedEOF
			}
			return hello, err
		}
	}
}
