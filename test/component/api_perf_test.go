//go:build component

package component

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ---------------------------------------------------------------------------
// Request spec & config
// ---------------------------------------------------------------------------

// RequestSpec defines a single API call pattern for the load test.
type RequestSpec struct {
	Method string
	Path   string
	Body   []byte
	Weight int
	Setup  func() ([]byte, error) // dynamic body; overrides Body
}

// PerfConfig holds load test parameters, populated from environment.
type PerfConfig struct {
	BaseURL  string
	Duration time.Duration
	Workers  int

	// Assertion thresholds
	MaxP99LatencyMs float64
	MinThroughput   float64 // req/s
	MaxFailureRate  float64 // percent
}

func configFromEnv() PerfConfig {
	return PerfConfig{
		BaseURL:         getEnvOrDefault("LOOP_BASE_URL", "http://localhost:18222"),
		Duration:        getDurationEnv("PERF_DURATION", 10*time.Second),
		Workers:         getIntEnv("PERF_WORKERS", 5),
		MaxP99LatencyMs: getFloatEnv("PERF_MAX_P99_LATENCY_MS", 100),
		MinThroughput:   getFloatEnv("PERF_MIN_THROUGHPUT", 50),
		MaxFailureRate:  getFloatEnv("PERF_MAX_FAILURE_RATE", 5),
	}
}

// ---------------------------------------------------------------------------
// Metrics collection
// ---------------------------------------------------------------------------

// EndpointMetrics tracks per-endpoint performance data.
type EndpointMetrics struct {
	Method      string
	Path        string
	Latencies   []float64 // milliseconds
	StatusCodes map[int]int64
	mu          sync.Mutex
}

func (m *EndpointMetrics) record(latencyMs float64, status int) {
	m.mu.Lock()
	m.Latencies = append(m.Latencies, latencyMs)
	m.StatusCodes[status]++
	m.mu.Unlock()
}

// LoadMetrics aggregates all request measurements.
type LoadMetrics struct {
	totalRequests atomic.Int64
	successful    atomic.Int64
	failed        atomic.Int64

	endpoints   map[string]*EndpointMetrics // key: "METHOD path"
	endpointsMu sync.Mutex
}

func newLoadMetrics() *LoadMetrics {
	return &LoadMetrics{endpoints: make(map[string]*EndpointMetrics)}
}

func (m *LoadMetrics) endpointFor(method, path string) *EndpointMetrics {
	key := method + " " + path
	m.endpointsMu.Lock()
	defer m.endpointsMu.Unlock()
	if ep, ok := m.endpoints[key]; ok {
		return ep
	}
	ep := &EndpointMetrics{Method: method, Path: path, StatusCodes: make(map[int]int64)}
	m.endpoints[key] = ep
	return ep
}

// ---------------------------------------------------------------------------
// Memory sampler — reads VmRSS from /proc/{pid}/status
// ---------------------------------------------------------------------------

// MemoryStats holds RSS samples taken during the load test.
type MemoryStats struct {
	Samples    []int64 // RSS in KB
	PeakKB     int64
	StartKB    int64
	EndKB      int64
	AvgKB      int64
	SampleRate time.Duration
}

// memorySampler periodically reads RSS of the target process.
type memorySampler struct {
	pid      string
	interval time.Duration
	samples  []int64
	mu       sync.Mutex
}

func newMemorySampler(pid string, interval time.Duration) *memorySampler {
	return &memorySampler{pid: pid, interval: interval}
}

// run samples until stop is closed. Call from a goroutine.
func (m *memorySampler) run(stop <-chan struct{}) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	// Take an initial sample immediately.
	if rss := m.readRSS(); rss > 0 {
		m.mu.Lock()
		m.samples = append(m.samples, rss)
		m.mu.Unlock()
	}
	for {
		select {
		case <-stop:
			// Final sample.
			if rss := m.readRSS(); rss > 0 {
				m.mu.Lock()
				m.samples = append(m.samples, rss)
				m.mu.Unlock()
			}
			return
		case <-ticker.C:
			if rss := m.readRSS(); rss > 0 {
				m.mu.Lock()
				m.samples = append(m.samples, rss)
				m.mu.Unlock()
			}
		}
	}
}

// stats returns aggregated memory statistics.
func (m *memorySampler) stats() *MemoryStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.samples) == 0 {
		return nil
	}
	ms := &MemoryStats{
		Samples:    m.samples,
		StartKB:    m.samples[0],
		EndKB:      m.samples[len(m.samples)-1],
		SampleRate: m.interval,
	}
	var sum int64
	for _, s := range m.samples {
		sum += s
		if s > ms.PeakKB {
			ms.PeakKB = s
		}
	}
	ms.AvgKB = sum / int64(len(m.samples))
	return ms
}

// readRSS reads VmRSS from /proc/{pid}/status (Linux only). Returns KB.
func (m *memorySampler) readRSS() int64 {
	f, err := os.Open("/proc/" + m.pid + "/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return v
				}
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

// PerfReport holds the final performance results.
type PerfReport struct {
	Duration      time.Duration
	NumWorkers    int
	TotalRequests int64
	Successful    int64
	Failed        int64
	Throughput    float64
	AvgLatencyMs  float64
	P50LatencyMs  float64
	P95LatencyMs  float64
	P99LatencyMs  float64
	MinLatencyMs  float64
	MaxLatencyMs  float64
	Memory        *MemoryStats
	Endpoints     map[string]*EndpointReport
}

// EndpointReport holds per-endpoint stats.
type EndpointReport struct {
	Method       string
	Path         string
	Total        int64
	Successful   int64
	Failed       int64
	AvgLatencyMs float64
	P99LatencyMs float64
	StatusCodes  map[int]int64
}

func generateReport(cfg PerfConfig, metrics *LoadMetrics, elapsed time.Duration) *PerfReport {
	report := &PerfReport{
		Duration:      elapsed,
		NumWorkers:    cfg.Workers,
		TotalRequests: metrics.totalRequests.Load(),
		Successful:    metrics.successful.Load(),
		Failed:        metrics.failed.Load(),
		Endpoints:     make(map[string]*EndpointReport),
	}
	if elapsed.Seconds() > 0 {
		report.Throughput = float64(report.TotalRequests) / elapsed.Seconds()
	}

	// Aggregate all latencies for global percentiles.
	var allLatencies []float64
	for key, ep := range metrics.endpoints {
		ep.mu.Lock()
		allLatencies = append(allLatencies, ep.Latencies...)

		epReport := &EndpointReport{
			Method:      ep.Method,
			Path:        ep.Path,
			Total:       int64(len(ep.Latencies)),
			StatusCodes: ep.StatusCodes,
		}
		for code, count := range ep.StatusCodes {
			if code >= 200 && code < 400 {
				epReport.Successful += count
			} else {
				epReport.Failed += count
			}
		}
		if len(ep.Latencies) > 0 {
			sorted := make([]float64, len(ep.Latencies))
			copy(sorted, ep.Latencies)
			sort.Float64s(sorted)
			epReport.AvgLatencyMs = avg(sorted)
			epReport.P99LatencyMs = percentile(sorted, 99)
		}
		ep.mu.Unlock()
		report.Endpoints[key] = epReport
	}

	if len(allLatencies) > 0 {
		sort.Float64s(allLatencies)
		report.AvgLatencyMs = avg(allLatencies)
		report.MinLatencyMs = allLatencies[0]
		report.MaxLatencyMs = allLatencies[len(allLatencies)-1]
		report.P50LatencyMs = percentile(allLatencies, 50)
		report.P95LatencyMs = percentile(allLatencies, 95)
		report.P99LatencyMs = percentile(allLatencies, 99)
	}

	return report
}

func printReport(w io.Writer, r *PerfReport) {
	line := strings.Repeat("=", 70)
	fmt.Fprintf(w, "\n%s\n  PERFORMANCE TEST REPORT\n%s\n\n", line, line)

	fmt.Fprintf(w, "  Duration:    %s\n", r.Duration.Round(time.Millisecond))
	fmt.Fprintf(w, "  Workers:     %d\n\n", r.NumWorkers)

	fmt.Fprintf(w, "  Total:       %d requests\n", r.TotalRequests)
	fmt.Fprintf(w, "  Successful:  %d (%.1f%%)\n", r.Successful, pct(r.Successful, r.TotalRequests))
	fmt.Fprintf(w, "  Failed:      %d (%.1f%%)\n", r.Failed, pct(r.Failed, r.TotalRequests))
	fmt.Fprintf(w, "  Throughput:  %.1f req/s\n\n", r.Throughput)

	fmt.Fprintf(w, "  Latency (ms):\n")
	fmt.Fprintf(w, "    Min: %.2f  Avg: %.2f  P50: %.2f  P95: %.2f  P99: %.2f  Max: %.2f\n\n",
		r.MinLatencyMs, r.AvgLatencyMs, r.P50LatencyMs, r.P95LatencyMs, r.P99LatencyMs, r.MaxLatencyMs)

	// Sort endpoints by key for stable output.
	keys := make([]string, 0, len(r.Endpoints))
	for k := range r.Endpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Fprintf(w, "  Per-endpoint:\n")
	fmt.Fprintf(w, "  %-30s %8s %8s %8s %10s %10s\n", "ENDPOINT", "TOTAL", "OK", "FAIL", "AVG(ms)", "P99(ms)")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 78))
	for _, key := range keys {
		ep := r.Endpoints[key]
		label := ep.Method + " " + ep.Path
		if len(label) > 30 {
			label = label[:27] + "..."
		}
		fmt.Fprintf(w, "  %-30s %8d %8d %8d %10.2f %10.2f\n",
			label, ep.Total, ep.Successful, ep.Failed, ep.AvgLatencyMs, ep.P99LatencyMs)
		if ep.Failed > 0 {
			// Show status code breakdown for endpoints with failures.
			codes := make([]int, 0, len(ep.StatusCodes))
			for c := range ep.StatusCodes {
				codes = append(codes, c)
			}
			sort.Ints(codes)
			var parts []string
			for _, c := range codes {
				parts = append(parts, fmt.Sprintf("%d=%d", c, ep.StatusCodes[c]))
			}
			fmt.Fprintf(w, "  %30s  status: %s\n", "", strings.Join(parts, " "))
		}
	}

	if m := r.Memory; m != nil {
		fmt.Fprintf(w, "\n  Memory (RSS):\n")
		fmt.Fprintf(w, "    Start: %.1f MB  Peak: %.1f MB  End: %.1f MB  Avg: %.1f MB  Delta: %+.1f MB\n",
			float64(m.StartKB)/1024, float64(m.PeakKB)/1024,
			float64(m.EndKB)/1024, float64(m.AvgKB)/1024,
			float64(m.EndKB-m.StartKB)/1024)
		fmt.Fprintf(w, "    Samples: %d (every %s)\n", len(m.Samples), m.SampleRate)
	}

	fmt.Fprintf(w, "\n%s\n\n", line)
}

// ---------------------------------------------------------------------------
// Load runner
// ---------------------------------------------------------------------------

func runLoadTest(cfg PerfConfig, specs []RequestSpec, client *http.Client) (*PerfReport, error) {
	metrics := newLoadMetrics()

	// Build weighted pool.
	var pool []RequestSpec
	for _, s := range specs {
		for i := 0; i < s.Weight; i++ {
			pool = append(pool, s)
		}
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("empty request pool")
	}

	// Start memory sampler if LOOP_PID is set.
	var memSampler *memorySampler
	memStop := make(chan struct{})
	if pid := os.Getenv("LOOP_PID"); pid != "" {
		memSampler = newMemorySampler(pid, 500*time.Millisecond)
		go memSampler.run(memStop)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Simple round-robin offset per worker to spread requests.
			idx := workerID
			for {
				select {
				case <-stop:
					return
				default:
				}

				spec := pool[idx%len(pool)]
				idx++

				body := spec.Body
				if spec.Setup != nil {
					if b, err := spec.Setup(); err == nil {
						body = b
					}
				}

				url := cfg.BaseURL + spec.Path
				var bodyReader io.Reader
				if body != nil {
					bodyReader = bytes.NewReader(body)
				}

				req, err := http.NewRequest(spec.Method, url, bodyReader)
				if err != nil {
					metrics.totalRequests.Add(1)
					metrics.failed.Add(1)
					continue
				}
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}

				start := time.Now()
				resp, err := client.Do(req)
				latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

				metrics.totalRequests.Add(1)
				if err != nil {
					metrics.failed.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				ep := metrics.endpointFor(spec.Method, spec.Path)
				ep.record(latencyMs, resp.StatusCode)

				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					metrics.successful.Add(1)
				} else {
					metrics.failed.Add(1)
				}
			}
		}(w)
	}

	time.Sleep(cfg.Duration)
	close(stop)
	wg.Wait()
	close(memStop)

	report := generateReport(cfg, metrics, cfg.Duration)
	if memSampler != nil {
		report.Memory = memSampler.stats()
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

type APIPerfTestSuite struct {
	suite.Suite

	cfg        PerfConfig
	client     *http.Client
	channelID  string
	lastReport *PerfReport
}

func TestAPIPerfTestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance component test in short mode")
	}
	suite.Run(t, new(APIPerfTestSuite))
}

func (s *APIPerfTestSuite) SetupSuite() {
	s.cfg = configFromEnv()
	s.client = &http.Client{Timeout: 10 * time.Second}

	// Verify service is up.
	resp, err := s.client.Get(s.cfg.BaseURL + "/api/health")
	s.Require().NoError(err, "Loop service not reachable at %s", s.cfg.BaseURL)
	resp.Body.Close()
	s.Require().Equal(http.StatusOK, resp.StatusCode)

	s.T().Logf("Connected to %s", s.cfg.BaseURL)
	s.T().Logf("Config: duration=%s workers=%d", s.cfg.Duration, s.cfg.Workers)

	// Seed: ensure a channel exists for task creation.
	s.channelID = s.ensureChannel("/tmp/loop-perf-test")
	s.T().Logf("Seeded channel: %s", s.channelID)
}

func (s *APIPerfTestSuite) TearDownSuite() {
	if s.lastReport != nil {
		printReport(os.Stdout, s.lastReport)
	}
}

func (s *APIPerfTestSuite) TestAPIPerformance() {
	specs := s.buildRequestSpecs()

	report, err := runLoadTest(s.cfg, specs, s.client)
	s.Require().NoError(err, "Load test failed")
	s.lastReport = report

	s.assertPerformanceCriteria(report)
}

func (s *APIPerfTestSuite) buildRequestSpecs() []RequestSpec {
	channelID := s.channelID
	taskCounter := atomic.Int64{}

	return []RequestSpec{
		{Method: "GET", Path: "/api/health", Weight: 20},
		{
			Method: "POST",
			Path:   "/api/channels",
			Weight: 10,
			Body:   toJSON(map[string]string{"dir_path": "/tmp/loop-perf-test", "platform": "local"}),
		},
		{Method: "GET", Path: "/api/channels?q=", Weight: 30},
		{
			Method: "POST",
			Path:   "/api/tasks",
			Weight: 10,
			Setup: func() ([]byte, error) {
				n := taskCounter.Add(1)
				return toJSON(map[string]any{
					"channel_id": channelID,
					"schedule":   "5m",
					"type":       "interval",
					"prompt":     fmt.Sprintf("perf test task %d", n),
				}), nil
			},
		},
		{Method: "GET", Path: "/api/tasks", Weight: 30},
	}
}

func (s *APIPerfTestSuite) assertPerformanceCriteria(report *PerfReport) {
	s.Assert().LessOrEqual(report.P99LatencyMs, s.cfg.MaxP99LatencyMs,
		"P99 latency exceeded: got %.2f ms, max %.2f ms", report.P99LatencyMs, s.cfg.MaxP99LatencyMs)

	s.Assert().GreaterOrEqual(report.Throughput, s.cfg.MinThroughput,
		"Throughput below threshold: got %.2f req/s, min %.2f req/s", report.Throughput, s.cfg.MinThroughput)

	failureRate := float64(0)
	if report.TotalRequests > 0 {
		failureRate = float64(report.Failed) / float64(report.TotalRequests) * 100
	}
	s.Assert().LessOrEqual(failureRate, s.cfg.MaxFailureRate,
		"Failure rate exceeded: got %.2f%%, max %.2f%%", failureRate, s.cfg.MaxFailureRate)
}

// ensureChannel creates or retrieves a channel for the given dir path.
func (s *APIPerfTestSuite) ensureChannel(dirPath string) string {
	body := toJSON(map[string]string{"dir_path": dirPath, "platform": "local"})
	resp, err := s.client.Post(s.cfg.BaseURL+"/api/channels", "application/json", bytes.NewReader(body))
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Require().Equal(http.StatusOK, resp.StatusCode)

	var result struct {
		ChannelID string `json:"channel_id"`
	}
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(&result))
	return result.ChannelID
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func toJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func avg(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return sum / float64(len(sorted))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func pct(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDurationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
