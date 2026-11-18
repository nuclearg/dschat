package chatsvr

import (
	"context"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"dschat/app/core/apierr"
	"dschat/app/core/deepseek"
	"dschat/app/core/model"
)

const maxAttachmentSize = 64 << 20

func (a *App) SaveAttachment(sessionID, name, mimeType string, r io.Reader, size int64) (*model.Attachment, error) {
	if size <= 0 || size > maxAttachmentSize {
		return nil, apierr.New(400, "FILE_TOO_LARGE", "attachment must be between 1 byte and 64 MB")
	}
	id := model.NewID()
	dir := filepath.Join(a.cfg.DataDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
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
		return nil, apierr.New(400, "FILE_TOO_LARGE", "attachment is too large")
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	isImage := strings.HasPrefix(mimeType, "image/")
	att := model.Attachment{ID: id, Name: name, MIME: mimeType, Size: n, IsImage: isImage}
	row := Attachment{
		ID: id, SessionID: sessionID, Name: name, MIME: mimeType, Size: n,
		Path: path, IsImage: isImage, CreatedAt: model.NowMS(),
	}
	if err := a.store.PutAttachment(row); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &att, nil
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

func (a *App) ensureFileID(ctx context.Context, client *deepseek.Client, attachmentID string) (string, error) {
	if attachmentID == "" {
		return "", nil
	}
	row, err := a.store.Attachment(attachmentID)
	if err != nil {
		return "", err
	}
	if row.FileID != "" {
		return row.FileID, nil
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
	if err := a.store.SetAttachmentFileID(attachmentID, id); err != nil {
		return "", err
	}
	return id, nil
}

func uploadPurpose(name, mimeType string) string {
	if strings.HasPrefix(mimeType, "image/") {
		return ""
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".doc", ".docx", ".ppt", ".pptx":
		return "rich_layout_recognition"
	default:
		return "user_data"
	}
}
