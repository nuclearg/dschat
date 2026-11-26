package chatsvr

import (
	"io"
	"testing"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
)

func TestDeleteAllRemovesAllLocalSessions(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	now := model.NowMS()
	for _, sess := range []model.Session{
		{ID: "owned", Title: "mine", OwnerID: app.deviceID, SyncState: SyncLocal, CreatedAt: now, UpdatedAt: now},
		{ID: "other", Title: "theirs", OwnerID: "other-device", SyncState: SyncLocal, CreatedAt: now, UpdatedAt: now},
	} {
		if err := app.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	res, err := app.DeleteAll()
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, id := range []string{"owned", "other"} {
		if _, err := app.store.Session(id); err == nil {
			t.Fatalf("session %s still exists", id)
		}
	}
}

func TestDetachSyncedSessionsOnServerSwitch(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	now := model.NowMS()
	synced := model.Session{ID: "cloud", Title: "cloud", OwnerID: "old-device", SyncState: SyncSynced, ServerID: "server-a", CreatedAt: now, UpdatedAt: now}
	local := model.Session{ID: "local", Title: "local", OwnerID: app.deviceID, SyncState: SyncLocal, CreatedAt: now, UpdatedAt: now}
	for _, sess := range []model.Session{synced, local} {
		if err := app.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.DetachSyncedSessions(app.deviceID); err != nil {
		t.Fatal(err)
	}
	got, err := app.store.Session("cloud")
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncState != SyncLocal || got.ServerID != "" || got.OwnerID != app.deviceID {
		t.Fatalf("detached = %+v", got)
	}
	got, err = app.store.Session("local")
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncState != SyncLocal || got.OwnerID != app.deviceID {
		t.Fatalf("local changed = %+v", got)
	}
}

func TestSyncBlockedTransferState(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	sess := model.Session{ID: "s", OwnerID: app.deviceID, SyncState: SyncLocal}
	if got := app.transferState(sess, false, false); got != "local" {
		t.Fatalf("initial = %s", got)
	}
	app.setSyncBlocked("auth")
	if got := app.transferState(sess, false, false); got != "blocked" {
		t.Fatalf("blocked = %s", got)
	}
	if reason := app.syncBlocked(); reason != "auth" {
		t.Fatalf("reason = %q", reason)
	}
	app.clearSyncBlocked()
	if got := app.transferState(sess, false, false); got != "local" {
		t.Fatalf("cleared = %s", got)
	}
}

func TestSyncErrorClassification(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	tests := []struct {
		name       string
		err        error
		wantBlock  string
		wantDetail string
	}{
		{name: "network", err: io.ErrUnexpectedEOF, wantBlock: "network"},
		{name: "limit", err: &RemoteError{Code: "StorageLimit", Message: "full"}, wantBlock: "limit", wantDetail: "StorageLimit: full"},
		{name: "local validation", err: apierr.New(400, "EMPTY", "question is empty")},
		{name: "not owner", err: &RemoteError{Code: "NotOwner", Message: "not owner"}},
		{name: "parent open", err: &RemoteError{Code: "ParentOpen", Message: "parent is open"}},
		{name: "turn exists", err: &RemoteError{Code: "TurnExists", Message: "turn exists"}},
		{name: "answer exists", err: &RemoteError{Code: "AnswerExists", Message: "answer exists"}},
		{name: "open turn", err: &RemoteError{Code: "OpenTurn", Message: "open turn"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app.clearSyncBlocked()
			app.noteSyncError(tt.err)
			if got := app.syncBlocked(); got != tt.wantBlock {
				t.Fatalf("block = %q, want %q", got, tt.wantBlock)
			}
			if tt.wantDetail != "" {
				if got := app.syncBlockedDetail(); got != tt.wantDetail {
					t.Fatalf("detail = %q, want %q", got, tt.wantDetail)
				}
			}
		})
	}
}
