//go:build darwin

package main

import (
	"flag"
	"log"

	"dschat/app/core/chatsvr"
	"dschat/app/desktop"
)

func main() {
	port := flag.Int("port", 5217, "local port (falls back to a free port if busy)")
	data := flag.String("data", "", "data directory")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	flag.Parse()
	if err := desktop.Run(chatsvr.Config{DataDir: *data, Port: *port}, !*noOpen); err != nil {
		log.Fatal(err)
	}
}
