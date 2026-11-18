package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/httpx"
	"dschat/app/core/model"
)

type Config struct {
	Addr string
	Data string
}

type Server struct {
	cfg      Config
	store    *Store
	hub      *hub
	serverID string

	mu          sync.Mutex
	pairCode    string
	pairExpires time.Time
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
	s := &Server{cfg: cfg, store: st, hub: newHub(), serverID: sid}
	if err := s.rotatePairCode(); err != nil {
		st.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) Close() error { return s.store.Close() }

func (s *Server) PairingCode() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pairCode, s.pairExpires
}

func (s *Server) rotatePairCode() error {
	code, err := randomCode()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.pairCode = code
	s.pairExpires = time.Now().Add(10 * time.Minute)
	s.mu.Unlock()
	log.Printf("pairing code: %s (valid 10 minutes)", code)
	return nil
}

func randomCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/meta", s.handleMeta)
	mux.HandleFunc("POST /v1/pair", s.handlePair)
	mux.HandleFunc("GET /v1/stream", s.auth(s.handleStream))

	mux.HandleFunc("POST /v1/sessions", s.auth(s.handleCreateSession))
	mux.HandleFunc("GET /v1/sessions", s.auth(s.handleListSessions))
	mux.HandleFunc("GET /v1/sessions/{id}", s.auth(s.handleGetSession))
	mux.HandleFunc("PATCH /v1/sessions/{id}", s.auth(s.handleRename))
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /v1/sessions/{id}/turns", s.auth(s.handleAppendTurn))
	mux.HandleFunc("POST /v1/sessions/{id}/turns/{tid}/answer", s.auth(s.handleAppendAnswer))
	mux.HandleFunc("POST /v1/sessions/{id}/claim", s.auth(s.handleClaim))
	return mux
}

type ctxKey string

const deviceKey ctxKey = "device"

func deviceFrom(r *http.Request) *Device {
	d, _ := r.Context().Value(deviceKey).(*Device)
	return d
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			apierr.Write(w, apierr.New(401, "UNAUTHORIZED", "missing bearer token"))
			return
		}
		d, err := s.store.DeviceByToken(token)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), deviceKey, d)
		next(w, r.WithContext(ctx))
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, 200, map[string]any{
		"protocol": ProtocolVersion,
		"serverId": s.serverID,
	})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Platform string `json:"platform"`
		DeviceID string `json:"deviceId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	s.mu.Lock()
	code := s.pairCode
	expires := s.pairExpires
	s.mu.Unlock()
	if time.Now().After(expires) {
		apierr.Write(w, apierr.New(400, "PAIR_EXPIRED", "pairing code expired"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Code), []byte(code)) != 1 {
		apierr.Write(w, apierr.New(400, "PAIR_INVALID", "invalid pairing code"))
		return
	}
	dev, token, err := s.store.CreateDevice(req.DeviceID, req.Name, req.Platform)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	_ = s.rotatePairCode()
	httpx.WriteJSON(w, 200, map[string]any{
		"deviceId": dev.ID,
		"token":    token,
		"serverId": s.serverID,
		"protocol": ProtocolVersion,
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	d := deviceFrom(r)
	sess, err := s.store.CreateSession(req.ID, req.Title, d.ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": sess.ID, "ownerDeviceId": sess.OwnerID, "created": true})
	httpx.WriteJSON(w, 200, sess)
}

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	list, err := s.store.ListSessions()
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"sessions": list})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, turns, err := s.store.GetSession(r.PathValue("id"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"session": sess, "turns": turns})
}

func (s *Server) handleAppendTurn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Turn model.Turn `json:"turn"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if req.Turn.CreatedAt == 0 {
		req.Turn.CreatedAt = model.NowMS()
	}
	d := deviceFrom(r)
	if err := s.store.AppendTurn(r.PathValue("id"), d.ID, req.Turn); err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{
		"type": "turn", "sessionId": r.PathValue("id"),
		"turnId": req.Turn.ID, "parentTurnId": req.Turn.ParentTurnID,
	})
	httpx.WriteJSON(w, 200, map[string]any{"turnId": req.Turn.ID})
}

func (s *Server) handleAppendAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answer model.Answer `json:"answer"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if req.Answer.CreatedAt == 0 {
		req.Answer.CreatedAt = model.NowMS()
	}
	d := deviceFrom(r)
	if err := s.store.AppendAnswer(r.PathValue("id"), d.ID, r.PathValue("tid"), req.Answer); err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "answer", "sessionId": r.PathValue("id"), "turnId": r.PathValue("tid")})
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	d := deviceFrom(r)
	sess, err := s.store.Claim(r.PathValue("id"), d.ID, req.Force)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": sess.ID, "ownerDeviceId": sess.OwnerID})
	httpx.WriteJSON(w, 200, sess)
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	d := deviceFrom(r)
	if err := s.store.Rename(r.PathValue("id"), d.ID, req.Title); err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "session", "sessionId": r.PathValue("id")})
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	if err := s.store.DeleteSession(r.PathValue("id"), d.ID); err != nil {
		apierr.Write(w, err)
		return
	}
	s.hub.broadcast(map[string]any{"type": "deleted", "sessionId": r.PathValue("id")})
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apierr.Write(w, apierr.New(500, "STREAM_UNSUPPORTED", "streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	httpx.WriteSSE(w, map[string]any{"type": "hello", "protocol": ProtocolVersion, "serverId": s.serverID})
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
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
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
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *hub) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
			// Slow consumer: drop. Clients refetch state on reconnect.
		}
	}
}
