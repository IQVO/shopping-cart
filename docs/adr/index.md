---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `shopping-cart`, in the order they were taken. The ADR bodies are
immutable; a changed decision gets a new ADR that supersedes the old one. The
*Status* column is copied from each ADR's own status line.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-shopping-cart-bounded-context) | Shopping Cart is a bounded context of the storefront domain | Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `shopping-cart`. |
| [0002](/docs/adr/0002-shopping-cart-assumptions) | Aggregates, invariants and assumptions | Accepted (2026-10-25). |
| [0003](/docs/adr/0003-consumed-contracts-and-local-read-model) | Consumed contracts and the local read models | Accepted (2026-10-25). |
| [0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) | CloudEvents envelope and type catalogue | Accepted (2026-10-25). |
| [0005](/docs/adr/0005-fleet-observability-standard) | Adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26). |
| [0006](/docs/adr/0006-a2a-inbound-adapter) | A2A inbound adapter (read-only agent) | Accepted (2026-10-30). |
| [0007](/docs/adr/0007-delivery-dates-in-the-destinations-local-calendar) | Delivery dates are days of the destination's local calendar | Accepted (2026-10-30). Supersedes the "`deliverBy*` are the UTC dates of the offer window" clause of |
