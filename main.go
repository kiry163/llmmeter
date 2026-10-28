package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/proxy"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if err := serve(serveArgs(os.Args[1:])); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// serveArgs drops an optional leading "serve" subcommand so that
// `llmmeter serve -config x` and `llmmeter -config x` behave the same.
func serveArgs(args []string) []string {
	if len(args) > 0 && args[0] == "serve" {
		return args[1:]
	}
	return args
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to config.yaml")
	dbPath := fs.String("db", "llmmeter.db", "path to sqlite database")
	listen := fs.String("listen", "", "override listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	cat, err := pricing.New(cfg.Pricing)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	p, err := proxy.New(cfg, cat, st, nil)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	webHandler := web.New(st, loc)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			w.Write([]byte("ok"))
		case r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/api/"):
			webHandler.ServeHTTP(w, r)
		default:
			p.ServeHTTP(w, r)
		}
	})

	if *listen != "" {
		cfg.Listen = *listen
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGHUP)
	go func() {
		for range reload {
			next, err := config.Load(*cfgPath)
			if err != nil {
				slog.Error("reload failed", "err", err)
				continue
			}
			nextCat, err := pricing.New(next.Pricing)
			if err != nil {
				slog.Error("reload pricing failed", "err", err)
				continue
			}
			if next.Listen != cfg.Listen {
				slog.Warn("listen change requires restart", "current", cfg.Listen, "next", next.Listen)
			}
			if err := p.Reload(next, nextCat); err != nil {
				slog.Error("reload apply failed", "err", err)
				continue
			}
			slog.Info("config reloaded")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	slog.Info("llmmeter listening", "addr", cfg.Listen, "upstream", cfg.Upstream.BaseURL)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		// ListenAndServe returns as soon as shutdown starts; wait for in-flight
		// handlers so their records reach the store before it closes.
		<-shutdownDone
		err = nil
	}
	if err != nil {
		return err
	}
	return st.Flush()
}
