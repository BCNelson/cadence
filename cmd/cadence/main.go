// Command cadence is the monitoring daemon. It loads YAML configuration,
// opens the LevelDB store, starts the engine + APIs + SSE bus, and
// serves the embedded React dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bcnelson/cadence/internal/alert"
	"github.com/bcnelson/cadence/internal/api"
	"github.com/bcnelson/cadence/internal/config"
	"github.com/bcnelson/cadence/internal/engine"
	"github.com/bcnelson/cadence/internal/sse"
	"github.com/bcnelson/cadence/internal/store"
	"github.com/bcnelson/cadence/internal/web"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// repeatableFlag collects every value of a flag that may appear more than
// once on the command line. Used for -c / --config.
type repeatableFlag []string

func (f *repeatableFlag) String() string     { return fmt.Sprintf("%v", *f) }
func (f *repeatableFlag) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	var paths repeatableFlag
	flag.Var(&paths, "c", "configuration file or directory (repeat for layering, left -> right)")
	flag.Var(&paths, "config", "alias for -c")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("cadence %s (%s, built %s)\n", version, commit, buildDate)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	if len(paths) == 0 {
		logger.Error("at least one -c <path> is required")
		os.Exit(2)
	}

	if err := run(paths); err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

// run is the real entry point — split out so it returns an error instead
// of calling os.Exit, making future tests easier.
func run(paths []string) error {
	reg, err := config.Load(paths, config.Options{})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	dataDir := reg.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	st, err := store.Open(dataDir, store.Options{
		MaxPings:  reg.Retention.Pings,
		MaxEvents: reg.Retention.Events,
	})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("store close", "err", err)
		}
	}()

	bus := sse.NewBus()
	alerter := alert.New(reg.Channels, alert.Options{})

	eng, err := engine.New(reg, st, engine.Options{
		Bus:     bus,
		Alerter: alerter,
	})
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}

	mux := http.NewServeMux()
	registerRoutes(mux, reg, eng, st, bus)

	listen := reg.Server.Listen
	if listen == "" {
		listen = ":8080"
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := eng.Run(ctx, time.Second); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("engine loop", "err", err)
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", listen, "checks", len(reg.Checks))
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown", "err", err)
	}
	return nil
}

// registerRoutes mounts every HTTP surface on mux. Ordering is incidental
// — the Go 1.22+ ServeMux dispatches by pattern specificity.
func registerRoutes(mux *http.ServeMux, reg *config.Registry, eng *engine.Engine, st *store.Store, bus *sse.Bus) {
	pingH := api.NewPingHandler(reg, eng, st)
	for _, r := range pingH.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}

	mgmtH := api.NewMgmtHandler(reg, eng, st)
	for _, r := range mgmtH.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}

	mux.HandleFunc("/events", bus.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Frontend: serve the embedded SPA. The catch-all "/" route has no
	// method so it stays strictly less specific than every other registered
	// pattern — Go's mux otherwise flags `GET /` against `/events` as
	// ambiguous (one wins on method, the other on path). For client-side
	// routes (anything not under /api, /ping, /events, /healthz) we fall
	// back to index.html so TanStack Router handles the path.
	frontFS := web.Assets()
	fileServer := http.FileServer(http.FS(frontFS))
	mux.Handle("/", spaFallback(frontFS, fileServer))
}

// spaFallback serves static assets when they exist, otherwise returns
// index.html so TanStack Router's client-side routing takes over. The
// API routes are mounted on more-specific patterns and won't fall through
// here.
func spaFallback(root fs.FS, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(root, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path — let the SPA handle it.
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
