package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/config"
	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/internal/health"
	"github.com/CliXiD/arch-api/internal/model"
	"github.com/CliXiD/arch-api/internal/observability"
	"github.com/CliXiD/arch-api/migrations"
)

const shutdownTimeout = 10 * time.Second

// Server timeouts, for the API and the metrics server alike.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

// newServer returns an http.Server for h on addr with the server timeouts.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Serve runs the API on cfg.HTTPAddr and /metrics on cfg.MetricsAddr until ctx is done, then shuts
// both down.
func Serve(ctx context.Context, cfg config.Serve, logger *slog.Logger) error {
	tracing := cfg.OTLPEndpoint != ""
	if tracing {
		shutdown, err := observability.SetupTracing(ctx, cfg.OTLPEndpoint, cfg.OTLPProtocol)
		if err != nil {
			return err
		}
		defer func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
			defer cancel()
			if err := shutdown(sctx); err != nil {
				logger.Warn("tracing shutdown failed", "error", err.Error())
			}
		}()
	}

	pool, err := db.NewPool(cfg.DatabaseURL, tracing)
	if err != nil {
		return err
	}
	defer pool.Close()

	expected, err := db.LatestVersion(migrations.FS)
	if err != nil {
		return err
	}

	metrics := observability.NewMetrics()
	metrics.RegisterPool(pool)

	svc := model.NewService(pool)
	gin.SetMode(gin.ReleaseMode)
	engine := NewEngine(Deps{
		Auth: auth.NewVerifier(auth.Options{
			Issuer:   cfg.OIDCIssuer,
			Audience: cfg.OIDCAudience,
			JWKSURL:  cfg.OIDCJWKSURL,
			Logger:   logger,
		}),
		Ready:       health.Checker{DB: pool, Versions: db.VersionReader{Pool: pool}, Expected: expected},
		Model:       svc,
		Writer:      svc,
		Logger:      logger,
		Metrics:     metrics,
		CORSOrigins: cfg.CORSOrigins,
		Tracing:     tracing,
	})

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics.Handler())

	servers := []*http.Server{
		newServer(cfg.HTTPAddr, engine),
		newServer(cfg.MetricsAddr, metricsMux),
	}
	errs := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			logger.Info("listening", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case runErr = <-errs:
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(sctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	return runErr
}
