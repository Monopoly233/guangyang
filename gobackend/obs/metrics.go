package obs

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
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
	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, workerJobsTotal, workerJobDuration)
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
		route := r.URL.Path
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

