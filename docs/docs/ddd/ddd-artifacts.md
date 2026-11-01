---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 10
description: "Index of the ddd-crew artifacts of Shopping Cart: core domain chart, bounded context canvas, context map, aggregate design canvas, message flow, EventStorming, ubiquitous language, class, ER, sequence diagrams and domain events."
---

# DDD artifacts (ddd-crew)

The strategic and tactical design of `shopping-cart`, drawn with the
[ddd-crew](https://github.com/ddd-crew) tools plus UML and ER diagrams. Every
diagram is Mermaid, checked against the code on `develop`, and carries a
`Source:` line naming the files it came from. The aggregator holds an earlier,
model-derived version of this pack under
[Shopping Cart in warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/contexts/shopping-cart);
where the two differ, these pages follow the code.

| Artifact | ddd-crew tool / notation | What it shows |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Where this context sits on business differentiation vs model complexity, and why it is Supporting. |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, business decisions, metrics. |
| [Context map](/docs/ddd/context-map) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | This context's slice of the fleet map: the pattern, channel and built status of each edge. |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | `Cart`: states, invariants, commands, events, concurrency, and the three read models. |
| [Domain message flow](/docs/ddd/domain-message-flow) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | The key scenarios as numbered command / event / query flows across contexts. |
| [EventStorming](/docs/ddd/eventstorming) | [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Design-level process flow with sticky-note colours, plus hotspots. |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Glossary | Every term with the code identifier that implements it. |
| [Class diagrams](/docs/ddd/class-diagram) | UML class diagram | Domain types, and the hexagonal ports with the adapters that exist. |
| [Entity-relationship diagram](/docs/ddd/entity-relationship) | ER diagram | The Postgres schema created by migration `0001_shopping_cart_schema`: seven tables and their keys. |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | UML sequence diagram | Add a line, quote delivery, close on checkout, the sweeps and the price feed, with error branches. |
| [Domain events](/docs/ddd/domain-events) | Event catalogue | Every event published and consumed: full CloudEvents type, topic, key, payload, producer, consumers. |

Related pages outside the pack: [Use cases](/docs/ddd/use-cases),
[Subdomain classification](/docs/ddd/subdomain-classification),
[Integration](/docs/ecosystem/integration) and the
[event catalogue](/docs/api-reference/events).

:::note[Sources of truth]
The code wins. Domain rules come from `internal/domain/cart`, `estimate` and
`shared`, behaviour from `internal/application/usecases`, the envelope from
`internal/adapters/kafka/cloudevents`, the schema from
`internal/adapters/outbound/postgres/migrations/`, the REST contract from
`apis/openapi.yaml`, the event contract from `apis/asyncapi.yaml`, and decisions
from the ADRs in `docs/adr/`. Where a page and the code disagree, the page is
wrong.
:::

:::info Not in this pack
The A2A agent ([ADR 0006](/docs/adr/0006-a2a-inbound-adapter)) is built, but it is
a read surface over the same two queries and adds no aggregate, entity or event,
so the pack has no page for it. There is no MCP server. The application layer, the
HTTP, Kafka and scheduler adapters, the outbox relay and the database schema are
covered by the pages above.
:::
