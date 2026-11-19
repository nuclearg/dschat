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
	app, err := chatsvr.New(cfg)
	if err != nil {
		return err
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Start(ctx); err != nil {
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
