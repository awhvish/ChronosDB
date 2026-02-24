package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"KV-Store/pkg/metrics"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (ww *responseWriterWrapper) WriteHeader(code int) {
	ww.statusCode = code
	ww.ResponseWriter.WriteHeader(code)
}

// httpLogger logs HTTP requests in the format: [HTTP] METHOD PATH STATUS_CODE STATUS_TEXT DURATION_MS
// When enableLogging is false, the handler is called directly without logging overhead
func httpLogger(handler http.HandlerFunc, enableLogging bool) http.HandlerFunc {
	if !enableLogging {
		return handler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}

		handler(ww, r)

		duration := time.Since(start)
		durationMs := duration.Milliseconds()

		// Build full path with query parameters
		fullPath := r.URL.Path
		if r.URL.RawQuery != "" {
			fullPath = fullPath + "?" + r.URL.RawQuery
		}

		// Log in the required format
		statusText := http.StatusText(ww.statusCode)
		log.Printf("[HTTP] %s %s %d %s %dms",
			r.Method,
			fullPath,
			ww.statusCode,
			statusText,
			durationMs,
		)
	}
}

func withMetrics(handler http.HandlerFunc, method, endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}

		handler(ww, r)

		duration := time.Since(start).Seconds()
		metrics.HttpRequestDuration.WithLabelValues(method, endpoint).Observe(duration)
		metrics.HttpRequestsTotal.WithLabelValues(method, endpoint, fmt.Sprintf("%d", ww.statusCode)).Inc()
	}
}
