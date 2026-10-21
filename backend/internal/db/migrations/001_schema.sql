-- Rewardly schema (3NF). Money is stored as integer cents, never floating point.

-- A customer can share their referral_code; referred_by records who invited them.
CREATE TABLE customers (
    id            BIGSERIAL   PRIMARY KEY,
    email         TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    referral_code TEXT        NOT NULL UNIQUE,
    referred_by   BIGINT      REFERENCES customers (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- E-mail addresses are unique regardless of case.
CREATE UNIQUE INDEX customers_email_key ON customers (lower(email));
-- Partial index: only referred customers are indexed (used to count referrals).
CREATE INDEX customers_referred_by_idx ON customers (referred_by) WHERE referred_by IS NOT NULL;

-- A campaign holds the discount rules. Vouchers are the codes that apply a campaign.
CREATE TABLE campaigns (
    id                 BIGSERIAL   PRIMARY KEY,
    slug               TEXT        UNIQUE, -- set only for system campaigns (referral rewards)
    name               TEXT        NOT NULL,
    kind               TEXT        NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value              INTEGER     NOT NULL CHECK (value > 0), -- percent (1-100) or cents
    max_discount_cents INTEGER     CHECK (max_discount_cents > 0),
    min_order_cents    INTEGER     NOT NULL DEFAULT 0 CHECK (min_order_cents >= 0),
    new_customers_only BOOLEAN     NOT NULL DEFAULT false,
    per_customer_limit INTEGER     NOT NULL DEFAULT 1 CHECK (per_customer_limit > 0),
    max_redemptions    INTEGER     CHECK (max_redemptions > 0), -- total budget; NULL = unlimited
    -- Deliberate denormalisation: a counter makes the budget check O(1) under a row lock.
    -- The CHECK below means the database itself refuses to oversell a campaign.
    redeemed_count     INTEGER     NOT NULL DEFAULT 0,
    starts_at          TIMESTAMPTZ,
    ends_at            TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (kind <> 'percent' OR value <= 100),
    CHECK (max_redemptions IS NULL OR redeemed_count <= max_redemptions),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at)
);

-- Public vouchers have no owner; personal vouchers (referral rewards) belong to one customer.
CREATE TABLE vouchers (
    id          BIGSERIAL   PRIMARY KEY,
    campaign_id BIGINT      NOT NULL REFERENCES campaigns (id),
    code        TEXT        NOT NULL UNIQUE CHECK (code = upper(code)),
    customer_id BIGINT      REFERENCES customers (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX vouchers_campaign_idx ON vouchers (campaign_id);
CREATE INDEX vouchers_customer_idx ON vouchers (customer_id) WHERE customer_id IS NOT NULL;

-- One row per checkout. The total is derived (subtotal - discount), so it is not stored.
CREATE TABLE orders (
    id              BIGSERIAL   PRIMARY KEY,
    customer_id     BIGINT      NOT NULL REFERENCES customers (id),
    idempotency_key TEXT        NOT NULL UNIQUE,
    subtotal_cents  INTEGER     NOT NULL CHECK (subtotal_cents > 0),
    discount_cents  INTEGER     NOT NULL DEFAULT 0,
    voucher_id      BIGINT      REFERENCES vouchers (id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (discount_cents >= 0 AND discount_cents <= subtotal_cents),
    CHECK (voucher_id IS NOT NULL OR discount_cents = 0)
);
-- Serves "orders of a customer, newest first" and "how often did this customer use voucher X".
CREATE INDEX orders_customer_idx ON orders (customer_id, voucher_id);
CREATE INDEX orders_voucher_idx ON orders (voucher_id) WHERE voucher_id IS NOT NULL;
