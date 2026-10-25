-- shopping-cart schema (ADR 0002, 0003). The cart holds no PII and no card
-- data: a shopper is an opaque reference, the destination a postal PREFIX.

CREATE TABLE carts (
    cart_id         text        PRIMARY KEY,
    shopper_id      text,
    state           text        NOT NULL CHECK (state IN ('OPEN','CHECKED_OUT','ABANDONED','MERGED')),
    destination     text,
    lines           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    quote           jsonb,
    selections      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at      timestamptz NOT NULL,
    last_changed_at timestamptz NOT NULL,
    version         bigint      NOT NULL CHECK (version >= 1)
);
CREATE INDEX carts_shopper_idx ON carts (shopper_id, created_at DESC, cart_id DESC) WHERE shopper_id IS NOT NULL;
CREATE INDEX carts_idle_idx ON carts (state, last_changed_at);

-- PurchasabilityView: the event-fed local read model of catalogue items and
-- their effective prices. A row may be a stub (catalogued false) when a price
-- event arrives before CatalogItemCreated.
CREATE TABLE rm_items (
    catalog_item_id text        PRIMARY KEY,
    catalogued      boolean     NOT NULL DEFAULT false,
    discontinued    boolean     NOT NULL DEFAULT false,
    priced          boolean     NOT NULL DEFAULT false,
    price_minor     bigint      NOT NULL DEFAULT 0 CHECK (price_minor >= 0),
    currency        text        NOT NULL DEFAULT '',
    price_version   bigint      NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL
);

-- EstimateView: the one current delivery charge schedule and tax table.
CREATE TABLE rm_current_schedule (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version   bigint  NOT NULL CHECK (version >= 1),
    tiers     jsonb   NOT NULL
);

CREATE TABLE rm_current_tax_table (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version   bigint  NOT NULL CHECK (version >= 1),
    bands     jsonb   NOT NULL
);

-- Infrastructure tables: inbox, idempotency keys, transactional outbox.
CREATE TABLE processed_events (
    event_id     text        PRIMARY KEY,
    event_type   text        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
    route        text        NOT NULL,
    key          text        NOT NULL,
    request_hash text        NOT NULL,
    status       integer     NOT NULL,
    content_type text        NOT NULL,
    body         bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (route, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE outbox_events (
    id           bigserial   PRIMARY KEY,
    event_id     text        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    subject      text        NOT NULL,
    key          bytea       NOT NULL,
    dataschema   text        NOT NULL,
    value        bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (id) WHERE published_at IS NULL;
