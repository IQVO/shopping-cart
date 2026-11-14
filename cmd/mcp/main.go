// Command mcp is the composition root for the shopping-cart MCP server
// (docs/adr/0009-read-only-mcp-server.md): it wires env config to the outbound
// Postgres read repositories, the repositories to the SAME read use cases the
// REST GETs and the A2A agent expose (GetCart, ListCarts), and those to the
// inbound MCP adapter, then serves MCP over Streamable HTTP (and nothing else:
// no stdio, no SSE). It is a third, independent deployable beside cmd/api and
// cmd/a2a, shipped in the same image as /app/mcp.
//
// The surface is READ-ONLY: no write use case and no write seam is wired here
// at all (no unit of work, no outbox, no inbox, no id generator, no
// delivery-promise quoter), so this binary can never append to the outbox. It
// never starts the outbox relay, a consumer or a sweep and never dials Kafka
// (no KAFKA_BROKERS, no consumer groups): those run in cmd/api only. It makes
// no REST, A2A or MCP call to any sibling context.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                 listen address (default :8090)
//	DATABASE_URL             REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	CART_CURRENCY            estimate currency before any price is known (default USD;
//	                         set it to the same value as cmd/api so every surface agrees)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans and metrics (default shopping-cart-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION          service.version, logged at boot too (default telemetry.DefaultServiceVersion)
//	ENVIRONMENT              deployment.environment.name, read by telemetry.Setup (default local)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundmcp "github.com/IQVO/shopping-cart/internal/adapters/inbound/mcp"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/clock"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/telemetry"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// errNoDatabase is the boot error of a missing DATABASE_URL: this binary has
// no in-memory mode.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")

// bootConfig is the validated configuration of cmd/mcp.
type bootConfig struct {
	databaseURL string
	currency    string
}

// checkCurrency validates CART_CURRENCY the way cmd/api does, so the estimate
// of every surface uses the same fallback currency.
func checkCurrency(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return "USD", nil
	}
	if len(c) != 3 || strings.ToUpper(c) != c {
		return "", fmt.Errorf("CART_CURRENCY: %q is not a three-letter upper-case currency code", c)
	}
	return c, nil
}

// loadBootConfig reads and validates the environment. Every problem is
// reported at once, each naming its variable, so the process fails fast at
// boot, before telemetry, any connection and the listener.
func loadBootConfig(env func(string) string) (bootConfig, error) {
	cfg := bootConfig{databaseURL: env("DATABASE_URL")}
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	var err error
	if cfg.currency, err = checkCurrency(env("CART_CURRENCY")); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return bootConfig{}, errors.Join(errs...)
	}
	return cfg, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	cfg, err := loadBootConfig(os.Getenv)
	if err != nil {
		return err
	}

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed last.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	deps, closeAdapters, err := buildDeps(context.Background(), logger, cfg.databaseURL, getenv("MIGRATIONS_DATABASE_URL", cfg.databaseURL), cfg.currency)
	if err != nil {
		return err
	}
	defer closeAdapters()

	logger.Info("mcp server starting", "version", tel.serviceVersion)

	srv := &http.Server{
		Addr:              getenv("MCP_ADDR", ":8090"),
		Handler:           newMCP(deps, logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveMCP(logger, srv)
}

// newMCP composes the MCP surface over the cart read side: the SAME GetCart and
// ListCarts use cases the REST GETs use behind the MCP adapter, and the HTTP
// router around them. Nothing that can write is passed in.
func newMCP(deps usecases.Deps, logger *slog.Logger, serviceName string) http.Handler {
	server := inboundmcp.NewServer(inboundmcp.Deps{GetCart: usecases.GetCart{Deps: deps}, ListCarts: usecases.ListCarts{Deps: deps}})
	return newRouter(inboundmcp.Handler(server), logger, serviceName)
}

// buildDeps migrates the database, connects a pgx pool and wires the Postgres
// repositories the two read use cases need (the cart repository and the three
// read-model repositories behind the estimate). The returned close func
// releases the pool. An empty databaseURL is a boot error: there is no
// fallback runtime.
//
// Like cmd/api this binary runs the idempotent embedded migrations on start,
// so it can boot against a fresh database before cmd/api has; golang-
// migrate's advisory lock makes concurrent starts safe. migrationsDatabaseURL
// is used ONLY for that step (a DIRECT Postgres DSN where DATABASE_URL points
// at a pooled connection, which cannot honour the session-scoped advisory
// lock); the runtime pool always uses databaseURL.
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL, currency string) (usecases.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return usecases.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return usecases.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return usecases.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return usecases.Deps{}, noop, err
	}
	logger.Info("postgres repositories configured")
	// READ-ONLY wiring: only the repositories the two read use cases touch. No
	// unit of work, outbox, inbox, id generator or quoter is constructed here,
	// so this binary has no seam to write through at all.
	return usecases.Deps{
		Carts: postgres.NewCartRepository(pool), Items: postgres.NewItemRepository(pool),
		Schedules: postgres.NewScheduleRepository(pool), TaxTables: postgres.NewTaxTableRepository(pool),
		Clock: clock.System{}, Currency: currency, Log: logger,
	}, pool.Close, nil
}

// serveMCP runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveMCP(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|warning|error (case-insensitive; "warning" is accepted
// like "warn" fleet-wide), defaulting to Info.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
