package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type PushMetrics struct {
	// TODO: remove all?
	all prometheus.Histogram
	// Prometheus types
	His *prometheus.HistogramVec
	Cnt *prometheus.CounterVec
}

func defaultHistBuckets() []float64 {
	return []float64{
		// float64(time.Millisecond) / 2,     // 0.5 ms
		// float64(time.Millisecond) * 2,     // 2ms
		// float64(time.Millisecond) * 10,    // 10ms
		// float64(time.Millisecond) * 50,    // 50ms
		float64(time.Millisecond) * 200,   // 200ms
		float64(time.Millisecond) * 500,   // 500ms
		float64(time.Millisecond) * 1000,  // 1s
		float64(time.Millisecond) * 10000, // 10s
	}
}

func newPushHist(name string, doc string, lbl prometheus.Labels) prometheus.Histogram {
	h := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:        name,
			Help:        doc,
			Buckets:     defaultHistBuckets(), // 5 buckets, each 5 centigrade wide.
			ConstLabels: lbl,
		},
	)

	return h
}

func newPushHistVec(name string, doc string, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    name,
			Help:    doc,
			Buckets: defaultHistBuckets(), // 5 buckets, each 5 centigrade wide.
		},
		labels,
	)
	return h
}

func NewPushMetrics() *PushMetrics {
	allHist := newPushHist("apns_pushtimesall", "push time delivery distribution", nil)
	prometheus.MustRegister(allHist)

	appsHistVec := newPushHistVec("apns_pushtimes", "push time delivery distribution by app", []string{"app"})
	prometheus.MustRegister(appsHistVec)

	pushCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "apns_pushcount",
		Help: "Total number of pushes",
	}, []string{"app", "state"})
	prometheus.MustRegister(pushCounter)

	return &PushMetrics{
		all: allHist,
		His: appsHistVec,
		Cnt: pushCounter,
	}
}

func (pm *PushMetrics) Update(ts time.Duration, app string, state string) {
	pm.all.Observe(float64(ts))
	pm.His.With(prometheus.Labels{"app": app}).Observe(float64(ts))
	pm.Cnt.With(prometheus.Labels{"app": app, "state": state}).Inc()
}

func (pm *PushMetrics) getHandler() http.Handler {
	return prometheus.UninstrumentedHandler()
}
