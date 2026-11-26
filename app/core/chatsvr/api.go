package chatsvr

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/deepseek"
	"dschat/app/core/httpx"
	"dschat/app/core/web"
)

const cookieName = "dschat"

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		// app/pid 用来识别「端口被另一个 dschat 占用」：启动失败时桌面层会探测这里。
		httpx.WriteJSON(w, 200, map[string]any{"ok": true, "app": "dschat", "pid": os.Getpid()})
	})
	mux.HandleFunc("POST /api/auth", a.handleAuth)
	mux.HandleFunc("GET /auth", a.handleLogin)

	mux.HandleFunc("GET /api/bootstrap", a.auth(a.handleBootstrap))
	mux.HandleFunc("GET /api/sessions", a.auth(a.handleListSessions))
	mux.HandleFunc("GET /api/sessions/{id}", a.auth(a.handleGetSession))
	mux.HandleFunc("POST /api/sessions", a.auth(a.handleCreateSession))
	mux.HandleFunc("POST /api/sessions/import", a.auth(a.handleImportSession))
	mux.HandleFunc("POST /api/official/share/import", a.auth(a.handleOfficialShareImport))
	mux.HandleFunc("PATCH /api/sessions/{id}", a.auth(a.handleRename))
	mux.HandleFunc("DELETE /api/sessions", a.auth(a.handleDeleteAll))
	mux.HandleFunc("DELETE /api/sessions/{id}", a.auth(a.handleDelete))
	mux.HandleFunc("POST /api/sessions/{id}/turns", a.auth(a.handleSend))
	mux.HandleFunc("POST /api/sessions/{id}/turns/{tid}/stop", a.auth(a.handleStop))
	mux.HandleFunc("POST /api/sessions/{id}/claim", a.auth(a.handleClaim))
	mux.HandleFunc("POST /api/sessions/{id}/sync", a.auth(a.handleSync))
	mux.HandleFunc("POST /api/sessions/{id}/select", a.auth(a.handleSelect))
	mux.HandleFunc("GET /api/events", a.auth(a.handleEvents))
	mux.HandleFunc("POST /api/attachments", a.auth(a.handleAttachmentUpload))
	mux.HandleFunc("GET /api/attachments/{id}", a.auth(a.handleAttachmentGet))
	mux.HandleFunc("GET /api/settings", a.auth(a.handleSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.handleSaveSettings))
	mux.HandleFunc("POST /api/settings/verify", a.auth(a.handleVerifyKey))
	mux.HandleFunc("POST /api/server/login", a.auth(a.handleServerLogin))
	// 官方模式：地址内置，客户端只有登录一个入口；
	// 注册与找回密码在 cloudsvr 的网页上（/register、/reset），客户端不参与。
	mux.HandleFunc("POST /api/server/officialLogin", a.auth(a.handleOfficialLogin))
	mux.HandleFunc("POST /api/server/disconnect", a.auth(a.handleDisconnect))
	mux.HandleFunc("GET /api/server/devices", a.auth(a.handleServerDevices))
	mux.HandleFunc("DELETE /api/server/devices/{id}", a.auth(a.handleRevokeServerDevice))
	mux.HandleFunc("GET /api/server/account", a.auth(a.handleServerAccount))
	mux.HandleFunc("GET /api/llm/models", a.auth(a.handleProxy))
	mux.HandleFunc("POST /api/llm/anthropic/v1/messages", a.auth(a.handleProxy))
	mux.HandleFunc("POST /api/llm/anthropic/v1/files", a.auth(a.handleProxy))
	mux.HandleFunc("DELETE /api/llm/anthropic/v1/files/{id}", a.auth(a.handleProxy))
	mux.HandleFunc("POST /api/llm/v1/tools/asr", a.auth(a.handleProxy))
	mux.HandleFunc("POST /api/llm/v1/tools/tts", a.auth(a.handleProxy))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		apierr.Write(w, apierr.New(404, "NOT_FOUND", "not found"))
	})
	mux.Handle("/", a.staticHandler())
	return a.originGuard(mux)
}

func (a *App) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !a.allowedOrigin(o) {
			apierr.Write(w, apierr.New(403, "BAD_ORIGIN", "origin not allowed"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) allowedOrigin(o string) bool {
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		if o == fmt.Sprintf("http://%s:%d", host, a.port) {
			return true
		}
	}
	return false
}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			apierr.Write(w, apierr.New(401, "UNAUTHORIZED", "missing or invalid local token"))
			return
		}
		if lang := strings.TrimSpace(r.Header.Get("X-Language")); lang != "" {
			a.setLanguage(lang)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
				apierr.Write(w, apierr.New(415, "UNSUPPORTED_MEDIA_TYPE", "unexpected content type"))
				return
			}
		}
		next(w, r)
	}
}

func (a *App) authorized(r *http.Request) bool {
	if c, err := r.Cookie(cookieName); err == nil {
		if subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.token)) == 1 {
			return true
		}
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(a.token)) == 1 {
			return true
		}
	}
	return false
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(a.token)) != 1 {
		apierr.Write(w, apierr.New(401, "UNAUTHORIZED", "invalid token"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    a.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   365 * 24 * 3600,
	})
	// The tray's "配置" entry appends settings=1 to this auth URL. Keep
	// non-secret query parameters across the token-consuming redirect;
	// otherwise the app opens normally and the requested dialog is lost.
	query := r.URL.Query()
	query.Del("t")
	target := "/"
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *App) handleAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(a.token)) != 1 {
		apierr.Write(w, apierr.New(401, "UNAUTHORIZED", "invalid token"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    a.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   365 * 24 * 3600,
	})
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	out, err := a.Bootstrap()
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, out)
}

func (a *App) handleListSessions(w http.ResponseWriter, r *http.Request) {
	out, err := a.Bootstrap()
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"sessions": out["sessions"]})
}

func (a *App) handleGetSession(w http.ResponseWriter, r *http.Request) {
	d, err := a.SessionDetail(r.PathValue("id"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, d)
}

func (a *App) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	sess, err := a.CreateSession(req.Title)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, sess)
}

func (a *App) handleSend(w http.ResponseWriter, r *http.Request) {
	var req SendRequest
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	t, err := a.Send(r.PathValue("id"), req)
	if err != nil {
		a.noteSyncError(err)
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, t)
}

func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := a.Stop(r.PathValue("id"), r.PathValue("tid")); err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if err := a.Claim(r.PathValue("id"), req.Force); err != nil {
		a.noteSyncError(err)
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleOfficialShareImport(w http.ResponseWriter, r *http.Request) {
	var req OfficialShareImportRequest
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	result, err := a.ImportOfficialShare(r.Context(), req.URL)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

func (a *App) handleImportSession(w http.ResponseWriter, r *http.Request) {
	var req ImportSessionRequest
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	res, err := a.ImportSession(req)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, res)
}

func (a *App) handleSync(w http.ResponseWriter, r *http.Request) {
	if err := a.SyncPending(r.PathValue("id")); err != nil {
		a.noteSyncError(err)
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TurnID string `json:"turnId"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if err := a.SelectVersion(r.PathValue("id"), req.TurnID); err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if err := a.Rename(r.PathValue("id"), req.Title); err != nil {
		a.noteSyncError(err)
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleDeleteAll(w http.ResponseWriter, r *http.Request) {
	result, err := a.DeleteAll()
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Delete(r.PathValue("id")); err != nil {
		a.noteSyncError(err)
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apierr.Write(w, apierr.New(500, "STREAM_UNSUPPORTED", "streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.hub.subscribe()
	defer a.hub.unsubscribe(ch)

	httpx.WriteSSE(w, map[string]any{"type": "hello"})
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
			httpx.WriteComment(w, "ping")
			flusher.Flush()
		}
	}
}

func (a *App) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(72 << 20); err != nil {
		apierr.Write(w, apierr.New(400, "BAD_UPLOAD", err.Error()))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		apierr.Write(w, apierr.New(400, "BAD_UPLOAD", "missing file field"))
		return
	}
	defer file.Close()
	sessionID := r.FormValue("sessionId")
	att, err := a.SaveAttachment(sessionID, header.Filename, header.Header.Get("Content-Type"), file, header.Size)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, att)
}

func (a *App) handleAttachmentGet(w http.ResponseWriter, r *http.Request) {
	row, err := a.OpenAttachment(r.PathValue("id"))
	if err == nil {
		if _, statErr := os.Stat(row.Path); statErr != nil {
			row, err = nil, statErr
		}
	}
	if err != nil {
		ctx, cancel := context.WithTimeout(a.baseCtx(), 60*time.Second)
		row, err = a.fetchRemoteAttachment(ctx, r.PathValue("id"))
		cancel()
	}
	if err != nil {
		apierr.Write(w, err)
		return
	}
	f, err := os.Open(row.Path)
	if err != nil {
		apierr.Write(w, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment file missing"))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", row.MIME)
	w.Header().Set("Content-Disposition", "inline; filename=\""+url.PathEscape(row.Name)+"\"")
	w.Header().Set("Cache-Control", "private, max-age=31536000")
	_, _ = io.Copy(w, f)
}

func (a *App) handleSettings(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, 200, a.settingsView())
}

func (a *App) settingsView() map[string]any {
	set := a.Settings()
	server := map[string]any{"paired": false, "url": "", "deviceId": "", "kind": ""}
	if s := a.Sync(); s != nil {
		server["paired"] = true
		server["url"] = s.BaseURL
		server["deviceId"] = s.DeviceID
		server["kind"] = a.serverKind(s.BaseURL)
	}
	autoStart, autoStartSupported := a.autoStartView()
	return map[string]any{
		"hasKey":             set.APIKey != "",
		"baseUrl":            set.BaseURL,
		"model":              set.Model,
		"thinking":           set.Thinking,
		"search":             set.Search,
		"safetyCheck":        set.SafetyCheck,
		"autoStart":          autoStart,
		"autoStartSupported": autoStartSupported,
		"deviceName":         set.DeviceName,
		"deviceId":           a.deviceID,
		"server":             server,
	}
}

func (a *App) autoStartView() (bool, bool) {
	if a.autoStart == nil {
		return false, false
	}
	enabled, err := a.autoStart.Enabled()
	if err != nil {
		return false, true
	}
	return enabled, true
}

func (a *App) handleVerifyKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		apierr.Write(w, apierr.New(400, "NO_API_KEY", "apiKey is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	client := deepseek.New(a.Settings().BaseURL, req.APIKey)
	if err := client.VerifyKey(ctx); err != nil {
		// 不要把网络超时、余额不足、限流都折叠成「Key 无效」：用户会以为
		// 自己的密钥坏了，实际上是另一类故障，重试或充值才对。
		var apiErr *deepseek.APIError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			apierr.Write(w, apierr.New(504, "VERIFY_TIMEOUT", err.Error()))
		case errors.As(err, &apiErr) && apiErr.Status == 401:
			apierr.Write(w, apierr.New(401, "INVALID_KEY", err.Error()))
		case errors.As(err, &apiErr) && apiErr.Status == 402:
			apierr.Write(w, apierr.New(402, "INSUFFICIENT_BALANCE", err.Error()))
		case errors.As(err, &apiErr) && apiErr.Status == 429:
			apierr.Write(w, apierr.New(429, "RATE_LIMITED", err.Error()))
		case errors.As(err, &apiErr) && apiErr.Status >= 500:
			apierr.Write(w, apierr.New(502, "UPSTREAM_ERROR", err.Error()))
		default:
			apierr.Write(w, apierr.New(502, "VERIFY_FAILED", err.Error()))
		}
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey      *string `json:"apiKey"`
		ClearKey    bool    `json:"clearKey"`
		BaseURL     *string `json:"baseUrl"`
		Model       *string `json:"model"`
		Thinking    *bool   `json:"thinking"`
		Search      *bool   `json:"search"`
		SafetyCheck *bool   `json:"safetyCheck"`
		AutoStart   *bool   `json:"autoStart"`
		DeviceName  *string `json:"deviceName"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" {
		_ = a.store.SetKV("api_key", strings.TrimSpace(*req.APIKey))
	}
	if req.ClearKey {
		_ = a.store.SetKV("api_key", "")
	}
	if req.BaseURL != nil {
		u, err := url.Parse(strings.TrimSpace(*req.BaseURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			apierr.Write(w, apierr.New(400, "BAD_URL", "baseUrl must be an http(s) URL"))
			return
		}
		_ = a.store.SetKV("base_url", strings.TrimRight(*req.BaseURL, "/"))
	}
	if req.Model != nil && *req.Model != "" {
		_ = a.store.SetKV("model", *req.Model)
	}
	if req.Thinking != nil {
		_ = a.store.SetKV("thinking", boolStr(*req.Thinking))
	}
	if req.Search != nil {
		_ = a.store.SetKV("search", boolStr(*req.Search))
	}
	if req.SafetyCheck != nil {
		_ = a.store.SetKV("safety_check", boolStr(*req.SafetyCheck))
	}
	if req.AutoStart != nil {
		if a.autoStart == nil {
			apierr.Write(w, apierr.New(400, "AUTO_START_UNSUPPORTED", "auto start is not supported on this platform"))
			return
		}
		if err := a.autoStart.Set(*req.AutoStart); err != nil {
			apierr.Write(w, apierr.New(500, "AUTO_START_FAILED", err.Error()))
			return
		}
	}
	if req.DeviceName != nil && strings.TrimSpace(*req.DeviceName) != "" {
		name := strings.TrimSpace(*req.DeviceName)
		_ = a.store.SetKV("device_name", name)
		a.device = name
	}
	a.loadSettings()
	httpx.WriteJSON(w, 200, a.settingsView())
}

func (a *App) handleServerLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		apierr.Write(w, apierr.New(400, "BAD_URL", "url must be an http(s) URL"))
		return
	}
	if req.Name == "" {
		req.Name = a.device
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 20*time.Second)
	defer cancel()
	res, err := Login(ctx, u.String(), req.Password, req.Name, runtime.GOOS, a.deviceID, a.currentLanguage())
	if err != nil {
		apierr.Write(w, remoteAPIError(err))
		return
	}
	base := strings.TrimRight(u.String(), "/")
	if err := a.pairServer(base, res, "selfHosted"); err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"paired": true, "url": base, "deviceId": res.DeviceID, "kind": "selfHosted"})
}

// handleOfficialLogin 官方模式登录：手机号 + 密码，地址用内置的 OfficialBaseURL。
func (a *App) handleOfficialLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		apierr.Write(w, err)
		return
	}
	if strings.TrimSpace(req.Phone) == "" || req.Password == "" {
		apierr.Write(w, apierr.New(400, "BAD_REQUEST", "phone and password are required"))
		return
	}
	if req.Name == "" {
		req.Name = a.device
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), officialTimeout)
	defer cancel()
	res, err := LoginOfficial(ctx, OfficialBaseURL, normalizePhone(req.Phone), req.Password,
		req.Name, runtime.GOOS, a.deviceID, a.currentLanguage())
	if err != nil {
		apierr.Write(w, remoteAPIError(err))
		return
	}
	if err := a.pairServer(OfficialBaseURL, res, "official"); err != nil {
		apierr.Write(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"paired": true, "url": OfficialBaseURL, "deviceId": res.DeviceID, "kind": "official"})
}

// handleOfficialSendCode 注册前置：请求云端发送短信验证码。
//
// 人机验证在云端做：captchaVerifyParam 原样透传，不在客户端判通过与否——
// 客户端判等于把闸门交给攻击者。

// handleOfficialRegister 官方模式注册：手机号 + 验证码 + 密码，成功后直接是登录态。

// handleOfficialResetPassword 找回密码：手机号 + 验证码 + 新密码。
//
// 成功后云端已吊销该账号其他设备的 token，本设备拿到的是新的登录态，所以走同一套配对流程。

// pairServer 把一次成功登录的结果落到本地：切换服务器、保存 token、起 SSE 与首次全量同步。
//
// 自建与官方两条路径共用同一段落库逻辑——两处各写一遍的话，"切服务器时把旧会话降级为本地"
// 这种容易漏的规则迟早只在一处生效。
func (a *App) pairServer(base string, res *LoginResult, kind string) error {
	a.cancelActiveTurns()
	if old := a.Sync(); old != nil {
		old.Close()
	}
	base = strings.TrimRight(base, "/")
	oldServerID, _ := a.store.KV("server_id")
	oldURL, _ := a.store.KV("last_server_url")
	if oldURL == "" {
		oldURL, _ = a.store.KV("server_url")
	}
	newServerID := res.ServerID
	if newServerID == "" {
		newServerID = base
	}
	serverChanged := false
	if oldServerID != "" {
		serverChanged = oldServerID != newServerID
	} else if oldURL != "" {
		serverChanged = strings.TrimRight(oldURL, "/") != base
	}
	if serverChanged {
		// Never let the new server's list reconcile away sessions that belong to
		// the previous server. Keep them as local conversations.
		detachedOwner := res.DeviceID
		if detachedOwner == "" {
			detachedOwner = a.deviceID
		}
		if err := a.store.DetachSyncedSessions(detachedOwner); err != nil {
			return err
		}
		a.emit(map[string]any{"type": "sessions_changed"})
	}
	_ = a.store.SetKV("server_url", base)
	_ = a.store.SetKV("last_server_url", base)
	_ = a.store.SetKV("server_kind", kind)
	_ = a.store.SetKV("server_id", newServerID)
	_ = a.store.SetKV("server_device_id", res.DeviceID)
	a.saveServerTokens(res.AccessToken, res.RefreshToken, res.AccessExpiresAt)
	a.clearSyncBlocked()
	s := NewSync(base, res.AccessToken, res.RefreshToken, res.AccessExpiresAt, res.DeviceID, a.currentLanguage())
	s.OnTokens = a.saveServerTokens
	s.OnError = a.noteSyncError
	a.setSync(s)
	go s.Run(a.baseCtx(), a.onRemoteEvent, func() { go a.syncAll(a.baseCtx()) })
	go a.syncAll(a.baseCtx())
	return nil
}

// remoteAPIError 把云端的业务错误原样透出：前端按 errCode 决定把用户导回哪一步
// （验证码错 → 停在验证码；手机号已注册 → 提示去登录；人机验证未通过 → 重弹滑块）。
func remoteAPIError(err error) error {
	var remote *RemoteError
	if errors.As(err, &remote) && remote.Code != "" {
		message := remote.Message
		if message == "" {
			message = remote.Code
		}
		return apierr.New(400, remote.Code, message)
	}
	return apierr.New(400, "REMOTE_ERROR", err.Error())
}

func (a *App) handleServerDevices(w http.ResponseWriter, r *http.Request) {
	s := a.Sync()
	if s == nil {
		apierr.Write(w, apierr.New(409, "NOT_PAIRED", "sync server is not connected"))
		return
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 20*time.Second)
	defer cancel()
	res, err := s.ListDevices(ctx)
	if err != nil {
		apierr.Write(w, remoteAPIError(err))
		return
	}
	httpx.WriteJSON(w, 200, res)
}

func (a *App) handleRevokeServerDevice(w http.ResponseWriter, r *http.Request) {
	s := a.Sync()
	if s == nil {
		apierr.Write(w, apierr.New(409, "NOT_PAIRED", "sync server is not connected"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		apierr.Write(w, apierr.New(400, "BAD_REQUEST", "deviceId is required"))
		return
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 20*time.Second)
	defer cancel()
	if err := s.RevokeDevice(ctx, id); err != nil {
		apierr.Write(w, remoteAPIError(err))
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleServerAccount(w http.ResponseWriter, r *http.Request) {
	s := a.Sync()
	if s == nil {
		apierr.Write(w, apierr.New(409, "NOT_PAIRED", "sync server is not connected"))
		return
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 20*time.Second)
	defer cancel()
	info, err := s.AccountInfo(ctx)
	if err != nil {
		apierr.Write(w, remoteAPIError(err))
		return
	}
	httpx.WriteJSON(w, 200, info)
}

func (a *App) handleDisconnect(w http.ResponseWriter, _ *http.Request) {
	a.cancelActiveTurns()
	if s := a.Sync(); s != nil {
		s.Close()
	}
	a.setSync(nil)
	a.clearSyncBlocked()
	// 断开后没有任何东西再能同步，会话必须回到本地，否则会卡在「标记为已同步、
	// 但既不能发消息也不能删除」的状态，违背本地优先。
	if err := a.store.DetachSyncedSessions(a.deviceID); err != nil {
		apierr.Write(w, err)
		return
	}
	a.emit(map[string]any{"type": "sessions_changed"})
	_ = a.store.SetKV("server_url", "")
	_ = a.store.SetKV("server_access_token", "")
	_ = a.store.SetKV("server_refresh_token", "")
	_ = a.store.SetKV("server_access_expires_at", "")
	_ = a.store.SetKV("server_device_id", "")
	_ = a.store.SetKV("server_kind", "")
	httpx.WriteJSON(w, 200, map[string]any{"paired": false})
}

// handleProxy forwards the official frontend's API calls to DeepSeek with the
// locally stored key. The browser never sees the key.
func (a *App) handleProxy(w http.ResponseWriter, r *http.Request) {
	set := a.Settings()
	if set.APIKey == "" {
		apierr.Write(w, apierr.New(401, "NO_API_KEY", "DeepSeek API key is not configured"))
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/llm")
	target := strings.TrimRight(set.BaseURL, "/") + suffix
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var body io.Reader
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			apierr.Write(w, apierr.New(400, "BAD_BODY", err.Error()))
			return
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	for _, h := range []string{"Content-Type", "Accept", "anthropic-beta", "anthropic-version"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("x-api-key", set.APIKey)
	if strings.HasPrefix(suffix, "/models") {
		req.Header.Set("Authorization", "Bearer "+set.APIKey)
	}
	resp, err := a.proxyClient.Do(req)
	if err != nil {
		apierr.Write(w, apierr.New(502, "UPSTREAM", err.Error()))
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Cache-Control", "anthropic-beta"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, resp.Body)
}

func (a *App) staticHandler() http.Handler {
	dist, err := fs.Sub(web.FS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	distServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lang := strings.TrimSpace(r.Header.Get("X-Language")); lang != "" {
			a.setLanguage(lang)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if p == "index.html" || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		distServer.ServeHTTP(w, r)
	})
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
