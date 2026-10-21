// Package promo holds the vouchers and referrals domain: the discount rules and
// the Postgres-backed service that applies them.
package promo

import "time"

type Kind string

const (
	Percent Kind = "percent"
	Fixed   Kind = "fixed"
)

// Campaign holds the rules a voucher applies. Money is always integer cents.
type Campaign struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Kind             Kind       `json:"kind"`
	Value            int        `json:"value"`
	MaxDiscountCents *int       `json:"maxDiscountCents,omitempty"`
	MinOrderCents    int        `json:"minOrderCents"`
	NewCustomersOnly bool       `json:"newCustomersOnly"`
	PerCustomerLimit int        `json:"perCustomerLimit"`
	MaxRedemptions   *int       `json:"maxRedemptions,omitempty"`
	RedeemedCount    int        `json:"redeemedCount"`
	StartsAt         *time.Time `json:"startsAt,omitempty"`
	EndsAt           *time.Time `json:"endsAt,omitempty"`
}

// CampaignStats is a campaign with its public code and how much discount it has given.
type CampaignStats struct {
	Campaign
	Code               string `json:"code,omitempty"`
	System             bool   `json:"system"`
	DiscountGivenCents int64  `json:"discountGivenCents"`
}

type NewCampaign struct {
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Kind             Kind       `json:"kind"`
	Value            int        `json:"value"`
	MaxDiscountCents *int       `json:"maxDiscountCents"`
	MinOrderCents    int        `json:"minOrderCents"`
	NewCustomersOnly bool       `json:"newCustomersOnly"`
	PerCustomerLimit int        `json:"perCustomerLimit"`
	MaxRedemptions   *int       `json:"maxRedemptions"`
	StartsAt         *time.Time `json:"startsAt"`
	EndsAt           *time.Time `json:"endsAt"`
}

type Customer struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	ReferralCode string    `json:"referralCode"`
	ReferredBy   *int64    `json:"referredBy,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type CustomerSummary struct {
	Customer
	Orders    int `json:"orders"`
	Referrals int `json:"referrals"`
}

type NewCustomer struct {
	Email        string `json:"email"`
	Name         string `json:"name"`
	ReferralCode string `json:"referralCode"`
}

// WalletVoucher is a personal voucher owned by one customer.
type WalletVoucher struct {
	Code     string   `json:"code"`
	Used     bool     `json:"used"`
	Campaign Campaign `json:"campaign"`
}

type Order struct {
	ID            int64     `json:"id"`
	CustomerID    int64     `json:"customerId"`
	SubtotalCents int       `json:"subtotalCents"`
	DiscountCents int       `json:"discountCents"`
	TotalCents    int       `json:"totalCents"`
	VoucherCode   *string   `json:"voucherCode,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type QuoteRequest struct {
	CustomerID    int64  `json:"customerId"`
	Code          string `json:"code"`
	SubtotalCents int    `json:"subtotalCents"`
}

type Quote struct {
	Code          string `json:"code"`
	CampaignName  string `json:"campaignName"`
	SubtotalCents int    `json:"subtotalCents"`
	DiscountCents int    `json:"discountCents"`
	TotalCents    int    `json:"totalCents"`
}

type CheckoutRequest struct {
	CustomerID     int64  `json:"customerId"`
	SubtotalCents  int    `json:"subtotalCents"`
	VoucherCode    string `json:"voucherCode"`
	IdempotencyKey string `json:"-"` // from the Idempotency-Key header
}

// Stats are the growth numbers shown on the dashboard.
type Stats struct {
	Customers          int   `json:"customers"`
	ReferredCustomers  int   `json:"referredCustomers"`
	Orders             int   `json:"orders"`
	OrdersWithVoucher  int   `json:"ordersWithVoucher"`
	DiscountGivenCents int64 `json:"discountGivenCents"`
	RevenueCents       int64 `json:"revenueCents"`
}
