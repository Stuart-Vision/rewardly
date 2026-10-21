package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type metrics struct {
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	redemptions prometheus.Counter
	discount    prometheus.Counter
	rejections  *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by route, method and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		redemptions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rewardly_vouchers_redeemed_total", Help: "Checkouts that applied a voucher.",
		}),
		discount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rewardly_discount_cents_total", Help: "Discount given, in cents.",
		}),
		rejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rewardly_checkout_rejections_total", Help: "Checkouts turned down by a voucher rule, by reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(m.requests, m.duration, m.redemptions, m.discount, m.rejections)
	return m
}

type ctxKey struct{}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// withRequestID reuses an incoming X-Request-ID or creates one, and echoes it back.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// observe logs every request and records Prometheus metrics. The route label is
// the matched pattern (e.g. "GET /api/customers/{id}/orders"), not the raw path,
// so the number of time series stays bounded.
func observe(log *slog.Logger, m *metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r) // ServeMux sets r.Pattern on this same request

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		m.requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		m.duration.WithLabelValues(route).Observe(elapsed.Seconds())

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		log.LogAttrs(r.Context(), level, "request",
			slog.String("request_id", requestIDFrom(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", elapsed))
	})
}

// recoverPanics turns a panic in a handler into a 500 instead of a dropped connection.
func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.ErrorContext(r.Context(), "panic", "value", v, "request_id", requestIDFrom(r.Context()))
				writeJSON(w, http.StatusInternalServerError, errorBody{errorDetail{"internal", "Something went wrong"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func cors(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-Request-ID")
		h.Set("Access-Control-Expose-Headers", "Idempotent-Replayed, X-Request-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
