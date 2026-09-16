// Package observability provides bounded, dependency-free metrics and HTTP
// instrumentation for the Record Hub process.
package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Labels are deliberately caller-controlled low-cardinality dimensions. Do
// not put tenant IDs, record IDs, event IDs, tokens, or payload values here.
type Labels map[string]string

type sample struct {
	name   string
	labels Labels
	value  float64
}

type durationSample struct {
	name   string
	labels Labels
	sum    float64
	count  uint64
}

// Registry is a small in-process metrics registry. It is safe for concurrent
// HTTP and worker use and has no Prometheus client dependency in the MVP.
type Registry struct {
	mu        sync.Mutex
	counters  map[string]sample
	gauges    map[string]sample
	durations map[string]durationSample
}

func NewRegistry() *Registry {
	return &Registry{
		counters:  make(map[string]sample),
		gauges:    make(map[string]sample),
		durations: make(map[string]durationSample),
	}
}

func (registry *Registry) IncCounter(name string, labels Labels) {
	registry.addCounter(name, labels, 1)
}

func (registry *Registry) AddCounter(name string, labels Labels, value float64) {
	if value == 0 {
		return
	}
	registry.addCounter(name, labels, value)
}

func (registry *Registry) addCounter(name string, labels Labels, value float64) {
	if registry == nil || !validMetricName(name) {
		return
	}
	key := sampleKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.counters == nil {
		registry.counters = make(map[string]sample)
	}
	current := registry.counters[key]
	if current.name == "" {
		current = sample{name: name, labels: cloneLabels(labels)}
	}
	current.value += value
	registry.counters[key] = current
}

func (registry *Registry) SetGauge(name string, value float64, labels Labels) {
	if registry == nil || !validMetricName(name) {
		return
	}
	key := sampleKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.gauges == nil {
		registry.gauges = make(map[string]sample)
	}
	registry.gauges[key] = sample{name: name, labels: cloneLabels(labels), value: value}
}

func (registry *Registry) ObserveDuration(name string, duration time.Duration, labels Labels) {
	if registry == nil || !validMetricName(name) || duration < 0 {
		return
	}
	key := sampleKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.durations == nil {
		registry.durations = make(map[string]durationSample)
	}
	current := registry.durations[key]
	if current.name == "" {
		current = durationSample{name: name, labels: cloneLabels(labels)}
	}
	current.sum += duration.Seconds()
	current.count++
	registry.durations[key] = current
}

// Handler exposes Prometheus text format using deterministic ordering.
func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if registry == nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte(registry.Render()))
	})
}

// Render returns the current metrics snapshot in Prometheus text format.
// It is primarily useful for diagnostics and deterministic tests.
func (registry *Registry) Render() string {
	registry.mu.Lock()
	counters := cloneSamples(registry.counters)
	gauges := cloneSamples(registry.gauges)
	durations := make([]durationSample, 0, len(registry.durations))
	for _, value := range registry.durations {
		durations = append(durations, cloneDuration(value))
	}
	registry.mu.Unlock()

	var builder strings.Builder
	writeSamples(&builder, "counter", counters)
	writeSamples(&builder, "gauge", gauges)
	sort.Slice(durations, func(i, j int) bool {
		return sampleKey(durations[i].name, durations[i].labels) < sampleKey(durations[j].name, durations[j].labels)
	})
	for _, value := range durations {
		fmt.Fprintf(&builder, "# TYPE %s summary\n", value.name)
		fmt.Fprintf(&builder, "%s_sum%s %s\n", value.name, formatLabels(value.labels), strconv.FormatFloat(value.sum, 'f', 6, 64))
		fmt.Fprintf(&builder, "%s_count%s %d\n", value.name, formatLabels(value.labels), value.count)
	}
	return builder.String()
}

func writeSamples(builder *strings.Builder, kind string, values map[string]sample) {
	ordered := make([]sample, 0, len(values))
	for _, value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return sampleKey(ordered[i].name, ordered[i].labels) < sampleKey(ordered[j].name, ordered[j].labels)
	})
	lastName := ""
	for _, value := range ordered {
		if value.name != lastName {
			fmt.Fprintf(builder, "# TYPE %s %s\n", value.name, kind)
			lastName = value.name
		}
		fmt.Fprintf(builder, "%s%s %s\n", value.name, formatLabels(value.labels), strconv.FormatFloat(value.value, 'f', 6, 64))
	}
}

func cloneSamples(values map[string]sample) map[string]sample {
	result := make(map[string]sample, len(values))
	for key, value := range values {
		value.labels = cloneLabels(value.labels)
		result[key] = value
	}
	return result
}

func cloneDuration(value durationSample) durationSample {
	value.labels = cloneLabels(value.labels)
	return value
}

func cloneLabels(labels Labels) Labels {
	if len(labels) == 0 {
		return nil
	}
	result := make(Labels, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

func sampleKey(name string, labels Labels) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(name)
	for _, key := range keys {
		builder.WriteByte(0)
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
	}
	return builder.String()
}

func formatLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "%s=\"%s\"", key, escapeLabel(labels[key]))
	}
	builder.WriteByte('}')
	return builder.String()
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func validMetricName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' || character == ':' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

// HTTPMiddleware records status, request count, latency, and authentication
// failures. It intentionally uses only method/status labels.
func HTTPMiddleware(registry *Registry, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		capture := &statusWriter{ResponseWriter: writer}
		next.ServeHTTP(capture, request)
		status := capture.status
		labels := Labels{"method": request.Method, "status": strconv.Itoa(status)}
		registry.IncCounter("record_hub_http_requests_total", labels)
		registry.ObserveDuration("record_hub_http_request_duration_seconds", time.Since(started), Labels{"method": request.Method, "status": strconv.Itoa(status)})
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			registry.IncCounter("record_hub_auth_failures_total", Labels{"status": strconv.Itoa(status)})
		}
		if status == http.StatusTooManyRequests {
			registry.IncCounter("record_hub_rate_limited_total", nil)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}
