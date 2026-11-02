package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestIdempotencyKeyRequiredAndReplay(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.do(http.MethodPost, "/carts", "", `{}`), http.StatusBadRequest, "idempotency-key-required")
	e.wantProblem(e.do(http.MethodPost, "/carts", strings.Repeat("k", 129), `{}`), http.StatusBadRequest, "idempotency-key-invalid")

	first := e.do(http.MethodPost, "/carts", "k1", `{"shopperId":"s1"}`)
	e.wantStatus(first, http.StatusCreated)
	again := e.do(http.MethodPost, "/carts", "k1", `{"shopperId":"s1"}`)
	e.wantStatus(again, http.StatusCreated)
	if again.Body != first.Body || again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay differs: %q vs %q (%v)", again.Body, first.Body, again.Header)
	}
	e.wantProblem(e.do(http.MethodPost, "/carts", "k1", `{"shopperId":"s2"}`), http.StatusConflict, "idempotency-key-reused")
	if n := len(e.get("/carts?shopperId=s1").json(t)["items"].([]any)); n != 1 {
		t.Fatalf("replay created again: %d carts", n)
	}
}

func TestIdempotencyRecordsClientErrorsNotServerErrors(t *testing.T) {
	e := newEnv(t)
	bad := e.do(http.MethodPost, "/carts", "k2", `{"shopperId":"bad id"}`)
	e.wantStatus(bad, http.StatusUnprocessableEntity)
	again := e.do(http.MethodPost, "/carts", "k2", `{"shopperId":"bad id"}`)
	e.wantStatus(again, http.StatusUnprocessableEntity)
	if again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("a client error is stored under its key")
	}
}

func TestOversizedBodyIsMalformed(t *testing.T) {
	e := newEnv(t)
	big := `{"shopperId":"` + strings.Repeat("a", 70<<10) + `"}`
	e.wantProblem(e.post("/carts", big), http.StatusBadRequest, "malformed-request")
}

func TestRejectNUL(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/carts/a%00b"), http.StatusNotFound, "resource-not-found")
	e.wantProblem(e.get("/carts?shopperId=a%00b"), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/carts", `{"shopperId":"a\u0000b"}`), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/carts", "{\"shopperId\":\"a\x00b\"}"), http.StatusBadRequest, "malformed-request")
}

func TestHealthAndReadiness(t *testing.T) {
	e := newEnv(t)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
	e.wantStatus(e.get("/readyz"), http.StatusOK)
	e.ready.SetNotReady()
	e.wantStatus(e.get("/readyz"), http.StatusServiceUnavailable)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
}
