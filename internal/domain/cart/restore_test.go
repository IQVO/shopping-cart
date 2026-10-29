package cart

import (
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

func TestSnapshotRestoreRoundTripLosesNothing(t *testing.T) {
	c := quoted(t)
	_ = c.SelectDelivery(2, "o3", at(4))
	_ = c.SelectDelivery(1, "o2", at(4))
	c.MarkPersisted()
	got, err := Restore(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Snapshot(), c.Snapshot()) || got.LoadedVersion() != c.Version() {
		t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), c.Snapshot())
	}
	if got.Quote() == c.Quote() {
		t.Fatal("restore must copy the quote")
	}
}

func TestRestoreRejectsACorruptRow(t *testing.T) {
	good := quoted(t).Snapshot()
	mutations := map[string]func(s *Snapshot){
		"bad id":             func(s *Snapshot) { s.ID = "" },
		"bad shopper":        func(s *Snapshot) { s.ShopperID = "bad id" },
		"bad state":          func(s *Snapshot) { s.State = "GONE" },
		"no version":         func(s *Snapshot) { s.Version = 0 },
		"no createdAt":       func(s *Snapshot) { s.CreatedAt = timeZero() },
		"no lastChanged":     func(s *Snapshot) { s.LastChangedAt = timeZero() },
		"bad destination":    func(s *Snapshot) { s.Destination = "78 7" },
		"zero quantity":      func(s *Snapshot) { s.Lines[0].Quantity = 0 },
		"repeated line":      func(s *Snapshot) { s.Lines[1].CatalogItemID = s.Lines[0].CatalogItemID },
		"too many lines":     func(s *Snapshot) { s.Lines = make([]Line, MaxLines+1) },
		"quote no dest":      func(s *Snapshot) { s.Destination = "" },
		"bad quote":          func(s *Snapshot) { s.Quote.Groups[0].GroupNo = 0 },
		"selection no offer": func(s *Snapshot) { s.Selections = []Selection{{1, "zzz"}} },
		"selection order":    func(s *Snapshot) { s.Selections = []Selection{{2, "o3"}, {1, "o1"}} },
		"selection no quote": func(s *Snapshot) { s.Quote = nil; s.Selections = []Selection{{1, "o1"}} },
	}
	for name, mutate := range mutations {
		s := quoted(t).Snapshot()
		_ = good
		mutate(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Fatalf("%s: got %v, want ErrInvalidSnapshot", name, err)
		}
	}
}

func TestPullEventsClearsTheQueue(t *testing.T) {
	c := newCart(t)
	_ = c.AddLine("item-a", 1, true, at(1))
	if len(c.PullEvents()) != 1 || len(c.PullEvents()) != 0 {
		t.Fatal("events are returned once")
	}
}

func TestCreateValidates(t *testing.T) {
	if _, err := Create("bad id", "", t0); err == nil {
		t.Fatal("bad cartId")
	}
	if _, err := Create("c1", "bad id", t0); err == nil {
		t.Fatal("bad shopperId")
	}
	if _, err := Create("c1", "", timeZero()); err == nil {
		t.Fatal("no instant")
	}
	c, err := Create("c1", "", t0)
	if err != nil || c.State() != StateOpen || c.Version() != 1 || c.LoadedVersion() != 0 || c.ShopperID() != "" || c.ID() != "c1" || !c.CreatedAt().Equal(t0) {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestStateAndTierValidity(t *testing.T) {
	for _, s := range []State{StateOpen, StateCheckedOut, StateAbandoned, StateMerged} {
		if !s.Valid() {
			t.Fatal(s)
		}
	}
	for _, tr := range []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard} {
		if !tr.Valid() {
			t.Fatal(tr)
		}
	}
	if State("x").Valid() || Tier("x").Valid() {
		t.Fatal("unknown values are invalid")
	}
}
