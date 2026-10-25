package kafka_test

import (
	"context"
	"errors"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/IQVO/shopping-cart/internal/adapters/inbound/kafka"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func TestRun_CommitsEveryMessageInOrderAfterHandlingIt(t *testing.T) {
	w := newWorld(t)
	consumer := w.checkout()
	reader := newFakeReader(completed("e1"), completed("e2"))
	if err := runUntilCommits(t, consumer, reader, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if got := reader.log(); len(got) != 4 || got[0] != "fetch:0" || got[1] != "commit:0" || got[2] != "fetch:1" || got[3] != "commit:1" {
		t.Fatalf("events = %v", got)
	}
	if w.state(t) != cart.StateCheckedOut || !w.claimed(t, "e1") || !w.claimed(t, "e2") {
		t.Fatal("both events must be claimed and the cart checked out")
	}
}

func TestRun_ATransientFailureRetriesTheSameMessageThenCommits(t *testing.T) {
	w := newWorld(t)
	w.carts.fail = 2
	reader := newFakeReader(completed("e1"))
	if err := runUntilCommits(t, w.checkout(), reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if w.carts.callCount() < 3 {
		t.Fatalf("attempts = %d, want at least 3 (two rolled back, one committed)", w.carts.callCount())
	}
	if !w.claimed(t, "e1") || w.state(t) != cart.StateCheckedOut {
		t.Fatal("a later attempt must commit the effect and the claim")
	}
}

func TestRun_AMessageThatNeverSucceedsIsDeadLetteredAndTheLoopMovesOn(t *testing.T) {
	w := newWorld(t)
	w.carts.fail = 1 << 30
	consumer := w.checkout()
	dlq := &fakeDLQWriter{}
	consumer.DLQ = dlq
	consumer.Topic = "t"
	reader := newFakeReader(completed("e1"))
	if err := runUntilCommits(t, consumer, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	msgs := dlq.published()
	if len(msgs) != 1 || msgs[0].Topic != "t.dlq" {
		t.Fatalf("dead letters = %+v", msgs)
	}
	if w.claimed(t, "e1") || w.state(t) != cart.StateOpen {
		t.Fatal("a rolled-back message leaves neither a claim nor an effect")
	}
}

func TestRun_APoisonPayloadIsDeadLetteredWithoutTouchingTheDatabase(t *testing.T) {
	w := newWorld(t)
	consumer := w.checkout()
	dlq := &fakeDLQWriter{}
	consumer.DLQ, consumer.Topic = dlq, "t"
	before := w.uow.count()
	poison := event("bad", kafkaconsumer.TypeCheckoutCompleted, map[string]any{"cartId": cartID})
	reader := newFakeReader(poison)
	if err := runUntilCommits(t, consumer, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if got := dlq.published(); len(got) != 1 || headerOf(got[0], "x-dlq-error") == "" {
		t.Fatalf("dead letters = %+v", got)
	}
	if w.uow.count() != before {
		t.Fatalf("a poison message opened %d units of work", w.uow.count()-before)
	}
}

func TestClose_ReleasesReaderAndDeadLetterWriter(t *testing.T) {
	w := newWorld(t)
	consumer := w.checkout()
	consumer.Reader, consumer.DLQ = newFakeReader(), &fakeDLQWriter{}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
}

func headerOf(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
