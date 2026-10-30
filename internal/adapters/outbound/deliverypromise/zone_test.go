package deliverypromise_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/deliverypromise"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// window is one QUOTED offer's answer; zone "" omits timeZone.
func window(earliest, latest, zone string) string {
	tz := ""
	if zone != "" {
		tz = fmt.Sprintf(`,"timeZone":%q`, zone)
	}
	return fmt.Sprintf(`{"offerId":"of-1","state":"QUOTED","tier":"STANDARD","groupNo":1,
	 "lines":[{"catalogItemId":"sku-1","quantity":2}],
	 "window":{"earliest":%q,"latest":%q%s},
	 "quotedAt":"2026-10-25T13:00:00Z","expiresAt":"2026-10-25T13:30:00Z"}`, earliest, latest, tz)
}

// zoneGateway answers every quote with offers and logs into the returned buffer.
func zoneGateway(t *testing.T, offers ...string) (*deliverypromise.Gateway, *bytes.Buffer) {
	t.Helper()
	body := `{"offers":[` + strings.Join(offers, ",") + `],"unavailable":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return deliverypromise.New(srv.URL, nil, func() time.Time { return fixedNow }, deliverypromise.WithLogger(logger)), logs
}

func quoteOffers(t *testing.T, g *deliverypromise.Gateway) []cart.QuotedOffer {
	t.Helper()
	q, err := g.Quote(context.Background(), "k", "787", cartLines[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Groups) != 1 {
		t.Fatalf("groups = %+v", q.Groups)
	}
	return q.Groups[0].Offers
}

func TestWindowDatesAreDaysOfTheDestinationsCalendar(t *testing.T) {
	for name, tc := range map[string]struct {
		earliest, latest, zone   string
		wantEarliest, wantLatest string
	}{
		// 09:00-21:00 CDT on Oct 31: the UTC dates would say Oct 31 - Nov 1.
		"west of UTC": {"2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "America/Chicago", "2026-10-31", "2026-10-31"},
		// 01:00-21:00 JST on Oct 31: the UTC dates would say Oct 30 - Oct 31.
		"east of UTC": {"2026-10-30T16:00:00Z", "2026-10-31T12:00:00Z", "Asia/Tokyo", "2026-10-31", "2026-10-31"},
		// Nov 1 is the day DST ends in Chicago (25 hours long): 00:30 CDT to
		// 23:30 CST. A fixed -05:00 offset would put the latest on Nov 2.
		"DST end day": {"2026-11-01T05:30:00Z", "2026-11-02T05:30:00Z", "America/Chicago", "2026-11-01", "2026-11-01"},
		// Mar 8 2026 is the day DST starts (23 hours long): 00:30 CST to
		// 23:30 CDT. A fixed -06:00 offset would put the latest on Mar 9.
		"DST start day": {"2026-03-08T06:30:00Z", "2026-03-09T04:30:00Z", "America/Chicago", "2026-03-08", "2026-03-08"},
		"UTC named":     {"2026-10-31T00:00:00Z", "2026-10-31T23:59:59Z", "UTC", "2026-10-31", "2026-10-31"},
	} {
		t.Run(name, func(t *testing.T) {
			g, logs := zoneGateway(t, window(tc.earliest, tc.latest, tc.zone))
			o := quoteOffers(t, g)[0]
			if o.DeliverByEarliest != tc.wantEarliest || o.DeliverByLatest != tc.wantLatest {
				t.Fatalf("dates = %s..%s, want %s..%s", o.DeliverByEarliest, o.DeliverByLatest, tc.wantEarliest, tc.wantLatest)
			}
			if logs.Len() != 0 {
				t.Fatalf("a usable zone logs nothing, got %q", logs.String())
			}
		})
	}
}

func TestAnUnusableWindowZoneFallsBackToUTCAndWarnsOncePerName(t *testing.T) {
	for name, zone := range map[string]string{
		"missing":       "",
		"unknown":       "Mars/Olympus_Mons",
		"process local": "Local",
		"path escape":   "../../etc/passwd",
	} {
		t.Run(name, func(t *testing.T) {
			chicagoEvening := window("2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", zone)
			g, logs := zoneGateway(t, chicagoEvening, chicagoEvening)
			for range 2 { // two offers per quote, two quotes: still one warning
				requireUTCDates(t, quoteOffers(t, g))
			}
			requireOneWarningNaming(t, logs.String(), zone)
		})
	}
}

func requireUTCDates(t *testing.T, offers []cart.QuotedOffer) {
	t.Helper()
	for _, o := range offers {
		if o.DeliverByEarliest != "2026-10-31" || o.DeliverByLatest != "2026-11-01" {
			t.Fatalf("UTC dates expected, got %s..%s", o.DeliverByEarliest, o.DeliverByLatest)
		}
	}
}

func requireOneWarningNaming(t *testing.T, logs, zone string) {
	t.Helper()
	if n := strings.Count(logs, "level=WARN"); n != 1 {
		t.Fatalf("want one WARN, got %d: %q", n, logs)
	}
	if !strings.Contains(logs, fmt.Sprintf("timeZone=%q", zone)) && !strings.Contains(logs, "timeZone="+zone+" ") {
		t.Fatalf("the warning names the zone: %q", logs)
	}
}

func TestEachUnusableZoneNameIsWarnedAboutSeparately(t *testing.T) {
	g, logs := zoneGateway(t,
		window("2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", ""),
		window("2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "Nowhere/Town"),
		window("2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", "America/Chicago"))
	offers := quoteOffers(t, g)
	if offers[2].DeliverByLatest != "2026-10-31" {
		t.Fatalf("a usable zone in the same answer still converts: %+v", offers[2])
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 2 {
		t.Fatalf("want two WARNs, got %d: %q", n, logs.String())
	}
}

func TestTheZoneCacheIsBounded(t *testing.T) {
	var offers []string
	for i := range 70 {
		offers = append(offers, window("2026-10-31T14:00:00Z", "2026-11-01T02:00:00Z", fmt.Sprintf("Nowhere/Town_%d", i)))
	}
	g, logs := zoneGateway(t, offers...)
	for _, o := range quoteOffers(t, g) {
		if o.DeliverByLatest != "2026-11-01" {
			t.Fatalf("past the bound a name still falls back to UTC: %+v", o)
		}
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 64 {
		t.Fatalf("want 64 WARNs (the bound), got %d", n)
	}
}

func TestANilLoggerKeepsTheDefault(t *testing.T) {
	g := deliverypromise.New("http://127.0.0.1:1", nil, nil, deliverypromise.WithLogger(nil))
	if g == nil {
		t.Fatal("New returned nil")
	}
}
