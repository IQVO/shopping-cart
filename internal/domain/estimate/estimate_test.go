package estimate

import (
	"testing"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func schedule() *Schedule {
	return &Schedule{Version: 3, Tiers: map[cart.Tier]TierCharge{
		cart.TierStandard: {ChargeMinor: 500, FreeOverMinor: 5000},
		cart.TierNextDay:  {ChargeMinor: 1200},
		cart.TierSameDay:  {ChargeMinor: 2500},
		cart.TierTwoDay:   {ChargeMinor: 800},
	}}
}

func taxTable() *TaxTable {
	return &TaxTable{Version: 2, Bands: []TaxBand{{From: "100", To: "199", RateBasisPoints: 500}, {From: "700", To: "799", RateBasisPoints: 825}}}
}

func lines() []cart.Line {
	return []cart.Line{{CatalogItemID: "a", Quantity: 2}, {CatalogItemID: "b", Quantity: 1}}
}

func prices() map[string]int64 { return map[string]int64{"a": 1000, "b": 700} }

func input() Input {
	return Input{
		Currency: "USD", Lines: lines(), Prices: prices(), Destination: "787",
		Groups:   []Group{{GroupNo: 1, LineNos: []int{1, 2}, Tier: cart.TierStandard, Selected: true}},
		Schedule: schedule(), TaxTable: taxTable(),
	}
}

func TestEstimateAddsItemsDeliveryAndTax(t *testing.T) {
	in := input()
	in.Groups[0].Tier = cart.TierNextDay // goods 2700 < free-over: standard would also charge
	got := Compute(in)
	// items 2700 + delivery 1200 = 3900; tax 8.25% = 321.75 -> 322
	want := Estimate{Currency: "USD", ItemsMinor: 2700, DeliveryMinor: 1200, TaxMinor: 322, TotalMinor: 4222, Complete: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDeliveryIsFreeWhenTheGroupGoodsReachTheFreeOverThreshold(t *testing.T) {
	in := input()
	in.Prices["a"] = 2500 // goods 5700 >= 5000
	got := Compute(in)
	if got.DeliveryMinor != 0 || got.ItemsMinor != 5700 || !got.Complete {
		t.Fatalf("%+v", got)
	}
	in.Prices["a"] = 2150 // goods 5000 reaches the threshold exactly
	if got := Compute(in); got.DeliveryMinor != 0 {
		t.Fatalf("threshold is inclusive: %+v", got)
	}
	in.Prices["a"] = 2149 // 4998
	if got := Compute(in); got.DeliveryMinor != 500 {
		t.Fatalf("below the threshold: %+v", got)
	}
}

func TestEstimateWithoutADestinationShowsTheItemSubtotalOnly(t *testing.T) {
	in := input()
	in.Destination = ""
	got := Compute(in)
	want := Estimate{Currency: "USD", ItemsMinor: 2700, TotalMinor: 2700}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestEstimateIsIncompleteWithoutASelectionForEveryGroup(t *testing.T) {
	in := input()
	in.Groups[0].Selected = false
	got := Compute(in)
	if got.Complete || got.DeliveryMinor != 0 || got.ItemsMinor != 2700 || got.TaxMinor != 223 || got.TotalMinor != 2923 {
		t.Fatalf("tax on the known goods only: %+v", got)
	}
	in.Groups = nil
	if got := Compute(in); got.Complete {
		t.Fatalf("no quote yet is incomplete: %+v", got)
	}
}

func TestEstimateIsIncompleteWhenAnInputIsUnknown(t *testing.T) {
	cases := map[string]func(in *Input){
		"unpriced line":   func(in *Input) { delete(in.Prices, "b") },
		"no schedule":     func(in *Input) { in.Schedule = nil },
		"no tax table":    func(in *Input) { in.TaxTable = nil },
		"no band":         func(in *Input) { in.Destination = "500" },
		"short prefix":    func(in *Input) { in.Destination = "78" },
		"tier not priced": func(in *Input) { delete(in.Schedule.Tiers, cart.TierStandard) },
	}
	for name, mutate := range cases {
		in := input()
		mutate(&in)
		if got := Compute(in); got.Complete {
			t.Fatalf("%s: must be incomplete: %+v", name, got)
		}
	}
}

func TestUnpricedLinesAreLeftOutOfTheSubtotal(t *testing.T) {
	in := input()
	delete(in.Prices, "b")
	in.Destination = ""
	if got := Compute(in); got.ItemsMinor != 2000 || got.TotalMinor != 2000 {
		t.Fatalf("%+v", got)
	}
}

func TestTheTaxBandMatchesTheFirstThreeCharactersUpperCased(t *testing.T) {
	tab := taxTable()
	for prefix, want := range map[string]int{"787": 825, "7": -1, "700": 825, "799": 825, "800": 0, "1004": 500, "099": 0} {
		if prefix == "7" {
			continue
		}
		if got := tab.RateFor(prefix); got != want {
			t.Fatalf("RateFor(%q) = %d, want %d", prefix, got, want)
		}
	}
	tab.Bands = append(tab.Bands, TaxBand{From: "ABC", To: "ABZ", RateBasisPoints: 100})
	if got := tab.RateFor("abd"); got != 100 {
		t.Fatalf("lower case destination: %d", got)
	}
}

func TestTwoGroupsAreChargedIndependently(t *testing.T) {
	in := input()
	in.Groups = []Group{
		{GroupNo: 1, LineNos: []int{1}, Tier: cart.TierStandard, Selected: true},
		{GroupNo: 2, LineNos: []int{2}, Tier: cart.TierSameDay, Selected: true},
	}
	got := Compute(in)
	if got.DeliveryMinor != 500+2500 || !got.Complete {
		t.Fatalf("%+v", got)
	}
}
