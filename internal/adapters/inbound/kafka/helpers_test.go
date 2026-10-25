package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/IQVO/shopping-cart/internal/adapters/inbound/kafka"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fastRetry keeps Run-loop tests quick while still exercising backoff.
var fastRetry = kafkaconsumer.RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}

var errInjected = errors.New("injected transient database failure")

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

const cartID = "cart-1"

// flakyCarts fails the first `fail` Save calls AFTER saving, so the unit of
// work must roll the half-done effect (and the inbox claim) back.
type flakyCarts struct {
	ports.CartRepository
	mu    sync.Mutex
	fail  int
	calls int
}

func (f *flakyCarts) Save(ctx context.Context, c *cart.Cart) error {
	if err := f.CartRepository.Save(ctx, c); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail > 0 {
		f.fail--
		return errInjected
	}
	return nil
}

func (f *flakyCarts) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// countingUoW counts how often a unit of work was opened, proving a
// deterministic bad message never even touches the database.
type countingUoW struct {
	inner ports.UnitOfWork
	mu    sync.Mutex
	calls int
}

func (u *countingUoW) Do(ctx context.Context, fn func(context.Context) error) error {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return u.inner.Do(ctx, fn)
}

func (u *countingUoW) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

// world is a set of consumers over the in-memory store with one OPEN cart.
type world struct {
	store *memory.Store
	carts *flakyCarts
	uow   *countingUoW
	deps  usecases.Deps
}

func newWorld(t *testing.T) *world {
	t.Helper()
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	w := &world{store: s, carts: &flakyCarts{CartRepository: s.Carts()}, uow: &countingUoW{inner: s.UnitOfWork()}}
	w.deps = usecases.Deps{
		Carts: w.carts, Items: s.Items(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Inbox: s.Inbox(),
		Outbox: s.Outbox(), UoW: w.uow, Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "USD", Log: testLogger(),
	}
	c, err := cart.Create(cartID, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Carts().Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) consumer() usecases.Consumer { return usecases.Consumer{Deps: w.deps} }

func (w *world) checkout() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "checkout", Topic: kafkaconsumer.CheckoutTopic, Handlers: kafkaconsumer.CheckoutHandlers(w.consumer()),
		Logger: testLogger(), Retry: fastRetry, DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true,
	}
}

func (w *world) pricing() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "pricing", Topic: kafkaconsumer.PricingTopic, Handlers: kafkaconsumer.PricingHandlers(w.consumer()),
		Logger: testLogger(), Retry: fastRetry, DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true,
	}
}

func (w *world) offers() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "offer-availability", Topic: kafkaconsumer.OfferAvailabilityTopic, Handlers: kafkaconsumer.OfferAvailabilityHandlers(w.consumer()),
		Logger: testLogger(), Retry: fastRetry, DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true,
	}
}

func (w *world) state(t *testing.T) cart.State {
	t.Helper()
	c, err := w.store.Carts().Get(context.Background(), cartID)
	if err != nil {
		t.Fatal(err)
	}
	return c.State()
}

// claimed reports whether the inbox already holds the event id (committed).
func (w *world) claimed(t *testing.T, id string) bool {
	t.Helper()
	var first bool
	err := w.store.UnitOfWork().Do(context.Background(), func(ctx context.Context) error {
		var err error
		first, err = w.store.Inbox().Claim(ctx, id, "probe")
		if err == nil {
			return errInjected // roll the probe back
		}
		return err
	})
	if !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
	return !first
}

// event renders a structured-mode CloudEvent of another service.
func event(id, typ string, data map[string]any) []byte {
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/test", "type": typ, "subject": cartID,
		"time": t0.Add(time.Hour).Format(time.RFC3339Nano), "datacontenttype": "application/json", "data": data,
	})
	if err != nil {
		panic(err)
	}
	return b
}

func completed(id string) []byte {
	return event(id, kafkaconsumer.TypeCheckoutCompleted, map[string]any{"cartId": cartID, "sessionId": "sess-1"})
}

// fakeReader is a scripted kafkaconsumer.Reader. It records every
// FetchMessage/CommitMessages in order so a test can assert that the offset
// was committed only after the handler succeeded, and exactly once. After the
// scripted messages it blocks until ctx is cancelled, like a real reader on an
// idle topic.
type fakeReader struct {
	mu       sync.Mutex
	msgs     []kafkago.Message
	next     int
	events   []string
	onCommit func()
}

func newFakeReader(values ...[]byte) *fakeReader {
	r := &fakeReader{}
	for i, v := range values {
		r.msgs = append(r.msgs, kafkago.Message{Partition: 0, Offset: int64(i), Value: v, Topic: "t"})
	}
	return r
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	if r.next < len(r.msgs) {
		m := r.msgs[r.next]
		r.next++
		r.events = append(r.events, fmt.Sprintf("fetch:%d", m.Offset))
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	for _, m := range msgs {
		r.events = append(r.events, fmt.Sprintf("commit:%d", m.Offset))
	}
	fn := r.onCommit
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

func (r *fakeReader) Close() error { return nil }

func (r *fakeReader) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *fakeReader) commits() int {
	n := 0
	for _, e := range r.log() {
		if len(e) > 6 && e[:6] == "commit" {
			n++
		}
	}
	return n
}

// fakeDLQWriter records every message WriteMessages publishes.
type fakeDLQWriter struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (w *fakeDLQWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *fakeDLQWriter) Close() error { return nil }

func (w *fakeDLQWriter) published() []kafkago.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]kafkago.Message(nil), w.msgs...)
}

// runUntilCommits runs c.Run until the reader saw `want` commits (or fails
// after 10s), then cancels and returns Run's error.
func runUntilCommits(t *testing.T, c *kafkaconsumer.Consumer, reader *fakeReader, want int) error {
	t.Helper()
	c.Reader = reader
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(10 * time.Second)
	for reader.commits() < want {
		select {
		case err := <-done:
			return err
		case <-deadline:
			t.Fatalf("timed out waiting for %d commits; events=%v", want, reader.log())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
		return nil
	}
}

// consumerFor returns a consumer holding every handler, so the poison cases can
// share one table.
func (w *world) consumerFor(string) *kafkaconsumer.Consumer {
	h := map[string]kafkaconsumer.HandlerFunc{}
	for _, m := range []map[string]kafkaconsumer.HandlerFunc{
		kafkaconsumer.OfferAvailabilityHandlers(w.consumer()), kafkaconsumer.PricingHandlers(w.consumer()), kafkaconsumer.CheckoutHandlers(w.consumer()),
	} {
		for k, v := range m {
			h[k] = v
		}
	}
	return &kafkaconsumer.Consumer{Name: "all", Topic: "t", Handlers: h, Logger: testLogger(), Retry: fastRetry, DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true}
}
