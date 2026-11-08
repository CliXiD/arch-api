package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/CliXiD/arch-api/internal/observability"
)

// Deps are what the engine needs. cmd/openapi passes none: it only reads the routes.
type Deps struct {
	Auth        Authenticator
	Ready       Readiness
	Logger      *slog.Logger
	Metrics     *observability.Metrics
	CORSOrigins []string
	Tracing     bool
}

// NewEngine builds the gin engine for HTTP_ADDR: tracing (when on), metrics, request log, CORS
// (before authentication, so preflights are answered), then the Huma API. /metrics is not here.
func NewEngine(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		d.Logger.ErrorContext(c.Request.Context(), "panic recovered", "route", observability.Route(c))
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	if d.Tracing {
		r.Use(otelgin.Middleware("arch-api"))
	}
	r.Use(d.Metrics.Middleware(), observability.RequestLogger(d.Logger))
	if len(d.CORSOrigins) > 0 {
		r.Use(cors.New(cors.Config{
			AllowOrigins: d.CORSOrigins,
			AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders: []string{"Authorization", "Content-Type", "traceparent"},
			MaxAge:       12 * time.Hour,
		}))
	}
	NewAPI(r, d)
	return r
}
