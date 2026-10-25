package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/shopping-cart/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/shopping-cart/internal/application/outbox"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Topic is this service's analytics topic. All four published events are
// analytics-only (model: "consumers: analytics only"), so the cart writes to
// no other topic.
const Topic = "retailnetwork.shopping-cart.analytics"

// schemaVersion is the dataschema version of every payload below.
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON) pinned by
// apis/asyncapi.yaml. `at` is the instant of the change, RFC 3339 in UTC.

type lineAddedData struct {
	CartID        string    `json:"cartId"`
	CatalogItemID string    `json:"catalogItemId"`
	Quantity      int       `json:"quantity"`
	At            time.Time `json:"at"`
}

type deliveryQuotedData struct {
	CartID                  string    `json:"cartId"`
	DestinationPostalPrefix string    `json:"destinationPostalPrefix"`
	GroupCount              int       `json:"groupCount"`
	At                      time.Time `json:"at"`
}

type abandonedData struct {
	CartID        string    `json:"cartId"`
	LastChangedAt time.Time `json:"lastChangedAt"`
}

type checkedOutData struct {
	CartID            string    `json:"cartId"`
	CheckoutSessionID string    `json:"checkoutSessionId"`
	At                time.Time `json:"at"`
}

// Encoder implements outbox.Encoder: each domain event becomes one outbox
// message holding the CloudEvents bytes, the Kafka key (the subject) and the
// content-type header. The CloudEvents `id` is minted HERE, once, and persisted
// with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// Topic overrides the destination topic (Topic when empty); tests use it
	// to isolate their topic on a shared broker.
	Topic string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := buildMessage(ev, newID(), e.topic(), data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func (e *Encoder) topic() string {
	if e.Topic != "" {
		return e.Topic
	}
	return Topic
}

func buildMessage(ev shared.Event, id, topic string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(), Time: ev.OccurredAt(),
		Stream: cloudevents.StreamAnalytics, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: topic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()), Subject: ev.SubjectID(),
		Key: []byte(ev.SubjectID()), DataSchema: cloudevents.DataSchema(cloudevents.StreamAnalytics, ev.EventName(), schemaVersion),
		Value: value, Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case cart.CartLineAdded:
		return lineAddedData{CartID: string(e.CartID), CatalogItemID: e.CatalogItemID, Quantity: e.Quantity, At: e.At}, nil
	case cart.CartDeliveryQuoted:
		return deliveryQuotedData{CartID: string(e.CartID), DestinationPostalPrefix: e.DestinationPostalPrefix, GroupCount: e.GroupCount, At: e.At}, nil
	case cart.CartAbandoned:
		return abandonedData{CartID: string(e.CartID), LastChangedAt: e.LastChangedAt}, nil
	case cart.CartCheckedOut:
		return checkedOutData{CartID: string(e.CartID), CheckoutSessionID: e.CheckoutSessionID, At: e.At}, nil
	}
	return nil, fmt.Errorf("kafka encoder: %T is not a published event", ev)
}

// Headers converts the stored outbox headers to Kafka headers.
func Headers(hs []outbox.Header) []kafkago.Header {
	out := make([]kafkago.Header, len(hs))
	for i, h := range hs {
		out[i] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
	}
	return out
}

// Writer is the part of *kafkago.Writer the relay sink needs.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Sink writes outbox messages to Kafka; each message names its own topic, so
// the writer behind it has none (see NewWriter).
type Sink struct{ W Writer }

// Send writes msgs, in order, and returns when the broker acknowledged them.
func (s Sink) Send(ctx context.Context, msgs ...outbox.Message) error {
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		out[i] = kafkago.Message{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: Headers(m.Headers)}
	}
	if err := s.W.WriteMessages(ctx, out...); err != nil {
		return fmt.Errorf("write %d messages to kafka: %w", len(out), err)
	}
	return nil
}
