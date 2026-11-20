package chatsvr

import (
	"io"
	"testing"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
)

func TestDeleteAllOwnedKeepsOtherDevices(t *testing.T) {
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
	res, err := app.DeleteAllOwned()
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 || res.Skipped != 1 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := app.store.Session("owned"); err == nil {
		t.Fatal("owned session still exists")
	}
	if _, err := app.store.Session("other"); err != nil {
		t.Fatalf("other device's session was deleted: %v", err)
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

func TestAnySyncErrorBlocks(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	app.noteSyncError(io.ErrUnexpectedEOF)
	if got := app.syncBlocked(); got != "network" {
		t.Fatalf("network block = %q", got)
	}
	app.clearSyncBlocked()
	app.noteSyncError(&RemoteError{Code: "StorageLimit", Message: "full"})
	if got := app.syncBlocked(); got != "limit" {
		t.Fatalf("limit block = %q", got)
	}
	app.clearSyncBlocked()
	app.noteSyncError(apierr.New(400, "EMPTY", "question is empty"))
	if got := app.syncBlocked(); got != "" {
		t.Fatalf("local validation should not block: %q", got)
	}
	app.noteSyncError(&RemoteError{Code: "NotOwner", Message: "not owner"})
	if got := app.syncBlocked(); got != "sync" {
		t.Fatalf("remote block = %q", got)
	}
	if got := app.syncBlockedDetail(); got != "NotOwner: not owner" {
		t.Fatalf("block detail = %q", got)
	}
}
