package chatsvr

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
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

// syncedHeaders are copied onto redirect requests explicitly. Go's default
// redirect logic drops Authorization when the host changes; sessionsvr may sit
// behind an http->https or host-normalization redirect, and the API must keep
// its Bearer token after that hop.
var syncedHeaders = []string{"Authorization", "X-Language", "X-Trace-Id", "Content-Type", "Accept"}

// newSyncHTTPClient returns the one client used for every chatsvr -> sessionsvr
// call. Go follows 301/302/303/307/308 by default, but 301/302/303 rewrite POST
// to GET and 307/308 need a replayable body. We force the original method and
// body across every redirect, including 307, so the v1 POST endpoint is never
// turned into a GET by an ingress/canonical-host redirect.
func newSyncHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: preservePostRedirect,
	}
}

// newTraceID 生成本次 chatsvr -> sessionsvr 调用的链路 ID。服务端不参与生成，
// 只负责透传和记录；重试/重定向沿用同一个 ID。
func newTraceID() string { return model.NewID() }

func preservePostRedirect(req *http.Request, via []*http.Request) error {
	// Same ten-hop ceiling as net/http's default policy; without this a
	// misconfigured redirect loop could run forever.
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	prev := via[len(via)-1]

	// 301/302/303 arrive here with Method changed to GET and Body nil.
	// 307/308 already carry GetBody, but recreate the body so all redirect
	// types have identical semantics and we do not accidentally consume the
	// redirect request's body.
	if req.Body != nil {
		_ = req.Body.Close()
	}
	req.Method = prev.Method
	req.Body = nil
	req.GetBody = prev.GetBody
	req.ContentLength = prev.ContentLength
	if prev.GetBody != nil {
		body, err := prev.GetBody()
		if err != nil {
			return err
		}
		req.Body = body
	}
	// Copy the headers the API depends on. This intentionally includes
	// Authorization even across hosts: the configured sync endpoint owns that
	// token, and a redirect to a different host is treated as the same service.
	for _, key := range syncedHeaders {
		if value := prev.Header.Get(key); value != "" {
			req.Header.Set(key, value)
		}
	}
	return nil
}

type RemoteError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (e *RemoteError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *RemoteError) IsAuthError() bool {
	return e.Code == "Unauthorized" || e.Code == "TokenExpired"
}

type RemoteSession struct {
	model.Session
	DeletedAt   int64 `json:"deletedAt,omitempty"`
	HasOpenTurn bool  `json:"hasOpenTurn"`
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
		HTTP:         newSyncHTTPClient(60 * time.Second),
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
	req.Header.Set("X-Trace-Id", newTraceID())
	resp, err := newSyncHTTPClient(60 * time.Second).Do(req)
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
	return parseLoginResult(env.Data)
}

// parseLoginResult 解析 auth.login / auth.register 的 data 节点。
//
// 自建与官方两种模式的响应形状完全一致（这是协议 v1 的硬约束），所以解析只有一份：
// 官方多回来的字段（serverId）自建模式也会给，反之亦然。
func parseLoginResult(data json.RawMessage) (*LoginResult, error) {
	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		DeviceID     string `json:"deviceId"`
		ServerID     string `json:"serverId"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
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
	traceID := newTraceID()
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
		req.Header.Set("X-Trace-Id", traceID)
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
	req.Header.Set("X-Trace-Id", newTraceID())
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

// RemoteDevice is the wire shape shared by sessionsvr and cloudsvr.
type RemoteDevice struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	LastSeen int64  `json:"lastSeen"`
	Current  bool   `json:"current"`
}

type DevicesResult struct {
	CurrentDeviceID string         `json:"currentDeviceId"`
	Devices         []RemoteDevice `json:"devices"`
}

// AccountInfo is only available on the official cloudsvr. The self-hosted
// sessionsvr returns EndpointNotFound, which callers translate into "hide the
// official-only card".
type AccountInfo struct {
	Items []AccountItem `json:"items"`
}

type AccountItem struct {
	Name   string         `json:"name"`
	Value  string         `json:"value"`
	Action *AccountAction `json:"action,omitempty"`
}

type AccountAction struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

func (s *Sync) ListDevices(ctx context.Context) (*DevicesResult, error) {
	var out DevicesResult
	if err := s.do(ctx, "devices.list", nil, &out); err != nil {
		return nil, err
	}
	if out.Devices == nil {
		out.Devices = []RemoteDevice{}
	}
	return &out, nil
}

func (s *Sync) RevokeDevice(ctx context.Context, deviceID string) error {
	return s.do(ctx, "devices.revoke", map[string]string{"deviceId": deviceID}, nil)
}

func (s *Sync) AccountInfo(ctx context.Context) (*AccountInfo, error) {
	var out AccountInfo
	if err := s.do(ctx, "account.info", nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []AccountItem{}
	}
	return &out, nil
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

// attachmentTarget is the success data returned by getUploadUrl/getDownloadUrl.
// The first POST only prepares a URL; the client then sends data directly to
// OSS (official) or to the server's own upload endpoint (self-hosted), and
// finally calls finishUpload for metadata.
type redirectInfo struct {
	URL           string            `json:"url"`
	Method        string            `json:"method"`
	Headers       map[string]string `json:"headers"`
	Multipart     bool              `json:"multipart"`
	ExpiresAt     int64             `json:"expiresAt"`
	ObjectKey     string            `json:"objectKey"`
	AlreadyExists bool              `json:"alreadyExists"`
	Name          string            `json:"name"`
	MIME          string            `json:"mime"`
	Body          map[string]string `json:"body"`
}

func (s *Sync) resolveURL(raw string) (string, error) {
	ref, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	base, err := url.Parse(strings.TrimRight(s.BaseURL, "/") + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

func (s *Sync) prepare(ctx context.Context, action string, payload any) (*redirectInfo, error) {
	var info redirectInfo
	if err := s.do(ctx, action, payload, &info); err != nil {
		return nil, err
	}
	if info.URL == "" && !info.AlreadyExists {
		return nil, fmt.Errorf("%s returned empty URL", action)
	}
	return &info, nil
}

func (s *Sync) directRequest(ctx context.Context, info *redirectInfo, body io.Reader, contentType string) (*http.Response, error) {
	target, err := s.resolveURL(info.URL)
	if err != nil {
		return nil, err
	}
	method := info.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range info.Headers {
		req.Header.Set(key, value)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Self-hosted upload/download endpoints still authenticate with Bearer.
	// OSS presigned URLs must not receive that header.
	if info.Multipart || sameOrigin(s.BaseURL, target) {
		req.Header.Set("Authorization", "Bearer "+s.currentAccess())
		// OSS presigned URLs must not receive extra signed headers; only the
		// self-hosted data plane gets the trace header.
		req.Header.Set("X-Trace-Id", newTraceID())
	}
	req.Header.Set("X-Language", s.language())
	return newSyncHTTPClient(60 * time.Second).Do(req)
}

func (s *Sync) finishUpload(ctx context.Context, sessionID, id, name string, info *redirectInfo, size int64, sum string) error {
	if info.AlreadyExists {
		return nil
	}
	var ignored map[string]any
	return s.do(ctx, "attachments.finishUpload", map[string]any{
		"id": id, "sessionId": sessionID, "objectKey": info.ObjectKey,
		"name": name, "size": size, "sha256": sum,
	}, &ignored)
}

func (s *Sync) PutAttachment(ctx context.Context, sessionID string, a model.Attachment, r io.Reader, size int64) error {
	// The content hash is part of the object key, so it must be known before
	// asking for an upload URL. Attachments are already bounded by the caller.
	data, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("attachment size changed while uploading")
	}
	sha := sha256.Sum256(data)
	md5sum := md5.Sum(data)
	shaHex := hex.EncodeToString(sha[:])
	md5Hex := hex.EncodeToString(md5sum[:])

	prep, err := s.prepare(ctx, "attachments.getUploadUrl", map[string]any{
		"id": a.ID, "sessionId": sessionID, "name": a.Name, "mime": a.MIME,
		"size": size, "md5": md5Hex, "sha256": shaHex,
	})
	if err != nil {
		return err
	}
	if prep.AlreadyExists {
		return nil
	}

	var body bytes.Buffer
	var contentType string
	if prep.Multipart {
		w := multipart.NewWriter(&body)
		_ = w.WriteField("id", a.ID)
		_ = w.WriteField("sessionId", sessionID)
		_ = w.WriteField("name", a.Name)
		_ = w.WriteField("mime", a.MIME)
		part, err := w.CreateFormFile("file", a.Name)
		if err != nil {
			return err
		}
		if _, err := part.Write(data); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		contentType = w.FormDataContentType()
	} else {
		body.Write(data)
	}

	resp, err := s.directRequest(ctx, prep, bytes.NewReader(body.Bytes()), contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resultError(resp.StatusCode, errBody)
	}
	return s.finishUpload(ctx, sessionID, a.ID, a.Name, prep, size, shaHex)
}

func (s *Sync) GetAttachment(ctx context.Context, id string) (io.ReadCloser, string, string, error) {
	info, err := s.prepare(ctx, "attachments.getDownloadUrl", map[string]string{"attachmentId": id})
	if err != nil {
		return nil, "", "", err
	}
	var body io.Reader
	contentType := ""
	if len(info.Body) > 0 {
		payload, err := json.Marshal(info.Body)
		if err != nil {
			return nil, "", "", err
		}
		body = bytes.NewReader(payload)
		contentType = "application/json"
	}
	resp, err := s.directRequest(ctx, info, body, contentType)
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
	name := info.Name
	if decoded, err := url.QueryUnescape(resp.Header.Get("X-Attachment-Name")); err == nil && decoded != "" {
		name = decoded
	}
	mimeType := info.MIME
	if decoded, err := url.QueryUnescape(resp.Header.Get("X-Attachment-Mime")); err == nil && decoded != "" {
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

func (s *Sync) DeleteSessions(ctx context.Context, sessionIDs []string) error {
	return s.do(ctx, "sessions.deleteAll", map[string]any{"sessionIds": sessionIDs}, nil)
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
	req.Header.Set("X-Trace-Id", newTraceID())
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
