// Package httpapi exposes the promo service as a JSON REST API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Stuart-Vision/rewardly/backend/internal/promo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Service is what the handlers need from the domain. Tests pass a fake.
type Service interface {
	CreateCustomer(ctx context.Context, in promo.NewCustomer) (promo.Customer, error)
	ListCustomers(ctx context.Context) ([]promo.CustomerSummary, error)
	CustomerVouchers(ctx context.Context, customerID int64) ([]promo.WalletVoucher, error)
	CustomerOrders(ctx context.Context, customerID int64) ([]promo.Order, error)
	CreateCampaign(ctx context.Context, in promo.NewCampaign) (promo.CampaignStats, error)
	ListCampaigns(ctx context.Context) ([]promo.CampaignStats, error)
	Quote(ctx context.Context, in promo.QuoteRequest) (promo.Quote, error)
	Checkout(ctx context.Context, in promo.CheckoutRequest) (promo.Order, bool, error)
	Stats(ctx context.Context) (promo.Stats, error)
}

type Options struct {
	Logger     *slog.Logger
	Registry   *prometheus.Registry
	Ready      func(context.Context) error // readiness check, e.g. a database ping
	CORSOrigin string
}

type server struct {
	svc     Service
	log     *slog.Logger
	metrics *metrics
	ready   func(context.Context) error
}

func New(svc Service, opt Options) http.Handler {
	s := &server{svc: svc, log: opt.Logger, metrics: newMetrics(opt.Registry), ready: opt.Ready}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(opt.Registry, promhttp.HandlerOpts{}))

	mux.HandleFunc("POST /api/customers", s.createCustomer)
	mux.HandleFunc("GET /api/customers", s.listCustomers)
	mux.HandleFunc("GET /api/customers/{id}/vouchers", s.customerVouchers)
	mux.HandleFunc("GET /api/customers/{id}/orders", s.customerOrders)
	mux.HandleFunc("GET /api/campaigns", s.listCampaigns)
	mux.HandleFunc("POST /api/campaigns", s.createCampaign)
	mux.HandleFunc("POST /api/quote", s.quote)
	mux.HandleFunc("POST /api/checkout", s.checkout)
	mux.HandleFunc("GET /api/stats", s.stats)

	return withRequestID(observe(s.log, s.metrics, recoverPanics(s.log, cors(opt.CORSOrigin, mux))))
}

// ---- JSON helpers ----

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

var statusFor = map[promo.ErrKind]int{
	promo.Invalid:  http.StatusBadRequest,
	promo.NotFound: http.StatusNotFound,
	promo.Conflict: http.StatusConflict,
	promo.Rejected: http.StatusUnprocessableEntity,
}

func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var pe *promo.Error
	if errors.As(err, &pe) {
		writeJSON(w, statusFor[pe.Kind], errorBody{errorDetail{pe.Code, pe.Message}})
		return
	}
	s.log.ErrorContext(r.Context(), "request failed", "err", err, "request_id", requestIDFrom(r.Context()))
	writeJSON(w, http.StatusInternalServerError, errorBody{errorDetail{"internal", "Something went wrong"}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &promo.Error{Kind: promo.Invalid, Code: "invalid_json", Message: "Invalid JSON body: " + err.Error()}
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, &promo.Error{Kind: promo.Invalid, Code: "invalid_id", Message: "id must be a positive integer"}
	}
	return id, nil
}

// respond writes v as JSON, or the error if there is one.
func respond[T any](s *server, w http.ResponseWriter, r *http.Request, status int, v T, err error) {
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// ---- Handlers ----

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in promo.NewCustomer
	if err := decode(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.svc.CreateCustomer(r.Context(), in)
	respond(s, w, r, http.StatusCreated, c, err)
}

func (s *server) listCustomers(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListCustomers(r.Context())
	respond(s, w, r, http.StatusOK, list, err)
}

func (s *server) customerVouchers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, err := s.svc.CustomerVouchers(r.Context(), id)
	respond(s, w, r, http.StatusOK, list, err)
}

func (s *server) customerOrders(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, err := s.svc.CustomerOrders(r.Context(), id)
	respond(s, w, r, http.StatusOK, list, err)
}

func (s *server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListCampaigns(r.Context())
	respond(s, w, r, http.StatusOK, list, err)
}

func (s *server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var in promo.NewCampaign
	if err := decode(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.svc.CreateCampaign(r.Context(), in)
	respond(s, w, r, http.StatusCreated, c, err)
}

func (s *server) quote(w http.ResponseWriter, r *http.Request) {
	var in promo.QuoteRequest
	if err := decode(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	q, err := s.svc.Quote(r.Context(), in)
	respond(s, w, r, http.StatusOK, q, err)
}

func (s *server) checkout(w http.ResponseWriter, r *http.Request) {
	var in promo.CheckoutRequest
	if err := decode(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	order, replayed, err := s.svc.Checkout(r.Context(), in)
	if err != nil {
		var pe *promo.Error
		if errors.As(err, &pe) && pe.Kind == promo.Rejected {
			s.metrics.rejections.WithLabelValues(pe.Code).Inc()
		}
		s.writeError(w, r, err)
		return
	}
	if replayed {
		// Same response as the first time, flagged so clients can tell.
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusOK, order)
		return
	}
	if order.VoucherCode != nil {
		s.metrics.redemptions.Inc()
		s.metrics.discount.Add(float64(order.DiscountCents))
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Stats(r.Context())
	respond(s, w, r, http.StatusOK, st, err)
}
