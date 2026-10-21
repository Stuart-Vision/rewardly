package promo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service applies the voucher and referral rules against Postgres.
//
// Concurrency model (READ COMMITTED + explicit row locks):
//   - Checkout locks the customer row first, so one customer's checkouts run one
//     at a time. That makes "first order" and "per-customer limit" checks safe.
//   - It then locks the campaign row, so redemptions of one campaign queue up and
//     each sees the previous one's redeemed_count. The budget can't be oversold.
//   - Locks are always taken in that order (customer, then campaign), so two
//     checkouts can't deadlock each other.
//   - FOR NO KEY UPDATE is used instead of FOR UPDATE so that inserts which only
//     reference these rows through a foreign key are not blocked.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const campaignCols = `c.id, c.name, c.kind, c.value, c.max_discount_cents, c.min_order_cents,
	c.new_customers_only, c.per_customer_limit, c.max_redemptions, c.redeemed_count, c.starts_at, c.ends_at`

func scanCampaign(row pgx.Row, extra ...any) (Campaign, error) {
	var c Campaign
	dest := append([]any{&c.ID, &c.Name, &c.Kind, &c.Value, &c.MaxDiscountCents, &c.MinOrderCents,
		&c.NewCustomersOnly, &c.PerCustomerLimit, &c.MaxRedemptions, &c.RedeemedCount, &c.StartsAt, &c.EndsAt}, extra...)
	err := row.Scan(dest...)
	return c, err
}

const orderSelect = `SELECT o.id, o.customer_id, o.subtotal_cents, o.discount_cents, v.code, o.created_at
	FROM orders o LEFT JOIN vouchers v ON v.id = o.voucher_id`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.CustomerID, &o.SubtotalCents, &o.DiscountCents, &o.VoucherCode, &o.CreatedAt)
	o.TotalCents = o.SubtotalCents - o.DiscountCents
	return o, err
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ---- Customers ----

func (s *Service) CreateCustomer(ctx context.Context, in NewCustomer) (Customer, error) {
	in.Email = strings.TrimSpace(in.Email)
	in.Name = strings.TrimSpace(in.Name)
	in.ReferralCode = normalizeCode(in.ReferralCode)
	if !emailPattern.MatchString(in.Email) || len(in.Email) > 254 {
		return Customer{}, invalid("a valid e-mail address is required")
	}
	if in.Name == "" || len(in.Name) > 80 {
		return Customer{}, invalid("name must be 1 to 80 characters")
	}

	var c Customer
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var referrer *int64
		if in.ReferralCode != "" {
			var id int64
			err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE referral_code = $1`, in.ReferralCode).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnknownReferral
			}
			if err != nil {
				return err
			}
			referrer = &id
		}
		err := tx.QueryRow(ctx, `INSERT INTO customers (email, name, referral_code, referred_by)
			VALUES ($1, $2, $3, $4)
			RETURNING id, email, name, referral_code, referred_by, created_at`,
			in.Email, in.Name, randomCode(8), referrer,
		).Scan(&c.ID, &c.Email, &c.Name, &c.ReferralCode, &c.ReferredBy, &c.CreatedAt)
		if isUniqueViolation(err, "customers_email_key") {
			return ErrEmailTaken
		}
		if err != nil {
			return err
		}
		// A referred customer gets a welcome voucher straight away.
		if referrer != nil {
			return issueVoucher(ctx, tx, "referral_welcome", c.ID)
		}
		return nil
	})
	return c, err
}

func (s *Service) ListCustomers(ctx context.Context) ([]CustomerSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.email, c.name, c.referral_code, c.referred_by, c.created_at,
		       (SELECT count(*) FROM orders o WHERE o.customer_id = c.id),
		       (SELECT count(*) FROM customers r WHERE r.referred_by = c.id)
		FROM customers c ORDER BY c.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CustomerSummary, error) {
		var c CustomerSummary
		err := row.Scan(&c.ID, &c.Email, &c.Name, &c.ReferralCode, &c.ReferredBy, &c.CreatedAt, &c.Orders, &c.Referrals)
		return c, err
	})
}

func (s *Service) customerExists(ctx context.Context, id int64) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1)`, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrCustomerNotFound
	}
	return nil
}

// CustomerVouchers lists the personal vouchers (referral rewards) a customer owns.
func (s *Service) CustomerVouchers(ctx context.Context, customerID int64) ([]WalletVoucher, error) {
	if err := s.customerExists(ctx, customerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+campaignCols+`, v.code, EXISTS (SELECT 1 FROM orders o WHERE o.voucher_id = v.id)
		FROM vouchers v JOIN campaigns c ON c.id = v.campaign_id
		WHERE v.customer_id = $1 ORDER BY v.id DESC`, customerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (WalletVoucher, error) {
		var w WalletVoucher
		c, err := scanCampaign(row, &w.Code, &w.Used)
		w.Campaign = c
		return w, err
	})
}

func (s *Service) CustomerOrders(ctx context.Context, customerID int64) ([]Order, error) {
	if err := s.customerExists(ctx, customerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, orderSelect+` WHERE o.customer_id = $1 ORDER BY o.id DESC LIMIT 50`, customerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Order, error) { return scanOrder(row) })
}

// ---- Campaigns ----

func (s *Service) CreateCampaign(ctx context.Context, in NewCampaign) (CampaignStats, error) {
	if err := in.Validate(); err != nil {
		return CampaignStats{}, err
	}
	out := CampaignStats{Code: in.Code}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := scanCampaign(tx.QueryRow(ctx, `
			INSERT INTO campaigns AS c (name, kind, value, max_discount_cents, min_order_cents, new_customers_only,
			                            per_customer_limit, max_redemptions, starts_at, ends_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING `+campaignCols,
			in.Name, in.Kind, in.Value, in.MaxDiscountCents, in.MinOrderCents, in.NewCustomersOnly,
			in.PerCustomerLimit, in.MaxRedemptions, in.StartsAt, in.EndsAt))
		if err != nil {
			return err
		}
		out.Campaign = c
		_, err = tx.Exec(ctx, `INSERT INTO vouchers (campaign_id, code) VALUES ($1, $2)`, c.ID, in.Code)
		if isUniqueViolation(err, "vouchers_code_key") {
			return ErrCodeTaken
		}
		return err
	})
	return out, err
}

func (s *Service) ListCampaigns(ctx context.Context) ([]CampaignStats, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+campaignCols+`,
		       coalesce((SELECT v.code FROM vouchers v WHERE v.campaign_id = c.id AND v.customer_id IS NULL ORDER BY v.id LIMIT 1), ''),
		       c.slug IS NOT NULL,
		       (SELECT coalesce(sum(o.discount_cents), 0) FROM orders o JOIN vouchers v ON v.id = o.voucher_id WHERE v.campaign_id = c.id)
		FROM campaigns c ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CampaignStats, error) {
		var cs CampaignStats
		c, err := scanCampaign(row, &cs.Code, &cs.System, &cs.DiscountGivenCents)
		cs.Campaign = c
		return cs, err
	})
}

// ---- Quote and checkout ----

// customerState returns who referred the customer and how many orders they have placed.
// With lock set it takes a row lock on the customer.
func customerState(ctx context.Context, tx pgx.Tx, id int64, lock bool) (referredBy *int64, priorOrders int, err error) {
	q := `SELECT referred_by FROM customers WHERE id = $1`
	if lock {
		q += ` FOR NO KEY UPDATE`
	}
	err = tx.QueryRow(ctx, q, id).Scan(&referredBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrCustomerNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE customer_id = $1`, id).Scan(&priorOrders)
	return referredBy, priorOrders, err
}

type applied struct {
	voucherID int64
	campaign  Campaign
	discount  int
}

// evaluate checks a voucher for a customer's order. With lock set it takes a row
// lock on the campaign, so concurrent redemptions of one campaign are serialised.
func (s *Service) evaluate(ctx context.Context, tx pgx.Tx, customerID int64, priorOrders int, code string, subtotal int, lock bool) (applied, error) {
	q := `SELECT ` + campaignCols + `, v.id, v.customer_id
		FROM vouchers v JOIN campaigns c ON c.id = v.campaign_id WHERE v.code = $1`
	if lock {
		q += ` FOR NO KEY UPDATE OF c`
	}
	var a applied
	var owner *int64
	c, err := scanCampaign(tx.QueryRow(ctx, q, code), &a.voucherID, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrVoucherNotFound
	}
	if err != nil {
		return a, err
	}
	// Someone else's personal voucher: answer as if it did not exist.
	if owner != nil && *owner != customerID {
		return a, ErrVoucherNotFound
	}
	var priorUses int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE customer_id = $1 AND voucher_id = $2`,
		customerID, a.voucherID).Scan(&priorUses); err != nil {
		return a, err
	}
	if err := Check(c, subtotal, Usage{PriorOrders: priorOrders, PriorUses: priorUses}, s.now()); err != nil {
		return a, err
	}
	a.campaign = c
	a.discount = Discount(c, subtotal)
	return a, nil
}

// Quote previews the discount a voucher would give. It never writes.
func (s *Service) Quote(ctx context.Context, in QuoteRequest) (Quote, error) {
	code := normalizeCode(in.Code)
	if code == "" {
		return Quote{}, invalid("code is required")
	}
	if err := validateSubtotal(in.SubtotalCents); err != nil {
		return Quote{}, err
	}
	var q Quote
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		_, prior, err := customerState(ctx, tx, in.CustomerID, false)
		if err != nil {
			return err
		}
		a, err := s.evaluate(ctx, tx, in.CustomerID, prior, code, in.SubtotalCents, false)
		if err != nil {
			return err
		}
		q = Quote{Code: code, CampaignName: a.campaign.Name, SubtotalCents: in.SubtotalCents,
			DiscountCents: a.discount, TotalCents: in.SubtotalCents - a.discount}
		return nil
	})
	return q, err
}

func orderByKey(ctx context.Context, db querier, key string) (Order, error) {
	return scanOrder(db.QueryRow(ctx, orderSelect+` WHERE o.idempotency_key = $1`, key))
}

// replay returns the stored result of an earlier checkout with the same key,
// or an error if the key was used for a different checkout.
func replay(o Order, in CheckoutRequest) (Order, bool, error) {
	if o.CustomerID != in.CustomerID || o.SubtotalCents != in.SubtotalCents {
		return Order{}, false, ErrIdempotencyReused
	}
	return o, true, nil
}

// Checkout places an order, applying a voucher if one is given. The order, the
// campaign counter and any referral reward are written in one transaction.
//
// It is idempotent: retrying with the same Idempotency-Key returns the original
// order (replayed = true) instead of charging or redeeming twice.
func (s *Service) Checkout(ctx context.Context, in CheckoutRequest) (order Order, replayed bool, err error) {
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 100 {
		return Order{}, false, invalid("an Idempotency-Key header of 1 to 100 characters is required")
	}
	if err := validateSubtotal(in.SubtotalCents); err != nil {
		return Order{}, false, err
	}
	code := normalizeCode(in.VoucherCode)

	// Fast path for a plain retry.
	if o, err := orderByKey(ctx, s.pool, in.IdempotencyKey); err == nil {
		return replay(o, in)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, err
	}

	var existing *Order
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		referredBy, prior, err := customerState(ctx, tx, in.CustomerID, true)
		if err != nil {
			return err
		}
		// A concurrent request with the same key may have committed while we waited
		// for the lock. Under READ COMMITTED this new statement sees it.
		if o, err := orderByKey(ctx, tx, in.IdempotencyKey); err == nil {
			existing = &o
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var voucherID *int64
		discount := 0
		if code != "" {
			a, err := s.evaluate(ctx, tx, in.CustomerID, prior, code, in.SubtotalCents, true)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE campaigns SET redeemed_count = redeemed_count + 1 WHERE id = $1`, a.campaign.ID); err != nil {
				return err
			}
			voucherID, discount = &a.voucherID, a.discount
		}

		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO orders (customer_id, idempotency_key, subtotal_cents, discount_cents, voucher_id)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			in.CustomerID, in.IdempotencyKey, in.SubtotalCents, discount, voucherID).Scan(&id); err != nil {
			return err
		}

		// Growth loop: a referred customer's first order earns their referrer a reward.
		if prior == 0 && referredBy != nil {
			if err := issueVoucher(ctx, tx, "referral_reward", *referredBy); err != nil {
				return err
			}
		}
		order, err = scanOrder(tx.QueryRow(ctx, orderSelect+` WHERE o.id = $1`, id))
		return err
	})
	if existing != nil {
		return replay(*existing, in)
	}
	if isUniqueViolation(err, "orders_idempotency_key_key") {
		// The same key was committed by a request for another customer.
		o, err := orderByKey(ctx, s.pool, in.IdempotencyKey)
		if err != nil {
			return Order{}, false, err
		}
		return replay(o, in)
	}
	if err != nil {
		return Order{}, false, err
	}
	return order, false, nil
}

// ---- Referrals ----

// issueVoucher gives a customer a personal voucher for a system campaign.
func issueVoucher(ctx context.Context, tx pgx.Tx, slug string, customerID int64) error {
	tag, err := tx.Exec(ctx, `INSERT INTO vouchers (campaign_id, code, customer_id)
		SELECT id, $2, $3 FROM campaigns WHERE slug = $1`, slug, "REF"+randomCode(8), customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("system campaign %q is missing", slug)
	}
	return nil
}

// Stats returns the growth numbers for the dashboard.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customers),
		(SELECT count(*) FROM customers WHERE referred_by IS NOT NULL),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM orders WHERE voucher_id IS NOT NULL),
		(SELECT coalesce(sum(discount_cents), 0) FROM orders),
		(SELECT coalesce(sum(subtotal_cents - discount_cents), 0) FROM orders)`).Scan(
		&st.Customers, &st.ReferredCustomers, &st.Orders, &st.OrdersWithVoucher, &st.DiscountGivenCents, &st.RevenueCents)
	return st, err
}

// randomCode returns n characters from an alphabet without look-alikes (0/O, 1/I).
func randomCode(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
