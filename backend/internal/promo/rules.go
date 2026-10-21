package promo

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Usage is what the rules need to know about the customer's history.
type Usage struct {
	PriorOrders int // orders the customer has already placed
	PriorUses   int // orders in which the customer already used this voucher
}

// Check reports why a campaign cannot be applied to an order, or nil if it can.
// It is a pure function, so every rule is covered by fast unit tests.
func Check(c Campaign, subtotalCents int, u Usage, now time.Time) error {
	switch {
	case c.StartsAt != nil && now.Before(*c.StartsAt):
		return ErrNotStarted
	case c.EndsAt != nil && !now.Before(*c.EndsAt):
		return ErrExpired
	case c.MaxRedemptions != nil && c.RedeemedCount >= *c.MaxRedemptions:
		return ErrBudgetExhausted
	case c.NewCustomersOnly && u.PriorOrders > 0:
		return ErrNewCustomersOnly
	case u.PriorUses >= c.PerCustomerLimit:
		return ErrAlreadyUsed
	case subtotalCents < c.MinOrderCents:
		return &Error{Rejected, "min_order", "The minimum order for this voucher is " + FormatCents(c.MinOrderCents)}
	}
	return nil
}

// Discount returns the discount in cents. Percentages round down to a whole cent,
// and the discount never exceeds the subtotal.
func Discount(c Campaign, subtotalCents int) int {
	var d int
	switch c.Kind {
	case Percent:
		d = subtotalCents * c.Value / 100
		if c.MaxDiscountCents != nil && d > *c.MaxDiscountCents {
			d = *c.MaxDiscountCents
		}
	case Fixed:
		d = c.Value
	}
	return min(d, subtotalCents)
}

func FormatCents(c int) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }

var codePattern = regexp.MustCompile(`^[A-Z0-9]{3,20}$`)

func normalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// MaxSubtotalCents bounds order size so integer arithmetic can never overflow.
const MaxSubtotalCents = 10_000_000

func validateSubtotal(cents int) error {
	if cents <= 0 || cents > MaxSubtotalCents {
		return invalid("subtotalCents must be between 1 and " + fmt.Sprint(MaxSubtotalCents))
	}
	return nil
}

// Validate normalises the input (upper-case code, default limits) and checks it.
func (in *NewCampaign) Validate() error {
	in.Code = normalizeCode(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.PerCustomerLimit == 0 {
		in.PerCustomerLimit = 1
	}
	switch {
	case !codePattern.MatchString(in.Code):
		return invalid("code must be 3 to 20 letters or digits")
	case in.Name == "" || len(in.Name) > 100:
		return invalid("name must be 1 to 100 characters")
	case in.Kind != Percent && in.Kind != Fixed:
		return invalid(`kind must be "percent" or "fixed"`)
	case in.Kind == Percent && (in.Value < 1 || in.Value > 100):
		return invalid("a percent value must be between 1 and 100")
	case in.Kind == Fixed && (in.Value < 1 || in.Value > 100_000):
		return invalid("a fixed value must be between 1 and 100000 cents")
	case in.MaxDiscountCents != nil && *in.MaxDiscountCents < 1:
		return invalid("maxDiscountCents must be positive")
	case in.MinOrderCents < 0 || in.MinOrderCents > MaxSubtotalCents:
		return invalid("minOrderCents is out of range")
	case in.PerCustomerLimit < 1 || in.PerCustomerLimit > 100:
		return invalid("perCustomerLimit must be between 1 and 100")
	case in.MaxRedemptions != nil && *in.MaxRedemptions < 1:
		return invalid("maxRedemptions must be positive")
	case in.StartsAt != nil && in.EndsAt != nil && !in.EndsAt.After(*in.StartsAt):
		return invalid("endsAt must be after startsAt")
	}
	return nil
}
