package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewLogger_AcceptsWarningAlias(t *testing.T) {
	for _, level := range []string{"warn", "warning", "WARNING"} {
		l := newLogger(level)
		if l.Enabled(context.Background(), slog.LevelInfo) || !l.Enabled(context.Background(), slog.LevelWarn) {
			t.Fatalf("LOG_LEVEL=%q: want warn level", level)
		}
	}
}

func TestGetenvFallsBackOnlyWhenUnset(t *testing.T) {
	t.Setenv("A2A_TEST_VAR", "")
	if got := getenv("A2A_TEST_VAR", "fb"); got != "fb" {
		t.Fatalf("empty = %q, want the fallback", got)
	}
	t.Setenv("A2A_TEST_VAR", "v")
	if got := getenv("A2A_TEST_VAR", "fb"); got != "v" {
		t.Fatalf("set = %q, want v", got)
	}
}

// TestMigrationsDatabaseURLFallback proves the fallback wiring run() applies
// before calling buildDeps: getenv("MIGRATIONS_DATABASE_URL", databaseURL)
// returns MIGRATIONS_DATABASE_URL's own value when set, and DATABASE_URL when
// not.
func TestMigrationsDatabaseURLFallback(t *testing.T) {
	const databaseURL = "postgres://u:p@pgbouncer.example:6432/shopping_cart?sslmode=disable"

	t.Run("falls back to DATABASE_URL when MIGRATIONS_DATABASE_URL is unset", func(t *testing.T) {
		t.Setenv("MIGRATIONS_DATABASE_URL", "")
		os.Unsetenv("MIGRATIONS_DATABASE_URL")
		if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != databaseURL {
			t.Fatalf("getenv fallback = %q, want %q", got, databaseURL)
		}
	})
	t.Run("uses MIGRATIONS_DATABASE_URL when set", func(t *testing.T) {
		const direct = "postgres://u:p@postgres-postgresql.example:5432/shopping_cart?sslmode=disable"
		t.Setenv("MIGRATIONS_DATABASE_URL", direct)
		if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != direct {
			t.Fatalf("getenv = %q, want the direct URL %q (must NOT keep using DATABASE_URL/PgBouncer)", got, direct)
		}
	})
}

// TestBuildDepsRunsTheMigrationsOnTheMigrationsURL proves buildDeps itself, not
// just the env read above, threads migrationsURL into the migration step: it
// gets a schemeless string that migrate rejects at once with a distinctive
// parse error, and DATABASE_URL an address nothing listens on. If buildDeps ran
// the migrations against databaseURL, the logged retry cause would be a dial
// error instead. The context expires during the first backoff, so the test
// reads the cause from the retry log line (the returned error wraps only the
// context error then).
func TestBuildDepsRunsTheMigrationsOnTheMigrationsURL(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u:p@127.0.0.1:1/shopping_cart?sslmode=disable&connect_timeout=1"
	)
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, closeAdapters, err := buildDeps(ctx, unreachableAppURL, bogusMigrationsURL, "USD", slog.New(slog.NewTextHandler(&logs, nil)))
	defer closeAdapters()
	if err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	if !strings.Contains(logs.String(), "parse scheme") {
		t.Fatalf("log = %q: expected the bogus-URL parse error from migrate; a dial error here would mean the migrations ran against databaseURL", logs.String())
	}
}

func TestBuildDepsRefusesAnEmptyDatabaseURL(t *testing.T) {
	_, _, err := buildDeps(context.Background(), "", "", "USD", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps(\"\") = %v, want an error naming DATABASE_URL", err)
	}
}
