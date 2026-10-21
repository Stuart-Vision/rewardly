package promo

import (
	"errors"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestDiscount(t *testing.T) {
	tests := []struct {
		name     string
		c        Campaign
		subtotal int
		want     int
	}{
		{"percent", Campaign{Kind: Percent, Value: 20}, 5000, 1000},
		{"percent rounds down to a whole cent", Campaign{Kind: Percent, Value: 15}, 999, 149},
		{"percent is capped", Campaign{Kind: Percent, Value: 50, MaxDiscountCents: ptr(1500)}, 10000, 1500},
		{"percent under the cap", Campaign{Kind: Percent, Value: 50, MaxDiscountCents: ptr(1500)}, 2000, 1000},
		{"100 percent", Campaign{Kind: Percent, Value: 100}, 1234, 1234},
		{"fixed", Campaign{Kind: Fixed, Value: 500}, 3000, 500},
		{"fixed never exceeds the subtotal", Campaign{Kind: Fixed, Value: 500}, 300, 300},
		{"largest order does not overflow", Campaign{Kind: Percent, Value: 100}, MaxSubtotalCents, MaxSubtotalCents},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Discount(tt.c, tt.subtotal); got != tt.want {
				t.Errorf("Discount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := Campaign{Kind: Fixed, Value: 500, PerCustomerLimit: 1}
	with := func(change func(*Campaign)) Campaign {
		c := base
		change(&c)
		return c
	}

	tests := []struct {
		name     string
		c        Campaign
		subtotal int
		usage    Usage
		want     string // error code, or "" when the voucher applies
	}{
		{"eligible", base, 1000, Usage{}, ""},
		{"not started yet", with(func(c *Campaign) { c.StartsAt = ptr(now.Add(time.Minute)) }), 1000, Usage{}, "not_started"},
		{"started", with(func(c *Campaign) { c.StartsAt = ptr(now) }), 1000, Usage{}, ""},
		{"expired exactly at the end time", with(func(c *Campaign) { c.EndsAt = ptr(now) }), 1000, Usage{}, "expired"},
		{"before the end time", with(func(c *Campaign) { c.EndsAt = ptr(now.Add(time.Second)) }), 1000, Usage{}, ""},
		{"budget used up", with(func(c *Campaign) { c.MaxRedemptions, c.RedeemedCount = ptr(5), 5 }), 1000, Usage{}, "budget_exhausted"},
		{"budget left", with(func(c *Campaign) { c.MaxRedemptions, c.RedeemedCount = ptr(5), 4 }), 1000, Usage{}, ""},
		{"first-order voucher on a repeat order", with(func(c *Campaign) { c.NewCustomersOnly = true }), 1000, Usage{PriorOrders: 1}, "new_customers_only"},
		{"first-order voucher on a first order", with(func(c *Campaign) { c.NewCustomersOnly = true }), 1000, Usage{}, ""},
		{"per-customer limit reached", with(func(c *Campaign) { c.PerCustomerLimit = 2 }), 1000, Usage{PriorUses: 2}, "already_used"},
		{"under the per-customer limit", with(func(c *Campaign) { c.PerCustomerLimit = 2 }), 1000, Usage{PriorUses: 1}, ""},
		{"below the minimum order", with(func(c *Campaign) { c.MinOrderCents = 2500 }), 2499, Usage{}, "min_order"},
		{"exactly the minimum order", with(func(c *Campaign) { c.MinOrderCents = 2500 }), 2500, Usage{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ""
			if err := Check(tt.c, tt.subtotal, tt.usage, now); err != nil {
				var e *Error
				if !errors.As(err, &e) {
					t.Fatalf("Check() returned %T, want *Error", err)
				}
				got = e.Code
			}
			if got != tt.want {
				t.Errorf("Check() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewCampaignValidate(t *testing.T) {
	valid := func() NewCampaign {
		return NewCampaign{Code: " summer10 ", Name: "Summer", Kind: Percent, Value: 10}
	}

	in := valid()
	if err := in.Validate(); err != nil {
		t.Fatalf("valid campaign rejected: %v", err)
	}
	if in.Code != "SUMMER10" || in.PerCustomerLimit != 1 {
		t.Errorf("not normalised: code %q, perCustomerLimit %d", in.Code, in.PerCustomerLimit)
	}

	start := time.Now()
	bad := map[string]func(*NewCampaign){
		"short code":            func(c *NewCampaign) { c.Code = "AB" },
		"code with symbols":     func(c *NewCampaign) { c.Code = "SAVE-10" },
		"empty name":            func(c *NewCampaign) { c.Name = "  " },
		"unknown kind":          func(c *NewCampaign) { c.Kind = "bogo" },
		"percent over 100":      func(c *NewCampaign) { c.Value = 101 },
		"zero value":            func(c *NewCampaign) { c.Value = 0 },
		"negative cap":          func(c *NewCampaign) { c.MaxDiscountCents = ptr(-1) },
		"zero budget":           func(c *NewCampaign) { c.MaxRedemptions = ptr(0) },
		"ends before it starts": func(c *NewCampaign) { c.StartsAt, c.EndsAt = ptr(start), ptr(start.Add(-time.Hour)) },
	}
	for name, change := range bad {
		t.Run(name, func(t *testing.T) {
			in := valid()
			change(&in)
			var e *Error
			if err := in.Validate(); !errors.As(err, &e) || e.Kind != Invalid {
				t.Errorf("Validate() = %v, want an Invalid error", err)
			}
		})
	}
}
