// hhub is the Household Hub program.
//
//	hhub serve [--addr :8080] [--data /data]   the server (Docker/Unraid, or by hand)
//	hhub background                            Windows: hosts the data (host mode) and/or shows Windows notifications
//	hhub toast-test                            Windows: shows one test notification
//	hhub version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // time zones work in a minimal container too

	"hhub/internal/client"
	"hhub/internal/core"
	"hhub/internal/web"
)

var version = "1.8.1"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve", "--addr", "--data":
		args := os.Args[1:]
		if cmd == "serve" {
			args = os.Args[2:]
		}
		serveCmd(args)
	case "background":
		background()
	case "toast-test":
		client.RegisterAppID(iconPath())
		if err := client.ShowToast("Household Hub", "Windows notifications work."); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println(version)
	case "--check":
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "usage: hhub serve|background|toast-test|version")
		os.Exit(2)
	}
}

// startServer opens the database and serves the app plus the reminder engine until ctx ends.
func startServer(ctx context.Context, addr, dataDir string) (*http.Server, *core.Store, error) {
	store, err := core.Open(filepath.Join(dataDir, "household.db"))
	if err != nil {
		return nil, nil, err
	}
	n := core.NewNotifier(store)
	go store.RunEngine(ctx, n)
	go store.RunMailEngine(ctx, n)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	srv := &http.Server{Handler: web.New(store, n, version), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
		}
	}()
	log.Printf("Household Hub %s on http://%s (data %s, time zone %s)", version, addr, dataDir, time.Local)
	return srv, store, nil
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("HHUB_ADDR", ":8080"), "listen address")
	data := fs.String("data", env("HHUB_DATA", "/data"), "data folder")
	fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, store, err := startServer(ctx, *addr, *data)
	if err != nil {
		log.Fatal(err)
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
	store.Close()
}

func iconPath() string {
	self, _ := os.Executable()
	return filepath.Join(filepath.Dir(self), "icon.png")
}

// background runs on Windows at sign-in. It hosts the data when this PC is the household server, and it turns
// new alerts into Windows notifications. The window talks to it on 127.0.0.1:18788.
func background() {
	os.MkdirAll(client.Dir(), 0o755)
	if lf, err := os.OpenFile(filepath.Join(client.Dir(), "background.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		if st, _ := lf.Stat(); st != nil && st.Size() > 5<<20 {
			lf.Truncate(0)
		}
		log.SetOutput(lf)
	}
	ctl, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", client.ControlPort))
	if err != nil {
		return // already running
	}
	client.RegisterAppID(iconPath())

	var mu sync.Mutex
	var srv *http.Server
	var store *core.Store
	var cancel context.CancelFunc
	var running client.Config
	apply := func() {
		mu.Lock()
		defer mu.Unlock()
		c := client.Load()
		want := c.Mode == "host"
		same := srv != nil && want && running.ListenAddr() == c.ListenAddr()
		if same {
			running = c
			return
		}
		if srv != nil {
			sctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
			srv.Shutdown(sctx)
			cc()
			cancel()
			store.Close()
			srv, store = nil, nil
		}
		running = c
		if want {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			s, st, err := startServer(ctx, c.ListenAddr(), client.DataDir())
			if err != nil {
				log.Printf("host mode: %v", err)
				cancel()
				return
			}
			srv, store = s, st
		}
	}
	apply()

	quit := make(chan struct{})
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-HHub") != "1" || r.Header.Get("Origin") != "" {
				http.Error(w, "forbidden", 403)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /status", guard(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"app": "household-hub-background", "mode": running.Mode, "serving": srv != nil, "version": version})
	}))
	mux.HandleFunc("POST /reload", guard(func(w http.ResponseWriter, r *http.Request) {
		apply()
		mu.Lock()
		serving := srv != nil
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "serving": serving})
	}))
	mux.HandleFunc("POST /quit", guard(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bye"))
		close(quit)
	}))
	go http.Serve(ctl, mux)

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	time.Sleep(3 * time.Second)
	for {
		client.PollOnce(client.ShowToast)
		select {
		case <-quit:
			mu.Lock()
			if srv != nil {
				srv.Close()
				cancel()
				store.Close()
			}
			mu.Unlock()
			return
		case <-tick.C:
		}
	}
}
