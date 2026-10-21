# Rewardly: vouchers and referrals for a food delivery app

[![CI](https://github.com/Stuart-Vision/rewardly/actions/workflows/ci.yml/badge.svg)](https://github.com/Stuart-Vision/rewardly/actions/workflows/ci.yml)

A growth feature set of the kind delivery platforms use to win and keep customers: **voucher campaigns** (percent or fixed discounts with budgets, minimum orders, first-order-only rules and per-customer limits) and a **referral programme** (a friend gets a welcome voucher, and the referrer is rewarded when that friend places a first order).

**Go** REST API · **React + TypeScript** frontend · **PostgreSQL**

![Checkout](docs/checkout.png)

## The hard part: correctness under concurrency

Vouchers are money. When a flash sale has a budget of 5 redemptions and 40 customers press "Place order" at the same moment, exactly 5 may win. The checkout runs in one Postgres transaction:

```
BEGIN                                              (READ COMMITTED)
  SELECT … FROM customers WHERE id = $1 FOR NO KEY UPDATE      ← one checkout per customer at a time
  SELECT … FROM orders WHERE idempotency_key = $2              ← a retry returns the original order
  SELECT … FROM vouchers JOIN campaigns … FOR NO KEY UPDATE OF campaigns   ← redemptions of one campaign queue up
  check the rules (dates, budget, first order, per-customer limit, minimum order)
  UPDATE campaigns SET redeemed_count = redeemed_count + 1
  INSERT INTO orders …
  INSERT INTO vouchers …   (referral reward, only on the referred customer's first order)
COMMIT
```

| Decision | Why |
| --- | --- |
| **Row locks (pessimistic)** rather than SERIALIZABLE with retries | Contention is concentrated on one hot row (the campaign). Queuing on it is simple and predictable; serialisable retries would mostly fail and retry under a flash sale. |
| **Fixed lock order** (customer, then campaign) | Two checkouts can never wait on each other in a cycle, so they can't deadlock. |
| **`FOR NO KEY UPDATE`**, not `FOR UPDATE` | Inserts that only *reference* the locked row through a foreign key (new orders, new vouchers) are not blocked. |
| **Idempotency keys** | The client sends an `Idempotency-Key` header. Retrying after a timeout returns the original order (`200` + `Idempotent-Replayed: true`) instead of charging twice. Reusing a key for a different order is refused with `409`. |
| **Defence in depth** | `CHECK (redeemed_count <= max_redemptions)` means the database itself refuses to oversell, even if application code had a bug. |
| **Integer cents** | Money is never a float. Percent discounts round down to a whole cent and never exceed the subtotal. |

### Tests prove it, and the tests were checked too

`go test ./...` runs:

- **Unit tests** (table-driven) for every discount and eligibility rule, plus input validation.
- **HTTP handler tests** with `httptest` and a fake service: status codes, error mapping, JSON validation, CORS, request IDs and metrics.
- **Integration tests against a real PostgreSQL** that release goroutines at the same instant:
  - 40 customers race for a budget of 10: exactly 10 win, 30 get `budget_exhausted`, and no orphan orders are left.
  - 1 customer fires 10 checkouts with a once-only voucher: exactly 1 succeeds.
  - 8 identical retries with the same idempotency key produce exactly 1 order.
  - A referred customer places 10 orders at once: the referrer gets exactly 1 reward.

To make sure these tests can actually fail, each lock was removed in turn and the suite re-run:

| Lock removed | Result |
| --- | --- |
| Campaign row lock | The budget test **fails**. Concurrent checkouts pass the budget check together, and the database CHECK constraint rejects the extra ones with errors. |
| Customer row lock | The referral test **fails**. The referrer received 2 and then 7 rewards instead of 1. |

## Schema (3NF)

```
customers (id, email UNIQUE lower(), name, referral_code UNIQUE, referred_by → customers)
campaigns (id, slug, name, kind, value, max_discount_cents, min_order_cents,
           new_customers_only, per_customer_limit, max_redemptions, redeemed_count, starts_at, ends_at)
vouchers  (id, campaign_id → campaigns, code UNIQUE, customer_id → customers NULL = public)
orders    (id, customer_id → customers, idempotency_key UNIQUE, subtotal_cents, discount_cents,
           voucher_id → vouchers NULL)
```

- The order total is derived (`subtotal − discount`), so it is not stored.
- `redeemed_count` is the one deliberate denormalisation. It gives an O(1) budget check under the row lock, guarded by a CHECK constraint.
- Indexes match the queries:
  - `orders (customer_id, voucher_id)` answers "how often has this customer used this voucher?"
  - Partial indexes on `vouchers (customer_id)` and `customers (referred_by)` index only the rows that have a value.
  - A unique index on `lower(email)` makes e-mail addresses unique regardless of case.

Migrations are plain SQL files embedded in the binary. They are applied at start-up under a Postgres advisory lock, so several instances can start at once safely.

## API

| Method | Path | |
| --- | --- | --- |
| `POST` | `/api/customers` | Sign up, optionally with `referralCode` |
| `GET` | `/api/customers` | Customers with order and referral counts |
| `GET` | `/api/customers/{id}/vouchers` | Personal vouchers (wallet) |
| `GET` | `/api/customers/{id}/orders` | Order history |
| `GET` / `POST` | `/api/campaigns` | List with redemption stats / create |
| `POST` | `/api/quote` | Preview a voucher's discount (read-only transaction) |
| `POST` | `/api/checkout` | Place an order; needs an `Idempotency-Key` header |
| `GET` | `/api/stats` | Growth numbers for the dashboard |
| `GET` | `/healthz`, `/readyz` | Liveness, and readiness (checks the database) |
| `GET` | `/metrics` | Prometheus metrics |

Errors share one shape with a stable code the UI can rely on: `{"error": {"code": "budget_exhausted", "message": "…"}}`. The codes map to statuses as follows: `400` invalid input, `404` not found, `409` conflict, `422` turned down by a voucher rule.

## Operations

- **Structured JSON logs** (`log/slog`) with a request ID per request, taken from `X-Request-ID` or generated.
- **Prometheus metrics:**
  - `http_requests_total` and `http_request_duration_seconds`, labelled by route *pattern* so the number of series stays bounded.
  - Business metrics: `rewardly_vouchers_redeemed_total`, `rewardly_discount_cents_total`, and `rewardly_checkout_rejections_total{reason}`. A spike in `budget_exhausted` means a campaign has sold out.
- **Graceful shutdown** on `SIGTERM`: in-flight requests finish before exit.
- **Server timeouts**: read, write and idle timeouts are set.
- **12-factor config** via environment variables: `DATABASE_URL`, `PORT`, `CORS_ORIGIN`.
- **Small, safe container image**: multi-stage Docker build into a distroless image that runs as a non-root user.
- **CI** (GitHub Actions):
  - Backend: gofmt check, `go vet`, then the tests with `-race` against a Postgres service container, then a Docker build.
  - Frontend: type-check and build.

## Run it

```bash
# Option 1: Docker
docker compose up --build          # API on http://localhost:8080

# Option 2: locally (Go 1.26+, Node 22+, a Postgres database)
cd backend
DATABASE_URL=postgres://user:pass@localhost:5432/rewardly go run ./cmd/api

cd frontend
npm install && npm run dev         # http://localhost:5173 (proxies /api to :8080; set API_PROXY to change)
```

Tests:

```bash
cd backend
go test ./...                                                        # unit + HTTP tests
TEST_DATABASE_URL=postgres://…/rewardly_test go test -race ./...     # also the Postgres integration tests (drops the schema!)
```

Demo vouchers: `WELCOME20` (20% off a first order, max 10.00), `LUNCH5` (5.00 off orders over 25.00), `FLASH50` (50% off, only 5 available).

## Layout

```
backend/
  cmd/api/              main: config, migrations, HTTP server, graceful shutdown
  internal/promo/       domain: rules.go (pure rules), service.go (transactions), errors.go
  internal/httpapi/     handlers, middleware (request ID, logging, metrics, recovery, CORS)
  internal/db/          pool and embedded SQL migrations
frontend/src/           React + TypeScript (checkout, customers and referrals, campaigns)
```

| Customers and referrals | Campaigns |
| --- | --- |
| ![Customers](docs/customers.png) | ![Campaigns](docs/campaigns.png) |
