package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"argus-sdr/internal/shared"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DataHandler struct {
	db               *sql.DB
	logger           *logger.Logger
	cfg              *config.Config
	collectorHandler *CollectorHandler
}

// SetCollectorHandler sets the collector handler for WebSocket communications
func (h *DataHandler) SetCollectorHandler(collectorHandler *CollectorHandler) {
	h.collectorHandler = collectorHandler
}

func NewDataHandler(db *sql.DB, log *logger.Logger, cfg *config.Config) *DataHandler {
	return &DataHandler{
		db:     db,
		logger: log,
		cfg:    cfg,
	}
}

// RequestData handles POST /api/data/request
func (h *DataHandler) RequestData(c *gin.Context) {
	var request shared.DataRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate unique request ID if not provided
	if request.ID == "" {
		request.ID = uuid.New().String()
	}
	userID := c.GetString("user_id")
	request.RequestedBy = userID
	request.Timestamp = time.Now().Unix()

	// Store request in database
	if err := h.createDataRequest(&request); err != nil {
		h.logger.Error("Failed to create data request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Forward to available collectors
	if err := h.forwardToCollectors(request); err != nil {
		h.logger.Error("Failed to forward to collectors: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No collectors available"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"request_id": request.ID,
		"status":     "processing",
	})
}

// GetRequestStatus handles GET /api/data/status/:id
func (h *DataHandler) GetRequestStatus(c *gin.Context) {
	requestID := c.Param("id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request ID is required"})
		return
	}

	status, err := h.getDataRequestStatus(requestID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Request not found"})
			return
		}
		h.logger.Error("Failed to get request status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get status"})
		return
	}

	c.JSON(http.StatusOK, status)
}

// GetAvailableDownloads handles GET /api/data/downloads/:id - returns all available downloads for a request
func (h *DataHandler) GetAvailableDownloads(c *gin.Context) {
	requestID := c.Param("id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request ID is required"})
		return
	}

	// Get all collector responses for this request
	responses, err := h.GetCollectorResponses(requestID)
	if err != nil {
		h.logger.Error("Failed to get collector responses: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get responses"})
		return
	}

	// Filter to only ready responses
	var availableDownloads []CollectorResponse
	for _, response := range responses {
		if response.Status == "ready" {
			availableDownloads = append(availableDownloads, response)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id": requestID,
		"available_downloads": availableDownloads,
		"total_ready": len(availableDownloads),
	})
}

// ListRequests handles GET /api/data/requests
func (h *DataHandler) ListRequests(c *gin.Context) {
	userID := c.GetString("user_id")

	requests, err := h.getDataRequestsByUser(userID)
	if err != nil {
		h.logger.Error("Failed to get user requests: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get requests"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// DownloadFile handles GET /api/data/download/:id
func (h *DataHandler) DownloadFile(c *gin.Context) {
	requestID := c.Param("id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request ID is required"})
		return
	}

	// Get request status to verify it's ready and get file path
	status, err := h.getDataRequestStatus(requestID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "Request not found"})
			return
		}
		h.logger.Error("Failed to get request status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get status"})
		return
	}

	// Check if file is ready for download
	if status.Status != "ready" {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"error": "File not ready for download",
			"status": status.Status,
		})
		return
	}

	// Check if file path exists
	if status.FilePath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "File path not available"})
		return
	}

	// For now, return a mock file
	// In a real implementation, this would stream the actual file from the collector
	fileName := fmt.Sprintf("%s_data.npz", requestID)

	// Set headers for file download
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", status.FileSize))

	// Generate mock file data
	mockData := fmt.Sprintf("# Mock SDR data file for request %s\n# Station: %s\n# File size: %d bytes\n# Generated at: %s\n",
		requestID, status.StationID, status.FileSize, time.Now().Format(time.RFC3339))

	// Pad to reach the expected file size
	remainingSize := int(status.FileSize) - len(mockData)
	if remainingSize > 0 {
		padding := make([]byte, remainingSize)
		for i := range padding {
			padding[i] = byte(i % 256) // Simple pattern for mock data
		}
		mockData += string(padding)
	}

	h.logger.Info("Serving download for request %s (%d bytes)", requestID, status.FileSize)

	c.Data(http.StatusOK, "application/octet-stream", []byte(mockData))
}

// createDataRequest stores a new data request in the database
func (h *DataHandler) createDataRequest(request *shared.DataRequest) error {
	query := `
		INSERT INTO data_requests (id, request_type, parameters, requested_by, status, created_at)
		VALUES (?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP)
	`
	_, err := h.db.Exec(query, request.ID, request.RequestType, request.Parameters, request.RequestedBy)
	return err
}

// getDataRequestStatus retrieves the status of a data request
func (h *DataHandler) getDataRequestStatus(requestID string) (*shared.DataRequestStatus, error) {
	query := `
		SELECT id, status, file_path, file_size, assigned_station
		FROM data_requests
		WHERE id = ?
	`

	var status shared.DataRequestStatus
	var filePath, stationID sql.NullString
	var fileSize sql.NullInt64

	err := h.db.QueryRow(query, requestID).Scan(
		&status.RequestID,
		&status.Status,
		&filePath,
		&fileSize,
		&stationID,
	)

	if err != nil {
		return nil, err
	}

	if filePath.Valid {
		status.FilePath = filePath.String
	}
	if fileSize.Valid {
		status.FileSize = fileSize.Int64
	}
	if stationID.Valid {
		status.StationID = stationID.String
	}

	return &status, nil
}

// getDataRequestsByUser retrieves all data requests for a specific user
func (h *DataHandler) getDataRequestsByUser(userID string) ([]shared.DataRequestStatus, error) {
	query := `
		SELECT id, status, file_path, file_size, assigned_station, created_at
		FROM data_requests
		WHERE requested_by = ?
		ORDER BY created_at DESC
		LIMIT 50
	`

	rows, err := h.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []shared.DataRequestStatus
	for rows.Next() {
		var req shared.DataRequestStatus
		var filePath, stationID sql.NullString
		var fileSize sql.NullInt64
		var createdAt string

		err := rows.Scan(
			&req.RequestID,
			&req.Status,
			&filePath,
			&fileSize,
			&stationID,
			&createdAt,
		)
		if err != nil {
			continue
		}

		if filePath.Valid {
			req.FilePath = filePath.String
		}
		if fileSize.Valid {
			req.FileSize = fileSize.Int64
		}
		if stationID.Valid {
			req.StationID = stationID.String
		}

		requests = append(requests, req)
	}

	return requests, nil
}

// forwardToCollectors sends the request to available collectors
func (h *DataHandler) forwardToCollectors(request shared.DataRequest) error {
	// Get available stations
	stations, err := h.getAvailableStations()
	if err != nil {
		return err
	}

	if len(stations) == 0 {
		return gin.Error{
			Err:  nil,
			Type: gin.ErrorTypePublic,
			Meta: "No stations available",
		}
	}

	// Limit to maximum of 3 collectors
	maxCollectors := 3
	if len(stations) > maxCollectors {
		stations = stations[:maxCollectors]
	}

	h.logger.Info("Forwarding request %s to %d collectors: %v", request.ID, len(stations), stations)

	// Send request to all selected collectors
	var lastError error
	successCount := 0

	for _, stationID := range stations {
		// Send WebSocket message to station
		if h.collectorHandler != nil {
			if err := h.collectorHandler.SendDataRequest(stationID, request); err != nil {
				h.logger.Error("Failed to send WebSocket message to station %s: %v", stationID, err)
				lastError = err
				continue
			}
			h.logger.Info("Forwarded request %s to station %s via WebSocket", request.ID, stationID)
			successCount++
		} else {
			h.logger.Warn("CollectorHandler not set, cannot send WebSocket message")
			lastError = fmt.Errorf("CollectorHandler not set")
		}
	}

	// If no collectors received the request successfully, return error
	if successCount == 0 {
		if lastError != nil {
			return lastError
		}
		return fmt.Errorf("failed to send request to any collectors")
	}

	// Update the request with the first assigned station for tracking purposes
	// The first station to complete will update the status
	if err := h.assignStation(request.ID, stations[0]); err != nil {
		h.logger.Error("Failed to assign station for tracking: %v", err)
		// Don't return error here as the requests were already sent
	}

	h.logger.Info("Successfully forwarded request %s to %d/%d collectors", request.ID, successCount, len(stations))
	return nil
}

// getAvailableStations returns a list of available station IDs
func (h *DataHandler) getAvailableStations() ([]string, error) {
	query := `
		SELECT station_id
		FROM collector_sessions
		WHERE status = 'connected'
		AND last_heartbeat > datetime('now', '-2 minutes')
	`

	rows, err := h.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stations []string
	for rows.Next() {
		var stationID string
		if err := rows.Scan(&stationID); err != nil {
			continue
		}
		stations = append(stations, stationID)
	}

	return stations, nil
}

// assignStation assigns a request to a specific station
func (h *DataHandler) assignStation(requestID, stationID string) error {
	query := `
		UPDATE data_requests
		SET assigned_station = ?, status = 'assigned'
		WHERE id = ?
	`
	_, err := h.db.Exec(query, stationID, requestID)
	return err
}

// UpdateDataRequestStatus updates the status of a data request
func (h *DataHandler) UpdateDataRequestStatus(requestID, status, filePath string, fileSize int64) error {
	query := `
		UPDATE data_requests
		SET status = ?, file_path = ?, file_size = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := h.db.Exec(query, status, filePath, fileSize, requestID)
	return err
}

// StoreCollectorResponse stores an individual collector response
func (h *DataHandler) StoreCollectorResponse(requestID, stationID, status, filePath string, fileSize int64, errorMessage string) error {
	query := `
		INSERT OR REPLACE INTO collector_responses
		(request_id, station_id, status, file_path, file_size, error_message, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`
	_, err := h.db.Exec(query, requestID, stationID, status, filePath, fileSize, errorMessage)
	if err != nil {
		return err
	}

	// Also update the main data_requests table if this is the first completion
	if status == "ready" {
		// Check if this is the first collector to complete
		var count int
		countQuery := `
			SELECT COUNT(*) FROM collector_responses
			WHERE request_id = ? AND status = 'ready'
		`
		if err := h.db.QueryRow(countQuery, requestID).Scan(&count); err == nil && count == 1 {
			// This is the first completion, update the main request status
			h.UpdateDataRequestStatus(requestID, status, filePath, fileSize)
		}
	}

	return nil
}

// GetCollectorResponses returns all collector responses for a request
func (h *DataHandler) GetCollectorResponses(requestID string) ([]CollectorResponse, error) {
	query := `
		SELECT request_id, station_id, status, file_path, file_size, error_message, completed_at
		FROM collector_responses
		WHERE request_id = ?
		ORDER BY completed_at ASC
	`

	rows, err := h.db.Query(query, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var responses []CollectorResponse
	for rows.Next() {
		var response CollectorResponse
		var filePath, errorMessage sql.NullString
		var fileSize sql.NullInt64
		var completedAt sql.NullString

		err := rows.Scan(
			&response.RequestID,
			&response.StationID,
			&response.Status,
			&filePath,
			&fileSize,
			&errorMessage,
			&completedAt,
		)
		if err != nil {
			continue
		}

		if filePath.Valid {
			response.FilePath = filePath.String
		}
		if fileSize.Valid {
			response.FileSize = fileSize.Int64
		}
		if errorMessage.Valid {
			response.ErrorMessage = errorMessage.String
		}
		if completedAt.Valid {
			response.CompletedAt = completedAt.String
		}

		responses = append(responses, response)
	}

	return responses, nil
}

// GetNextAvailableDownload returns the next available download for a request that hasn't been retrieved yet
func (h *DataHandler) GetNextAvailableDownload(requestID string, excludeStations []string) (*shared.DataRequestStatus, error) {
	// Build query to exclude already downloaded stations
	query := `
		SELECT cr.request_id, cr.station_id, cr.status, cr.file_path, cr.file_size
		FROM collector_responses cr
		WHERE cr.request_id = ? AND cr.status = 'ready'
	`
	args := []interface{}{requestID}

	if len(excludeStations) > 0 {
		placeholders := make([]string, len(excludeStations))
		for i, station := range excludeStations {
			placeholders[i] = "?"
			args = append(args, station)
		}
		query += ` AND cr.station_id NOT IN (` + fmt.Sprintf("%s", placeholders[0])
		for i := 1; i < len(placeholders); i++ {
			query += `, ` + placeholders[i]
		}
		query += `)`
	}

	query += ` ORDER BY cr.completed_at ASC LIMIT 1`

	var status shared.DataRequestStatus
	var filePath sql.NullString
	var fileSize sql.NullInt64

	err := h.db.QueryRow(query, args...).Scan(
		&status.RequestID,
		&status.StationID,
		&status.Status,
		&filePath,
		&fileSize,
	)

	if err != nil {
		return nil, err
	}

	if filePath.Valid {
		status.FilePath = filePath.String
	}
	if fileSize.Valid {
		status.FileSize = fileSize.Int64
	}

	return &status, nil
}

// CollectorResponse represents a response from an individual collector
type CollectorResponse struct {
	RequestID    string `json:"request_id"`
	StationID    string `json:"station_id"`
	Status       string `json:"status"`
	FilePath     string `json:"file_path,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

// RegisterCollectorSession registers a new collector session
func (h *DataHandler) RegisterCollectorSession(stationID string) error {
	query := `
		INSERT OR REPLACE INTO collector_sessions (station_id, connected_at, last_heartbeat, status)
		VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'connected')
	`
	_, err := h.db.Exec(query, stationID)
	return err
}

// UpdateCollectorHeartbeat updates the last heartbeat for a collector
func (h *DataHandler) UpdateCollectorHeartbeat(stationID string) error {
	query := `
		UPDATE collector_sessions
		SET last_heartbeat = CURRENT_TIMESTAMP
		WHERE station_id = ?
	`
	_, err := h.db.Exec(query, stationID)
	return err
}