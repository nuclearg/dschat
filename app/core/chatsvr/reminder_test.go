package chatsvr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dschat/app/core/model"
)

// seedTurn 造一条「已完成的问答」，answer 带上该轮生效的 latest_reminder 快照。
func seedTurn(t *testing.T, app *App, sessID, id, parent, text, reminder string) {
	t.Helper()
	turn := model.Turn{
		ID: id, SessionID: sessID, ParentTurnID: parent,
		Q:         model.Q{Text: text, CreatedAt: 1},
		CreatedAt: 1,
	}
	if err := app.store.UpsertTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetSelection(sessID, parent, id); err != nil {
		t.Fatal(err)
	}
	ans := model.Answer{
		Status:         model.StatusDone,
		StopReason:     "end_turn",
		Blocks:         []map[string]any{{"type": "text", "text": "ok"}},
		LatestReminder: reminder,
		CreatedAt:      2,
	}
	if err := app.store.PutAnswer(id, ans, SyncLocal); err != nil {
		t.Fatal(err)
	}
}

func rolesOf(messages []any) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, strOf(m.(map[string]any)["role"]))
	}
	return out
}

// latest_reminder 必须插在对应提问之前，且内容不变时不重复插入（与官方 demo 一致）。
func TestBuildMessagesInsertsLatestReminder(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	sess := model.Session{ID: "s1", Title: "t", OwnerID: app.deviceID, SyncState: SyncLocal, CreatedAt: 1, UpdatedAt: 1}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	day1 := "2026-11-20,星期五,北京市,Web,中文"
	day3 := "2026-11-22,星期日,上海市,Web,中文"
	seedTurn(t, app, "s1", "t1", model.RootTurnID, "q1", day1)
	seedTurn(t, app, "s1", "t2", "t1", "q2", day1) // 同一天：不应重复插入
	seedTurn(t, app, "s1", "t3", "t2", "q3", day3)

	now := model.Turn{
		ID: "t4", SessionID: "s1", ParentTurnID: "t3",
		Q: model.Q{Text: "q4", CreatedAt: 3}, CreatedAt: 3,
	}
	messages, _, err := app.buildMessages(context.Background(), nil, sess, now, day3)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"latest_reminder", "user", "assistant", "user", "assistant", "latest_reminder", "user", "assistant", "user"}
	got := rolesOf(messages)
	if len(got) != len(want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles = %v, want %v", got, want)
		}
	}
	if content := strOf(messages[0].(map[string]any)["content"]); content != day1 {
		t.Fatalf("first reminder = %q, want %q", content, day1)
	}
	if content := strOf(messages[5].(map[string]any)["content"]); content != day3 {
		t.Fatalf("second reminder = %q, want %q", content, day3)
	}
}

// 历史里没有快照（例如导入的会话）时不应补一条空的 latest_reminder。
func TestBuildMessagesSkipsEmptyReminder(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	sess := model.Session{ID: "s1", Title: "t", OwnerID: app.deviceID, SyncState: SyncLocal, CreatedAt: 1, UpdatedAt: 1}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	seedTurn(t, app, "s1", "t1", model.RootTurnID, "q1", "")

	now := model.Turn{
		ID: "t2", SessionID: "s1", ParentTurnID: "t1",
		Q: model.Q{Text: "q2", CreatedAt: 3}, CreatedAt: 3,
	}
	messages, _, err := app.buildMessages(context.Background(), nil, sess, now, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range rolesOf(messages) {
		if role == "latest_reminder" {
			t.Fatalf("unexpected latest_reminder in %v", rolesOf(messages))
		}
	}
}

// 端到端：Send 的 latestReminder 必须出现在发给上游的请求体里，且排在用户消息之前。
func TestSendForwardsLatestReminderUpstream(t *testing.T) {
	bodies := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case bodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.store.SetKV("base_url", upstream.URL); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetKV("api_key", "test-key"); err != nil {
		t.Fatal(err)
	}
	app.loadSettings()

	sess := model.Session{ID: "s1", Title: "t", OwnerID: app.deviceID, SyncState: SyncLocal, CreatedAt: 1, UpdatedAt: 1}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	reminder := "2026-11-20,星期五,北京市,Web,中文"
	turn, err := app.Send("s1", SendRequest{Text: "今天天气怎么样", LatestReminder: reminder})
	if err != nil {
		t.Fatal(err)
	}

	var body []byte
	select {
	case body = <-bodies:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream never received a request")
	}

	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode upstream body: %v\n%s", err, body)
	}
	if len(payload.Messages) < 2 {
		t.Fatalf("messages = %v", payload.Messages)
	}
	if role := strOf(payload.Messages[0]["role"]); role != "latest_reminder" {
		t.Fatalf("first role = %q, want latest_reminder", role)
	}
	if content := strOf(payload.Messages[0]["content"]); content != reminder {
		t.Fatalf("reminder = %q, want %q", content, reminder)
	}
	if role := strOf(payload.Messages[1]["role"]); role != "user" {
		t.Fatalf("second role = %q, want user", role)
	}

	// 快照要落到这一轮的 answer 上，供下次回放历史使用。
	deadline := time.Now().Add(3 * time.Second)
	for {
		saved, err := app.store.Turn(turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Answer != nil {
			if saved.Answer.LatestReminder != reminder {
				t.Fatalf("stored reminder = %q, want %q", saved.Answer.LatestReminder, reminder)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("answer was never stored")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
