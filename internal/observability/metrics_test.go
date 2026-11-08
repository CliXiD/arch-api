package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsMiddlewareUsesRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := NewMetrics()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/things/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/things/a", "/things/b", "/nope/secret-path"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	assert.InDelta(t, 2, testutil.ToFloat64(m.requests.WithLabelValues("GET", "/things/:id", "200")), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(m.requests.WithLabelValues("GET", "unmatched", "404")), 0)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	assert.Contains(t, body, "http_requests_total")
	assert.Contains(t, body, "http_request_duration_seconds_bucket")
	assert.Contains(t, body, "go_goroutines")
	assert.NotContains(t, body, "secret-path", "never the raw path")
}

type fakeStat struct{}

func (fakeStat) AcquiredConns() int32 { return 1 }
func (fakeStat) IdleConns() int32     { return 2 }
func (fakeStat) TotalConns() int32    { return 3 }
func (fakeStat) MaxConns() int32      { return 4 }

func TestPoolGauges(t *testing.T) {
	ch := make(chan prometheus.Metric, 4)
	collectPool(fakeStat{}, ch)
	close(ch)
	var names []string
	for metric := range ch {
		names = append(names, metric.Desc().String())
	}
	require.Len(t, names, 4)
	joined := strings.Join(names, "\n")
	for _, name := range []string{"db_pool_acquired_connections", "db_pool_idle_connections", "db_pool_total_connections", "db_pool_max_connections"} {
		assert.Contains(t, joined, `"`+name+`"`)
	}
}
