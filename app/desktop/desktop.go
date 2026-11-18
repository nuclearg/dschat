package desktop

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"dschat/app/core/chatsvr"
)

// Run starts the local engine and blocks until SIGINT/SIGTERM.
func Run(cfg chatsvr.Config, open bool) error {
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
	if open {
		OpenBrowser(app.URL())
	}
	<-ctx.Done()
	return nil
}
