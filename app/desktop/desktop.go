package desktop

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"dschat/app/core/chatsvr"
)

// Run starts the local engine. With tray=true it blocks in the menu bar shell;
// otherwise it opens the browser and waits for SIGINT/SIGTERM.
func Run(cfg chatsvr.Config, open, tray bool) error {
	cfg.AutoStart = autoStartManager{}
	app, err := chatsvr.New(cfg)
	if err != nil {
		if tray {
			ShowStartupError(err, cfg.Port)
		}
		return err
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Start(ctx); err != nil {
		// 带界面启动时用户看不到 stderr，必须用弹窗告知；-serve 只保留日志输出。
		if tray {
			ShowStartupError(err, cfg.Port)
		}
		return err
	}
	if tray && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		go func() {
			<-ctx.Done()
			quitTray()
		}()
		runTray(app, open)
		return nil
	}
	if open {
		OpenBrowser(app.URL())
	}
	<-ctx.Done()
	return nil
}
