//go:build darwin || windows

package desktop

import (
	"runtime"

	"github.com/getlantern/systray"

	"dschat/app/core/chatsvr"
)

// quitTray asks the platform shell to exit; safe from a signal goroutine.
func quitTray() { systray.Quit() }

// runTray blocks on the platform menu bar / notification area.
func runTray(app *chatsvr.App, open bool) {
	systray.Run(func() {
		if runtime.GOOS == "darwin" {
			systray.SetTemplateIcon(iconPNG, iconPNG)
		} else {
			systray.SetIcon(iconICO)
		}
		systray.SetTooltip("dschat")

		mOpen := systray.AddMenuItem("打开", "打开 dschat")
		mSettings := systray.AddMenuItem("配置", "打开设置")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "退出 dschat")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					OpenBrowser(app.URL())
				case <-mSettings.ClickedCh:
					OpenBrowser(app.URL() + "&settings=1")
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
		if open {
			OpenBrowser(app.URL())
		}
	}, func() {})
}
