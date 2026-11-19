package chatsvr

import (
	"context"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
)

// ImportSessionRequest is the browser-side representation of one DeepSeek
// conversation after the archive has been parsed. Turn IDs are not supplied:
// every import creates a fresh dschat tree, so importing the same ZIP twice
// intentionally creates two copies.
type ImportSessionRequest struct {
	Title     string          `json:"title"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
	Messages  []ImportMessage `json:"messages"`
}

type ImportMessage struct {
	ID              string           `json:"id"`
	Role            string           `json:"role"`
	Content         string           `json:"content"`
	CreatedAt       int64            `json:"createdAt"`
	ParentID        *string          `json:"parentId"`
	SelectedChildID *string          `json:"selectedChildId"`
	Files           []ImportFile     `json:"files"`
	Blocks          []map[string]any `json:"blocks"`
	Status          string           `json:"status"`
	Thinking        bool             `json:"thinking"`
	Search          bool             `json:"search"`
}

type ImportFile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	Size    int64  `json:"size"`
	IsImage bool   `json:"isImage"`
}

type ImportSessionResult struct {
	Session model.Session `json:"session"`
	Synced  bool          `json:"synced"`
	Warning string        `json:"warning,omitempty"`
}

func (a *App) ImportSession(req ImportSessionRequest) (*ImportSessionResult, error) {
	if len(req.Messages) == 0 {
		return nil, apierr.New(400, "EMPTY_IMPORT", "archive contains no messages")
	}
	now := model.NowMS()
	created := req.CreatedAt
	if created <= 0 {
		created = now
	}
	updated := req.UpdatedAt
	if updated <= 0 {
		updated = now
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "导入的对话"
	}
	sess := model.Session{
		ID: model.NewID(), Title: title, OwnerID: a.deviceID,
		SyncState: SyncLocal, CreatedAt: created, UpdatedAt: updated,
	}
	if err := a.store.CreateSession(sess); err != nil {
		return nil, err
	}
	byID := make(map[string]*ImportMessage, len(req.Messages))
	children := map[string][]*ImportMessage{}
	for i := range req.Messages {
		m := &req.Messages[i]
		byID[m.ID] = m
		if m.ParentID != nil {
			children[*m.ParentID] = append(children[*m.ParentID], m)
		}
	}
	turnByAssistant := map[string]string{}
	turns := make([]model.Turn, 0, len(req.Messages))

	var walkUser func(u *ImportMessage) error
	walkUser = func(u *ImportMessage) error {
		parentTurn := model.RootTurnID
		if u.ParentID != nil {
			if id, ok := turnByAssistant[*u.ParentID]; ok {
				parentTurn = id
			}
		}
		atts, err := a.importAttachments(sess.ID, u.Files, now)
		if err != nil {
			return err
		}
		assistants := make([]*ImportMessage, 0, len(children[u.ID]))
		for _, child := range children[u.ID] {
			if child.Role == "assistant" {
				assistants = append(assistants, child)
			}
		}

		var chosen string
		selected := ""
		if len(assistants) == 0 {
			t := model.Turn{
				ID: model.NewID(), SessionID: sess.ID, ParentTurnID: parentTurn,
				Q:         model.Q{Text: u.Content, Attachments: atts, CreatedAt: u.CreatedAt},
				CreatedAt: u.CreatedAt,
			}
			if t.CreatedAt == 0 {
				t.CreatedAt = now
			}
			if err := a.store.UpsertTurn(t); err != nil {
				return err
			}
			turns = append(turns, t)
			chosen = t.ID
		} else {
			for _, as := range assistants {
				turnID := model.NewID()
				answer := model.Answer{
					Status:    importAnswerStatus(as.Status),
					Blocks:    as.Blocks,
					Thinking:  as.Thinking,
					Search:    as.Search,
					CreatedAt: as.CreatedAt,
				}
				if answer.Blocks == nil {
					answer.Blocks = []map[string]any{}
				}
				if answer.CreatedAt == 0 {
					answer.CreatedAt = u.CreatedAt
				}
				t := model.Turn{
					ID: turnID, SessionID: sess.ID, ParentTurnID: parentTurn,
					Q:         model.Q{Text: u.Content, Attachments: atts, CreatedAt: u.CreatedAt},
					CreatedAt: u.CreatedAt, Answer: &answer,
				}
				if t.CreatedAt == 0 {
					t.CreatedAt = now
				}
				if err := a.store.UpsertTurn(t); err != nil {
					return err
				}
				if err := a.store.PutAnswer(turnID, answer, SyncLocal); err != nil {
					return err
				}
				turns = append(turns, t)
				turnByAssistant[as.ID] = turnID
				chosen = turnID
				if u.SelectedChildID != nil && *u.SelectedChildID == as.ID {
					selected = turnID
				}
			}
			if selected != "" {
				chosen = selected
			}
		}
		if err := a.store.SetSelection(sess.ID, parentTurn, chosen); err != nil {
			return err
		}
		for _, as := range assistants {
			for _, next := range children[as.ID] {
				if next.Role == "user" {
					if err := walkUser(next); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}

	roots := make([]*ImportMessage, 0)
	for i := range req.Messages {
		m := &req.Messages[i]
		if m.Role == "user" && m.ParentID == nil {
			roots = append(roots, m)
		}
	}
	if len(roots) == 0 {
		for i := range req.Messages {
			if req.Messages[i].Role == "user" {
				roots = append(roots, &req.Messages[i])
				break
			}
		}
	}
	if len(roots) == 0 {
		_ = a.store.DeleteSession(sess.ID)
		return nil, apierr.New(400, "EMPTY_IMPORT", "archive contains no user messages")
	}
	for _, root := range roots {
		if err := walkUser(root); err != nil {
			return nil, err
		}
	}
	if len(turns) == 0 {
		_ = a.store.DeleteSession(sess.ID)
		return nil, apierr.New(400, "EMPTY_IMPORT", "archive contains no turns")
	}
	// A missing attachment is metadata-only; it is displayed but skipped from
	// future model requests.
	a.emit(map[string]any{"type": "session_created", "sessionId": sess.ID})

	synced := false
	warning := ""
	if a.Sync() != nil {
		if err := a.syncImportedSession(&sess, turns); err != nil {
			a.noteSyncError(err)
			warning = err.Error()
		} else {
			synced = true
			sess.SyncState = SyncSynced
			a.clearSyncBlocked()
		}
	}
	return &ImportSessionResult{Session: sess, Synced: synced, Warning: warning}, nil
}

func importAnswerStatus(status string) string {
	if strings.EqualFold(status, "done") || strings.EqualFold(status, "finished") {
		return model.StatusDone
	}
	return model.StatusAborted
}

func (a *App) importAttachments(sessionID string, files []ImportFile, now int64) ([]model.Attachment, error) {
	out := make([]model.Attachment, 0, len(files))
	for _, f := range files {
		var row *Attachment
		if id := strings.TrimSpace(f.ID); id != "" {
			if existing, err := a.store.Attachment(id); err == nil {
				row = existing
				_ = a.store.SetAttachmentSessionID(id, sessionID)
				// The browser upload often has no MIME (ZIP entries have no
				// type); restore image identity from the archive metadata.
				if f.IsImage && (!existing.IsImage || !strings.HasPrefix(existing.MIME, "image/")) {
					imageMIME := f.MIME
					if !strings.HasPrefix(imageMIME, "image/") {
						imageMIME = mime.TypeByExtension(filepath.Ext(existing.Name))
					}
					if !strings.HasPrefix(imageMIME, "image/") {
						imageMIME = "image/png"
					}
					_ = a.store.SetAttachmentImageMeta(id, imageMIME, true)
					existing.MIME = imageMIME
					existing.IsImage = true
				}
			}
		}
		if row == nil {
			name := strings.TrimSpace(f.Name)
			if name == "" {
				name = "attachment"
			}
			mimeType := f.MIME
			if mimeType == "" {
				mimeType = mime.TypeByExtension(filepath.Ext(name))
			}
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			isImage := f.IsImage || strings.HasPrefix(mimeType, "image/")
			created := Attachment{
				ID: model.NewID(), SessionID: sessionID, Name: name, MIME: mimeType,
				Size: f.Size, Path: "", IsImage: isImage, CreatedAt: now,
			}
			if err := a.store.PutAttachment(created); err != nil {
				return nil, err
			}
			row = &created
		}
		out = append(out, model.Attachment{
			ID: row.ID, Name: row.Name, MIME: row.MIME, Size: row.Size, IsImage: row.IsImage,
		})
	}
	return out, nil
}

func (a *App) syncImportedSession(sess *model.Session, turns []model.Turn) error {
	s := a.Sync()
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.baseCtx(), 2*time.Minute)
	defer cancel()
	if err := a.ensureRemoteSession(ctx, *sess); err != nil {
		return err
	}
	for _, t := range turns {
		if err := a.ensureRemoteAttachments(ctx, sess.ID, t.Q.Attachments); err != nil {
			return err
		}
		if err := s.AppendTurn(ctx, sess.ID, t); err != nil {
			return err
		}
		if t.Answer != nil {
			if err := s.AppendAnswer(ctx, sess.ID, t.ID, *t.Answer); err != nil {
				return err
			}
		}
	}
	if err := a.store.SetSessionSynced(sess.ID, a.currentServerID()); err != nil {
		return err
	}
	for _, t := range turns {
		if t.Answer != nil {
			_ = a.store.SetAnswerSync(t.ID, SyncSynced)
		}
	}
	a.emit(map[string]any{"type": "session_updated", "sessionId": sess.ID})
	return nil
}
