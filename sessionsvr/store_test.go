package main

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
)

func TestAppendClaimAndOpenTurn(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	d1, _, err := st.CreateDevice("", "dev1", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := st.CreateDevice("", "dev2", "android")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession("", "s", d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A turn whose parent is not closed must be rejected.
	err = st.AppendTurn(sess.ID, d1.ID, model.Turn{ID: "T2", ParentTurnID: "missing", Q: model.Q{Text: "x"}})
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != "PARENT_OPEN" {
		t.Fatalf("append with invalid parent = %v, want PARENT_OPEN", err)
	}
	if err := st.AppendTurn(sess.ID, d1.ID, model.Turn{ID: "T1", ParentTurnID: "0", Q: model.Q{Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	// Non-owners cannot answer.
	if err := st.AppendAnswer(sess.ID, d2.ID, "T1", model.Answer{Status: model.StatusDone}); err == nil {
		t.Fatal("expected NOT_OWNER")
	}
	if err := st.AppendAnswer(sess.ID, d1.ID, "T1", model.Answer{Status: model.StatusDone, Blocks: []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	// Open a second turn, then try to claim without force.
	if err := st.AppendTurn(sess.ID, d1.ID, model.Turn{ID: "T2", ParentTurnID: "T1", Q: model.Q{Text: "again"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim(sess.ID, d2.ID, false); err == nil {
		t.Fatal("expected OPEN_TURN")
	}
	s, err := st.Claim(sess.ID, d2.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.OwnerID != d2.ID {
		t.Fatalf("owner = %s, want %s", s.OwnerID, d2.ID)
	}
	// The new owner can close the open turn; the old owner cannot.
	if err := st.AppendAnswer(sess.ID, d1.ID, "T2", model.Answer{Status: model.StatusAborted}); err == nil {
		t.Fatal("old owner should not be able to answer")
	}
	if err := st.AppendAnswer(sess.ID, d2.ID, "T2", model.Answer{Status: model.StatusAborted, Blocks: []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	_, turns, err := st.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[1].Answer == nil || turns[1].Answer.Status != model.StatusAborted {
		t.Fatalf("turns = %+v", turns)
	}
}

func TestSaveAttachment(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SaveAttachment(Attachment{ID: "A1", Name: "a.txt", MIME: "text/plain"}, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	path, meta, err := st.AttachmentPath("A1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != 5 || meta.Name != "a.txt" || meta.MIME != "text/plain" {
		t.Fatalf("meta = %+v", meta)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("blob = %q", data)
	}

	// Re-uploading the same id replaces the blob.
	if err := st.SaveAttachment(Attachment{ID: "A1", Name: "a.txt", MIME: "text/plain"}, strings.NewReader("hey")); err != nil {
		t.Fatal(err)
	}
	if path, meta, err = st.AttachmentPath("A1"); err != nil {
		t.Fatal(err)
	}
	if meta.Size != 3 {
		t.Fatalf("size after replace = %d", meta.Size)
	}
	if data, err = os.ReadFile(path); err != nil || string(data) != "hey" {
		t.Fatalf("blob after replace = %q, %v", data, err)
	}

	if _, _, err := st.AttachmentPath("missing"); err == nil {
		t.Fatal("expected ATTACHMENT_NOT_FOUND")
	}
}

func TestPasswordAuthTokens(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, _, err := st.CreateDevice("dev-auth", "dev", "test")
	if err != nil {
		t.Fatal(err)
	}
	access, refresh, _, err := st.CreateAuthSession(d.ID, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.DeviceByAccess(access)
	if err != nil || got.ID != d.ID {
		t.Fatalf("DeviceByAccess = %+v, %v", got, err)
	}
	newAccess, newRefresh, _, got, err := st.RotateRefresh(refresh, time.Minute, time.Hour)
	if err != nil || got.ID != d.ID {
		t.Fatalf("RotateRefresh = %+v, %v", got, err)
	}
	if _, err := st.DeviceByAccess(access); err == nil {
		t.Fatal("old access token should be revoked after rotation")
	}
	if _, _, _, _, err := st.RotateRefresh(refresh, time.Minute, time.Hour); err == nil {
		t.Fatal("old refresh token must not be reusable")
	}
	if _, err := st.DeviceByAccess(newAccess); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeRefresh(newRefresh); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := st.RotateRefresh(newRefresh, time.Minute, time.Hour); err == nil {
		t.Fatal("revoked refresh token must fail")
	}
}

func TestSoftDeleteAndCleanup(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := model.NowMS()
	sess, err := st.CreateSession("s-soft", "title", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendTurn(sess.ID, "dev", model.Turn{
		ID: "t-soft", SessionID: sess.ID, ParentTurnID: "0",
		Q: model.Q{Text: "hello"}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	att := Attachment{ID: "att-soft", SessionID: sess.ID, Name: "a.txt", MIME: "text/plain", CreatedAt: now}
	if err := st.SaveAttachment(att, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	path, _, err := st.AttachmentPath(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSession(sess.ID, "dev"); err != nil {
		t.Fatal(err)
	}
	if list, err := st.ListSessions(); err != nil || len(list) != 1 || list[0].DeletedAt == 0 {
		t.Fatalf("list after delete = %v, %v", list, err)
	}
	if _, _, err := st.GetSession(sess.ID); err == nil {
		t.Fatal("deleted session still readable")
	}
	n, err := st.CleanupExpired(now + int64(DeletedSessionTTL/time.Millisecond) + 1000)
	if err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("attachment file still exists: %v", err)
	}
	if _, _, err := st.GetSession(sess.ID); err == nil {
		t.Fatal("expired session still readable")
	}
}

func TestSchemaV1(t *testing.T) {
	if ProtocolVersion != 1 {
		t.Fatalf("protocol = %d", ProtocolVersion)
	}
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.db.Query(`pragma table_info(sessions)`)
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	rows.Close()
	if !columns["deleted_at"] {
		t.Fatalf("sessions columns = %v", columns)
	}
	var version string
	if err := st.db.QueryRow(`select value from meta where key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "1" {
		t.Fatalf("schema version = %q", version)
	}
}
