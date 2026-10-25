//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/testing/kafkatest"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST and Kafka. delivery-promise is the one HTTP dependency and is a
// local fake server. Every Kafka read is bounded by a deadline; run it with
// -timeout 300s.

const (
	topicAnalytics = "retailnetwork.shopping-cart.analytics"
	topicOffers    = "retailnetwork.offer-availability.events"
	topicPricing   = "retailnetwork.pricing.events"
	topicCheckout  = "retailnetwork.checkout.events"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}

// quoteAnswer is a delivery-promise QuoteResponse: one group, two quoted
// offers, one constraint.
const quoteAnswer = `{"offers":[
 {"offerId":"of-1","state":"QUOTED","tier":"NEXT_DAY","groupNo":1,"lines":[{"catalogItemId":"sku-100","quantity":2}],
  "window":{"earliest":"2026-10-26T12:00:00Z","latest":"2026-10-26T20:00:00Z"},"quotedAt":"2026-10-25T13:00:00Z","expiresAt":"2099-01-01T00:00:00Z"},
 {"offerId":"of-2","state":"QUOTED","tier":"STANDARD","groupNo":1,"lines":[{"catalogItemId":"sku-100","quantity":2}],
  "window":{"earliest":"2026-10-28T12:00:00Z","latest":"2026-10-29T20:00:00Z"},"quotedAt":"2026-10-25T13:00:00Z","expiresAt":"2099-01-01T00:00:00Z"}],
 "unavailable":[{"tier":"SAME_DAY","constraint":"PAST_ORDER_DEADLINE","groupNo":1}]}`

type fakeDP struct {
	*httptest.Server
	mu    sync.Mutex
	calls int
	keys  []string
}

func (f *fakeDP) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func fakeDeliveryPromise(t *testing.T) *fakeDP {
	t.Helper()
	f := &fakeDP{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/delivery-offers:quote" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.calls++
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(quoteAnswer))
	}))
	t.Cleanup(f.Close)
	return f
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// service is a booted instance of the real entry point.
type service struct {
	base string
	stop func() error
}

func boot(t *testing.T, env map[string]string) *service {
	t.Helper()
	addr := freeAddr(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("HTTP_ADDR", addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	var (
		stopOnce sync.Once
		stopErr  error
	)
	s := &service{base: "http://" + addr, stop: func() error {
		stopOnce.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(60 * time.Second):
				stopErr = fmt.Errorf("the service did not stop")
			}
		})
		return stopErr
	}}
	t.Cleanup(func() { _ = s.stop() })
	eventually(t, 90*time.Second, "readyz to report ready", func() bool {
		resp, err := http.Get(s.base + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == 200
	})
	return s
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

type reply struct {
	status int
	body   string
	header http.Header
}

func (s *service) call(t *testing.T, method, path, key, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: string(b), header: resp.Header}
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON: %s", r.body)
	}
	return m
}

func event(id, typ, subject string, data map[string]any) kafkatest.Message {
	b, _ := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/test", "type": typ, "subject": subject,
		"time": time.Now().UTC().Format(time.RFC3339Nano), "datacontenttype": "application/json", "data": data,
	})
	return kafkatest.Message{Key: subject, Value: b}
}
