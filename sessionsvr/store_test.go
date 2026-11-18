package main

import (
	"testing"

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
