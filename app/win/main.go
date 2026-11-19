//go:build windows

package main

import (
	"flag"
	"log"

	"dschat/app/core/chatsvr"
	"dschat/app/desktop"
)

func main() {
	port := flag.Int("port", chatsvr.DefaultPort, "local fixed port")
	data := flag.String("data", "", "data directory")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	serve := flag.Bool("serve", false, "run headless without the tray")
	flag.Parse()
	if err := desktop.Run(chatsvr.Config{DataDir: *data, Port: *port}, !*noOpen, !*serve); err != nil {
		log.Fatal(err)
	}
}
