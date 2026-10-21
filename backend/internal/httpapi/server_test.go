package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Stuart-Vision/rewardly/backend/internal/promo"
	"github.com/prometheus/client_golang/prometheus"
)

// fakeService embeds the interface so a test only implements the methods it uses.
type fakeService struct {
	Service
	checkout func(promo.CheckoutRequest) (promo.Order, bool, error)
	orders   func(int64) ([]promo.Order, error)
}

func (f *fakeService) Checkout(_ context.Context, in promo.CheckoutRequest) (promo.Order, bool, error) {
	return f.checkout(in)
}

func (f *fakeService) CustomerOrders(_ context.Context, id int64) ([]promo.Order, error) {
	return f.orders(id)
}

func newTestServer(svc Service) http.Handler {
	return New(svc, Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registry:   prometheus.NewRegistry(),
		Ready:      func(context.Context) error { return nil },
		CORSOrigin: "*",
	})
}

func do(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not an error body: %s", rec.Body)
	}
	return body.Error.Code
}

func TestCheckoutStatusCodes(t *testing.T) {
	code := "LUNCH5"
	tests := []struct {
		name       string
		body       string
		result     func(promo.CheckoutRequest) (promo.Order, bool, error)
		wantStatus int
		wantCode   string
		wantReplay bool
	}{
		{
			name: "new order",
			body: `{"customerId":1,"subtotalCents":3000,"voucherCode":"LUNCH5"}`,
			result: func(in promo.CheckoutRequest) (promo.Order, bool, error) {
				return promo.Order{ID: 7, SubtotalCents: 3000, DiscountCents: 500, TotalCents: 2500, VoucherCode: &code}, false, nil
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "replayed order",
			body: `{"customerId":1,"subtotalCents":3000}`,
			result: func(promo.CheckoutRequest) (promo.Order, bool, error) {
				return promo.Order{ID: 7}, true, nil
			},
			wantStatus: http.StatusOK,
			wantReplay: true,
		},
		{
			name: "voucher rule rejects",
			body: `{"customerId":1,"subtotalCents":3000,"voucherCode":"FLASH50"}`,
			result: func(promo.CheckoutRequest) (promo.Order, bool, error) {
				return promo.Order{}, false, promo.ErrBudgetExhausted
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "budget_exhausted",
		},
		{
			name: "unknown customer",
			body: `{"customerId":99,"subtotalCents":3000}`,
			result: func(promo.CheckoutRequest) (promo.Order, bool, error) {
				return promo.Order{}, false, promo.ErrCustomerNotFound
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "customer_not_found",
		},
		{
			name: "unexpected error is hidden",
			body: `{"customerId":1,"subtotalCents":3000}`,
			result: func(promo.CheckoutRequest) (promo.Order, bool, error) {
				return promo.Order{}, false, errors.New("connection reset")
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
		{
			name:       "malformed JSON never reaches the service",
			body:       `{"customerId":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "unknown fields are rejected",
			body:       `{"customerId":1,"subtotalCents":3000,"discountCents":3000}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got promo.CheckoutRequest
			svc := &fakeService{checkout: func(in promo.CheckoutRequest) (promo.Order, bool, error) {
				got = in
				if tt.result == nil {
					t.Fatal("service should not be called")
				}
				return tt.result(in)
			}}
			rec := do(t, newTestServer(svc), "POST", "/api/checkout", tt.body, map[string]string{"Idempotency-Key": "abc"})

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode != "" && errorCode(t, rec) != tt.wantCode {
				t.Errorf("error code = %q, want %q", errorCode(t, rec), tt.wantCode)
			}
			if replay := rec.Header().Get("Idempotent-Replayed") == "true"; replay != tt.wantReplay {
				t.Errorf("Idempotent-Replayed = %v, want %v", replay, tt.wantReplay)
			}
			if tt.result != nil && got.IdempotencyKey != "abc" {
				t.Errorf("Idempotency-Key header not passed to the service: %q", got.IdempotencyKey)
			}
		})
	}
}

func TestPathIDValidation(t *testing.T) {
	svc := &fakeService{orders: func(id int64) ([]promo.Order, error) { return []promo.Order{}, nil }}
	h := newTestServer(svc)
	for path, want := range map[string]int{
		"/api/customers/12/orders":  http.StatusOK,
		"/api/customers/abc/orders": http.StatusBadRequest,
		"/api/customers/0/orders":   http.StatusBadRequest,
	} {
		if rec := do(t, h, "GET", path, "", nil); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestRoutingAndCrossCutting(t *testing.T) {
	h := newTestServer(&fakeService{})

	if rec := do(t, h, "DELETE", "/api/checkout", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/checkout = %d, want 405", rec.Code)
	}
	if rec := do(t, h, "OPTIONS", "/api/checkout", "", nil); rec.Code != http.StatusNoContent ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Errorf("CORS preflight = %d %v", rec.Code, rec.Header())
	}
	rec := do(t, h, "GET", "/healthz", "", map[string]string{"X-Request-ID": "trace-123"})
	if rec.Code != http.StatusOK || rec.Header().Get("X-Request-ID") != "trace-123" {
		t.Errorf("healthz = %d, request id %q", rec.Code, rec.Header().Get("X-Request-ID"))
	}
	if do(t, h, "GET", "/readyz", "", nil).Code != http.StatusOK {
		t.Error("readyz should be 200 when the database answers")
	}
}

func TestMetricsCountRejectionsByReason(t *testing.T) {
	svc := &fakeService{checkout: func(promo.CheckoutRequest) (promo.Order, bool, error) {
		return promo.Order{}, false, promo.ErrAlreadyUsed
	}}
	h := newTestServer(svc)
	for range 3 {
		do(t, h, "POST", "/api/checkout", `{"customerId":1,"subtotalCents":100}`, map[string]string{"Idempotency-Key": "k"})
	}
	body := do(t, h, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`rewardly_checkout_rejections_total{reason="already_used"} 3`,
		`http_requests_total{method="POST",route="POST /api/checkout",status="422"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
