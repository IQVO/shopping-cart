package kafka_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/kafka"
	"github.com/IQVO/shopping-cart/internal/application/outbox"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 23, 10, 15, 30, 123456000, time.UTC)

const fixedID = "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40"

func lineAdded() cart.CartLineAdded {
	return cart.CartLineAdded{Header: shared.Header{Version: 3, At: t0}, CartID: "cart-1", CatalogItemID: "item-100", Quantity: 2}
}

func deliveryQuoted() cart.CartDeliveryQuoted {
	return cart.CartDeliveryQuoted{Header: shared.Header{Version: 4, At: t0}, CartID: "cart-1", DestinationPostalPrefix: "787", GroupCount: 2}
}

func abandoned() cart.CartAbandoned {
	return cart.CartAbandoned{Header: shared.Header{Version: 5, At: t0.Add(time.Hour)}, CartID: "cart-1", LastChangedAt: t0}
}

func checkedOut() cart.CartCheckedOut {
	return cart.CartCheckedOut{Header: shared.Header{Version: 6, At: t0}, CartID: "cart-1", CheckoutSessionID: "sess-9"}
}

func encodeOne(t *testing.T, ev shared.Event) outbox.Message {
	t.Helper()
	enc := &kafka.Encoder{NewID: func() string { return fixedID }}
	msgs, err := enc.Encode(ev)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Encode: %v (%d messages)", err, len(msgs))
	}
	return msgs[0]
}

// assertGolden compares the CloudEvents bytes with the golden file EXACTLY.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s differs from its golden file:\n got %s\nwant %s", name, got, want)
	}
}

func assertEnvelope(t *testing.T, msg outbox.Message, entity, name, subject string) {
	t.Helper()
	wantType := "com.retailnetwork.storefront.shopping-cart." + entity + "." + name
	if msg.EventType != wantType || msg.EventID != fixedID || msg.Topic != "retailnetwork.shopping-cart.analytics" {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Subject != subject || string(msg.Key) != subject {
		t.Fatalf("subject/key = %q %q", msg.Subject, msg.Key)
	}
	if msg.DataSchema != "urn:retailnetwork:shopping-cart:analytics:"+name+":v1" {
		t.Fatalf("dataschema = %q", msg.DataSchema)
	}
	if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != cloudevents.MediaType {
		t.Fatalf("headers = %+v", msg.Headers)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil || e.Type() != wantType || e.Source() != "/retailnetwork/shopping-cart" || e.ID() != fixedID {
		t.Fatalf("decode: %v %v", err, e)
	}
}

func TestCartLineAddedGolden(t *testing.T) {
	msg := encodeOne(t, lineAdded())
	assertGolden(t, "CartLineAdded", msg.Value)
	assertEnvelope(t, msg, "cart", "CartLineAdded", "cart-1")
}

func TestCartDeliveryQuotedGolden(t *testing.T) {
	msg := encodeOne(t, deliveryQuoted())
	assertGolden(t, "CartDeliveryQuoted", msg.Value)
	assertEnvelope(t, msg, "cart", "CartDeliveryQuoted", "cart-1")
}

func TestCartAbandonedGolden(t *testing.T) {
	msg := encodeOne(t, abandoned())
	assertGolden(t, "CartAbandoned", msg.Value)
	assertEnvelope(t, msg, "cart", "CartAbandoned", "cart-1")
}

func TestCartCheckedOutGolden(t *testing.T) {
	msg := encodeOne(t, checkedOut())
	assertGolden(t, "CartCheckedOut", msg.Value)
	assertEnvelope(t, msg, "cart", "CartCheckedOut", "cart-1")
}

func TestDataCarriesTheModelFields(t *testing.T) {
	var env struct{ Data map[string]any }
	if err := json.Unmarshal(encodeOne(t, lineAdded()).Value, &env); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cartId", "catalogItemId", "quantity", "at"} {
		if _, ok := env.Data[k]; !ok {
			t.Fatalf("data %v lacks %s", env.Data, k)
		}
	}
}
