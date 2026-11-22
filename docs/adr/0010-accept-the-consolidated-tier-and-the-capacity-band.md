# ADR 0010: Accept delivery-promise's CONSOLIDATED tier and carry the capacity band

## Status

Accepted (2026-11-18). Additive. Extends the "four tiers" of
[ADR 0002](./0002-shopping-cart-assumptions.md).

## Context

delivery-promise defines an opt-in fifth tier, `CONSOLIDATED` (its ADR 0024): a
customer-chosen delivery day, quoted only when the request lists the tier or names
`consolidatedDeliveryDay`. An offer of that tier carries `consolidationEligible`, and
any offer may carry a `capacityBand` (`AMPLE`, `TIGHT`, `SCARCE`; its ADR 0023). The
order chain downstream of the cart (customer-ordering) already stores both. The cart
did not: `cart.Tier.Valid()` and the OpenAPI `Tier` enum had four values, so a
`CONSOLIDATED` offer failed quote validation and could not be selected.

## Decision

- **`CONSOLIDATED` is a cart tier**, in `cart.Tier`, `Valid()` and the OpenAPI `Tier`
  enum. `Valid()` stays strict: any other value is still rejected.
- **`QuotedOffer` carries two optional fields**, `consolidationEligible` (boolean)
  and `capacityBand` (`AMPLE`, `TIGHT`, `SCARCE`), through the quote, the selection,
  the checkout requote acceptance, the stored quote, REST, MCP and A2A. Both are
  omitted when absent, so an offer without them is byte-identical to before.
- **Invariants.** `consolidationEligible` is accepted only on a `CONSOLIDATED` offer
  (it is never `false` by default: absent means not asked); `capacityBand`, when
  present, is one of the three bands.
- **The gateway is tolerant.** It keeps `consolidationEligible` only on a
  `CONSOLIDATED` offer and drops a `capacityBand` it does not know: the band is a
  signal only, and a value added later must not fail a whole quote.
- **The cart does not request the tier.** `POST /delivery-quotes` still names no
  tiers, so the cart quotes the four classic tiers. The storefront BFF decides when
  to ask for `CONSOLIDATED`; this change only makes the cart able to hold, select and
  relay such an offer.
- **No money.** The capacity band changes no offer, window or order and the network
  computes no fee from it. The cart's delivery charge estimate has no `CONSOLIDATED`
  entry in the pricing schedule, so a cart whose selection is `CONSOLIDATED` has an
  incomplete estimate (`estimateComplete` false), never an invented charge. A pricing
  event naming `CONSOLIDATED` is still refused: the pricing contract names four tiers.

## Consequences

- REST: the response enum `Tier` gains a value (a `response-property-enum-value-added`
  warning in the `spec-breaking` gate, per the fleet decision) and `QuotedOffer` gains
  two optional properties. The request side (`delivery-requote-acceptances`) gains the
  enum value and the two optional properties, which oasdiff classifies as non-breaking.
- Stored carts need no migration: the two JSON keys are optional.
- Consumers that pin this contract: see the PR description.
