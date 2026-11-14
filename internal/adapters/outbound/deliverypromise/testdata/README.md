# delivery-promise contract data

`delivery-promise.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/delivery-promise `develop`
(the producer's own file; the former warehouse-docs copy and the local `timeZone` edit of ADR 0007 are superseded,
the producer now declares `DeliveryWindow.timeZone`). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the producer's `develop`.
Refresh = copy the producer's file over it, never edit it, then fix what the contract tests report.
Only `POST /delivery-quotes` (operation `quoteDeliveryOffers`) is called by shopping-cart.

Re-pinned 2026-11-14 after delivery-promise ADR 0026 (CARRIER_HANDOFF areas: `carrierId`, `handoffNodeId`,
`carrierArea`, `carrierWindowHours`) and ADR 0027 (`pickupPointId`; exactly one of `destinationPostalPrefix` or
`pickupPointId`). All of it is additive for this caller: the cart still sends `destinationPostalPrefix` and
its tolerant decoder ignores the new response fields. Whether the cart should carry a pickup point or show a
carrier window is a product decision, not part of a re-pin.

`quote-response.example.json` is a response that satisfies the `QuoteResponse` schema of that copy (the spec
carries no inline example for this route). `TestExampleSatisfiesTheSpec` checks it against the copy's
`required` lists, and `TestGatewayDecodesTheSpecExample` feeds it through the gateway. Its first offer names
`America/Chicago`; the other two omit `timeZone`, as an offer quoted before the field existed does. Every offer
carries `orderByAt` (required since delivery-promise 0.2.0; the cart does not read it). Refresh both
together when the network contract changes.
