//go:build darwin || windows

package desktop

import (
	"runtime"

	"github.com/energye/systray"

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

		mOpen.Click(func() { OpenBrowser(app.URL()) })
		mSettings.Click(func() { OpenBrowser(app.URL() + "&settings=1") })
		mQuit.Click(func() { systray.Quit() })

		// energye/systray exposes a real tray-icon double-click event. A
		// single click intentionally does nothing on macOS/Windows; the menu
		// stays available through right-click.
		systray.SetOnDClick(func(systray.IMenu) {
			OpenBrowserNewWindow(app.URL())
		})
		systray.SetOnRClick(func(menu systray.IMenu) {
			_ = menu.ShowMenu()
		})
		systray.SetTooltip("dschat · 双击打开新窗口，右键菜单")

		if open {
			OpenBrowser(app.URL())
		}
	}, func() {})
}
