package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the HTTP metrics and the registry served on METRICS_ADDR.
type Metrics struct {
	Registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers the HTTP metrics and the Go and process collectors.
func NewMetrics() *Metrics {
	labels := []string{"method", "route", "status"}
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route template and status.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration by method, route template and status.",
			Buckets: prometheus.DefBuckets,
		}, labels),
	}
	m.Registry.MustRegister(
		m.requests,
		m.duration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Middleware records every request under its route template, or "unmatched".
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		values := []string{c.Request.Method, Route(c), strconv.Itoa(c.Writer.Status())}
		m.requests.WithLabelValues(values...).Inc()
		m.duration.WithLabelValues(values...).Observe(time.Since(start).Seconds())
	}
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{Registry: m.Registry})
}

// RegisterPool adds the pgxpool gauges, read from pool.Stat() on every scrape.
func (m *Metrics) RegisterPool(pool *pgxpool.Pool) {
	m.Registry.MustRegister(poolCollector{stat: pool.Stat})
}

// PoolStat is the subset of *pgxpool.Stat the collector reads.
type PoolStat interface {
	AcquiredConns() int32
	IdleConns() int32
	TotalConns() int32
	MaxConns() int32
}

var (
	poolAcquired = prometheus.NewDesc("db_pool_acquired_connections", "Connections currently acquired from the pool.", nil, nil)
	poolIdle     = prometheus.NewDesc("db_pool_idle_connections", "Idle connections in the pool.", nil, nil)
	poolTotal    = prometheus.NewDesc("db_pool_total_connections", "Total connections in the pool.", nil, nil)
	poolMax      = prometheus.NewDesc("db_pool_max_connections", "Maximum size of the pool.", nil, nil)
)

type poolCollector struct {
	stat func() *pgxpool.Stat
}

func (poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- poolAcquired
	ch <- poolIdle
	ch <- poolTotal
	ch <- poolMax
}

func (p poolCollector) Collect(ch chan<- prometheus.Metric) {
	collectPool(p.stat(), ch)
}

func collectPool(s PoolStat, ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(poolAcquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(poolIdle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(poolTotal, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(poolMax, prometheus.GaugeValue, float64(s.MaxConns()))
}
