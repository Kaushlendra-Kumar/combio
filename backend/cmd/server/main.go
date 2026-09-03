package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaushlendra/combio/backend/internal/auth"
	"github.com/kaushlendra/combio/backend/internal/room"
	"github.com/kaushlendra/combio/backend/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	seed := flag.Int64("seed", 0, "fixed deal seed for every room (0 = random per room)")
	flag.Parse()

	logger := log.New(os.Stdout, "combio ", log.LstdFlags)

	dev := auth.NewDevTokens()
	rooms := room.NewManager(room.ManagerOptions{
		Timers: room.DefaultTimers(),
		Seed:   *seed,
	})
	srv := server.New(server.Options{Dev: dev, Rooms: rooms, Logger: logger})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Periodically close rooms nobody is connected to.
	stopReaper := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if n := rooms.Reap(); n > 0 {
					logger.Printf("reaped %d empty room(s)", n)
				}
			case <-stopReaper:
				return
			}
		}
	}()

	go func() {
		logger.Printf("listening on %s  (DEV AUTH: POST /auth/dev?name=You, then ws://host/ws?token=...)", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	logger.Println("shutting down")
	close(stopReaper)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop accepting, then close the live sockets and every room. HTTP's own
	// Shutdown does not track hijacked connections, so the server has to close
	// the WebSockets itself or the process would never drain.
	if err := srv.Shutdown(ctx); err != nil {
		logger.Printf("shutdown: %v", err)
	}
	_ = httpSrv.Shutdown(ctx)
	logger.Println("stopped")
}
