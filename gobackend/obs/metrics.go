package obs

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	appInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gy",
			Subsystem: "app",
			Name:      "info",
			Help:      "Static app info for deployment verification.",
		},
		[]string{"service", "version"},
	)

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gy",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests processed.",
		},
		[]string{"method", "route", "code"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gy",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	workerJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gy",
			Subsystem: "worker",
			Name:      "jobs_total",
			Help:      "Total worker jobs processed.",
		},
		[]string{"worker", "result"},
	)
	workerJobDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gy",
			Subsystem: "worker",
			Name:      "job_duration_seconds",
			Help:      "Worker job duration in seconds.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 80, 160},
		},
		[]string{"worker"},
	)
)

func init() {
	prometheus.MustRegister(appInfo, httpRequestsTotal, httpRequestDuration, workerJobsTotal, workerJobDuration)
}

func SetAppInfo(service string) {
	svc := strings.TrimSpace(service)
	if svc == "" {
		svc = "gobackend"
	}
	ver := strings.TrimSpace(os.Getenv("APP_VERSION"))
	if ver == "" {
		ver = "dev"
	}
	appInfo.WithLabelValues(svc, ver).Set(1)
}

// MetricsMiddleware records request count/latency.
// NOTE: route label is best-effort (path without query). It's fine for internal use;
// if you want strict low-cardinality metrics, replace with a router that provides a pattern.
func MetricsMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		next.ServeHTTP(rec, r)
		route := normalizeRouteLabel(r.URL.Path)
		code := strconv.Itoa(rec.code)
		httpRequestsTotal.WithLabelValues(r.Method, route, code).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.code = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func RecordWorkerJob(worker string, start time.Time, err error) {
	res := "ok"
	if err != nil {
		res = "error"
	}
	workerJobsTotal.WithLabelValues(worker, res).Inc()
	workerJobDuration.WithLabelValues(worker).Observe(time.Since(start).Seconds())
}

func normalizeRouteLabel(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "/"
	}
	// Reduce cardinality for jobId routes.
	// /compare/jobs/{jobId}
	// /compare/jobs/{jobId}/export
	// /compare/jobs/{jobId}/cancel
	if strings.HasPrefix(p, "/compare/jobs/") {
		rest := strings.TrimPrefix(p, "/compare/jobs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 {
			return "/compare/jobs/:jobId"
		}
		if len(parts) >= 2 {
			switch parts[1] {
			case "export":
				return "/compare/jobs/:jobId/export"
			case "cancel":
				return "/compare/jobs/:jobId/cancel"
			default:
				return "/compare/jobs/:jobId/" + parts[1]
			}
		}
	}
	return p
}

