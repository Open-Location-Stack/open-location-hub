package observability

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger constructs the repository's structured production logger.
func NewLogger(level string) (*zap.Logger, func(), error) {
	cfg := zap.NewProductionConfig()
	if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
		cfg.Level.SetLevel(zap.InfoLevel)
	}
	l, err := cfg.Build()
	if err != nil {
		return nil, nil, err
	}
	if runtime := Global(); runtime.LogsEnabled() {
		l = l.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return zapcore.NewTee(core, runtime.BridgeCore("github.com/formation-res/open-location-hub"))
		}))
	}
	return l, func() { _ = l.Sync() }, nil
}

// RequestLogger returns middleware that logs method, path, status, and
// latency for each HTTP request.
func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			duration := time.Since(start)
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			method := r.Method
			switch method {
			case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
			default:
				method = "OTHER"
			}
			// WebSocket sessions have their own delivery metrics.
			if route != "/v2/ws/socket" {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				RuntimeFromContext(r.Context()).RecordHTTPRequest(r.Context(), route, method, status, duration)
			}
			logger.Info("http request",
				String("method", r.Method),
				String("path", r.URL.Path),
				Int("status", ww.Status()),
				Duration("duration", duration),
			)
		})
	}
}

// String creates a string field for structured logging.
func String(k, v string) zap.Field { return zap.String(k, v) }

// Int creates an integer field for structured logging.
func Int(k string, v int) zap.Field { return zap.Int(k, v) }

// Duration creates a duration field for structured logging.
func Duration(k string, v time.Duration) zap.Field { return zap.Duration(k, v) }

// Error creates an error field for structured logging.
func Error(err error) zap.Field { return zap.Error(err) }
