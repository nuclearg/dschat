package chatsvr

import (
	"context"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/deepseek"
	"dschat/app/core/model"
)

const (
	maxAttachmentSize = 64 << 20
	// DeepSeek 文件默认不传 expires_after 会永久有效；我们上传 7 天并提前重传。
	fileIDTTL = 6 * 24 * time.Hour
)

func (a *App) SaveAttachment(sessionID, name, mimeType string, r io.Reader, size int64) (*model.Attachment, error) {
	row, err := a.saveAttachmentAt(model.NewID(), sessionID, name, mimeType, r, size)
	if err != nil {
		return nil, err
	}
	return &model.Attachment{
		ID: row.ID, Name: row.Name, MIME: row.MIME, Size: row.Size, IsImage: row.IsImage,
	}, nil
}

// saveAttachmentAt writes a blob for a known attachment id (local upload or sync download).
func (a *App) saveAttachmentAt(id, sessionID, name, mimeType string, r io.Reader, size int64) (*Attachment, error) {
	if id == "" || strings.ContainsAny(id, "/\\") {
		return nil, apierr.New(400, "INVALID_ATTACHMENT", "invalid attachment id")
	}
	if size > maxAttachmentSize {
		return nil, apierr.New(413, "FILE_TOO_LARGE", "attachment is too large")
	}
	dir := filepath.Join(a.cfg.DataDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxAttachmentSize+1))
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return nil, closeErr
	}
	if n > maxAttachmentSize {
		_ = os.Remove(path)
		return nil, apierr.New(413, "FILE_TOO_LARGE", "attachment is too large")
	}
	if name == "" {
		name = id
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	isImage := strings.HasPrefix(mimeType, "image/")
	row := Attachment{
		ID: id, SessionID: sessionID, Name: name, MIME: mimeType, Size: n,
		Path: path, IsImage: isImage, CreatedAt: model.NowMS(),
	}
	if err := a.store.PutAttachment(row); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &row, nil
}

func (a *App) loadAttachments(ids []string) ([]model.Attachment, error) {
	out := make([]model.Attachment, 0, len(ids))
	for _, id := range ids {
		row, err := a.store.Attachment(id)
		if err != nil {
			return nil, err
		}
		out = append(out, model.Attachment{
			ID: row.ID, Name: row.Name, MIME: row.MIME, Size: row.Size, IsImage: row.IsImage,
		})
	}
	return out, nil
}

func (a *App) OpenAttachment(id string) (*Attachment, error) {
	return a.store.Attachment(id)
}

// ensureRemoteAttachments uploads any missing blobs to sessionsvr before a turn is appended.
func (a *App) ensureRemoteAttachments(ctx context.Context, sessionID string, atts []model.Attachment) error {
	if len(atts) == 0 {
		return nil
	}
	s := a.Sync()
	if s == nil {
		return nil
	}
	for _, att := range atts {
		ok, err := s.HasAttachment(ctx, att.ID)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		row, err := a.store.Attachment(att.ID)
		if err != nil {
			return err
		}
		if row.Path == "" {
			continue // metadata-only attachment from an imported DeepSeek archive
		}
		f, err := os.Open(row.Path)
		if err != nil {
			return err
		}
		err = s.PutAttachment(ctx, sessionID, att, f, row.Size)
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// ensureLocalAttachment downloads a blob from sessionsvr if this device does not have it yet.
// File ids are deliberately not synced: every device uploads with its own DeepSeek key.
func (a *App) ensureLocalAttachment(ctx context.Context, att model.Attachment, sessionID string) (*Attachment, error) {
	if row, err := a.store.Attachment(att.ID); err == nil {
		if _, statErr := os.Stat(row.Path); statErr == nil {
			return row, nil
		}
	}
	s := a.Sync()
	if s == nil {
		return nil, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment not found")
	}
	rc, name, mimeType, err := s.GetAttachment(ctx, att.ID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if name == "" {
		name = att.Name
	}
	if mimeType == "" {
		mimeType = att.MIME
	}
	return a.saveAttachmentAt(att.ID, sessionID, name, mimeType, rc, 0)
}

func (a *App) fetchRemoteAttachment(ctx context.Context, id string) (*Attachment, error) {
	s := a.Sync()
	if s == nil {
		return nil, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment not found")
	}
	rc, name, mimeType, err := s.GetAttachment(ctx, id)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return a.saveAttachmentAt(id, "", name, mimeType, rc, 0)
}

func (a *App) ensureFileID(ctx context.Context, client *deepseek.Client, att model.Attachment) (string, error) {
	row, err := a.store.Attachment(att.ID)
	if err != nil {
		row, err = a.ensureLocalAttachment(ctx, att, "")
		if err != nil {
			return "", err
		}
	}
	if row.FileID != "" && row.FileUploadedAt > 0 && time.Since(time.UnixMilli(row.FileUploadedAt)) < fileIDTTL {
		return row.FileID, nil
	}
	if row.Path == "" {
		return "", nil
	}
	if _, err := os.Stat(row.Path); err != nil {
		row, err = a.ensureLocalAttachment(ctx, att, row.SessionID)
		if err != nil {
			return "", err
		}
	}
	f, err := os.Open(row.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	purpose := uploadPurpose(row.Name, row.MIME)
	id, err := client.UploadFile(ctx, row.Name, row.MIME, f, purpose)
	if err != nil {
		return "", err
	}
	if err := a.store.SetAttachmentFileID(row.ID, id, model.NowMS()); err != nil {
		return "", err
	}
	return id, nil
}

func uploadPurpose(name, mimeType string) string {
	if strings.HasPrefix(mimeType, "image/") {
		return "user_data"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".doc", ".docx", ".ppt", ".pptx":
		return "rich_layout_recognition"
	default:
		return "user_data"
	}
}
