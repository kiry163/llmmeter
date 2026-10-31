package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/gateway"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/secure"
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
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Println(version)
			return nil
		case "init":
			return runInit(args[1:], os.Stdin, os.Stdout)
		case "serve":
			args = args[1:]
		}
	}
	return runServe(args)
}

// runServe loads the configuration, validates it and starts the HTTP server.
func runServe(args []string) error {
	fs := flag.NewFlagSet("llmmeter", flag.ContinueOnError)
	configFlag := fs.String("config", "", "configuration file path (default: $XDG_CONFIG_HOME/llmmeter/config.yaml)")
	dbPath := fs.String("db", "", "SQLite database path (default: server.database or $XDG_CACHE_HOME/llmmeter/llmmeter.db)")
	listen := fs.String("listen", "", "HTTP listen address (default: server.listen or 127.0.0.1:8100)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (usage: llmmeter [serve] [-config path] [-db path] [-listen addr])", fs.Arg(0))
	}

	paths, err := config.ResolvePaths(*configFlag, *dbPath, *listen)
	if err != nil {
		return err
	}
	cfgStore, err := config.OpenStore(paths.Config, config.WithEffectiveDatabase(paths.Database))
	if err != nil {
		return err
	}
	// An existing configuration without a session secret would accept logins
	// and then fail to issue sessions. Generate one and persist it. Setup mode
	// (no configuration file) is left untouched: setup generates the secret
	// with the rest of the configuration.
	if err := ensureSessionSecret(cfgStore); err != nil {
		log.Printf("warning: could not persist a session secret; logins stay unavailable until one is configured: %v", err)
	}
	snap := cfgStore.Snapshot()
	if err := validateStartup(paths.Listen, snap); err != nil {
		return err
	}

	st, err := store.Open(paths.Database)
	if err != nil {
		return fmt.Errorf("open database %s: %w", paths.Database, err)
	}
	// Close is idempotent, so it covers both early failures and the graceful
	// shutdown path below.
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := metrics.New(st, time.Now)
	go hub.Run(ctx)

	gw := gateway.New(cfgStore, st, hub, time.Now)
	srv := api.New(api.Config{
		Version:     version,
		Listen:      paths.Listen,
		DBPath:      paths.Database,
		Paths:       paths,
		ConfigStore: cfgStore,
		Store:       st,
		Hub:         hub,
		Now:         time.Now,
	})

	httpSrv := &http.Server{
		Addr:              paths.Listen,
		Handler:           api.RootHandler(srv.Handler(), webui.Handler(), gw),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE streams and slow upstream responses must not be
		// cut off.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go pruneLoop(ctx, cfgStore, st, time.Now)
	go reloadOnSIGHUP(ctx, cfgStore, srv)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()

	setup := "setup required"
	if len(snap.Config.Upstreams) > 0 {
		setup = "configured"
	}
	fmt.Printf("llmmeter %s listening on http://%s (config %s, database %s, %s)\n",
		version, paths.Listen, paths.Config, paths.Database, setup)

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

// runInit writes a fresh configuration file without starting the server or
// creating the database. It refuses to overwrite an existing file and prints
// the generated gateway key to stdout.
func runInit(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("llmmeter init", flag.ContinueOnError)
	configFlag := fs.String("config", "", "configuration file path (default: $XDG_CONFIG_HOME/llmmeter/config.yaml)")
	listen := fs.String("listen", "", "HTTP listen address (default: 127.0.0.1:8100)")
	database := fs.String("database", "", "SQLite database path (default: $XDG_CACHE_HOME/llmmeter/llmmeter.db)")
	timezone := fs.String("timezone", "", "IANA timezone name (default: UTC)")
	upstreamName := fs.String("upstream-name", "", "upstream display name (required)")
	upstreamURL := fs.String("upstream-url", "", "upstream base URL (required)")
	upstreamKey := fs.String("upstream-key", "", "upstream API key (optional)")
	keyName := fs.String("key-name", "", "local gateway key name (default: default)")
	passwordStdin := fs.Bool("admin-password-stdin", false, "read the admin password from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (usage: llmmeter init [flags])", fs.Arg(0))
	}

	name := strings.TrimSpace(*upstreamName)
	if name == "" {
		return errors.New("init: -upstream-name is required")
	}
	baseURL := strings.TrimSpace(*upstreamURL)
	if baseURL == "" {
		return errors.New("init: -upstream-url is required")
	}

	paths, err := config.ResolvePaths(*configFlag, *database, *listen)
	if err != nil {
		return err
	}

	passwordHash := ""
	if *passwordStdin {
		password, err := readPassword(stdin)
		if err != nil {
			return err
		}
		if password == "" {
			return errors.New("init: -admin-password-stdin was set but stdin carried no password")
		}
		passwordHash, err = secure.HashPassword(password)
		if err != nil {
			return err
		}
	}

	secret, err := secure.RandomSecret()
	if err != nil {
		return err
	}
	gatewayKey := secure.NewGatewayKey()

	keyLabel := strings.TrimSpace(*keyName)
	if keyLabel == "" {
		keyLabel = "default"
	}
	upstreamID := slug(name, "upstream")
	keyID := slug(keyLabel, "default")

	cfg := config.Default()
	cfg.Server.Listen = paths.Listen
	cfg.Server.Database = paths.Database
	cfg.General.Timezone = strings.TrimSpace(*timezone)
	cfg.Auth.AdminPasswordHash = passwordHash
	cfg.Auth.SessionSecret = base64.StdEncoding.EncodeToString(secret)
	cfg.Upstreams = []config.Upstream{{
		ID:      upstreamID,
		Name:    name,
		BaseURL: baseURL,
		APIKey:  strings.TrimSpace(*upstreamKey),
	}}
	cfg.GatewayKeys = []config.GatewayKey{{
		ID:         keyID,
		Name:       keyLabel,
		Key:        gatewayKey,
		UpstreamID: upstreamID,
	}}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if err := writeInitConfig(paths.Config, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "gateway key: %s\n", gatewayKey)
	return nil
}

// writeInitConfig creates path exclusively and fills it with cfg. A file that
// already exists is never overwritten. The configuration store performs the
// atomic 0600 write; the exclusive create reserves the path first so two
// concurrent init runs cannot both succeed.
func writeInitConfig(path string, cfg *config.Config) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("init: configuration %s already exists; refusing to overwrite", path)
	}
	if err != nil {
		return fmt.Errorf("init: create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("init: create %s: %w", path, err)
	}

	st, err := config.OpenStore(path)
	if err == nil {
		err = st.Update(func(c *config.Config) error {
			*c = *cfg
			return nil
		})
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// readPassword reads one line from r, dropping the trailing newline and a
// Windows carriage return.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("init: read admin password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// slug converts a display name into the identifier shape the configuration
// requires: lowercase letters, digits and single dashes, at most 32 bytes.
func slug(name, fallback string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if b.Len() >= 32 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}

// ensureSessionSecret writes a generated session secret to an existing
// configuration that lacks one. It does nothing in setup mode (no config file)
// or when a secret is already present. The update re-checks the secret under
// the store lock so a concurrent write is never overwritten.
func ensureSessionSecret(cfgStore *config.Store) error {
	snap := cfgStore.Snapshot()
	if snap == nil || snap.Config.Auth.SessionSecret != "" || !cfgStore.Writable() {
		return nil
	}
	secret, err := secure.RandomSecret()
	if err != nil {
		return err
	}
	return cfgStore.Update(func(c *config.Config) error {
		if c.Auth.SessionSecret == "" {
			c.Auth.SessionSecret = base64.StdEncoding.EncodeToString(secret)
		}
		return nil
	})
}

// reloadOnSIGHUP re-reads the configuration on SIGHUP. A failed reload keeps
// the previous snapshot and only logs the error. Listen and database changes
// therefore take effect on the next restart, not in this process.
func reloadOnSIGHUP(ctx context.Context, cfgStore *config.Store, srv *api.Server) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if err := cfgStore.Reload(); err != nil {
				log.Printf("config reload failed, keeping the previous configuration: %v", err)
				continue
			}
			// Upstream endpoints and keys may have changed; the catalog cache
			// is keyed by those values, but drop it immediately like the API
			// reload endpoint does.
			srv.InvalidateCatalog()
			log.Println("configuration reloaded")
		}
	}
}

// validateStartup refuses to expose the server beyond this machine unless an
// admin password is configured. Only loopback addresses may run open.
func validateStartup(listen string, snap *config.Snapshot) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if snap != nil && snap.Config.Auth.AdminPasswordHash != "" {
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
func pruneLoop(ctx context.Context, cfgStore *config.Store, st *store.Store, now func() time.Time) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var locName string
	var next time.Time
	for {
		snap := cfgStore.Snapshot()
		loc := snap.Location
		if loc == nil {
			loc = time.UTC
		}
		// Build returns a new *time.Location on every reload, so compare the
		// zone name rather than the pointer: only an actual timezone change
		// should move the schedule.
		if loc.String() != locName {
			locName = loc.String()
			next = nextPrune(now(), loc)
		}
		if !now().Before(next) {
			st.EnqueuePrune(now().In(loc).AddDate(0, 0, -snap.Config.General.RetentionDays))
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
		loc = time.UTC
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 3, 0, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
