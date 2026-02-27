package observability

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

func NewLogger(level string) (*zap.Logger, func(), error) {
	cfg := zap.NewProductionConfig()
	if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
		cfg.Level.SetLevel(zap.InfoLevel)
	}
	l, err := cfg.Build()
	if err != nil {
		return nil, nil, err
	}
	return l, func() { _ = l.Sync() }, nil
}

func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			logger.Info("http request",
				String("method", r.Method),
				String("path", r.URL.Path),
				Int("status", ww.Status()),
				Duration("duration", time.Since(start)),
			)
		})
	}
}

func String(k, v string) zap.Field                 { return zap.String(k, v) }
func Int(k string, v int) zap.Field                { return zap.Int(k, v) }
func Duration(k string, v time.Duration) zap.Field { return zap.Duration(k, v) }
func Error(err error) zap.Field                    { return zap.Error(err) }
