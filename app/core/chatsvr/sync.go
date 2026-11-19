package chatsvr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"dschat/app/core/model"
)

type RemoteError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
	TurnID  string         `json:"turnId,omitempty"`
}

func (e *RemoteError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *RemoteError) IsOpenTurn() bool { return e.Code == "OpenTurn" }

func (e *RemoteError) IsAuthError() bool {
	return e.Code == "Unauthorized" || e.Code == "TokenExpired"
}

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
	BaseURL         string
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
	DeviceID        string
	Language        string
	HTTP            *http.Client

	// OnTokens persists rotated credentials; called with access/refresh and
	// access expiry in milliseconds.
	OnTokens func(access, refresh string, accessExpiresAt int64)
	// OnError receives sync/stream errors so the app can surface auth blocks.
	OnError func(error)

	authMu sync.Mutex
	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewSync(baseURL, access, refresh string, accessExpiresAt int64, deviceID, language string) *Sync {
	s := &Sync{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		AccessToken:  access,
		RefreshToken: refresh,
		DeviceID:     deviceID,
		Language:     language,
		HTTP:         &http.Client{Timeout: 60 * time.Second},
	}
	if accessExpiresAt > 0 {
		s.AccessExpiresAt = time.UnixMilli(accessExpiresAt)
	}
	return s
}

func (s *Sync) SetLanguage(language string) {
	s.authMu.Lock()
	s.Language = language
	s.authMu.Unlock()
}

func (s *Sync) language() string {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.Language == "" {
		return "zh-CN"
	}
	return s.Language
}

func (s *Sync) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

type LoginResult struct {
	AccessToken     string
	RefreshToken    string
	ExpiresIn       int64
	DeviceID        string
	ServerID        string
	AccessExpiresAt int64
}

type resultEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	ErrCode string          `json:"errCode"`
}

func resultError(status int, body []byte) *RemoteError {
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err == nil && !env.Success && env.ErrCode != "" {
		var data map[string]any
		_ = json.Unmarshal(env.Data, &data)
		return &RemoteError{Status: status, Code: env.ErrCode, Message: env.Error, Data: data}
	}
	return &RemoteError{Status: status, Code: "HttpError", Message: strings.TrimSpace(string(body))}
}

func Login(ctx context.Context, baseURL, password, name, platform, localDeviceID, language string) (*LoginResult, error) {
	body, _ := json.Marshal(map[string]string{
		"password": password, "name": name, "platform": platform, "deviceId": localDeviceID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/auth.login", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if language == "" {
		language = "zh-CN"
	}
	req.Header.Set("X-Language", language)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, resultError(resp.StatusCode, data)
	}
	var env resultEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, resultError(resp.StatusCode, data)
	}
	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		DeviceID     string `json:"deviceId"`
		ServerID     string `json:"serverId"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return nil, err
	}
	res := &LoginResult{
		AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn,
		DeviceID: out.DeviceID, ServerID: out.ServerID,
	}
	if out.ExpiresIn > 0 {
		res.AccessExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second).UnixMilli()
	}
	return res, nil
}

func (s *Sync) do(ctx context.Context, action string, in any, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	authAction := strings.HasPrefix(action, "auth.")
	for attempt := 0; attempt < 2; attempt++ {
		if !authAction {
			if err := s.ensureAccess(ctx); err != nil {
				return err
			}
		}
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/v1/"+action, body)
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Language", s.language())
		if !authAction {
			req.Header.Set("Authorization", "Bearer "+s.currentAccess())
		}
		resp, err := s.HTTP.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return resultError(resp.StatusCode, data)
		}
		var env resultEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return &RemoteError{Status: resp.StatusCode, Code: "HttpError", Message: "invalid response"}
		}
		if !env.Success {
			re := resultError(resp.StatusCode, data)
			if attempt == 0 && !authAction && re.IsAuthError() && s.hasRefresh() {
				if err := s.Refresh(ctx); err != nil {
					return err
				}
				continue
			}
			return re
		}
		if out != nil && len(env.Data) > 0 {
			return json.Unmarshal(env.Data, out)
		}
		return nil
	}
	return &RemoteError{Status: 200, Code: "Unauthorized", Message: "token refresh failed"}
}

func (s *Sync) currentAccess() string {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.AccessToken
}

func (s *Sync) hasRefresh() bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.RefreshToken != ""
}

func (s *Sync) ensureAccess(ctx context.Context) error {
	s.authMu.Lock()
	access := s.AccessToken
	exp := s.AccessExpiresAt
	refresh := s.RefreshToken
	s.authMu.Unlock()
	if access != "" && (exp.IsZero() || time.Until(exp) > 60*time.Second) {
		return nil
	}
	if refresh == "" {
		return nil
	}
	return s.Refresh(ctx)
}

// Refresh rotates the refresh token and stores the new pair.
func (s *Sync) Refresh(ctx context.Context) error {
	s.authMu.Lock()
	refresh := s.RefreshToken
	s.authMu.Unlock()
	if refresh == "" {
		return &RemoteError{Status: 200, Code: "Unauthorized", Message: "missing refresh token"}
	}
	body, _ := json.Marshal(map[string]string{"refreshToken": refresh})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/v1/auth.refresh", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Language", s.language())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return resultError(resp.StatusCode, data)
	}
	var env resultEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if !env.Success {
		return resultError(resp.StatusCode, data)
	}
	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return err
	}
	expiresAt := time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	s.authMu.Lock()
	s.AccessToken = out.AccessToken
	s.RefreshToken = out.RefreshToken
	s.AccessExpiresAt = expiresAt
	cb := s.OnTokens
	s.authMu.Unlock()
	if cb != nil {
		cb(out.AccessToken, out.RefreshToken, expiresAt.UnixMilli())
	}
	return nil
}

func (s *Sync) CreateSession(ctx context.Context, id, title string) (*model.Session, error) {
	var out struct {
		Session model.Session `json:"session"`
	}
	if err := s.do(ctx, "sessions.create", map[string]string{"id": id, "title": title}, &out); err != nil {
		return nil, err
	}
	return &out.Session, nil
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
	if err := s.do(ctx, "sessions.get", map[string]string{"sessionId": id}, &out); err != nil {
		return nil, nil, err
	}
	return out.Turns, &out.Session, nil
}

func (s *Sync) ListSessions(ctx context.Context) ([]RemoteSession, error) {
	var out struct {
		Sessions []RemoteSession `json:"sessions"`
	}
	if err := s.do(ctx, "sessions.list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (s *Sync) AppendTurn(ctx context.Context, sessionID string, t model.Turn) error {
	return s.do(ctx, "sessions.turn.append", map[string]any{"sessionId": sessionID, "turn": t}, nil)
}

func (s *Sync) AppendAnswer(ctx context.Context, sessionID, turnID string, a model.Answer) error {
	return s.do(ctx, "sessions.turn.answer", map[string]any{"sessionId": sessionID, "turnId": turnID, "answer": a}, nil)
}

func (s *Sync) HasAttachment(ctx context.Context, id string) (bool, error) {
	var out struct {
		Exists bool `json:"exists"`
	}
	if err := s.do(ctx, "attachments.head", map[string]string{"attachmentId": id}, &out); err != nil {
		return false, err
	}
	return out.Exists, nil
}

func (s *Sync) PutAttachment(ctx context.Context, sessionID string, a model.Attachment, r io.Reader, size int64) error {
	if err := s.ensureAccess(ctx); err != nil {
		return err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("id", a.ID)
	_ = w.WriteField("sessionId", sessionID)
	_ = w.WriteField("name", a.Name)
	_ = w.WriteField("mime", a.MIME)
	part, err := w.CreateFormFile("file", a.Name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/v1/attachments.put", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Language", s.language())
	req.Header.Set("Authorization", "Bearer "+s.currentAccess())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resultErrorOrNil(resp.StatusCode, data)
}

func resultErrorOrNil(status int, data []byte) error {
	var env resultEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return &RemoteError{Status: status, Code: "HttpError", Message: string(data)}
	}
	if !env.Success {
		return resultError(status, data)
	}
	return nil
}

func (s *Sync) GetAttachment(ctx context.Context, id string) (io.ReadCloser, string, string, error) {
	if err := s.ensureAccess(ctx); err != nil {
		return nil, "", "", err
	}
	body, _ := json.Marshal(map[string]string{"attachmentId": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/v1/attachments.get", bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Language", s.language())
	req.Header.Set("Authorization", "Bearer "+s.currentAccess())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		return nil, "", "", resultError(resp.StatusCode, data)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		return nil, "", "", resultError(resp.StatusCode, data)
	}
	name := resp.Header.Get("X-Attachment-Name")
	if decoded, err := urlQueryUnescape(name); err == nil {
		name = decoded
	}
	mimeType := resp.Header.Get("X-Attachment-Mime")
	if decoded, err := urlQueryUnescape(mimeType); err == nil {
		mimeType = decoded
	}
	return resp.Body, name, mimeType, nil
}

func (s *Sync) Claim(ctx context.Context, sessionID string, force bool) (*model.Session, error) {
	var out struct {
		Session model.Session `json:"session"`
	}
	if err := s.do(ctx, "sessions.claim", map[string]any{"sessionId": sessionID, "force": force}, &out); err != nil {
		return nil, err
	}
	return &out.Session, nil
}

func (s *Sync) Rename(ctx context.Context, sessionID, title string) error {
	return s.do(ctx, "sessions.rename", map[string]string{"sessionId": sessionID, "title": title}, nil)
}

func (s *Sync) DeleteSession(ctx context.Context, sessionID string) error {
	return s.do(ctx, "sessions.delete", map[string]string{"sessionId": sessionID}, nil)
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
		// Reconcile before opening the stream: deletion/recovery events may
		// have been missed while offline, and the stream has no replay.
		if onReconnect != nil {
			onReconnect()
		}
		_, err := s.stream(ctx, onEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if s.OnError != nil {
				s.OnError(err)
			}
			log.Printf("sessionsvr stream: %v", err)
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
	if err := s.ensureAccess(ctx); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/v1/stream", bytes.NewReader([]byte("{}")))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.currentAccess())
	req.Header.Set("X-Language", s.language())
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, resultError(resp.StatusCode, data)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		re := resultError(resp.StatusCode, data)
		if re.IsAuthError() && s.hasRefresh() {
			_ = s.Refresh(ctx)
		}
		return false, re
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
					var env struct {
						Success bool        `json:"success"`
						Data    RemoteEvent `json:"data"`
						Error   string      `json:"error"`
						ErrCode string      `json:"errCode"`
					}
					if json.Unmarshal(bytes.Join(data, []byte("\n")), &env) == nil {
						if !env.Success {
							re := &RemoteError{Status: 200, Code: env.ErrCode, Message: env.Error}
							if re.IsAuthError() && s.hasRefresh() {
								_ = s.Refresh(ctx)
							}
							return hello, re
						}
						if env.Data.Type == "hello" {
							hello = true
						}
						onEvent(env.Data)
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

func urlQueryUnescape(s string) (string, error) {
	return url.QueryUnescape(s)
}
