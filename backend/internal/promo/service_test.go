package promo

// Integration tests against a real Postgres. They run when TEST_DATABASE_URL is set
// (CI starts a Postgres service container) and are skipped otherwise.
// Each test drops and recreates the schema, so point it at a throwaway database.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Stuart-Vision/rewardly/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database tests")
	}
	ctx := context.Background()
	// Enough connections that every racing goroutine really runs at the same time.
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	pool, err := db.Connect(ctx, url+sep+"pool_max_conns=50")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Open the connections up front; otherwise racing goroutines are spread out
	// by connection setup time and rarely overlap.
	var conns []*pgxpool.Conn
	for range 20 {
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Release()
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return NewService(pool)
}

func mustCustomer(t *testing.T, s *Service, email, referral string) Customer {
	t.Helper()
	c, err := s.CreateCustomer(context.Background(), NewCustomer{Email: email, Name: email, ReferralCode: referral})
	if err != nil {
		t.Fatalf("create customer %s: %v", email, err)
	}
	return c
}

func mustCampaign(t *testing.T, s *Service, in NewCampaign) {
	t.Helper()
	if _, err := s.CreateCampaign(context.Background(), in); err != nil {
		t.Fatalf("create campaign %s: %v", in.Code, err)
	}
}

// race runs fn(0..n-1) in n goroutines released at the same instant, so they all
// read the database before any of them writes: the worst case for a race condition.
func race(n int, fn func(i int)) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn(i)
		}()
	}
	close(start)
	wg.Wait()
}

func errCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return "unexpected: " + err.Error()
	}
	return ""
}

func campaignByCode(t *testing.T, s *Service, code string) CampaignStats {
	t.Helper()
	list, err := s.ListCampaigns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.Code == code {
			return c
		}
	}
	t.Fatalf("campaign %s not found", code)
	return CampaignStats{}
}

// 40 different customers race for a voucher with a budget of 10.
// Exactly 10 must win; the rest must be told the budget is used up.
func TestCheckoutNeverOversellsBudget(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCampaign(t, s, NewCampaign{Code: "RACE10", Name: "Race", Kind: Fixed, Value: 300, MaxRedemptions: ptr(10)})

	const n = 40
	customers := make([]Customer, n)
	for i := range customers {
		customers[i] = mustCustomer(t, s, fmt.Sprintf("racer%d@example.com", i), "")
	}

	codes := make([]string, n)
	race(n, func(i int) {
		_, _, err := s.Checkout(ctx, CheckoutRequest{
			CustomerID: customers[i].ID, SubtotalCents: 2000, VoucherCode: "race10",
			IdempotencyKey: fmt.Sprintf("race-%d", i),
		})
		codes[i] = errCode(err)
	})

	won, soldOut := 0, 0
	for _, c := range codes {
		switch c {
		case "":
			won++
		case "budget_exhausted":
			soldOut++
		default:
			t.Errorf("unexpected result %q", c)
		}
	}
	if won != 10 || soldOut != n-10 {
		t.Errorf("won %d, sold out %d; want 10 and %d", won, soldOut, n-10)
	}
	if got := campaignByCode(t, s, "RACE10"); got.RedeemedCount != 10 || got.DiscountGivenCents != 10*300 {
		t.Errorf("redeemed %d, discount %d; want 10 and %d", got.RedeemedCount, got.DiscountGivenCents, 10*300)
	}
	// Rejected checkouts roll back completely: no order without its voucher.
	if st, _ := s.Stats(ctx); st.Orders != 10 {
		t.Errorf("orders = %d, want 10", st.Orders)
	}
}

// One customer fires 10 checkouts at once with a once-per-customer voucher.
func TestCheckoutPerCustomerLimitUnderConcurrency(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCampaign(t, s, NewCampaign{Code: "ONCE", Name: "Once", Kind: Fixed, Value: 200})
	c := mustCustomer(t, s, "eager@example.com", "")

	var mu sync.Mutex
	results := map[string]int{}
	race(10, func(i int) {
		_, _, err := s.Checkout(ctx, CheckoutRequest{CustomerID: c.ID, SubtotalCents: 1000, VoucherCode: "ONCE",
			IdempotencyKey: fmt.Sprintf("once-%d", i)})
		mu.Lock()
		results[errCode(err)]++
		mu.Unlock()
	})
	if results[""] != 1 || results["already_used"] != 9 {
		t.Errorf("results = %v, want 1 success and 9 already_used", results)
	}
}

// A client retrying the same request (say, after a timeout) must not create a second order.
func TestCheckoutIsIdempotent(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	c := mustCustomer(t, s, "retry@example.com", "")
	req := CheckoutRequest{CustomerID: c.ID, SubtotalCents: 3000, VoucherCode: "LUNCH5", IdempotencyKey: "key-1"}

	type result struct {
		order    Order
		replayed bool
		err      error
	}
	results := make([]result, 8)
	race(len(results), func(i int) {
		o, replayed, err := s.Checkout(ctx, req)
		results[i] = result{o, replayed, err}
	})

	fresh := 0
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("checkout failed: %v", r.err)
		}
		if r.order.ID != results[0].order.ID || r.order.DiscountCents != 500 {
			t.Errorf("got order %+v, want the same order with a 500 discount", r.order)
		}
		if !r.replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Errorf("%d requests created an order, want exactly 1", fresh)
	}
	if got := campaignByCode(t, s, "LUNCH5").RedeemedCount; got != 1 {
		t.Errorf("LUNCH5 redeemed %d times, want 1", got)
	}

	// Same key, different order: refuse rather than return the wrong order.
	req.SubtotalCents = 9999
	if _, _, err := s.Checkout(ctx, req); errCode(err) != "idempotency_key_reused" {
		t.Errorf("reused key with a new body: got %v, want idempotency_key_reused", err)
	}
}

func TestReferralLoop(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	alice := mustCustomer(t, s, "alice@example.com", "")
	bob := mustCustomer(t, s, "bob@example.com", alice.ReferralCode)
	if bob.ReferredBy == nil || *bob.ReferredBy != alice.ID {
		t.Fatalf("bob.ReferredBy = %v, want %d", bob.ReferredBy, alice.ID)
	}

	// Bob gets a welcome voucher for signing up with a referral code.
	wallet, err := s.CustomerVouchers(ctx, bob.ID)
	if err != nil || len(wallet) != 1 {
		t.Fatalf("bob's wallet = %v, %v; want one welcome voucher", wallet, err)
	}
	welcome := wallet[0].Code

	// Alice can't use Bob's personal voucher.
	if _, err := s.Quote(ctx, QuoteRequest{CustomerID: alice.ID, Code: welcome, SubtotalCents: 2000}); errCode(err) != "voucher_not_found" {
		t.Errorf("alice using bob's voucher: got %v, want voucher_not_found", err)
	}

	// Bob's first order: 30% of 40.00 is 12.00, capped at 8.00.
	order, _, err := s.Checkout(ctx, CheckoutRequest{CustomerID: bob.ID, SubtotalCents: 4000, VoucherCode: welcome, IdempotencyKey: "bob-1"})
	if err != nil {
		t.Fatal(err)
	}
	if order.DiscountCents != 800 || order.TotalCents != 3200 {
		t.Errorf("bob's order = %+v, want discount 800 and total 3200", order)
	}

	// That first order earns Alice a reward voucher...
	if w, _ := s.CustomerVouchers(ctx, alice.ID); len(w) != 1 || w[0].Campaign.Value != 500 {
		t.Fatalf("alice's wallet = %+v, want one 5.00 reward", w)
	}
	// ...but Bob's second order does not earn her another one.
	if _, _, err := s.Checkout(ctx, CheckoutRequest{CustomerID: bob.ID, SubtotalCents: 1000, IdempotencyKey: "bob-2"}); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.CustomerVouchers(ctx, alice.ID); len(w) != 1 {
		t.Errorf("alice has %d rewards after bob's second order, want 1", len(w))
	}
	if st, _ := s.Stats(ctx); st.ReferredCustomers != 1 || st.Orders != 2 {
		t.Errorf("stats = %+v, want 1 referred customer and 2 orders", st)
	}
}

// A referred customer places several orders at the same moment. Each one could look
// like their "first order", but the referrer must be rewarded exactly once.
func TestReferralRewardIssuedOnceUnderConcurrency(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	alice := mustCustomer(t, s, "alice@example.com", "")
	bob := mustCustomer(t, s, "bob@example.com", alice.ReferralCode)

	race(10, func(i int) {
		if _, _, err := s.Checkout(ctx, CheckoutRequest{CustomerID: bob.ID, SubtotalCents: 1200,
			IdempotencyKey: fmt.Sprintf("bob-%d", i)}); err != nil {
			t.Error(err)
		}
	})
	if w, _ := s.CustomerVouchers(ctx, alice.ID); len(w) != 1 {
		t.Errorf("alice got %d referral rewards, want exactly 1", len(w))
	}
}

func TestQuoteDoesNotRedeem(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	c := mustCustomer(t, s, "quote@example.com", "")
	for range 3 {
		q, err := s.Quote(ctx, QuoteRequest{CustomerID: c.ID, Code: "FLASH50", SubtotalCents: 2000})
		if err != nil || q.DiscountCents != 1000 || q.TotalCents != 1000 {
			t.Fatalf("quote = %+v, %v; want 10.00 off", q, err)
		}
	}
	if got := campaignByCode(t, s, "FLASH50").RedeemedCount; got != 0 {
		t.Errorf("quotes redeemed the voucher %d times, want 0", got)
	}
}

func TestVoucherRulesAgainstHistory(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	c := mustCustomer(t, s, "regular@example.com", "")
	if _, _, err := s.Checkout(ctx, CheckoutRequest{CustomerID: c.ID, SubtotalCents: 1500, IdempotencyKey: "r-1"}); err != nil {
		t.Fatal(err)
	}
	// WELCOME20 is for first orders only, and this customer has ordered before.
	if _, err := s.Quote(ctx, QuoteRequest{CustomerID: c.ID, Code: "WELCOME20", SubtotalCents: 2000}); errCode(err) != "new_customers_only" {
		t.Errorf("WELCOME20 on a repeat order: got %v", err)
	}
	if _, err := s.Quote(ctx, QuoteRequest{CustomerID: c.ID, Code: "LUNCH5", SubtotalCents: 2000}); errCode(err) != "min_order" {
		t.Errorf("LUNCH5 under 25.00: got %v", err)
	}
	if _, err := s.Quote(ctx, QuoteRequest{CustomerID: c.ID, Code: "NOPE", SubtotalCents: 2000}); errCode(err) != "voucher_not_found" {
		t.Errorf("unknown code: got %v", err)
	}
	if _, err := s.Quote(ctx, QuoteRequest{CustomerID: 999999, Code: "LUNCH5", SubtotalCents: 3000}); errCode(err) != "customer_not_found" {
		t.Errorf("unknown customer: got %v", err)
	}
}

func TestCreateCustomerAndCampaignConflicts(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCustomer(t, s, "Taken@Example.com", "")
	if _, err := s.CreateCustomer(ctx, NewCustomer{Email: "taken@example.COM", Name: "Copy"}); errCode(err) != "email_taken" {
		t.Errorf("duplicate e-mail in another case: got %v, want email_taken", err)
	}
	if _, err := s.CreateCustomer(ctx, NewCustomer{Email: "new@example.com", Name: "New", ReferralCode: "NOSUCHCODE"}); errCode(err) != "unknown_referral" {
		t.Errorf("unknown referral code: got %v, want unknown_referral", err)
	}
	if _, err := s.CreateCampaign(ctx, NewCampaign{Code: "lunch5", Name: "Dup", Kind: Fixed, Value: 100}); errCode(err) != "code_taken" {
		t.Errorf("duplicate voucher code: got %v, want code_taken", err)
	}
	// The failed campaign insert must not leave an orphan campaign behind.
	list, _ := s.ListCampaigns(ctx)
	for _, c := range list {
		if c.Name == "Dup" {
			t.Error("campaign row was committed although its voucher insert failed")
		}
	}
}
