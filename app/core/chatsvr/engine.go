package chatsvr

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/deepseek"
	"dschat/app/core/model"
)

type SendRequest struct {
	Text          string   `json:"text"`
	AttachmentIDs []string `json:"attachmentIds,omitempty"`
	RetryOf       string   `json:"retryOf,omitempty"`
	EditOf        string   `json:"editOf,omitempty"`
	// LatestReminder 由前端按当前日期与地点生成，格式为「日期,星期,地点,Web,语言」。
	LatestReminder string `json:"latestReminder,omitempty"`
}

type SessionView struct {
	model.Session
	ReadOnly      bool   `json:"readOnly"`
	HasOpenTurn   bool   `json:"hasOpenTurn"`
	TransferState string `json:"transferState"`
	HasFragment   bool   `json:"hasFragment"`
	// BlockedReason/BlockedDetail are populated only while transferState is
	// "blocked"; they carry the last transport/server/auth/limit error for the
	// tooltip.
	BlockedReason string `json:"blockedReason,omitempty"`
	BlockedDetail string `json:"blockedDetail,omitempty"`
}

type SessionDetail struct {
	Session     model.Session               `json:"session"`
	Turns       []model.Turn                `json:"turns"`
	Path        []string                    `json:"path"`
	ReadOnly    bool                        `json:"readOnly"`
	HasOpenTurn bool                        `json:"hasOpenTurn"`
	LocalOutput map[string][]map[string]any `json:"localOutput,omitempty"`
}

func (a *App) baseCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) CreateSession(title string) (*model.Session, error) {
	now := model.NowMS()
	sess := model.Session{
		ID:        model.NewID(),
		Title:     title,
		OwnerID:   a.deviceID,
		CreatedAt: now,
		UpdatedAt: now,
		SyncState: SyncLocal,
	}
	if err := a.store.CreateSession(sess); err != nil {
		return nil, err
	}
	a.emit(map[string]any{"type": "session_created", "sessionId": sess.ID})
	return &sess, nil
}

func (a *App) SessionDetail(id string) (*SessionDetail, error) {
	sess, err := a.store.Session(id)
	if err != nil {
		return nil, err
	}
	turns, err := a.store.Turns(id)
	if err != nil {
		return nil, err
	}
	// 正在生成的回合还没落库；把内存里的部分输出附上，新窗口打开时能补齐前缀。
	for i := range turns {
		if turns[i].Answer == nil {
			if partial := a.getPartial(turns[i].ID); partial != nil {
				turns[i].Answer = partial
			}
		}
	}
	sels, err := a.store.Selections(id)
	if err != nil {
		return nil, err
	}
	path := visiblePath(turns, sels)
	ids := make([]string, 0, len(path))
	open := false
	for _, t := range path {
		ids = append(ids, t.ID)
		if t.Answer == nil || t.Answer.Status == model.StatusStreaming {
			open = true
		}
	}
	outs, _ := a.store.LocalOutputs(id)
	return &SessionDetail{
		Session:     *sess,
		Turns:       turns,
		Path:        ids,
		ReadOnly:    sess.SyncState == SyncSynced && sess.OwnerID != a.deviceID,
		HasOpenTurn: open,
		LocalOutput: outs,
	}, nil
}

func (a *App) Bootstrap() (map[string]any, error) {
	sessions, err := a.store.Sessions()
	if err != nil {
		return nil, err
	}
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		turns, _ := a.store.Turns(s.ID)
		open := false
		for _, t := range turns {
			if t.Answer == nil {
				open = true
				break
			}
		}
		hasFragment := false
		if outputs, err := a.store.LocalOutputs(s.ID); err == nil {
			hasFragment = len(outputs) > 0
		}
		pending := false
		if s.SyncState == SyncSynced {
			pending, _ = a.store.HasPending(s.ID)
		}
		state := a.transferState(s, hasFragment, pending)
		reason, detail := "", ""
		if state == "blocked" {
			reason = a.syncBlocked()
			detail = a.syncBlockedDetail()
		}
		views = append(views, SessionView{
			Session:       s,
			ReadOnly:      s.SyncState == SyncSynced && s.OwnerID != a.deviceID,
			HasOpenTurn:   open,
			TransferState: state,
			HasFragment:   hasFragment,
			BlockedReason: reason,
			BlockedDetail: detail,
		})
	}
	set := a.Settings()
	autoStart, autoStartSupported := a.autoStartView()
	server := map[string]any{"paired": false, "url": "", "deviceId": "", "kind": ""}
	if s := a.Sync(); s != nil {
		server["paired"] = true
		server["url"] = s.BaseURL
		server["deviceId"] = s.DeviceID
		server["kind"] = a.serverKind(s.BaseURL)
	}
	return map[string]any{
		"device": map[string]any{"id": a.deviceID, "name": a.device},
		"settings": map[string]any{
			"hasKey":             set.APIKey != "",
			"baseUrl":            set.BaseURL,
			"model":              set.Model,
			"thinking":           set.Thinking,
			"search":             set.Search,
			"safetyCheck":        set.SafetyCheck,
			"autoStart":          autoStart,
			"autoStartSupported": autoStartSupported,
			"deviceName":         set.DeviceName,
		},
		"server": server,
		// 注册与找回密码在 cloudsvr 的网页上，客户端只负责把地址给前端拼链接
		"officialBaseUrl": OfficialBaseURL,
		"sessions":        views,
	}, nil
}

func (a *App) serverKind(baseURL string) string {
	kind, _ := a.store.KV("server_kind")
	if kind == "official" || kind == "selfHosted" {
		return kind
	}
	if strings.TrimRight(baseURL, "/") == strings.TrimRight(OfficialBaseURL, "/") {
		return "official"
	}
	return "selfHosted"
}

func (a *App) transferState(s model.Session, hasFragment, pending bool) string {
	if hasFragment {
		return "fragment"
	}
	if a.syncBlocked() != "" {
		return "blocked"
	}
	a.mu.Lock()
	uploading := a.sending[s.ID]
	a.mu.Unlock()
	if uploading {
		return "uploading"
	}
	if s.SyncState != SyncSynced {
		return "local"
	}
	// A session synced to another sessionsvr must never look synced here.
	if s.ServerID != "" && s.ServerID != a.currentServerID() {
		return "local"
	}
	if pending {
		return "pending"
	}
	return "synced"
}

func (a *App) Send(sessionID string, req SendRequest) (*model.Turn, error) {
	sess, err := a.store.Session(sessionID)
	if err != nil {
		return nil, err
	}
	if sess.SyncState == SyncSynced && sess.OwnerID != a.deviceID {
		// Attempt an automatic, non-forced claim. The server rejects it if
		// either side still has an open turn, so this only takes over when
		// there is no unresolved data to lose.
		localTurns, err := a.store.Turns(sessionID)
		if err != nil {
			return nil, err
		}
		for _, t := range localTurns {
			if t.Answer == nil {
				return nil, apierr.New(409, "OPEN_TURN", "会话仍有未完成的回合，需要确认后接管")
			}
		}
		if pending, err := a.store.HasPending(sessionID); err != nil {
			return nil, err
		} else if pending {
			return nil, apierr.New(409, "PENDING_SYNC", "会话有尚未同步的本地内容，需要确认后接管")
		}
		if outs, err := a.store.LocalOutputs(sessionID); err != nil {
			return nil, err
		} else if len(outs) > 0 {
			return nil, apierr.New(409, "LOCAL_OUTPUT", "会话有未同步的本地输出，需要确认后接管")
		}
		if err := a.Claim(sessionID, false); err != nil {
			return nil, err
		}
		sess, err = a.store.Session(sessionID)
		if err != nil {
			return nil, err
		}
	}
	// 原子地占住这个 session，防止两个窗口在“检查”和“注册 active”之间同时挤进来。
	// sending 覆盖 resolveTurn / 远端 append 这段慢路径，active 覆盖真正的生成阶段。
	a.mu.Lock()
	_, running := a.active[sessionID]
	if !running {
		running = a.sending[sessionID]
	}
	if !running {
		a.sending[sessionID] = true
	}
	a.mu.Unlock()
	if running {
		return nil, apierr.New(409, "TURN_IN_PROGRESS", "上一轮尚未结束，请等待完成或先停止")
	}
	registered := false
	defer func() {
		if !registered {
			a.mu.Lock()
			delete(a.sending, sessionID)
			a.mu.Unlock()
		}
	}()

	turns, err := a.store.Turns(sessionID)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Turn{}
	for i := range turns {
		byID[turns[i].ID] = &turns[i]
	}
	sels, _ := a.store.Selections(sessionID)
	atts, err := a.loadAttachments(req.AttachmentIDs)
	if err != nil {
		return nil, err
	}
	parent, q, err := resolveTurn(turns, byID, sels, req, atts)
	if err != nil {
		return nil, err
	}
	if parent != model.RootTurnID {
		pt, ok := byID[parent]
		if !ok || pt.Answer == nil {
			return nil, apierr.New(409, "PARENT_OPEN", "parent turn is not closed")
		}
	}
	if strings.TrimSpace(q.Text) == "" && len(q.Attachments) == 0 {
		return nil, apierr.New(400, "EMPTY", "question is empty")
	}
	// New local sessions can start their remote lifecycle on first send. A
	// detached local session with history stays local; uploading an existing
	// tree is a separate operation, not an implicit side effect of one turn.
	// 同步服务的配置可能已经没了（断开时异常退出、换了数据目录），而会话还留着
	// synced 标记。这种情况必须就地降级成本地会话，否则发消息和删除都会被同步服务挡住。
	if sess.SyncState == SyncSynced && a.Sync() == nil {
		if err := a.store.SetSessionLocal(sess.ID, a.deviceID); err != nil {
			return nil, err
		}
		sess.SyncState = SyncLocal
		a.emit(map[string]any{"type": "sessions_changed"})
	}
	syncActive := sess.SyncState == SyncSynced
	if !syncActive && a.Sync() != nil && len(turns) == 0 {
		syncActive = true
	}
	now := model.NowMS()
	t := model.Turn{ID: model.NewID(), SessionID: sessionID, ParentTurnID: parent, Q: q, CreatedAt: now}
	if err := a.store.UpsertTurn(t); err != nil {
		return nil, err
	}
	if err := a.store.SetSelection(sessionID, parent, t.ID); err != nil {
		return nil, err
	}
	_ = a.store.TouchSession(sessionID)

	if syncActive {
		ctx, cancel := context.WithTimeout(a.baseCtx(), 30*time.Second)
		// Local-first: the turn and its local answer must survive a sync
		// failure. A sessionsvr outage, quota error or auth problem only
		// updates the sync badge; it must never make the local chat unusable.
		var syncErr error
		upCtx, upCancel := context.WithTimeout(a.baseCtx(), 60*time.Second)
		syncErr = a.ensureRemoteAttachments(upCtx, sessionID, q.Attachments)
		upCancel()
		if syncErr == nil {
			// A synced session already has its remote row. Calling
			// sessions.create again makes cloudsvr return SessionExists on every
			// turn after the first; only create when this session is still local.
			if sess.SyncState != SyncSynced {
				syncErr = a.ensureRemoteSession(ctx, *sess)
			}
			if syncErr == nil {
				if s := a.Sync(); s != nil {
					syncErr = s.AppendTurn(ctx, sessionID, t)
				}
			}
		}
		cancel()
		if syncErr == nil && sess.SyncState != SyncSynced {
			if applyErr := a.store.SetSessionSynced(sessionID, a.currentServerID()); applyErr == nil {
				sess.SyncState = SyncSynced
				sess.ServerID = a.currentServerID()
				a.emit(map[string]any{"type": "session_updated", "sessionId": sessionID})
			}
		}
		if syncErr == nil {
			a.clearSyncBlocked()
		} else {
			a.noteSyncError(syncErr)
		}
		// Deliberately no rollback of t/selection here. The local tree is the
		// source of truth while the sync service is unavailable; a later
		// reconnect can reconcile it.
	}

	ctx, cancel := context.WithCancel(a.baseCtx())
	at := &activeTurn{turnID: t.ID, cancel: cancel}
	a.mu.Lock()
	a.active[sessionID] = at
	delete(a.sending, sessionID)
	a.mu.Unlock()
	registered = true
	a.emit(map[string]any{"type": "turn_started", "sessionId": sessionID, "turnId": t.ID})
	go a.generate(ctx, *sess, t, at, req.LatestReminder)
	return &t, nil
}

// AttachmentIDs is a helper so the empty-check reads naturally.
func resolveTurn(turns []model.Turn, byID map[string]*model.Turn, sels map[string]string, req SendRequest, atts []model.Attachment) (string, model.Q, error) {
	now := model.NowMS()
	switch {
	case req.RetryOf != "" && req.EditOf != "":
		return "", model.Q{}, apierr.New(400, "BAD_REQUEST", "retryOf and editOf are mutually exclusive")
	case req.RetryOf != "":
		t, ok := byID[req.RetryOf]
		if !ok {
			return "", model.Q{}, apierr.New(404, "TURN_NOT_FOUND", "turn not found")
		}
		q := t.Q
		q.CreatedAt = now
		q.Attachments = append([]model.Attachment(nil), t.Q.Attachments...)
		return t.ParentTurnID, q, nil
	case req.EditOf != "":
		t, ok := byID[req.EditOf]
		if !ok {
			return "", model.Q{}, apierr.New(404, "TURN_NOT_FOUND", "turn not found")
		}
		if len(atts) == 0 {
			atts = append([]model.Attachment(nil), t.Q.Attachments...)
		}
		return t.ParentTurnID, model.Q{Text: req.Text, Attachments: atts, CreatedAt: now}, nil
	default:
		path := visiblePath(turns, sels)
		parent := model.RootTurnID
		if len(path) > 0 {
			parent = path[len(path)-1].ID
		}
		return parent, model.Q{Text: req.Text, Attachments: atts, CreatedAt: now}, nil
	}
}

func visiblePath(turns []model.Turn, sels map[string]string) []model.Turn {
	byParent := map[string][]model.Turn{}
	for _, t := range turns {
		byParent[t.ParentTurnID] = append(byParent[t.ParentTurnID], t)
	}
	var out []model.Turn
	cur := model.RootTurnID
	seen := map[string]bool{}
	for {
		kids := byParent[cur]
		if len(kids) == 0 {
			break
		}
		var pick *model.Turn
		if sels != nil {
			if id, ok := sels[cur]; ok {
				for i := range kids {
					if kids[i].ID == id {
						pick = &kids[i]
						break
					}
				}
			}
		}
		if pick == nil {
			p := kids[0]
			for _, k := range kids[1:] {
				if k.CreatedAt > p.CreatedAt || (k.CreatedAt == p.CreatedAt && k.ID > p.ID) {
					p = k
				}
			}
			pick = &p
		}
		if seen[pick.ID] {
			break
		}
		seen[pick.ID] = true
		out = append(out, *pick)
		cur = pick.ID
	}
	return out
}

func chain(turns []model.Turn, parentID string) []model.Turn {
	byID := map[string]model.Turn{}
	for _, t := range turns {
		byID[t.ID] = t
	}
	var rev []model.Turn
	cur := parentID
	seen := map[string]bool{}
	for cur != "" && cur != model.RootTurnID {
		if seen[cur] {
			break
		}
		seen[cur] = true
		t, ok := byID[cur]
		if !ok {
			break
		}
		rev = append(rev, t)
		cur = t.ParentTurnID
	}
	out := make([]model.Turn, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}

func (a *App) ensureRemoteSession(ctx context.Context, sess model.Session) error {
	s := a.Sync()
	if s == nil {
		return apierr.New(400, "NO_SERVER", "尚未连接数据同步服务")
	}
	remote, err := s.CreateSession(ctx, sess.ID, sess.Title)
	if err != nil {
		// A local session can be reattached to a server where its row still
		// exists (offline/disconnect, or a previous create succeeded before
		// the local synced flag was persisted). Treat that as the existing
		// remote session instead of surfacing SessionExists to the user.
		var remoteErr *RemoteError
		if !errors.As(err, &remoteErr) || remoteErr.Code != "SessionExists" {
			return err
		}
		_, remote, err = s.GetSession(ctx, sess.ID)
		if err != nil {
			return err
		}
	}
	if remote.OwnerID != "" && remote.OwnerID != a.deviceID {
		_ = a.store.SetSessionOwner(sess.ID, remote.OwnerID)
		return apierr.New(409, "READ_ONLY", "session is owned by another device")
	}
	return nil
}

func (a *App) Stop(sessionID, turnID string) error {
	a.mu.Lock()
	at := a.active[sessionID]
	a.mu.Unlock()
	if at == nil || at.turnID != turnID {
		return apierr.New(404, "TURN_NOT_RUNNING", "turn is not running")
	}
	at.markStopped()
	at.cancel()
	return nil
}

func (a *App) Claim(sessionID string, force bool) error {
	s := a.Sync()
	if s == nil {
		return apierr.New(400, "NO_SERVER", "尚未连接数据同步服务")
	}
	a.mu.Lock()
	if at := a.active[sessionID]; at != nil {
		at.markPreempted()
		at.cancel()
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(a.baseCtx(), 30*time.Second)
	defer cancel()
	remote, err := s.Claim(ctx, sessionID, force)
	if err != nil {
		return err
	}
	a.clearSyncBlocked()
	if err := a.store.SetSessionOwner(sessionID, remote.OwnerID); err != nil {
		return err
	}
	_ = a.pullSession(ctx, sessionID)
	// Close turns left open by the previous owner.
	turns, err := a.store.Turns(sessionID)
	if err != nil {
		return err
	}
	for _, t := range turns {
		if t.Answer != nil {
			continue
		}
		aborted := model.Answer{Status: model.StatusAborted, Blocks: []map[string]any{}, CreatedAt: model.NowMS()}
		if err := a.store.PutAnswer(t.ID, aborted, SyncPending); err != nil {
			return err
		}
		if err := s.AppendAnswer(ctx, sessionID, t.ID, aborted); err == nil {
			_ = a.store.SetAnswerSync(t.ID, SyncSynced)
		}
	}
	a.emit(map[string]any{"type": "session_updated", "sessionId": sessionID})
	return nil
}

func (a *App) cancelActiveTurns() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, at := range a.active {
		at.cancel()
	}
}

func (a *App) currentServerID() string {
	v, _ := a.store.KV("server_id")
	return v
}

func (a *App) SyncPending(sessionID string) error {
	s := a.Sync()
	if s == nil {
		// 没有同步服务可推：把会话还原为本地即可，不需要用户处理。
		if err := a.store.SetSessionLocal(sessionID, a.deviceID); err != nil {
			return err
		}
		a.emit(map[string]any{"type": "sessions_changed"})
		return nil
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 60*time.Second)
	defer cancel()
	sess, err := a.store.Session(sessionID)
	if err != nil {
		return err
	}
	if sess.SyncState != SyncSynced {
		return nil
	}
	if err := a.ensureRemoteSession(ctx, *sess); err != nil {
		return err
	}
	turns, err := a.store.Turns(sessionID)
	if err != nil {
		return err
	}
	for _, t := range turns {
		if t.Answer == nil {
			continue
		}
		state, ok, _ := a.store.AnswerState(t.ID)
		if !ok || state != SyncPending {
			continue
		}
		if err := s.AppendAnswer(ctx, sessionID, t.ID, *t.Answer); err != nil {
			return err
		}
		_ = a.store.SetAnswerSync(t.ID, SyncSynced)
	}
	a.clearSyncBlocked()
	a.emit(map[string]any{"type": "session_updated", "sessionId": sessionID})
	return nil
}

// syncSessionTitle pushes the locally generated title to the sync service.
// It is deliberately best-effort: a title failure must not fail the answer.
func (a *App) syncSessionTitle(sessionID string) {
	sess, err := a.store.Session(sessionID)
	if err != nil || sess.SyncState != SyncSynced || sess.OwnerID != a.deviceID {
		return
	}
	s := a.Sync()
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 15*time.Second)
	defer cancel()
	if err := s.Rename(ctx, sessionID, sess.Title); err != nil {
		a.noteSyncError(err)
		return
	}
	a.clearSyncBlocked()
}

func (a *App) Rename(sessionID, title string) error {
	sess, err := a.store.Session(sessionID)
	if err != nil {
		return err
	}
	if sess.SyncState == SyncSynced {
		if sess.OwnerID != a.deviceID {
			return apierr.New(409, "READ_ONLY", "session is owned by another device")
		}
		if s := a.Sync(); s != nil {
			ctx, cancel := context.WithTimeout(a.baseCtx(), 15*time.Second)
			defer cancel()
			if err := s.Rename(ctx, sessionID, title); err != nil {
				return err
			}
			a.clearSyncBlocked()
		}
	}
	if err := a.store.SetSessionTitle(sessionID, title); err != nil {
		return err
	}
	a.emit(map[string]any{"type": "session_updated", "sessionId": sessionID})
	return nil
}

type DeleteAllResult struct {
	Deleted int `json:"deleted"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// DeleteAllOwned deletes every local session owned by this device. Sessions
// owned by another device are read-only and are intentionally left in place.
func (a *App) DeleteAllOwned() (DeleteAllResult, error) {
	var result DeleteAllResult
	sessions, err := a.store.Sessions()
	if err != nil {
		return result, err
	}
	for _, sess := range sessions {
		if sess.OwnerID != a.deviceID {
			result.Skipped++
			continue
		}
		if err := a.Delete(sess.ID); err != nil {
			result.Failed++
			continue
		}
		result.Deleted++
	}
	return result, nil
}

func (a *App) Delete(sessionID string) error {
	sess, err := a.store.Session(sessionID)
	if err != nil {
		return err
	}
	if sess.SyncState == SyncSynced {
		s := a.Sync()
		if s == nil {
			// 同步服务已不存在：这只是本地数据，删除是纯本地操作，不该被拦住。
			return a.deleteLocalSession(sessionID)
		}
		if sess.OwnerID != a.deviceID {
			return apierr.New(409, "READ_ONLY", "session is owned by another device")
		}
		ctx, cancel := context.WithTimeout(a.baseCtx(), 15*time.Second)
		defer cancel()
		if err := s.DeleteSession(ctx, sessionID); err != nil {
			var remote *RemoteError
			if errors.As(err, &remote) && remote.Code == "SessionNotFound" {
				return a.deleteLocalSession(sessionID)
			}
			return err
		}
		a.clearSyncBlocked()
	}
	return a.deleteLocalSession(sessionID)
}

// deleteLocalSession cancels any active generation and removes the session,
// its local rows, and its attachment files.
func (a *App) deleteLocalSession(sessionID string) error {
	a.mu.Lock()
	if at := a.active[sessionID]; at != nil {
		at.markPreempted()
		at.cancel()
	}
	delete(a.active, sessionID)
	a.mu.Unlock()

	paths, err := a.store.DeleteSessionWithAttachments(sessionID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		_ = os.Remove(path)
	}
	a.emit(map[string]any{"type": "session_deleted", "sessionId": sessionID})
	return nil
}

func (a *App) generate(ctx context.Context, sess model.Session, t model.Turn, at *activeTurn, reminder string) {
	defer func() {
		a.mu.Lock()
		if cur := a.active[sess.ID]; cur == at {
			delete(a.active, sess.ID)
		}
		a.mu.Unlock()
		at.cancel()
	}()

	settings := a.Settings()
	client := deepseek.New(settings.BaseURL, settings.APIKey)
	blocks := []map[string]any{}
	var stopReason string
	var usage *model.Usage
	var title string
	var streamErr error

	if settings.APIKey == "" {
		a.failTurn(sess, t, errors.New("尚未配置 API Key"))
		return
	}
	messages, hasFiles, err := a.buildMessages(ctx, client, sess, t, reminder)
	if err != nil {
		a.failTurn(sess, t, err)
		return
	}
	startedAt := time.Now()
	lastPartial := time.Time{}
	events, err := client.Stream(ctx, deepseek.Request{
		Model:       settings.Model,
		System:      deepseek.SystemPrompt,
		Messages:    messages,
		Thinking:    settings.Thinking,
		Search:      settings.Search,
		SafetyCheck: settings.SafetyCheck,
		ReadURL:     true,
		Title:       true,
		HasFiles:    hasFiles,
	})
	if err != nil {
		a.failTurn(sess, t, err)
		return
	}
	for ev := range events {
		switch ev.Type {
		case "block_start":
			for len(blocks) <= ev.Index {
				blocks = append(blocks, nil)
			}
			b := cloneMap(ev.Block)
			blocks[ev.Index] = b
			a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
			a.emit(map[string]any{"type": "block_start", "sessionId": sess.ID, "turnId": t.ID, "index": ev.Index, "block": b})
		case "text":
			b := ensureBlock(&blocks, ev.Index)
			b["text"] = strOf(b["text"]) + ev.Text
			if time.Since(lastPartial) > 150*time.Millisecond {
				a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
				lastPartial = time.Now()
			}
			a.emitDelta(sess.ID, t.ID, ev.Index, "text", ev.Text)
		case "thinking":
			b := ensureBlock(&blocks, ev.Index)
			b["thinking"] = strOf(b["thinking"]) + ev.Text
			if time.Since(lastPartial) > 150*time.Millisecond {
				a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
				lastPartial = time.Now()
			}
			a.emitDelta(sess.ID, t.ID, ev.Index, "thinking", ev.Text)
		case "signature":
			b := ensureBlock(&blocks, ev.Index)
			b["signature"] = ev.Signature
			a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
			a.emit(map[string]any{"type": "signature", "sessionId": sess.ID, "turnId": t.ID, "index": ev.Index, "signature": ev.Signature})
		case "citation":
			b := ensureBlock(&blocks, ev.Index)
			cits, _ := b["citations"].([]any)
			b["citations"] = append(cits, ev.Citation)
			a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
			a.emit(map[string]any{"type": "citation", "sessionId": sess.ID, "turnId": t.ID, "index": ev.Index, "citation": ev.Citation})
		case "block_stop":
			a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
		case "message_delta":
			if ev.StopReason != "" {
				stopReason = ev.StopReason
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
			if ev.Title != "" {
				title = ev.Title
			}
			a.setPartial(t.ID, blocks, settings.Model, settings.Thinking, settings.Search)
		case "message_stop":
			if ev.Usage != nil {
				usage = ev.Usage
			}
		case "error":
			streamErr = ev.Err
		}
	}

	a.clearPartial(t.ID)
	stopped, preempted := at.state()
	if preempted {
		_ = a.store.PutLocalOutput(t.ID, sess.ID, blocks, "preempted")
		a.emit(map[string]any{"type": "turn_preempted", "sessionId": sess.ID, "turnId": t.ID})
		return
	}
	status := model.StatusDone
	if streamErr != nil || (ctx.Err() != nil && !stopped) {
		status = model.StatusAborted
	}
	if stopped {
		status = model.StatusDone
		stopReason = "user_stopped"
	}
	if stopReason == "" && status == model.StatusDone {
		stopReason = "end_turn"
	}
	for len(blocks) > 0 && blocks[len(blocks)-1] == nil {
		blocks = blocks[:len(blocks)-1]
	}
	ans := model.Answer{
		Status:         status,
		StopReason:     stopReason,
		Blocks:         blocks,
		Usage:          usage,
		Model:          settings.Model,
		Thinking:       settings.Thinking,
		Search:         settings.Search,
		LatestReminder: reminder,
		Title:          title,
		DurationMs:     time.Since(startedAt).Milliseconds(),
		CreatedAt:      model.NowMS(),
	}
	state := SyncLocal
	if sess.SyncState == SyncSynced {
		state = SyncPending
	}
	if err := a.store.PutAnswer(t.ID, ans, state); err != nil {
		log.Printf("save answer: %v", err)
	}
	if title != "" {
		// The first append creates the remote session before the model returns
		// its title, so the title has to be pushed separately; otherwise every
		// other device sees an untitled session until someone renames it.
		_ = a.store.SetSessionTitleIfEmpty(sess.ID, title)
		a.syncSessionTitle(sess.ID)
	}
	_ = a.store.TouchSession(sess.ID)
	synced := false
	if state == SyncPending {
		if err := a.pushAnswer(sess.ID, t.ID, ans); err == nil {
			synced = true
		}
	}
	if streamErr != nil {
		a.emit(map[string]any{"type": "error", "sessionId": sess.ID, "turnId": t.ID, "message": streamErr.Error()})
	}
	a.emit(map[string]any{"type": "turn_finished", "sessionId": sess.ID, "turnId": t.ID, "status": ans.Status, "synced": synced, "answer": ans})
}

func (a *App) failTurn(sess model.Session, t model.Turn, cause error) {
	a.clearPartial(t.ID)
	ans := model.Answer{Status: model.StatusAborted, Blocks: []map[string]any{}, CreatedAt: model.NowMS()}
	state := SyncLocal
	if sess.SyncState == SyncSynced {
		state = SyncPending
	}
	_ = a.store.PutAnswer(t.ID, ans, state)
	_ = a.store.TouchSession(sess.ID)
	if state == SyncPending {
		_ = a.pushAnswer(sess.ID, t.ID, ans)
	}
	a.emit(map[string]any{"type": "error", "sessionId": sess.ID, "turnId": t.ID, "message": cause.Error()})
	a.emit(map[string]any{"type": "turn_finished", "sessionId": sess.ID, "turnId": t.ID, "status": ans.Status})
}

func (a *App) emitDelta(sessionID, turnID string, index int, kind, text string) {
	a.emit(map[string]any{"type": "delta", "sessionId": sessionID, "turnId": turnID, "index": index, "kind": kind, "text": text})
}

func ensureBlock(blocks *[]map[string]any, index int) map[string]any {
	for len(*blocks) <= index {
		*blocks = append(*blocks, map[string]any{})
	}
	b := (*blocks)[index]
	if b == nil {
		b = map[string]any{"type": "text"}
		(*blocks)[index] = b
	}
	return b
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}

func (a *App) buildMessages(ctx context.Context, client *deepseek.Client, sess model.Session, t model.Turn, reminder string) ([]any, bool, error) {
	turns, err := a.store.Turns(sess.ID)
	if err != nil {
		return nil, false, err
	}
	hist := chain(turns, t.ParentTurnID)
	messages := []any{}
	hasFiles := false
	// latest_reminder 是独立角色，放在对应提问之前；只在首次出现或内容变化时插入，
	// 与官方 demo 一致，避免每轮重复同一条。
	previous := ""
	appendReminder := func(value string) {
		if value == "" || value == previous {
			return
		}
		messages = append(messages, map[string]any{"role": "latest_reminder", "content": value})
		previous = value
	}
	for _, h := range hist {
		if !usableAnswer(h.Answer) {
			continue
		}
		uc, files, err := a.userContent(ctx, client, h.Q)
		if err != nil {
			return nil, false, err
		}
		if uc == nil {
			continue
		}
		ac := assistantContent(h.Answer)
		if ac == nil {
			continue
		}
		// 用这一轮当时的快照，而不是用当前时间重算。
		appendReminder(h.Answer.LatestReminder)
		messages = append(messages, map[string]any{"role": "user", "content": uc})
		messages = append(messages, map[string]any{"role": "assistant", "content": ac})
		hasFiles = hasFiles || files
	}
	uc, files, err := a.userContent(ctx, client, t.Q)
	if err != nil {
		return nil, false, err
	}
	if uc == nil {
		return nil, false, apierr.New(400, "EMPTY", "question is empty")
	}
	appendReminder(reminder)
	messages = append(messages, map[string]any{"role": "user", "content": uc})
	hasFiles = hasFiles || files
	return messages, hasFiles, nil
}

func usableAnswer(a *model.Answer) bool {
	if a == nil || a.Status != model.StatusDone {
		return false
	}
	if a.StopReason == "refusal" {
		return false
	}
	text := ""
	for _, b := range a.Blocks {
		if strOf(b["type"]) == "text" {
			text += strOf(b["text"])
		}
	}
	return strings.TrimSpace(text) != ""
}

func (a *App) userContent(ctx context.Context, client *deepseek.Client, q model.Q) (any, bool, error) {
	var blocks []any
	for _, att := range q.Attachments {
		fileID, err := a.ensureFileID(ctx, client, att)
		if err != nil {
			return nil, false, err
		}
		if fileID == "" {
			continue
		}
		typ := "document"
		if att.IsImage {
			typ = "image"
		}
		blocks = append(blocks, map[string]any{
			"type":   typ,
			"source": map[string]any{"type": "file", "file_id": fileID},
		})
	}
	if len(blocks) == 0 {
		if strings.TrimSpace(q.Text) == "" {
			return nil, false, nil
		}
		return q.Text, false, nil
	}
	if q.Text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": q.Text})
	}
	return blocks, true, nil
}

func assistantContent(a *model.Answer) []any {
	var out []any
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, map[string]any{"type": "text", "text": text.String()})
			text.Reset()
		}
	}
	for _, b := range a.Blocks {
		switch strOf(b["type"]) {
		case "text":
			s := strOf(b["text"])
			if strings.TrimSpace(s) == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(s)
		case "thinking":
			// Historical thinking is not sent back.
		case "server_tool_use":
			if strOf(b["kind"]) == "read_link" {
				flush()
				out = append(out, b)
			}
		case "web_fetch_tool_result":
			if strOf(b["kind"]) == "read_link" {
				flush()
				out = append(out, b)
			}
		}
	}
	flush()
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *App) onRemoteEvent(ev RemoteEvent) {
	switch ev.Type {
	case "hello":
		return
	case "session":
		if ev.OwnerDeviceID != "" && ev.OwnerDeviceID != a.deviceID {
			a.mu.Lock()
			if at := a.active[ev.SessionID]; at != nil {
				at.markPreempted()
				at.cancel()
			}
			a.mu.Unlock()
			_ = a.store.SetSessionOwner(ev.SessionID, ev.OwnerDeviceID)
		}
		go a.pullSession(a.baseCtx(), ev.SessionID)
	case "turn", "answer":
		go a.pullSession(a.baseCtx(), ev.SessionID)
	case "deleted":
		if err := a.deleteLocalSession(ev.SessionID); err != nil {
			log.Printf("delete session %s: %v", ev.SessionID, err)
		}
		return
	}
	a.emit(map[string]any{"type": "remote_changed", "sessionId": ev.SessionID})
}

func (a *App) pullSession(ctx context.Context, sessionID string) error {
	s := a.Sync()
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	turns, remote, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	local, err := a.store.Session(sessionID)
	if err != nil {
		now := model.NowMS()
		local = &model.Session{ID: sessionID, OwnerID: remote.OwnerID, CreatedAt: now, UpdatedAt: now, SyncState: SyncSynced, ServerID: a.currentServerID()}
		if err := a.store.CreateSession(*local); err != nil {
			return err
		}
	}
	// A detached/local session is never promoted to synced just because the
	// server happens to have a session with the same id. Only the remote-create
	// path above and an explicit successful append do that.
	if remote.OwnerID != "" && remote.OwnerID != local.OwnerID {
		_ = a.store.SetSessionOwner(sessionID, remote.OwnerID)
	}
	if remote.Title != "" && remote.Title != local.Title {
		_ = a.store.SetSessionTitle(sessionID, remote.Title)
	}
	for _, t := range turns {
		t.SessionID = sessionID
		_ = a.store.UpsertTurn(t)
		if t.Answer != nil {
			_ = a.store.PutAnswer(t.ID, *t.Answer, SyncSynced)
		}
		for _, att := range t.Q.Attachments {
			if _, err := a.ensureLocalAttachment(ctx, att, sessionID); err != nil {
				a.noteSyncError(err)
				log.Printf("pull attachment %s: %v", att.ID, err)
			}
		}
	}
	a.emit(map[string]any{"type": "remote_changed", "sessionId": sessionID})
	return nil
}

func (a *App) syncAll(ctx context.Context) {
	s := a.Sync()
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	local, localErr := a.store.Sessions()
	if localErr != nil {
		log.Printf("sync local list: %v", localErr)
		return
	}
	list, err := s.ListSessions(ctx)
	if err != nil {
		a.noteSyncError(err)
		log.Printf("sync list: %v", err)
		return
	}
	if reason := a.syncBlocked(); reason == "network" || reason == "server" || reason == "sync" {
		a.clearSyncBlocked()
	}
	remote := make(map[string]bool, len(list))
	for _, rs := range list {
		remote[rs.ID] = true
	}
	for _, rs := range list {
		if err := a.pullSession(ctx, rs.ID); err != nil {
			a.noteSyncError(err)
		}
	}
	// A soft-deleted session disappears from the remote list. Reconcile it
	// locally so devices that were offline during the delete also remove it.
	serverID := a.currentServerID()
	for _, sess := range local {
		// Only sessions belonging to this sessionsvr may be reconciled away.
		if sess.SyncState == SyncSynced && sess.ServerID == serverID && serverID != "" && !remote[sess.ID] {
			if err := a.deleteLocalSession(sess.ID); err != nil {
				log.Printf("sync delete %s: %v", sess.ID, err)
			}
		}
	}
	a.emit(map[string]any{"type": "sessions_changed"})
}

func (a *App) SelectVersion(sessionID, turnID string) error {
	t, err := a.store.Turn(turnID)
	if err != nil {
		return err
	}
	if t.SessionID != sessionID {
		return apierr.New(404, "TURN_NOT_FOUND", "turn not found")
	}
	if err := a.store.SetSelection(sessionID, t.ParentTurnID, t.ID); err != nil {
		return err
	}
	a.emit(map[string]any{"type": "session_updated", "sessionId": sessionID})
	return nil
}

func (a *App) setPartial(turnID string, blocks []map[string]any, modelName string, thinking, search bool) {
	a.partialMu.Lock()
	a.partials[turnID] = &model.Answer{
		Status:    model.StatusStreaming,
		Blocks:    cloneBlocks(blocks),
		Model:     modelName,
		Thinking:  thinking,
		Search:    search,
		CreatedAt: model.NowMS(),
	}
	a.partialMu.Unlock()
}

func (a *App) clearPartial(turnID string) {
	a.partialMu.Lock()
	delete(a.partials, turnID)
	a.partialMu.Unlock()
}

func (a *App) getPartial(turnID string) *model.Answer {
	a.partialMu.Lock()
	defer a.partialMu.Unlock()
	answer, ok := a.partials[turnID]
	if !ok {
		return nil
	}
	out := *answer
	out.Blocks = cloneBlocks(answer.Blocks)
	return &out
}

func cloneBlocks(blocks []map[string]any) []map[string]any {
	if len(blocks) == 0 {
		return []map[string]any{}
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return []map[string]any{}
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}
