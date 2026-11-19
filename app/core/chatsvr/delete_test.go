package chatsvr

import (
	"testing"

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
