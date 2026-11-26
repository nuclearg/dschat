package chatsvr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"dschat/app/core/apierr"
)

const officialShareEndpoint = "https://chat.deepseek.com/api/v0/share/content?share_id="

type OfficialShareImportRequest struct {
	URL string `json:"url"`
}

type officialShareEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BizCode int    `json:"biz_code"`
		BizMsg  string `json:"biz_msg"`
		BizData struct {
			Title    string `json:"title"`
			Messages []any  `json:"messages"`
		} `json:"biz_data"`
	} `json:"data"`
}

// ImportOfficialShare reads a public DeepSeek share link and materializes it
// as a local session. Sharing is deliberately the only official-web entry
// point: it needs no account credentials, cookie export or TLS interception.
func (a *App) ImportOfficialShare(ctx context.Context, rawURL string) (*ImportSessionResult, error) {
	shareID, err := officialShareID(rawURL)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, officialShareEndpoint+url.QueryEscape(shareID), nil)
	if err != nil {
		return nil, err
	}
	// These headers are required by the public share endpoint's WAF. They do
	// not carry the user's account, token or cookie.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://chat.deepseek.com/share/"+shareID)
	req.Header.Set("Origin", "https://chat.deepseek.com")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, apierr.New(502, "OFFICIAL_SHARE_UNAVAILABLE", "无法读取 DeepSeek 分享链接")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.New(502, "OFFICIAL_SHARE_UNAVAILABLE", fmt.Sprintf("DeepSeek 分享链接返回 HTTP %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, apierr.New(502, "OFFICIAL_SHARE_UNAVAILABLE", "读取 DeepSeek 分享内容失败")
	}
	var envelope officialShareEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, apierr.New(502, "OFFICIAL_SHARE_INVALID", "DeepSeek 分享内容格式无法识别")
	}
	if envelope.Code != 0 || envelope.Data.BizCode != 0 {
		msg := envelope.Data.BizMsg
		if msg == "" {
			msg = envelope.Msg
		}
		if msg == "" {
			msg = "分享不存在或已失效"
		}
		return nil, apierr.New(404, "OFFICIAL_SHARE_NOT_FOUND", msg)
	}
	reqPayload, warnings, err := buildOfficialShareImport(envelope.Data.BizData.Title, envelope.Data.BizData.Messages)
	if err != nil {
		return nil, err
	}
	result, err := a.importSession(*reqPayload, false)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		result.Warning = strings.Join(warnings, "；")
	}
	return result, nil
}

func officialShareID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apierr.New(400, "OFFICIAL_SHARE_URL_REQUIRED", "请粘贴 DeepSeek 分享链接")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", apierr.New(400, "OFFICIAL_SHARE_URL_INVALID", "分享链接格式不正确")
	}
	if u.Host != "chat.deepseek.com" && u.Host != "www.chat.deepseek.com" {
		return "", apierr.New(400, "OFFICIAL_SHARE_URL_INVALID", "只支持 chat.deepseek.com 的分享链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "share" || parts[1] == "" {
		return "", apierr.New(400, "OFFICIAL_SHARE_URL_INVALID", "不是有效的 DeepSeek 分享链接")
	}
	id := parts[1]
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", apierr.New(400, "OFFICIAL_SHARE_URL_INVALID", "分享 ID 格式不正确")
		}
	}
	return id, nil
}

func buildOfficialShareImport(title string, rawMessages []any) (*ImportSessionRequest, []string, error) {
	req := &ImportSessionRequest{Title: strings.TrimSpace(title)}
	warnings := []string{}
	selected := ""
	for _, raw := range rawMessages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := anyString(m["message_id"])
		if id == "" {
			continue
		}
		role := strings.ToLower(anyString(m["role"]))
		parent := anyString(m["parent_id"])
		var parentPtr *string
		if parent != "" {
			parentPtr = &parent
		}
		created := int64(anyFloat(m["inserted_at"]) * 1000)
		if created <= 0 {
			created = time.Now().UnixMilli()
		}
		fragments, _ := m["fragments"].([]any)
		msg := ImportMessage{
			ID:        id,
			ParentID:  parentPtr,
			Role:      role,
			CreatedAt: created,
			Thinking:  anyBool(m["thinking_enabled"]),
			Search:    anyBool(m["search_enabled"]),
			Status:    anyString(m["status"]),
		}
		if role == "user" {
			msg.Content = firstFragmentText(fragments, "REQUEST")
		} else {
			msg.Blocks = officialBlocks(fragments)
			msg.Content = ""
		}
		msg.Files = officialFiles(fragments)
		if len(msg.Files) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d 个附件需要重新上传", len(msg.Files)))
		}
		if role == "assistant" {
			selected = id
		}
		req.Messages = append(req.Messages, msg)
	}
	if len(req.Messages) == 0 {
		return nil, nil, apierr.New(404, "OFFICIAL_SHARE_EMPTY", "分享里没有可导入的消息")
	}
	// The share endpoint returns the conversation in display order. The last
	// assistant is the current path; selecting it preserves that path.
	if selected != "" {
		last := selected
		for i := range req.Messages {
			if req.Messages[i].Role == "user" {
				for j := range req.Messages {
					if req.Messages[j].Role == "assistant" && req.Messages[j].ParentID != nil && *req.Messages[j].ParentID == req.Messages[i].ID && req.Messages[j].ID == last {
						req.Messages[i].SelectedChildID = &last
					}
				}
			}
		}
	}
	if req.Title == "" || strings.EqualFold(req.Title, "Shared Conversation") {
		for _, m := range req.Messages {
			if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
				req.Title = truncateRunes(strings.TrimSpace(m.Content), 32)
				break
			}
		}
	}
	if req.Title == "" {
		req.Title = "来自分享的对话"
	}
	return req, warnings, nil
}

func officialBlocks(fragments []any) []map[string]any {
	blocks := []map[string]any{}
	for _, raw := range fragments {
		f, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := strings.ToUpper(anyString(f["type"]))
		text := anyString(f["content"])
		switch typ {
		case "RESPONSE":
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
		case "TEMPLATE_RESPONSE":
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text, "isTemplate": true})
			}
		case "THINK":
			if text != "" {
				block := map[string]any{"type": "thinking", "thinking": text}
				if secs := anyFloat(f["elapsed_secs"]); secs > 0 {
					block["elapsedSeconds"] = secs
				}
				blocks = append(blocks, block)
			}
		}
	}
	return blocks
}

func officialFiles(fragments []any) []ImportFile {
	out := []ImportFile{}
	for _, raw := range fragments {
		f, ok := raw.(map[string]any)
		if !ok || strings.ToUpper(anyString(f["type"])) != "FILE" {
			continue
		}
		files, _ := f["files"].([]any)
		for _, rawFile := range files {
			file, ok := rawFile.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, ImportFile{
				ID: anyString(file["file_id"]), Name: anyString(file["name"]),
				MIME: anyString(file["mime"]), Size: int64(anyFloat(file["size"])),
				IsImage: strings.HasPrefix(anyString(file["mime"]), "image/"),
			})
		}
	}
	return out
}

func firstFragmentText(fragments []any, kind string) string {
	for _, raw := range fragments {
		f, ok := raw.(map[string]any)
		if ok && strings.ToUpper(anyString(f["type"])) == kind {
			return anyString(f["content"])
		}
	}
	return ""
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	}
	return ""
}

func anyFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case int:
		return float64(x)
	}
	return 0
}

func anyBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
