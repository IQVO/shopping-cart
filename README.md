# shopping-cart

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Shopping Cart** (Supporting, storefront tier).

Holds a shopper's intent: cart lines, the destination postal prefix, the delivery quote delivery-promise returned for the cart (per shipment group, with its offers and expiry), the shopper's tier selection per group, and a labelled, non-binding estimate (effective prices + selected tier charges + tax band) so the total cost is visible up front [1].

Status: built to the model (domain, application, Postgres, Kafka, REST, BDD); the PR is awaiting review. The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.shopping-cart.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
