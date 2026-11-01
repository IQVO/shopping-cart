# ADR 0007: Delivery dates are days of the destination's local calendar

## Status

Accepted (2026-10-30). Supersedes the "`deliverBy*` are the UTC dates of the offer window" clause of
ADR 0002 A14 (see the A18 amendment there).

## Context

delivery-promise quotes a delivery WINDOW as two instants. A Chicago evening slot, Oct 31 09:00-21:00
local, is `2026-10-31T14:00:00Z` .. `2026-11-01T02:00:00Z`. The cart turned each instant into a calendar
date with `Earliest.UTC().Format(cart.DateLayout)` (`internal/adapters/outbound/deliverypromise/mapping.go`),
so the cart said "Oct 31 - Nov 1" for a promise that is one evening at the destination. East of UTC the
error runs the other way (a Tokyo morning slot showed the previous day).

A delivery date is a day of the DESTINATION's calendar. Only delivery-promise knows the zone: it is the
time zone of the delivery station that serves the destination. Alternatives weighed:

- **A zone table in the cart, keyed by postal prefix.** Duplicates the network's topology in the storefront,
  drifts the moment a station is re-zoned, and is a second source of truth for a fact the provider owns.
- **Expose instants in the cart contract (`format: date-time`).** Breaking for every cart consumer, and the
  consumer still needs the zone to show a day.
- **A fixed UTC offset on the window.** Wrong across a DST change inside the window (a 23- or 25-hour day).

delivery-promise adds an optional `timeZone` (IANA name) to `DeliveryWindow` (branch
`feature/window-time-zone`, additive: offers quoted before it omit it).

## Decision

1. The delivery-promise gateway reads the optional `window.timeZone` (`windowDTO.TimeZone` in
   `internal/adapters/outbound/deliverypromise/gateway.go`) and formats `deliverByEarliest` /
   `deliverByLatest` as the day each instant falls on in THAT zone (`time.Time.In`, so DST is the zone
   database's, not a fixed offset).
2. A missing name, `Local` (the process's zone, never the destination's) or a name the zone database does not
   know falls back to UTC, today's dates, and is logged at WARN once per name per process
   (`internal/adapters/outbound/deliverypromise/zone.go`). The gateway caches at most 64 names so a misbehaving
   provider cannot grow its memory; past the bound a name is still resolved, without caching or a warning.
3. The cart contract is unchanged in shape: `deliverByEarliest` / `deliverByLatest` stay `format: date`, and
   `apis/openapi.yaml` now says they are days of the destination's local calendar.
4. `POST /carts/{cartId}/delivery-requote-acceptances` receives dates, not windows: the cart stores what the caller sends
   and converts nothing. The A2A adapter only renders stored dates. Neither path changes; the contract
   description states the meaning a sender must honour.
5. `cmd/api/tzdata.go` imports `time/tzdata`, so a zone resolves on any base image, not only one that ships the zone
   database.
6. The spec copy the gateway's contract test reads
   (`internal/adapters/outbound/deliverypromise/testdata/delivery-promise.openapi.yaml`) gains the optional
   `timeZone`, ahead of the warehouse-docs copy; its README records the edit.

## Consequences

- The cart shows the day the shopper will actually receive the delivery, including across DST changes.
- Carts already holding a quote keep their UTC dates until they are re-quoted (a quote expires in minutes).
- Until delivery-promise ships the field, every window logs one WARN for the empty name and dates stay UTC:
  the fallback is today's behaviour, so the two repos can merge in either order.
- A sender of `delivery-requote-acceptances` that still formats windows in UTC sends UTC days; that is the sender's
  mapping to fix, not something the cart can detect.
