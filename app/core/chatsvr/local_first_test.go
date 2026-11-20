package chatsvr

import (
	"net/http/httptest"
	"testing"

	"dschat/app/core/model"
)

// seedSession 造一个会话，按需标成已同步。
func seedSession(t *testing.T, app *App, id, syncState string) {
	t.Helper()
	sess := model.Session{
		ID: id, Title: "t", OwnerID: app.deviceID,
		SyncState: syncState, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
}

func sessionState(t *testing.T, app *App, id string) string {
	t.Helper()
	sess, err := app.store.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	return sess.SyncState
}

// 断开数据同步服务后，已同步会话必须回到本地，否则会被同步服务挡住而无法使用。
func TestDisconnectDetachesSyncedSessions(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	seedSession(t, app, "s1", SyncSynced)
	seedSession(t, app, "s2", SyncSynced)

	rec := httptest.NewRecorder()
	app.handleDisconnect(rec, httptest.NewRequest("POST", "/api/server/disconnect", nil))
	if rec.Code != 200 {
		t.Fatalf("disconnect status = %d, body = %s", rec.Code, rec.Body.String())
	}

	for _, id := range []string{"s1", "s2"} {
		if state := sessionState(t, app, id); state != SyncLocal {
			t.Fatalf("session %s sync state = %q, want local", id, state)
		}
	}
}

// 同步服务配置已经不存在、但会话还留着 synced 标记时，发消息要就地降级为本地。
func TestSendOnOrphanedSyncedSessionDegradesToLocal(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	seedSession(t, app, "s1", SyncSynced)
	if app.Sync() != nil {
		t.Fatal("test expects no sync service")
	}

	turn, err := app.Send("s1", SendRequest{Text: "hi"})
	if err != nil {
		t.Fatalf("Send() = %v, want no error", err)
	}
	if turn == nil {
		t.Fatal("Send() returned no turn")
	}
	if state := sessionState(t, app, "s1"); state != SyncLocal {
		t.Fatalf("sync state = %q, want local", state)
	}
}

// 没有同步服务时，删除一个被标成 synced 的会话应当直接删本地，而不是报错。
func TestDeleteSyncedSessionWithoutServer(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	seedSession(t, app, "s1", SyncSynced)
	if err := app.Delete("s1"); err != nil {
		t.Fatalf("Delete() = %v, want no error", err)
	}
	if _, err := app.store.Session("s1"); err == nil {
		t.Fatal("session still exists after delete")
	}
}

// 没有同步服务时点「重试同步」不应报错：会话落成本地即可。
func TestSyncPendingWithoutServerDegradesToLocal(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	seedSession(t, app, "s1", SyncSynced)
	if err := app.SyncPending("s1"); err != nil {
		t.Fatalf("SyncPending() = %v, want no error", err)
	}
	if state := sessionState(t, app, "s1"); state != SyncLocal {
		t.Fatalf("sync state = %q, want local", state)
	}
}

// 启动时如果没有任何同步配置，遗留的 synced 标记应当立刻纠正，界面才不会误显示「已同步」。
func TestStartupDetachesSyncedSessionsWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	app, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// 没有同步配置的情况下留下一条 synced 会话（例如配置被清掉、数据目录被搬过）。
	seedSession(t, app, "s1", SyncSynced)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Sync() != nil {
		t.Fatal("test expects no sync service")
	}
	if state := sessionState(t, reopened, "s1"); state != SyncLocal {
		t.Fatalf("sync state = %q, want local", state)
	}
}
