package promo

// ErrKind groups errors so the HTTP layer can map them to status codes
// without the domain knowing about HTTP.
type ErrKind int

const (
	Invalid  ErrKind = iota + 1 // the request is malformed
	NotFound                    // a referenced customer or voucher does not exist
	Conflict                    // a uniqueness rule would be broken
	Rejected                    // a well-formed request that the voucher rules turn down
)

// Error is a domain error with a stable Code the frontend can rely on.
type Error struct {
	Kind    ErrKind
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func invalid(msg string) error { return &Error{Kind: Invalid, Code: "invalid", Message: msg} }

var (
	ErrCustomerNotFound  = &Error{NotFound, "customer_not_found", "Customer not found"}
	ErrVoucherNotFound   = &Error{NotFound, "voucher_not_found", "Voucher code not found"}
	ErrUnknownReferral   = &Error{Invalid, "unknown_referral", "Referral code not found"}
	ErrEmailTaken        = &Error{Conflict, "email_taken", "An account with this e-mail already exists"}
	ErrCodeTaken         = &Error{Conflict, "code_taken", "This voucher code is already in use"}
	ErrIdempotencyReused = &Error{Conflict, "idempotency_key_reused", "This Idempotency-Key was already used for a different checkout"}

	ErrNotStarted       = &Error{Rejected, "not_started", "This voucher is not active yet"}
	ErrExpired          = &Error{Rejected, "expired", "This voucher has expired"}
	ErrBudgetExhausted  = &Error{Rejected, "budget_exhausted", "This voucher has been fully redeemed"}
	ErrNewCustomersOnly = &Error{Rejected, "new_customers_only", "This voucher is for first orders only"}
	ErrAlreadyUsed      = &Error{Rejected, "already_used", "You have already used this voucher the maximum number of times"}
)
