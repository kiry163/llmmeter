package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/gateway"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	webui "github.com/kiry163/llmmeter/web"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "llmmeter:", err)
		os.Exit(1)
	}
}

// run executes the command line. The "serve" subcommand is optional; bare
// flags start the server.
func run(args []string) error {
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("llmmeter", flag.ContinueOnError)
	dbPath := fs.String("db", "llmmeter.db", "SQLite database path")
	listen := fs.String("listen", "127.0.0.1:8100", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (usage: llmmeter [serve] [-db path] [-listen addr])", fs.Arg(0))
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", *dbPath, err)
	}
	// Close is idempotent, so it covers both early failures and the graceful
	// shutdown path below.
	defer st.Close()

	repo, err := settings.Open(st.DB())
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	snap := repo.Snapshot()
	if err := validateStartup(*listen, snap); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := metrics.New(st, time.Now)
	go hub.Run(ctx)

	gw := gateway.New(repo, st, hub, time.Now)
	srv := api.New(api.Config{
		Version: version,
		Listen:  *listen,
		DBPath:  *dbPath,
		Repo:    repo,
		Store:   st,
		Hub:     hub,
		Now:     time.Now,
	})

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           api.RootHandler(srv.Handler(), webui.Handler(), gw),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE streams and slow upstream responses must not be
		// cut off.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go pruneLoop(ctx, repo, st, time.Now)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()

	setup := "setup required"
	if snap.SetupDone {
		setup = "configured"
	}
	fmt.Printf("llmmeter %s listening on http://%s (database %s, %s)\n", version, *listen, *dbPath, setup)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	case <-sigCtx.Done():
		log.Println("shutting down")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	// SSE handlers only return when their subscriber channel closes; sweep them
	// first so Shutdown drains ordinary in-flight requests instead of waiting
	// out the full deadline with live dashboards attached.
	hub.CloseSubscribers()
	shutdownErr := httpSrv.Shutdown(shutdownCtx)

	cancel()
	hub.Stop()
	return errors.Join(shutdownErr, st.Close())
}

// validateStartup refuses to expose the server beyond this machine unless an
// admin password is configured. Only loopback addresses may run open.
func validateStartup(listen string, snap *settings.Snapshot) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if snap != nil && snap.AdminPasswordHash != "" {
		return nil
	}
	shown := host
	if shown == "" {
		shown = "all interfaces"
	}
	return fmt.Errorf("refusing to listen on %s without an admin password: other machines could reach the API; configure a password first (run on 127.0.0.1 and complete setup)", shown)
}

// isLoopbackHost reports whether host only reaches the local machine.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pruneLoop enqueues retention pruning once per day at 03:00 in the configured
// timezone. The snapshot is re-read every minute so a timezone change moves the
// next run instead of waiting for the old schedule.
func pruneLoop(ctx context.Context, repo *settings.Repo, st *store.Store, now func() time.Time) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var locName string
	var loc *time.Location
	var next time.Time
	for {
		snap := repo.Snapshot()
		loc = snap.Location
		if loc == nil {
			loc = time.Local
		}
		// settings.Refresh builds a new *time.Location on every write, so
		// compare the zone name rather than the pointer: only an actual
		// timezone change should move the schedule.
		if loc.String() != locName {
			locName = loc.String()
			next = nextPrune(now(), loc)
		}
		if !now().Before(next) {
			st.EnqueuePrune(now().In(loc).AddDate(0, 0, -snap.RetentionDays))
			next = nextPrune(now(), loc)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// nextPrune returns the next local 03:00 at or after now.
func nextPrune(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 3, 0, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
