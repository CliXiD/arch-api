// Package observability sets up logs, Prometheus metrics and OpenTelemetry tracing.
package observability

import (
	"io"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

// ActorKey is the gin context key where authentication stores the caller's subject.
const ActorKey = "observability.actor"

// NewLogger returns a JSON logger writing to w at level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Route is the gin route template of the request, or "unmatched". Never the raw path.
func Route(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// RequestLogger writes one line per request. It logs no headers, query strings, bodies or
// claims; the actor is the principal's subject only.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := c.Request.Context()
		c.Next()

		traceID := ""
		if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
			traceID = sc.TraceID().String()
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "request",
			slog.String("method", c.Request.Method),
			slog.String("route", Route(c)),
			slog.Int("status", c.Writer.Status()),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("trace_id", traceID),
			slog.String("actor", c.GetString(ActorKey)),
		)
	}
}
