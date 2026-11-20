package desktop

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"dschat/app/core/chatsvr"
)

// freePort 借一个当前空闲的端口；测试里立刻释放，避免长期占用。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStartReturnsPortInUseError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	app, err := chatsvr.New(chatsvr.Config{DataDir: t.TempDir(), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	err = app.Start(context.Background())
	var portErr *chatsvr.PortInUseError
	if !errors.As(err, &portErr) {
		t.Fatalf("Start() error = %v, want *chatsvr.PortInUseError", err)
	}
	if portErr.Port != port {
		t.Fatalf("PortInUseError.Port = %d, want %d", portErr.Port, port)
	}
}

// 占用者是自己以外的进程时，弹窗文案应给出 PID 与建议。
func TestPortBusyMessageNamesOtherProgram(t *testing.T) {
	busy := &chatsvr.PortInUseError{Port: 5217}
	got := buildPortBusyMessage(5217, busy, occupant{
		PID: 4242, Name: "Google Chrome", Path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}, false)

	for _, want := range []string{"5217", "Google Chrome", "PID 4242", "请退出该程序后重试", "-port"} {
		if !strings.Contains(got, want) {
			t.Fatalf("message missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "不能同时运行两个") {
		t.Fatalf("message should not claim a duplicate dschat:\n%s", got)
	}
}

// 占用者是另一个 dschat 时，必须明确告知不能同时运行两个。
func TestPortBusyMessageFlagsDuplicateDschat(t *testing.T) {
	busy := &chatsvr.PortInUseError{Port: 5217}
	got := buildPortBusyMessage(5217, busy, occupant{
		PID: 99, Name: "dschat", Path: "/Applications/dschat.app/Contents/MacOS/dschat",
	}, true)

	if !strings.Contains(got, "不能同时运行两个 dschat") {
		t.Fatalf("message should flag the duplicate dschat:\n%s", got)
	}
	if !strings.Contains(got, "另一个 dschat") {
		t.Fatalf("message should name the occupant as another dschat:\n%s", got)
	}
}

// 拿不到进程信息时给出手动排查命令，而不是空白。
func TestPortBusyMessageFallsBackToCommand(t *testing.T) {
	busy := &chatsvr.PortInUseError{Port: 5217}
	got := buildPortBusyMessage(5217, busy, occupant{}, false)
	if !strings.Contains(got, "未能识别") || !strings.Contains(got, "lsof -nP -iTCP:5217") {
		t.Fatalf("message should suggest lsof:\n%s", got)
	}
}

// 端到端验证身份探测：真实启动的 chatsvr 会在 /api/health 里自报 dschat。
func TestProbeHealthIdentifiesDschat(t *testing.T) {
	port := freePort(t)
	app, err := chatsvr.New(chatsvr.Config{DataDir: t.TempDir(), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}

	info, ok := probeHealth(port)
	if !ok {
		t.Fatal("probeHealth() failed against a running chatsvr")
	}
	if info.App != "dschat" {
		t.Fatalf("probeHealth().App = %q, want dschat", info.App)
	}
	if info.PID <= 0 {
		t.Fatalf("probeHealth().PID = %d, want a real pid", info.PID)
	}
}

// 端口空闲时探测应失败，不能误判成 dschat。
func TestProbeHealthOnFreePort(t *testing.T) {
	if _, ok := probeHealth(freePort(t)); ok {
		t.Fatal("probeHealth() should fail when nothing listens on the port")
	}
}

// 真实监听时应能同时拿到占用 PID 并判定为 dschat。
func TestWhoIsOnPortDetectsRunningDschat(t *testing.T) {
	port := freePort(t)
	app, err := chatsvr.New(chatsvr.Config{DataDir: t.TempDir(), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}

	occ, isDschat := whoIsOnPort(port)
	if !isDschat {
		t.Fatalf("whoIsOnPort(%d) did not detect dschat; occupant = %+v", port, occ)
	}
	if occ.PID <= 0 {
		t.Fatalf("whoIsOnPort(%d) returned no PID: %+v", port, occ)
	}
}
