package handlers

import (
	"database/sql"
	"net/http"
	"runtime"
	"time"

	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"
	"argus-sdr/pkg/metrics"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	db      *sql.DB
	log     *logger.Logger
	cfg     *config.Config
	metrics *metrics.SystemMetrics
}

// HealthStatus represents the overall system health status
type HealthStatus struct {
	Status       string                 `json:"status"`
	Timestamp    time.Time              `json:"timestamp"`
	Version      string                 `json:"version"`
	Uptime       time.Duration          `json:"uptime"`
	Environment  string                 `json:"environment"`
	Components   map[string]ComponentHealth `json:"components"`
	SystemInfo   SystemInfo             `json:"system_info"`
	Metrics      metrics.MetricsSnapshot `json:"metrics"`
}

// ComponentHealth represents the health of individual components
type ComponentHealth struct {
	Status    string                 `json:"status"`
	Message   string                 `json:"message,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
	LastCheck time.Time              `json:"last_check"`
}

// SystemInfo contains system resource information
type SystemInfo struct {
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutine"`
	NumCPU       int    `json:"num_cpu"`
	MemStats     MemoryStats `json:"memory_stats"`
}

// MemoryStats contains memory usage statistics
type MemoryStats struct {
	Alloc        uint64 `json:"alloc_bytes"`
	TotalAlloc   uint64 `json:"total_alloc_bytes"`
	Sys          uint64 `json:"sys_bytes"`
	NumGC        uint32 `json:"num_gc"`
	HeapAlloc    uint64 `json:"heap_alloc_bytes"`
	HeapSys      uint64 `json:"heap_sys_bytes"`
	HeapInuse    uint64 `json:"heap_inuse_bytes"`
	HeapReleased uint64 `json:"heap_released_bytes"`
}

func NewHealthHandler(db *sql.DB, log *logger.Logger, cfg *config.Config, metrics *metrics.SystemMetrics) *HealthHandler {
	return &HealthHandler{
		db:      db,
		log:     log,
		cfg:     cfg,
		metrics: metrics,
	}
}

// GetHealth returns comprehensive system health information
func (h *HealthHandler) GetHealth(c *gin.Context) {
	startTime := time.Now()
	
	status := h.performHealthChecks()
	
	// Record response time
	if h.metrics != nil {
		h.metrics.RecordResponseTime(time.Since(startTime))
	}
	
	// Set HTTP status based on overall health
	httpStatus := http.StatusOK
	if status.Status == "unhealthy" {
		httpStatus = http.StatusServiceUnavailable
	} else if status.Status == "degraded" {
		httpStatus = http.StatusPartialContent
	}
	
	c.JSON(httpStatus, status)
}

// GetMetrics returns detailed system metrics
func (h *HealthHandler) GetMetrics(c *gin.Context) {
	if h.metrics == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Metrics collection not enabled",
		})
		return
	}
	
	snapshot := h.metrics.GetSnapshot()
	c.JSON(http.StatusOK, snapshot)
}

// GetReadiness returns a simple readiness check
func (h *HealthHandler) GetReadiness(c *gin.Context) {
	// Check if essential services are ready
	ready := true
	issues := []string{}
	
	// Check database connectivity
	if err := h.db.Ping(); err != nil {
		ready = false
		issues = append(issues, "database_unavailable")
	}
	
	if ready {
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"timestamp": time.Now(),
		})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"issues": issues,
			"timestamp": time.Now(),
		})
	}
}

// GetLiveness returns a simple liveness check
func (h *HealthHandler) GetLiveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "alive",
		"timestamp": time.Now(),
	})
}

// performHealthChecks executes all health checks and returns overall status
func (h *HealthHandler) performHealthChecks() HealthStatus {
	now := time.Now()
	components := make(map[string]ComponentHealth)
	overallHealthy := true
	
	// Database health check
	dbHealth := h.checkDatabaseHealth()
	components["database"] = dbHealth
	if dbHealth.Status != "healthy" {
		overallHealthy = false
	}
	
	// WebSocket connections health
	wsHealth := h.checkWebSocketHealth()
	components["websockets"] = wsHealth
	if wsHealth.Status != "healthy" {
		overallHealthy = false
	}
	
	// Collector connections health
	collectorHealth := h.checkCollectorHealth()
	components["collectors"] = collectorHealth
	if collectorHealth.Status == "unhealthy" {
		overallHealthy = false
	}
	
	// System resources health
	systemHealth := h.checkSystemHealth()
	components["system"] = systemHealth
	if systemHealth.Status != "healthy" {
		overallHealthy = false
	}
	
	// Data processing health
	dataHealth := h.checkDataProcessingHealth()
	components["data_processing"] = dataHealth
	if dataHealth.Status == "unhealthy" {
		overallHealthy = false
	}
	
	// Determine overall status
	status := "healthy"
	if !overallHealthy {
		// Check if any component is completely down
		unhealthyCount := 0
		for _, comp := range components {
			if comp.Status == "unhealthy" {
				unhealthyCount++
			}
		}
		if unhealthyCount > 0 {
			status = "unhealthy"
		} else {
			status = "degraded"
		}
	}
	
	// Get system info and metrics
	systemInfo := h.getSystemInfo()
	var metricsSnapshot metrics.MetricsSnapshot
	if h.metrics != nil {
		metricsSnapshot = h.metrics.GetSnapshot()
	}
	
	return HealthStatus{
		Status:      status,
		Timestamp:   now,
		Version:     "0.0.1", // TODO: Get from build info
		Uptime:      metricsSnapshot.Uptime,
		Environment: h.cfg.Environment,
		Components:  components,
		SystemInfo:  systemInfo,
		Metrics:     metricsSnapshot,
	}
}

// checkDatabaseHealth checks database connectivity and performance
func (h *HealthHandler) checkDatabaseHealth() ComponentHealth {
	start := time.Now()
	details := make(map[string]interface{})
	
	// Test basic connectivity
	if err := h.db.Ping(); err != nil {
		return ComponentHealth{
			Status:    "unhealthy",
			Message:   "Database ping failed",
			Details:   map[string]interface{}{"error": err.Error()},
			LastCheck: time.Now(),
		}
	}
	
	// Test a simple query
	var count int
	err := h.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	queryTime := time.Since(start)
	
	details["user_count"] = count
	details["ping_time_ms"] = queryTime.Milliseconds()
	
	if err != nil {
		return ComponentHealth{
			Status:    "degraded",
			Message:   "Database query failed",
			Details:   details,
			LastCheck: time.Now(),
		}
	}
	
	// Check for slow queries
	status := "healthy"
	message := "Database is responsive"
	if queryTime > 100*time.Millisecond {
		status = "degraded"
		message = "Database queries are slow"
	}
	
	return ComponentHealth{
		Status:    status,
		Message:   message,
		Details:   details,
		LastCheck: time.Now(),
	}
}

// checkWebSocketHealth checks WebSocket connection health
func (h *HealthHandler) checkWebSocketHealth() ComponentHealth {
	details := make(map[string]interface{})
	
	// Get connection counts from metrics
	var activeConnections int64 = 0
	var totalMessages int64 = 0
	var errors int64 = 0
	
	if h.metrics != nil {
		snapshot := h.metrics.GetSnapshot()
		activeConnections = snapshot.ActiveConnections
		totalMessages = snapshot.WebSocketMessages
		errors = snapshot.WebSocketErrors
	}
	
	details["active_connections"] = activeConnections
	details["total_messages"] = totalMessages
	details["errors"] = errors
	
	status := "healthy"
	message := "WebSocket connections are healthy"
	
	// Check error rate
	if totalMessages > 0 && errors > 0 {
		errorRate := float64(errors) / float64(totalMessages)
		details["error_rate"] = errorRate
		
		if errorRate > 0.1 { // 10% error rate
			status = "unhealthy"
			message = "High WebSocket error rate"
		} else if errorRate > 0.05 { // 5% error rate
			status = "degraded"
			message = "Elevated WebSocket error rate"
		}
	}
	
	return ComponentHealth{
		Status:    status,
		Message:   message,
		Details:   details,
		LastCheck: time.Now(),
	}
}

// checkCollectorHealth checks collector connection health
func (h *HealthHandler) checkCollectorHealth() ComponentHealth {
	details := make(map[string]interface{})
	
	// Query active collector sessions
	var activeCollectors int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM collector_sessions 
		WHERE status = 'connected' AND last_heartbeat > datetime('now', '-2 minutes')
	`).Scan(&activeCollectors)
	
	if err != nil {
		return ComponentHealth{
			Status:    "unhealthy",
			Message:   "Failed to query collector status",
			Details:   map[string]interface{}{"error": err.Error()},
			LastCheck: time.Now(),
		}
	}
	
	details["active_collectors"] = activeCollectors
	
	status := "healthy"
	message := "Collector connections are healthy"
	
	if activeCollectors == 0 {
		status = "degraded"
		message = "No active collectors connected"
	} else if activeCollectors < 3 {
		status = "degraded"
		message = "Fewer than minimum recommended collectors"
	}
	
	return ComponentHealth{
		Status:    status,
		Message:   message,
		Details:   details,
		LastCheck: time.Now(),
	}
}

// checkSystemHealth checks system resource health
func (h *HealthHandler) checkSystemHealth() ComponentHealth {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	details := map[string]interface{}{
		"goroutines":    runtime.NumGoroutine(),
		"heap_alloc_mb": memStats.HeapAlloc / 1024 / 1024,
		"heap_sys_mb":   memStats.HeapSys / 1024 / 1024,
		"num_gc":        memStats.NumGC,
	}
	
	status := "healthy"
	message := "System resources are healthy"
	
	// Check for excessive memory usage (>500MB heap)
	if memStats.HeapAlloc > 500*1024*1024 {
		status = "degraded"
		message = "High memory usage detected"
	}
	
	// Check for excessive goroutines (>1000)
	if runtime.NumGoroutine() > 1000 {
		status = "degraded"
		message = "High goroutine count detected"
	}
	
	return ComponentHealth{
		Status:    status,
		Message:   message,
		Details:   details,
		LastCheck: time.Now(),
	}
}

// checkDataProcessingHealth checks data processing pipeline health
func (h *HealthHandler) checkDataProcessingHealth() ComponentHealth {
	details := make(map[string]interface{})
	
	// Query recent data request statistics
	var pendingRequests, failedRequests int
	var avgProcessingTime float64
	
	err := h.db.QueryRow(`
		SELECT 
			COUNT(CASE WHEN status = 'pending' THEN 1 END),
			COUNT(CASE WHEN status = 'error' AND created_at > datetime('now', '-1 hour') THEN 1 END)
		FROM data_requests
	`).Scan(&pendingRequests, &failedRequests)
	
	if err != nil {
		return ComponentHealth{
			Status:    "degraded",
			Message:   "Failed to query data processing status",
			Details:   map[string]interface{}{"error": err.Error()},
			LastCheck: time.Now(),
		}
	}
	
	details["pending_requests"] = pendingRequests
	details["failed_requests_last_hour"] = failedRequests
	details["avg_processing_time_ms"] = avgProcessingTime
	
	status := "healthy"
	message := "Data processing is healthy"
	
	if pendingRequests > 50 {
		status = "degraded"
		message = "High number of pending requests"
	}
	
	if failedRequests > 10 {
		status = "unhealthy"
		message = "High failure rate in data processing"
	}
	
	return ComponentHealth{
		Status:    status,
		Message:   message,
		Details:   details,
		LastCheck: time.Now(),
	}
}

// getSystemInfo returns current system information
func (h *HealthHandler) getSystemInfo() SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	return SystemInfo{
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		NumCPU:       runtime.NumCPU(),
		MemStats: MemoryStats{
			Alloc:        memStats.Alloc,
			TotalAlloc:   memStats.TotalAlloc,
			Sys:          memStats.Sys,
			NumGC:        memStats.NumGC,
			HeapAlloc:    memStats.HeapAlloc,
			HeapSys:      memStats.HeapSys,
			HeapInuse:    memStats.HeapInuse,
			HeapReleased: memStats.HeapReleased,
		},
	}
}