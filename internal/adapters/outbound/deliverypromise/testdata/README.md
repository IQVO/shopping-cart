# delivery-promise contract data

`delivery-promise.openapi.yaml` is a byte copy of `network/apis/delivery-promise/openapi.yaml` from
warehouse-docs `origin/main` at commit `7101fdb615ed58f6e6db917abd862cb1563fd7cc` (the network OHS), plus one additive edit made
ahead of the docs copy (ADR 0007): the optional `timeZone` property of `DeliveryWindow` that delivery-promise
adds on branch `feature/window-time-zone`. Re-copy from warehouse-docs once that change lands there. Only
`POST /delivery-quotes` (operation `quoteDeliveryOffers`) is called by shopping-cart.

`quote-response.example.json` is a response that satisfies the `QuoteResponse` schema of that copy (the spec
carries no inline example for this route). `TestExampleSatisfiesTheSpec` checks it against the copy's
`required` lists, and `TestGatewayDecodesTheSpecExample` feeds it through the gateway. Its first offer names
`America/Chicago`; the other two omit `timeZone`, as an offer quoted before the field existed does. Refresh both
together when the network contract changes.
