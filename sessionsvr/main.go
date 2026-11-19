package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	var (
		addr   = flag.String("addr", ":5218", "listen address")
		data   = flag.String("data", "", "data directory (default ~/.dschat/sessionsvr)")
		config = flag.String("config", defaultConfigPath(), "config.properties path (default: next to the executable)")
	)
	flag.Parse()

	fileCfg, err := LoadConfig(*config)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if fileCfg.Password == "" {
		log.Printf("============================================================")
		log.Printf("WARNING: PASSWORD is empty in %s", *config)
		log.Printf("WARNING: anyone who can reach this server can read/write data.")
		log.Printf("WARNING: use a password unless this is a trusted local machine.")
		log.Printf("WARNING: config file: %s", *config)
		log.Printf("============================================================")
	}

	dir := *data
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		dir = filepath.Join(home, ".dschat", "sessionsvr")
	}

	srv, err := New(Config{Addr: *addr, Data: dir, Password: fileCfg.Password})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("sessionsvr listening on %s, data %s, config %s, password %s", *addr, dir, *config, map[bool]string{true: "set", false: "EMPTY"}[fileCfg.Password != ""])
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.CleanupLoop(ctx)
	<-ctx.Done()

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdown)
}
