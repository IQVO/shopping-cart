// Command a2a is the composition root for the shopping-cart A2A agent: it wires
// env config to the Postgres cart repository and the three read-model
// repositories, wires them into the SAME GetCart and ListCarts use case structs
// cmd/api's REST GETs use, then serves Agent2Agent JSON-RPC over HTTP with the
// Agent Card at the well-known path. It is a second, independent deployable
// beside cmd/api (the REST service), per ADR 0006.
//
// It reads the same Postgres as cmd/api: DATABASE_URL is mandatory and its
// absence is a boot error that names it; there is no in-memory store. It is
// READ-ONLY, so it needs no Kafka, no outbox, no relay, no consumers, no
// schedulers and no delivery-promise client: a missing or malformed mandatory
// variable is a boot error naming it, raised before telemetry, any connection
// and the listener.
//
// It calls no other bounded context and contains no outbound A2A client.
//
// Environment:
//
//	DATABASE_URL             REQUIRED. Postgres DSN
//	A2A_AGENT_URL            REQUIRED. Absolute http(s) URL of this agent's
//	                         JSON-RPC endpoint, advertised on the Agent Card
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	A2A_ADDR                 listen address (default :8093)
//	CART_CURRENCY            estimate currency before any price is known (default USD;
//	                         set it to the same value as cmd/api so both surfaces agree)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans (default shopping-cart-a2a)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION          service.version (default dev)
//	ENVIRONMENT              deployment.environment.name (default local)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/clock"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/telemetry"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/bootretry"
)

// errNoDatabase is the boot error of a missing DATABASE_URL: cmd/a2a reads the
// same Postgres as cmd/api and has no in-memory store.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")

// errNoAgentURL is the boot error for a missing A2A_AGENT_URL: the Agent Card
// must advertise the absolute URL clients dial (ADR 0006).
var errNoAgentURL = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL of this agent's JSON-RPC endpoint (ADR 0006), e.g. http://<release>-a2a.<ns>.svc.cluster.local:8093/")

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// telemetryFlushTimeout bounds the final export attempt on shutdown. Without a
// deadline the exporter would retry against an unreachable Collector well past
// what an orchestrator will wait for.
const telemetryFlushTimeout = 5 * time.Second

// bootConfig is the validated configuration of cmd/a2a: real Postgres and the
// URL the Agent Card advertises, with no default and no degraded mode behind
// either.
type bootConfig struct {
	databaseURL string
	agentURL    string
	currency    string
}

// checkAgentURL validates A2A_AGENT_URL: an absolute http(s) URL. The card is
// the discovery contract; a relative or schemeless URL is a misconfiguration
// every client would hit, so it is refused at boot, never silently served.
func checkAgentURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL: the Agent Card must advertise the endpoint clients dial (ADR 0006)", raw)
	}
	return nil
}

// checkCurrency validates CART_CURRENCY the way cmd/api does, so the estimate
// of both surfaces uses the same fallback currency.
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
	cfg := bootConfig{databaseURL: env("DATABASE_URL"), agentURL: strings.TrimSpace(env("A2A_AGENT_URL"))}
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	if err := checkAgentURL(cfg.agentURL); err != nil {
		errs = append(errs, err)
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
	if err := run(context.Background()); err != nil {
		slog.Error("a2a agent exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the agent and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	cfg, err := loadBootConfig(os.Getenv)
	if err != nil {
		return err
	}

	// The same non-blocking telemetry setup as cmd/api: an unreachable
	// Collector degrades to dropped telemetry, never an agent that won't start.
	serviceName := getenv("OTEL_SERVICE_NAME", "shopping-cart-a2a")
	version := getenv("SERVICE_VERSION", telemetry.DefaultServiceVersion)
	otlp := getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultEndpoint)
	shutdownTelemetry, err := telemetry.Setup(ctx, serviceName, version, otlp)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}()
	logger.Info("telemetry configured", "service_name", serviceName, "otlp_endpoint", otlp)

	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after the
	// HTTP drain.
	deps, closeAdapters, err := buildDeps(ctx, cfg.databaseURL, getenv("MIGRATIONS_DATABASE_URL", cfg.databaseURL), cfg.currency, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newAgent(deps, cfg.agentURL, version),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveUntilSignal(ctx, logger, srv)
}

// buildDeps wires the Postgres adapters the two read use cases need, running
// the embedded migrations first (idempotent: every binary that owns a view of
// the schema may start first). Nothing else is built: no outbox, no unit of
// work, no id generator, no delivery-promise quoter, no inbox, no Kafka.
//
// migrationsURL is used ONLY for the migration step: a direct, non-pooled
// connection (session-scoped advisory lock), while databaseURL may sit behind
// PgBouncer.
func buildDeps(ctx context.Context, databaseURL, migrationsURL, currency string, logger *slog.Logger) (usecases.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return usecases.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(migrationsURL) }); err != nil {
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
	logger.Info("postgres adapters configured")
	return usecases.Deps{
		Carts: postgres.NewCartRepository(pool), Items: postgres.NewItemRepository(pool),
		Schedules: postgres.NewScheduleRepository(pool), TaxTables: postgres.NewTaxTableRepository(pool),
		Clock: clock.System{}, Currency: currency, Log: logger,
	}, pool.Close, nil
}

// serveUntilSignal runs srv until SIGINT/SIGTERM (or a listen error), then
// drains it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("a2a agent listening (JSON-RPC over HTTP)", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive; "warning" is accepted like "warn" fleet-wide). The handler
// is wrapped so a record written with a span-carrying context also carries
// trace_id and span_id.
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
