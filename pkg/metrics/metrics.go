package metrics

import (
	"sync"
	"time"
)

// SystemMetrics holds various system performance metrics
type SystemMetrics struct {
	mu                    sync.RWMutex
	StartTime            time.Time
	RequestCount         int64
	ErrorCount           int64
	ActiveConnections    int64
	TotalConnections     int64
	ActiveCollectors     int64
	ActiveReceivers      int64
	DataRequestsTotal    int64
	DataRequestsPending  int64
	DataRequestsComplete int64
	DataRequestsFailed   int64
	FilesTransferred     int64
	BytesTransferred     int64
	ICESessionsActive    int64
	ICESessionsTotal     int64
	DatabaseQueries      int64
	DatabaseErrors       int64
	WebSocketMessages    int64
	WebSocketErrors      int64
	ResponseTimes        *ResponseTimeTracker
}

// ResponseTimeTracker tracks response time statistics
type ResponseTimeTracker struct {
	mu           sync.RWMutex
	samples      []time.Duration
	maxSamples   int
	totalTime    time.Duration
	requestCount int64
	minTime      time.Duration
	maxTime      time.Duration
}

// NewSystemMetrics creates a new SystemMetrics instance
func NewSystemMetrics() *SystemMetrics {
	return &SystemMetrics{
		StartTime:     time.Now(),
		ResponseTimes: NewResponseTimeTracker(1000), // Keep last 1000 samples
	}
}

// NewResponseTimeTracker creates a new response time tracker
func NewResponseTimeTracker(maxSamples int) *ResponseTimeTracker {
	return &ResponseTimeTracker{
		samples:    make([]time.Duration, 0, maxSamples),
		maxSamples: maxSamples,
		minTime:    time.Hour, // Start with a high value
	}
}

// IncrementRequestCount increments the total request count
func (m *SystemMetrics) IncrementRequestCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RequestCount++
}

// IncrementErrorCount increments the error count
func (m *SystemMetrics) IncrementErrorCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorCount++
}

// SetActiveConnections sets the current active connection count
func (m *SystemMetrics) SetActiveConnections(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveConnections = count
}

// IncrementTotalConnections increments the total connection count
func (m *SystemMetrics) IncrementTotalConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalConnections++
}

// SetActiveCollectors sets the current active collector count
func (m *SystemMetrics) SetActiveCollectors(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveCollectors = count
}

// SetActiveReceivers sets the current active receiver count
func (m *SystemMetrics) SetActiveReceivers(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveReceivers = count
}

// IncrementDataRequests increments various data request counters
func (m *SystemMetrics) IncrementDataRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DataRequestsTotal++
	m.DataRequestsPending++
}

// CompleteDataRequest marks a data request as complete
func (m *SystemMetrics) CompleteDataRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DataRequestsPending > 0 {
		m.DataRequestsPending--
	}
	m.DataRequestsComplete++
}

// FailDataRequest marks a data request as failed
func (m *SystemMetrics) FailDataRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DataRequestsPending > 0 {
		m.DataRequestsPending--
	}
	m.DataRequestsFailed++
}

// IncrementFileTransfer increments file transfer metrics
func (m *SystemMetrics) IncrementFileTransfer(bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FilesTransferred++
	m.BytesTransferred += bytes
}

// SetICESessionsActive sets the current active ICE sessions count
func (m *SystemMetrics) SetICESessionsActive(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ICESessionsActive = count
}

// IncrementICESessionsTotal increments the total ICE sessions count
func (m *SystemMetrics) IncrementICESessionsTotal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ICESessionsTotal++
}

// IncrementDatabaseQueries increments database query count
func (m *SystemMetrics) IncrementDatabaseQueries() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DatabaseQueries++
}

// IncrementDatabaseErrors increments database error count
func (m *SystemMetrics) IncrementDatabaseErrors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DatabaseErrors++
}

// IncrementWebSocketMessages increments WebSocket message count
func (m *SystemMetrics) IncrementWebSocketMessages() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WebSocketMessages++
}

// IncrementWebSocketErrors increments WebSocket error count
func (m *SystemMetrics) IncrementWebSocketErrors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WebSocketErrors++
}

// RecordResponseTime records a response time sample
func (m *SystemMetrics) RecordResponseTime(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ResponseTimes.AddSample(duration)
}

// AddSample adds a response time sample to the tracker
func (rt *ResponseTimeTracker) AddSample(duration time.Duration) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Add to samples ring buffer
	if len(rt.samples) >= rt.maxSamples {
		// Remove oldest sample
		rt.samples = rt.samples[1:]
	}
	rt.samples = append(rt.samples, duration)

	// Update aggregates
	rt.totalTime += duration
	rt.requestCount++

	if duration < rt.minTime {
		rt.minTime = duration
	}
	if duration > rt.maxTime {
		rt.maxTime = duration
	}
}

// GetStats returns current response time statistics
func (rt *ResponseTimeTracker) GetStats() ResponseTimeStats {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	stats := ResponseTimeStats{
		Count:   int64(len(rt.samples)),
		MinTime: rt.minTime,
		MaxTime: rt.maxTime,
	}

	if len(rt.samples) > 0 {
		// Calculate average from current samples
		var total time.Duration
		for _, sample := range rt.samples {
			total += sample
		}
		stats.AvgTime = total / time.Duration(len(rt.samples))

		// Calculate percentiles
		stats.P50, stats.P95, stats.P99 = rt.calculatePercentiles()
	}

	return stats
}

// ResponseTimeStats holds response time statistics
type ResponseTimeStats struct {
	Count   int64
	MinTime time.Duration
	MaxTime time.Duration
	AvgTime time.Duration
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
}

// calculatePercentiles calculates response time percentiles
func (rt *ResponseTimeTracker) calculatePercentiles() (p50, p95, p99 time.Duration) {
	if len(rt.samples) == 0 {
		return 0, 0, 0
	}

	// Create a sorted copy
	sorted := make([]time.Duration, len(rt.samples))
	copy(sorted, rt.samples)

	// Simple insertion sort (fine for our sample size)
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}

	n := len(sorted)
	p50 = sorted[n*50/100]
	p95 = sorted[min(n-1, n*95/100)]
	p99 = sorted[min(n-1, n*99/100)]

	return p50, p95, p99
}

// GetSnapshot returns a snapshot of current metrics
func (m *SystemMetrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return MetricsSnapshot{
		Timestamp:            time.Now(),
		Uptime:              time.Since(m.StartTime),
		RequestCount:         m.RequestCount,
		ErrorCount:           m.ErrorCount,
		ActiveConnections:    m.ActiveConnections,
		TotalConnections:     m.TotalConnections,
		ActiveCollectors:     m.ActiveCollectors,
		ActiveReceivers:      m.ActiveReceivers,
		DataRequestsTotal:    m.DataRequestsTotal,
		DataRequestsPending:  m.DataRequestsPending,
		DataRequestsComplete: m.DataRequestsComplete,
		DataRequestsFailed:   m.DataRequestsFailed,
		FilesTransferred:     m.FilesTransferred,
		BytesTransferred:     m.BytesTransferred,
		ICESessionsActive:    m.ICESessionsActive,
		ICESessionsTotal:     m.ICESessionsTotal,
		DatabaseQueries:      m.DatabaseQueries,
		DatabaseErrors:       m.DatabaseErrors,
		WebSocketMessages:    m.WebSocketMessages,
		WebSocketErrors:      m.WebSocketErrors,
		ResponseTimeStats:    m.ResponseTimes.GetStats(),
	}
}

// MetricsSnapshot represents a point-in-time view of system metrics
type MetricsSnapshot struct {
	Timestamp            time.Time         `json:"timestamp"`
	Uptime              time.Duration     `json:"uptime"`
	RequestCount         int64             `json:"request_count"`
	ErrorCount           int64             `json:"error_count"`
	ActiveConnections    int64             `json:"active_connections"`
	TotalConnections     int64             `json:"total_connections"`
	ActiveCollectors     int64             `json:"active_collectors"`
	ActiveReceivers      int64             `json:"active_receivers"`
	DataRequestsTotal    int64             `json:"data_requests_total"`
	DataRequestsPending  int64             `json:"data_requests_pending"`
	DataRequestsComplete int64             `json:"data_requests_complete"`
	DataRequestsFailed   int64             `json:"data_requests_failed"`
	FilesTransferred     int64             `json:"files_transferred"`
	BytesTransferred     int64             `json:"bytes_transferred"`
	ICESessionsActive    int64             `json:"ice_sessions_active"`
	ICESessionsTotal     int64             `json:"ice_sessions_total"`
	DatabaseQueries      int64             `json:"database_queries"`
	DatabaseErrors       int64             `json:"database_errors"`
	WebSocketMessages    int64             `json:"websocket_messages"`
	WebSocketErrors      int64             `json:"websocket_errors"`
	ResponseTimeStats    ResponseTimeStats `json:"response_time_stats"`
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}