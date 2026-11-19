//go:build !darwin && !windows

package desktop

import "dschat/app/core/chatsvr"

func runTray(_ *chatsvr.App, _ bool) {}

func quitTray() {}
