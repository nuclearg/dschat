package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/httpx"
	"dschat/app/core/model"
)

type Config struct {
	Addr     string
	Data     string
	Password string
}

type Server struct {
	cfg      Config
	store    *Store
	hub      *hub
	serverID string
	password string
}

func New(cfg Config) (*Server, error) {
	if cfg.Data == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	st, err := OpenStore(cfg.Data)
	if err != nil {
		return nil, err
	}
	sid, err := st.ServerID()
	if err != nil {
		st.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, store: st, hub: newHub(), serverID: sid, password: cfg.Password}
	// Password changes invalidate all existing sessions (including refresh tokens).
	fingerprint := hashToken(s.password)
	var previous string
	_ = st.db.QueryRow(`select value from meta where key='password_fingerprint'`).Scan(&previous)
	if previous != fingerprint {
		_ = st.ClearAuthSessions()
		_, _ = st.db.Exec(`insert or replace into meta(key,value) values('password_fingerprint',?)`, fingerprint)
	}
	s.cleanupExpired()
	return s, nil
}

func (s *Server) Close() error { return s.store.Close() }

func (s *Server) cleanupExpired() {
	n, err := s.store.CleanupExpired(time.Now().Add(-DeletedSessionTTL).UnixMilli())
	if err != nil {
		log.Printf("sessionsvr cleanup: %v", err)
	} else if n > 0 {
		log.Printf("sessionsvr cleanup: removed %d expired session(s)", n)
	}
}

func (s *Server) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpired()
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/meta.get", s.handleMeta)
	mux.HandleFunc("POST /v1/auth.login", s.handleLogin)
	mux.HandleFunc("POST /v1/auth.refresh", s.handleRefresh)
	mux.HandleFunc("POST /v1/auth.logout", s.handleLogout)
	mux.HandleFunc("POST /v1/stream", s.auth(s.handleStream))

	mux.HandleFunc("POST /v1/sessions.create", s.auth(s.handleCreateSession))
	mux.HandleFunc("POST /v1/sessions.list", s.auth(s.handleListSessions))
	mux.HandleFunc("POST /v1/sessions.get", s.auth(s.handleGetSession))
	mux.HandleFunc("POST /v1/sessions.rename", s.auth(s.handleRename))
	mux.HandleFunc("POST /v1/sessions.delete", s.auth(s.handleDelete))
	mux.HandleFunc("POST /v1/sessions.turn.append", s.auth(s.handleAppendTurn))
	mux.HandleFunc("POST /v1/sessions.turn.answer", s.auth(s.handleAppendAnswer))
	mux.HandleFunc("POST /v1/sessions.claim", s.auth(s.handleClaim))
	mux.HandleFunc("POST /v1/attachments.put", s.auth(s.handlePutAttachment))
	mux.HandleFunc("POST /v1/attachments.head", s.auth(s.handleHeadAttachment))
	mux.HandleFunc("POST /v1/attachments.get", s.auth(s.handleGetAttachment))
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeFail(w, langOf(r), "MethodNotAllowed", localize(langOf(r), "METHOD_NOT_ALLOWED", "only POST is supported"), nil)
			return
		}
		writeFail(w, langOf(r), "EndpointNotFound", localize(langOf(r), "ENDPOINT_NOT_FOUND", "endpoint not found"), nil)
	})
	return mux
}

type ctxKey string

const deviceKey ctxKey = "device"

func deviceFrom(r *http.Request) *Device {
	d, _ := r.Context().Value(deviceKey).(*Device)
	return d
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			writeFail(w, langOf(r), "Unauthorized", localize(langOf(r), "UNAUTHORIZED", "missing bearer token"), nil)
			return
		}
		d, err := s.store.DeviceByAccess(token)
		if err != nil {
			writeAppError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), deviceKey, d)
		next(w, r.WithContext(ctx))
	}
}

// ---- Result envelope -------------------------------------------------------

type result struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	ErrCode string `json:"errCode,omitempty"`
}

func writeOK(w http.ResponseWriter, data any) {
	httpx.WriteJSON(w, 200, result{Success: true, Data: data})
}

func writeFail(w http.ResponseWriter, lang, code, message string, data map[string]any) {
	var payload any
	if len(data) > 0 {
		payload = data
	}
	httpx.WriteJSON(w, 200, result{Success: false, Data: payload, Error: message, ErrCode: code})
}

func writeAppError(w http.ResponseWriter, r *http.Request, err error) {
	lang := langOf(r)
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		writeFail(w, lang, "Unknown", localize(lang, "UNKNOWN", err.Error()), nil)
		return
	}
	code := publicCode(ae.Code)
	message := localize(lang, ae.Code, ae.Message)
	writeFail(w, lang, code, message, ae.Extra)
}

func publicCode(code string) string {
	switch code {
	case "BAD_REQUEST":
		return "IllegalArguments"
	case "INVALID_PASSWORD":
		return "InvalidPassword"
	case "UNAUTHORIZED":
		return "Unauthorized"
	case "SESSION_EXISTS":
		return "SessionExists"
	case "SESSION_NOT_FOUND":
		return "SessionNotFound"
	case "NOT_OWNER":
		return "NotOwner"
	case "PARENT_OPEN":
		return "ParentOpen"
	case "TURN_EXISTS":
		return "TurnExists"
	case "TURN_NOT_FOUND":
		return "TurnNotFound"
	case "ANSWER_EXISTS":
		return "AnswerExists"
	case "OPEN_TURN":
		return "OpenTurn"
	case "INVALID_TURN":
		return "InvalidTurn"
	case "INVALID_ATTACHMENT":
		return "InvalidAttachment"
	case "ATTACHMENT_NOT_FOUND":
		return "AttachmentNotFound"
	case "FILE_TOO_LARGE":
		return "FileTooLarge"
	case "STREAM_UNSUPPORTED":
		return "StreamUnsupported"
	case "INTERNAL":
		return "Unknown"
	default:
		if code == "" {
			return "Unknown"
		}
		return code
	}
}

func langOf(r *http.Request) string {
	lang := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Language")))
	if strings.HasPrefix(lang, "zh") {
		return "zh"
	}
	return "en"
}

func localize(lang, code, fallback string) string {
	if lang != "zh" {
		return fallback
	}
	switch code {
	case "INVALID_PASSWORD":
		return "密码错误"
	case "UNAUTHORIZED":
		return "登录已失效，请重新登录"
	case "NOT_OWNER":
		return "该会话由其他设备持有"
	case "SESSION_NOT_FOUND":
		return "会话不存在"
	case "SESSION_EXISTS":
		return "会话已存在"
	case "PARENT_OPEN":
		return "上一轮尚未结束"
	case "TURN_EXISTS":
		return "该回合已存在"
	case "TURN_NOT_FOUND":
		return "回合不存在"
	case "ANSWER_EXISTS":
		return "该回合已经有回答"
	case "OPEN_TURN":
		return "存在未完成的回合"
	case "INVALID_TURN":
		return "回合参数不合法"
	case "INVALID_ATTACHMENT":
		return "附件参数不合法"
	case "ATTACHMENT_NOT_FOUND":
		return "附件不存在"
	case "FILE_TOO_LARGE":
		return "附件超过大小限制"
	case "STREAM_UNSUPPORTED":
		return "当前代理不支持 SSE"
	case "METHOD_NOT_ALLOWED":
		return "只支持 POST"
	case "ENDPOINT_NOT_FOUND":
		return "接口不存在"
	case "UNKNOWN":
		return "系统繁忙，请稍后再试"
	default:
		return "请求失败"
	}
}

// ---- Meta / auth -----------------------------------------------------------

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string]any{
		"protocol":    ProtocolVersion,
		"serverId":    s.serverID,
		"authModes":   []string{"password"},
		"passwordSet": s.password != "",
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		DeviceID string `json:"deviceId"`
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	if s.password != "" && subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.password)) != 1 {
		writeFail(w, langOf(r), "InvalidPassword", localize(langOf(r), "INVALID_PASSWORD", "password is incorrect"), nil)
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		req.DeviceID = model.NewID()
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = "device"
	}
	if strings.TrimSpace(req.Platform) == "" {
		req.Platform = "unknown"
	}
	dev, _, err := s.store.CreateDevice(req.DeviceID, req.Name, req.Platform)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	access, refresh, expiresIn, err := s.store.CreateAuthSession(dev.ID, 15*time.Minute, 30*24*time.Hour)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{
		"accessToken":  access,
		"refreshToken": refresh,
		"expiresIn":    expiresIn,
		"deviceId":     dev.ID,
		"serverId":     s.serverID,
		"protocol":     ProtocolVersion,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	access, refresh, expiresIn, dev, err := s.store.RotateRefresh(req.RefreshToken, 15*time.Minute, 30*24*time.Hour)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{
		"accessToken":  access,
		"refreshToken": refresh,
		"expiresIn":    expiresIn,
		"deviceId":     dev.ID,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	_ = s.store.RevokeRefresh(req.RefreshToken)
	writeOK(w, map[string]any{})
}

// ---- Sessions --------------------------------------------------------------

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	d := deviceFrom(r)
	sess, err := s.store.CreateSession(req.ID, req.Title, d.ID)
	if err != nil {
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Code == "SESSION_EXISTS" {
			if existing, metaErr := s.store.GetSessionMeta(req.ID); metaErr == nil {
				writeOK(w, map[string]any{"session": existing})
				return
			}
		}
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": sess.ID, "ownerDeviceId": sess.OwnerID})
	writeOK(w, map[string]any{"session": sess})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSessions()
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"sessions": list})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	sess, turns, err := s.store.GetSession(req.SessionID)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"session": sess, "turns": turns})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	d := deviceFrom(r)
	if err := s.store.Rename(req.SessionID, d.ID, req.Title); err != nil {
		writeAppError(w, r, err)
		return
	}
	sess, err := s.store.GetSessionMeta(req.SessionID)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": sess.ID, "ownerDeviceId": sess.OwnerID})
	writeOK(w, map[string]any{"session": sess})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	d := deviceFrom(r)
	if err := s.store.DeleteSession(req.SessionID, d.ID); err != nil {
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "deleted", "sessionId": req.SessionID})
	writeOK(w, map[string]any{})
}

func (s *Server) handleAppendTurn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string     `json:"sessionId"`
		Turn      model.Turn `json:"turn"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	if req.Turn.CreatedAt == 0 {
		req.Turn.CreatedAt = model.NowMS()
	}
	d := deviceFrom(r)
	if err := s.store.AppendTurn(req.SessionID, d.ID, req.Turn); err != nil {
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{
		"type": "turn", "sessionId": req.SessionID,
		"turnId": req.Turn.ID, "parentTurnId": req.Turn.ParentTurnID,
	})
	writeOK(w, map[string]any{"turnId": req.Turn.ID})
}

func (s *Server) handleAppendAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string       `json:"sessionId"`
		TurnID    string       `json:"turnId"`
		Answer    model.Answer `json:"answer"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	if req.Answer.CreatedAt == 0 {
		req.Answer.CreatedAt = model.NowMS()
	}
	d := deviceFrom(r)
	if err := s.store.AppendAnswer(req.SessionID, d.ID, req.TurnID, req.Answer); err != nil {
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "answer", "sessionId": req.SessionID, "turnId": req.TurnID})
	writeOK(w, map[string]any{})
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Force     bool   `json:"force"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	d := deviceFrom(r)
	sess, err := s.store.Claim(req.SessionID, d.ID, req.Force)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": sess.ID, "ownerDeviceId": sess.OwnerID})
	writeOK(w, map[string]any{"session": sess})
}

// ---- Attachments -----------------------------------------------------------

func (s *Server) handlePutAttachment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(72 << 20); err != nil {
		writeFail(w, langOf(r), "IllegalArguments", localize(langOf(r), "BAD_REQUEST", err.Error()), nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeFail(w, langOf(r), "IllegalArguments", localize(langOf(r), "BAD_REQUEST", "missing file field"), nil)
		return
	}
	defer file.Close()
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		writeFail(w, langOf(r), "InvalidAttachment", localize(langOf(r), "INVALID_ATTACHMENT", "attachment id is required"), nil)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = header.Filename
	}
	mimeType := r.FormValue("mime")
	if mimeType == "" {
		mimeType = header.Header.Get("Content-Type")
	}
	sessionID := r.FormValue("sessionId")
	if err := s.store.SaveAttachment(Attachment{ID: id, SessionID: sessionID, Name: name, MIME: mimeType, CreatedAt: model.NowMS()}, file); err != nil {
		writeAppError(w, r, err)
		return
	}
	meta, err := s.store.AttachmentMeta(id)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"attachment": meta})
}

func (s *Server) handleHeadAttachment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AttachmentID string `json:"attachmentId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	meta, err := s.store.AttachmentMeta(req.AttachmentID)
	if err != nil {
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Code == "ATTACHMENT_NOT_FOUND" {
			writeOK(w, map[string]any{"exists": false})
			return
		}
		writeAppError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"exists": true, "attachment": meta})
}

func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AttachmentID string `json:"attachmentId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		writeAppError(w, r, err)
		return
	}
	path, meta, err := s.store.AttachmentPath(req.AttachmentID)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeFail(w, langOf(r), "AttachmentNotFound", localize(langOf(r), "ATTACHMENT_NOT_FOUND", "attachment file missing"), nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", meta.MIME)
	w.Header().Set("X-Attachment-Name", url.QueryEscape(meta.Name))
	w.Header().Set("X-Attachment-Mime", url.QueryEscape(meta.MIME))
	if cd := mime.FormatMediaType("inline", map[string]string{"filename": meta.Name}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	http.ServeContent(w, r, meta.Name, time.UnixMilli(meta.CreatedAt), f)
}

// ---- SSE -------------------------------------------------------------------

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeFail(w, langOf(r), "StreamUnsupported", localize(langOf(r), "STREAM_UNSUPPORTED", "streaming unsupported"), nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	writeSSEResult(w, map[string]any{"type": "hello", "protocol": ProtocolVersion, "serverId": s.serverID})
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: {\"success\":true,\"data\":%s}\n\n", msg)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSEResult(w http.ResponseWriter, data any) {
	b, err := json.Marshal(result{Success: true, Data: data})
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}

type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast(event map[string]any) {
	b, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
		}
	}
}
