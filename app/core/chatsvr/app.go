package chatsvr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dschat/app/core/deepseek"
	"dschat/app/core/model"
)

const DefaultPort = 5217

// PortInUseError 表示固定端口已被别的程序占用。桌面层据此弹出对话框，
// 并在弹窗里指出占用端口的是哪个程序（常见情况是另一个 dschat）。
type PortInUseError struct {
	Port int
	Err  error
}

func (e *PortInUseError) Error() string {
	// 直接复用底层 bind 报错，避免日志里出现重复的 listen 前缀。
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("listen 127.0.0.1:%d: address already in use", e.Port)
}

func (e *PortInUseError) Unwrap() error { return e.Err }

// isAddrInUse 同时按 errno 与文案判断，兼容不同平台的 bind 报错措辞。
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

// AutoStart is implemented by desktop shells that can register the app with
// the operating system's login items. Mobile and headless builds leave it nil.
type AutoStart interface {
	Enabled() (bool, error)
	Set(enabled bool) error
}

type Config struct {
	DataDir    string
	Port       int
	DeviceName string
	AutoStart  AutoStart
}

type Settings struct {
	APIKey   string
	BaseURL  string
	Model    string
	Thinking bool
	Search   bool
	// SafetyCheck 控制请求体是否携带 safety_check；默认开启，只有显式关闭时才不下发。
	SafetyCheck bool
	DeviceName  string
}

type App struct {
	cfg       Config
	store     *Store
	hub       *hub
	autoStart AutoStart
	deviceID  string
	device    string
	token     string
	port      int

	setMu sync.RWMutex
	set   Settings

	langMu   sync.RWMutex
	language string

	blockMu       sync.RWMutex
	blocked       string
	blockedDetail string

	ctx context.Context

	mu      sync.Mutex
	active  map[string]*activeTurn
	sending map[string]bool

	partialMu sync.Mutex
	partials  map[string]*model.Answer

	syncMu sync.Mutex
	sync   *Sync

	proxyClient *http.Client
}

type activeTurn struct {
	turnID    string
	cancel    context.CancelFunc
	stopped   bool
	preempted bool
	mu        sync.Mutex
}

func (a *activeTurn) markStopped() {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
}

func (a *activeTurn) markPreempted() {
	a.mu.Lock()
	a.preempted = true
	a.mu.Unlock()
}

func (a *activeTurn) state() (stopped, preempted bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped, a.preempted
}

func New(cfg Config) (*App, error) {
	if cfg.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cfg.DataDir = filepath.Join(home, ".dschat", "app")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:         cfg,
		store:       st,
		hub:         newHub(),
		autoStart:   cfg.AutoStart,
		active:      map[string]*activeTurn{},
		sending:     map[string]bool{},
		partials:    map[string]*model.Answer{},
		language:    "zh-CN",
		proxyClient: &http.Client{},
	}
	if lang, _ := st.KV("ui_language"); lang != "" {
		a.language = lang
	}
	a.blocked, _ = st.KV("sync_blocked")
	a.blockedDetail, _ = st.KV("sync_blocked_detail")
	a.deviceID, err = a.ensureKV("device_id", model.NewID())
	if err != nil {
		st.Close()
		return nil, err
	}
	name := cfg.DeviceName
	if name == "" {
		name, _ = os.Hostname()
	}
	a.device, _ = a.ensureKV("device_name", name)
	if a.device == "" {
		a.device = name
	}
	a.token, err = a.ensureToken()
	if err != nil {
		st.Close()
		return nil, err
	}
	a.loadSettings()
	a.loadSync()
	// 没有同步配置时，会话不该继续带着 synced 标记：界面会显示「已同步」，而任何
	// 写操作都会被一个并不存在的服务挡住。启动时就一次性还原为本地会话。
	if a.Sync() == nil {
		_ = a.store.DetachSyncedSessions(a.deviceID)
	}
	return a, nil
}

func (a *App) Close() error {
	a.mu.Lock()
	for _, t := range a.active {
		t.cancel()
	}
	a.active = map[string]*activeTurn{}
	a.mu.Unlock()
	return a.store.Close()
}

func (a *App) ensureKV(k, def string) (string, error) {
	v, err := a.store.KV(k)
	if err != nil {
		return "", err
	}
	if v == "" {
		v = def
		if err := a.store.SetKV(k, v); err != nil {
			return "", err
		}
	}
	return v, nil
}

func (a *App) ensureToken() (string, error) {
	p := filepath.Join(a.cfg.DataDir, "api-token")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return string(b), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.WriteFile(p, []byte(t), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

func (a *App) loadSettings() {
	get := func(k string) string { v, _ := a.store.KV(k); return v }
	a.setMu.Lock()
	defer a.setMu.Unlock()
	a.set = Settings{
		APIKey:   get("api_key"),
		BaseURL:  orDefault(get("base_url"), deepseek.DefaultBaseURL),
		Model:    orDefault(get("model"), "deepseek-flash"),
		Thinking: get("thinking") == "1",
		Search:   get("search") == "1",
		// 未写入过该设置时视为开启；只有显式存成 "0" 才是关闭。
		SafetyCheck: get("safety_check") != "0",
		DeviceName:  orDefault(get("device_name"), a.device),
	}
}

func (a *App) Settings() Settings {
	a.setMu.RLock()
	defer a.setMu.RUnlock()
	return a.set
}

func (a *App) currentLanguage() string {
	a.langMu.RLock()
	defer a.langMu.RUnlock()
	if a.language == "" {
		return "zh-CN"
	}
	return a.language
}

func (a *App) setLanguage(language string) {
	language = strings.TrimSpace(language)
	if language == "" {
		return
	}
	a.langMu.Lock()
	changed := a.language != language
	a.language = language
	a.langMu.Unlock()
	if changed {
		_ = a.store.SetKV("ui_language", language)
	}
	if s := a.Sync(); s != nil {
		s.SetLanguage(language)
	}
}

func (a *App) syncBlocked() string {
	a.blockMu.RLock()
	defer a.blockMu.RUnlock()
	return a.blocked
}

func (a *App) syncBlockedDetail() string {
	a.blockMu.RLock()
	defer a.blockMu.RUnlock()
	return a.blockedDetail
}

func (a *App) setSyncBlocked(reason string) {
	a.setSyncBlockedDetail(reason, "")
}

func (a *App) setSyncBlockedDetail(reason, detail string) {
	a.blockMu.Lock()
	if a.blocked == reason && a.blockedDetail == detail {
		a.blockMu.Unlock()
		return
	}
	a.blocked = reason
	a.blockedDetail = detail
	a.blockMu.Unlock()
	_ = a.store.SetKV("sync_blocked", reason)
	_ = a.store.SetKV("sync_blocked_detail", detail)
	a.emit(map[string]any{"type": "sessions_changed"})
}

func (a *App) clearSyncBlocked() {
	a.setSyncBlockedDetail("", "")
}

// noteSyncError is deliberately a catch-all: any failed upload/download is a
// sync block until a later operation succeeds. Local validation errors do not
// reach this function.
func (a *App) noteSyncError(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		reason := "sync"
		switch remote.Code {
		case "Unauthorized", "TokenExpired", "InvalidPassword":
			reason = "auth"
		case "MembershipExpired", "StorageLimit", "DeviceLimit", "SyncDisabled", "RateLimited":
			reason = "limit"
		case "HttpError", "Unknown":
			reason = "server"
		default:
			code := strings.ToLower(remote.Code)
			if strings.Contains(code, "limit") || strings.Contains(code, "quota") ||
				strings.Contains(code, "credit") || strings.Contains(code, "concurr") ||
				strings.Contains(code, "busy") || strings.Contains(code, "capacity") {
				reason = "limit"
			}
		}
		detail := remote.Code
		if remote.Message != "" {
			detail += ": " + remote.Message
		}
		a.setSyncBlockedDetail(reason, detail)
		return
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		a.setSyncBlockedDetail("network", err.Error())
	}
}

func (a *App) loadSync() {
	url, _ := a.store.KV("server_url")
	access, _ := a.store.KV("server_access_token")
	refresh, _ := a.store.KV("server_refresh_token")
	expiresRaw, _ := a.store.KV("server_access_expires_at")
	device, _ := a.store.KV("server_device_id")
	var expiresAt int64
	if n, err := strconv.ParseInt(expiresRaw, 10, 64); err == nil {
		expiresAt = n
	}
	if url != "" && (access != "" || refresh != "") {
		s := NewSync(url, access, refresh, expiresAt, device, a.currentLanguage())
		s.OnTokens = a.saveServerTokens
		s.OnError = a.noteSyncError
		a.sync = s
		return
	}
}

func (a *App) saveServerTokens(access, refresh string, accessExpiresAt int64) {
	_ = a.store.SetKV("server_access_token", access)
	_ = a.store.SetKV("server_refresh_token", refresh)
	_ = a.store.SetKV("server_access_expires_at", strconv.FormatInt(accessExpiresAt, 10))
}

func (a *App) Sync() *Sync {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	return a.sync
}

func (a *App) setSync(s *Sync) {
	a.syncMu.Lock()
	a.sync = s
	a.syncMu.Unlock()
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func (a *App) Start(ctx context.Context) error {
	a.ctx = ctx
	a.selfCheck()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", a.cfg.Port))
	if err != nil {
		if isAddrInUse(err) {
			return &PortInUseError{Port: a.cfg.Port, Err: err}
		}
		return fmt.Errorf("listen 127.0.0.1:%d: %w", a.cfg.Port, err)
	}
	a.port = ln.Addr().(*net.TCPAddr).Port

	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server: %v", err)
		}
	}()

	if s := a.Sync(); s != nil {
		go s.Run(ctx, a.onRemoteEvent, func() { go a.syncAll(a.baseCtx()) })
	}

	log.Printf("dschat listening on http://127.0.0.1:%d", a.port)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	return nil
}

func (a *App) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/auth?t=%s", a.port, a.token)
}

func (a *App) selfCheck() {
	sessions, err := a.store.Sessions()
	if err != nil {
		return
	}
	for _, s := range sessions {
		if s.OwnerID != a.deviceID {
			continue
		}
		turns, err := a.store.Turns(s.ID)
		if err != nil {
			continue
		}
		for _, t := range turns {
			if t.Answer != nil {
				continue
			}
			if _, _, ok := a.store.LocalOutput(t.ID); ok {
				continue
			}
			if _, has, _ := a.store.AnswerState(t.ID); has {
				continue
			}
			aborted := model.Answer{Status: model.StatusAborted, Blocks: []map[string]any{}, CreatedAt: model.NowMS()}
			state := SyncLocal
			if s.SyncState == SyncSynced {
				state = SyncPending
			}
			if err := a.store.PutAnswer(t.ID, aborted, state); err != nil {
				continue
			}
			if s.SyncState == SyncSynced {
				go func(sessionID, turnID string, ans model.Answer) { _ = a.pushAnswer(sessionID, turnID, ans) }(s.ID, t.ID, aborted)
			}
		}
	}
}

func (a *App) pushAnswer(sessionID, turnID string, answer model.Answer) error {
	s := a.Sync()
	if s == nil {
		return errors.New("not connected to a sessionsvr")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.AppendAnswer(ctx, sessionID, turnID, answer); err != nil {
		log.Printf("sync answer %s: %v", turnID, err)
		return err
	}
	return a.store.SetAnswerSync(turnID, SyncSynced)
}

var ErrReadOnly = errors.New("session is owned by another device")
var ErrBusy = errors.New("a turn is already running")

func (a *App) emit(v any) { a.hub.broadcast(v) }
