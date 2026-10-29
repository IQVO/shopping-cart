package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// recordingMetrics counts the ports.CartMetrics calls of a use case.
type recordingMetrics struct {
	mu       sync.Mutex
	added    int
	rejected []string
}

var _ ports.CartMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) LineAdded(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added++
}

func (m *recordingMetrics) LineRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func TestAddLineCountsEachAttemptOnceByOutcome(t *testing.T) {
	e := newEnv(t)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	c := e.newCart(t, "")
	add := usecases.AddLine{Deps: e.deps}
	e.stock(t, "sku-1", 1000)
	ctx := context.Background()

	if _, err := add.Execute(ctx, c.CartID, "sku-1", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := add.Execute(ctx, c.CartID, "sku-1", 1); err != nil { // merged
		t.Fatal(err)
	}
	if m.added != 2 || len(m.rejected) != 0 {
		t.Fatalf("after two adds: added=%d rejected=%v, want 2 / none", m.added, m.rejected)
	}
	if _, err := add.Execute(ctx, c.CartID, "ghost", 1); err == nil {
		t.Fatal("an unknown item must be refused")
	}
	if m.added != 2 || !reflect.DeepEqual(m.rejected, []string{"item-not-purchasable"}) {
		t.Fatalf("after a refusal: added=%d rejected=%v", m.added, m.rejected)
	}
}

func TestAddLineRejectionReasonIsTheProblemSlug(t *testing.T) {
	e := newEnv(t)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	c := e.newCart(t, "")
	e.stock(t, "sku-1", 1000)
	add := usecases.AddLine{Deps: e.deps}
	ctx := context.Background()

	_, errQty := add.Execute(ctx, c.CartID, "sku-1", 100)
	_, errMissing := add.Execute(ctx, "nope", "sku-1", 1)
	if errQty == nil || !errors.Is(errMissing, ports.ErrNotFound) {
		t.Fatalf("errors = %v / %v", errQty, errMissing)
	}
	if want := []string{"quantity-out-of-range", "not-found"}; !reflect.DeepEqual(m.rejected, want) || m.added != 0 {
		t.Fatalf("rejected=%v added=%d, want %v", m.rejected, m.added, want)
	}
}

func TestAddLineWithoutMetricsRecordsNothing(t *testing.T) {
	e := newEnv(t)
	c := e.newCart(t, "")
	e.stock(t, "sku-1", 1000)
	e.add(t, c.CartID, "sku-1", 1)
}
