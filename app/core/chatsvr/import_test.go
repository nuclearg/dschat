package chatsvr

import (
	"testing"

	"dschat/app/core/model"
)

func TestImportSessionBuildsTurnTree(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	user := ImportMessage{
		ID: "u1", Role: "user", Content: "hello", CreatedAt: 1000,
		Files: []ImportFile{{Name: "doc.pdf", MIME: "application/pdf", Size: 10, IsImage: false}},
	}
	assistant := ImportMessage{
		ID: "a1", Role: "assistant", CreatedAt: 2000,
		ParentID: strPtr("u1"), Status: "done", Thinking: true,
		Blocks: []map[string]any{{"type": "thinking", "thinking": "hm"}, {"type": "text", "text": "hi"}},
	}
	res, err := app.ImportSession(ImportSessionRequest{
		Title: "imported", CreatedAt: 500, UpdatedAt: 3000,
		Messages: []ImportMessage{user, assistant},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.SyncState != SyncLocal {
		t.Fatalf("sync state = %s", res.Session.SyncState)
	}
	turns, err := app.store.Turns(res.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].Q.Text != "hello" || turns[0].ParentTurnID != model.RootTurnID {
		t.Fatalf("turn = %+v", turns[0])
	}
	if len(turns[0].Q.Attachments) != 1 || turns[0].Q.Attachments[0].Name != "doc.pdf" {
		t.Fatalf("attachments = %+v", turns[0].Q.Attachments)
	}
	if turns[0].Answer == nil || turns[0].Answer.Status != model.StatusDone || len(turns[0].Answer.Blocks) != 2 {
		t.Fatalf("answer = %+v", turns[0].Answer)
	}

	// Importing the same payload again must create a second copy.
	res2, err := app.ImportSession(ImportSessionRequest{
		Title: "imported", CreatedAt: 500, UpdatedAt: 3000,
		Messages: []ImportMessage{user, assistant},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Session.ID == res.Session.ID {
		t.Fatal("duplicate import reused the session id")
	}
}

func strPtr(s string) *string { return &s }
