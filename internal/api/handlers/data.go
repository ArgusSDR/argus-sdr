package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"argus-sdr/internal/auth"
	"argus-sdr/internal/models"
	"argus-sdr/internal/shared"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type DataHandler struct {
	db               *sql.DB
	logger           *logger.Logger
	cfg              *config.Config
	collectorHandler *CollectorHandler
	receiverConns    map[string]*websocket.Conn
	connMutex        sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	HandshakeTimeout: 30 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
}

// SetCollectorHandler sets the collector handler for WebSocket communications
func (h *DataHandler) SetCollectorHandler(collectorHandler *CollectorHandler) {
	h.collectorHandler = collectorHandler
}

func NewDataHandler(db *sql.DB, log *logger.Logger, cfg *config.Config) *DataHandler {
	return &DataHandler{
		db:            db,
		logger:        log,
		cfg:           cfg,
		receiverConns: make(map[string]*websocket.Conn),
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
	userIDInt, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found"})
		return
	}
	userID := fmt.Sprintf("%d", userIDInt)
	request.RequestedBy = userID
	request.Timestamp = time.Now().Unix()
	
	h.logger.Debug("RequestData: userID=%s, request.RequestedBy=%s", userID, request.RequestedBy)

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

// RequestDataWithICE handles POST /api/data/request-ice for direct P2P file transfer
func (h *DataHandler) RequestDataWithICE(c *gin.Context) {
	var request struct {
		shared.DataRequest
		UseICE    bool   `json:"use_ice" binding:"required"`
		StationID string `json:"station_id,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !request.UseICE {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ICE direct transfer must be enabled for this endpoint"})
		return
	}

	userIDInt, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found"})
		return
	}

	// Generate unique request ID if not provided
	if request.ID == "" {
		request.ID = uuid.New().String()
	}
	
	userID := fmt.Sprintf("%d", userIDInt)
	request.RequestedBy = userID
	request.Timestamp = time.Now().Unix()

	// Store the data request
	if err := h.createDataRequest(&request.DataRequest); err != nil {
		h.logger.Error("Failed to create data request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Create ICE session for direct P2P transfer
	sessionID, err := h.createICESessionForDataRequest(request.ID, userIDInt.(int), request.StationID, request.DataRequest)
	if err != nil {
		h.logger.Error("Failed to create ICE session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ICE session"})
		return
	}

	h.logger.Info("ICE-enabled data request created: request_id=%s, session_id=%s", request.ID, sessionID)

	c.JSON(http.StatusAccepted, gin.H{
		"request_id": request.ID,
		"session_id": sessionID,
		"status":     "ice_session_created",
		"message":    "ICE session created for direct P2P file transfer",
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
	userIDInt, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found"})
		return
	}
	userID := fmt.Sprintf("%d", userIDInt)

	requests, err := h.getDataRequestsByUser(userID)
	if err != nil {
		h.logger.Error("Failed to get user requests: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get requests"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// DownloadFile handles GET /api/data/download/:id/:station_id
func (h *DataHandler) DownloadFile(c *gin.Context) {
	requestID := c.Param("id")
	stationID := c.Param("station_id")

	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request ID is required"})
		return
	}

	if stationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Station ID is required"})
		return
	}

	// Get the specific collector response for this request and station
	var response CollectorResponse
	query := `
		SELECT request_id, station_id, status, download_url, file_size
		FROM collector_responses
		WHERE request_id = ? AND station_id = ? AND status = 'ready'
	`

	var downloadURL sql.NullString
	var fileSize sql.NullInt64

	err := h.db.QueryRow(query, requestID, stationID).Scan(
		&response.RequestID,
		&response.StationID,
		&response.Status,
		&downloadURL,
		&fileSize,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not ready or not found"})
			return
		}
		h.logger.Error("Failed to query collector response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get file info"})
		return
	}

	if !downloadURL.Valid || downloadURL.String == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Download URL not available"})
		return
	}

	// Proxy the request to the collector
	h.logger.Info("Proxying download request for %s from station %s to %s", requestID, stationID, downloadURL.String)

	// Create HTTP client and make request to collector
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(downloadURL.String)
	if err != nil {
		h.logger.Error("Failed to proxy download request: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Failed to download from collector"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.logger.Error("Collector returned status %d for download", resp.StatusCode)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Collector download failed"})
		return
	}

	// Set appropriate headers
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s_%s_data.npz\"", requestID, stationID))
	if fileSize.Valid {
		c.Header("Content-Length", fmt.Sprintf("%d", fileSize.Int64))
	}

	// Copy the response body directly to the client
	c.DataFromReader(http.StatusOK, resp.ContentLength, "application/octet-stream", resp.Body, nil)
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

	// Send notification to receiver if data is ready
	if status == "ready" {
		h.logger.Info("Timestamp: Sending WebSocket notification to receiver at %s", time.Now().Format("2006-01-02 15:04:05.000"))
		if err := h.NotifyReceiverDataReady(requestID, stationID); err != nil {
			h.logger.Error("Failed to notify receiver about ready data: %v", err)
		} else {
			h.logger.Info("Timestamp: WebSocket notification sent successfully at %s", time.Now().Format("2006-01-02 15:04:05.000"))
		}
	}

	return nil
}

// UpdateCollectorResponseURL updates the download URL for a specific collector response
func (h *DataHandler) UpdateCollectorResponseURL(requestID, stationID, downloadURL string) error {
	query := `
		UPDATE collector_responses
		SET download_url = ?
		WHERE request_id = ? AND station_id = ?
	`
	_, err := h.db.Exec(query, downloadURL, requestID, stationID)
	return err
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

// ReceiverWebSocketHandler handles WebSocket connections for receivers
func (h *DataHandler) ReceiverWebSocketHandler(c *gin.Context) {
	// Authenticate manually for WebSocket connections
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
		return
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenString == authHeader {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
		return
	}

	claims, err := auth.ValidateToken(tokenString, h.cfg.Auth.JWTSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	// Check client type
	if claims.ClientType != 2 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied for client type"})
		return
	}

	userID := fmt.Sprintf("%d", claims.UserID)
	h.logger.Info("WebSocket authentication successful for user %s", claims.Email)

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade WebSocket connection: %v", err)
		return
	}

	h.logger.Info("WebSocket upgrade successful for user %s", userID)

	// Don't set read deadline initially - let it be open
	conn.SetWriteDeadline(time.Time{})  // No write deadline
	conn.SetReadDeadline(time.Time{})   // No read deadline initially

	// Set up ping/pong handler
	conn.SetPongHandler(func(string) error {
		h.logger.Debug("Received pong from user %s", userID)
		return nil
	})

	h.connMutex.Lock()
	h.receiverConns[userID] = conn
	h.connMutex.Unlock()

	h.logger.Info("Receiver WebSocket connected: %s", userID)

	// Handle connection cleanup
	defer func() {
		h.connMutex.Lock()
		delete(h.receiverConns, userID)
		h.connMutex.Unlock()
		conn.Close()
		h.logger.Info("Receiver WebSocket disconnected: %s", userID)
	}()

	// Set up a ping/pong mechanism for connection monitoring
	// The connection is primarily for sending notifications TO the client, not reading FROM it
	
	// Set up a channel to detect when connection is closed
	connectionClosed := make(chan bool, 1)
	
	// Set up close handler
	conn.SetCloseHandler(func(code int, text string) error {
		h.logger.Debug("WebSocket close handler called for user %s: %d %s", userID, code, text)
		connectionClosed <- true
		return nil
	})

	// Start a ping/pong based connection monitor
	go func() {
		defer func() {
			if r := recover(); r != nil {
				h.logger.Error("Recovered from panic in WebSocket monitor for user %s: %v", userID, r)
				connectionClosed <- true
			}
		}()
		
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()
		
		for {
			select {
			case <-pingTicker.C:
				// Send ping to check if connection is still alive
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					h.logger.Debug("Failed to send ping to user %s: %v", userID, err)
					connectionClosed <- true
					return
				}
				conn.SetWriteDeadline(time.Time{})
				h.logger.Debug("Sent ping to user %s", userID)
			}
		}
	}()

	// Wait for connection to close
	<-connectionClosed
	h.logger.Debug("WebSocket connection monitoring ended for user %s", userID)
}

// NotifyReceiverDataReady sends a notification to a receiver when data is ready
func (h *DataHandler) NotifyReceiverDataReady(requestID, stationID string) error {
	// Get the user who made the request
	userID, err := h.getUserForRequest(requestID)
	if err != nil {
		return fmt.Errorf("failed to get user for request: %w", err)
	}

	h.logger.Debug("NotifyReceiverDataReady: requestID=%s, stationID=%s, userID=%s", requestID, stationID, userID)

	h.connMutex.RLock()
	conn, exists := h.receiverConns[userID]
	h.logger.Debug("WebSocket connections available: %v", func() []string {
		var keys []string
		for k := range h.receiverConns {
			keys = append(keys, k)
		}
		return keys
	}())
	h.connMutex.RUnlock()

	if !exists {
		h.logger.Debug("No active WebSocket connection for user %s", userID)
		return nil
	}

	notification := map[string]interface{}{
		"type":       "data_ready",
		"request_id": requestID,
		"station_id": stationID,
		"timestamp":  time.Now().Unix(),
	}

	// Set write deadline to avoid blocking
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	
	if err := conn.WriteJSON(notification); err != nil {
		h.logger.Error("Failed to send notification to user %s: %v", userID, err)
		// Remove the connection if it's broken
		h.connMutex.Lock()
		delete(h.receiverConns, userID)
		h.connMutex.Unlock()
		return err
	}
	
	// Clear write deadline
	conn.SetWriteDeadline(time.Time{})

	h.logger.Info("Sent data ready notification to user %s for request %s from station %s", userID, requestID, stationID)
	return nil
}

// getUserForRequest retrieves the user ID for a given request ID
func (h *DataHandler) getUserForRequest(requestID string) (string, error) {
	query := `SELECT requested_by FROM data_requests WHERE id = ?`
	var userID string
	err := h.db.QueryRow(query, requestID).Scan(&userID)
	h.logger.Debug("getUserForRequest: requestID=%s, userID=%s, err=%v", requestID, userID, err)
	return userID, err
}

// NotifyReceiverOfICEOffer sends a WebSocket notification to a receiver about a new ICE offer
func (h *DataHandler) NotifyReceiverOfICEOffer(userID int, sessionID, offerSDP string) error {
	userIDStr := fmt.Sprintf("%d", userID)
	
	h.connMutex.RLock()
	conn, exists := h.receiverConns[userIDStr]
	h.connMutex.RUnlock()

	if !exists {
		h.logger.Debug("No active WebSocket connection for user %d", userID)
		return nil
	}

	notification := map[string]interface{}{
		"type":       "ice_offer",
		"session_id": sessionID,
		"offer_sdp":  offerSDP,
		"timestamp":  time.Now().Unix(),
	}

	// Set write deadline to avoid blocking
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	
	if err := conn.WriteJSON(notification); err != nil {
		h.logger.Error("Failed to send ICE offer notification to user %d: %v", userID, err)
		// Remove the connection if it's broken
		h.connMutex.Lock()
		delete(h.receiverConns, userIDStr)
		h.connMutex.Unlock()
		return err
	}
	
	// Clear write deadline
	conn.SetWriteDeadline(time.Time{})

	h.logger.Info("Sent ICE offer notification to user %d for session %s", userID, sessionID)
	return nil
}

// NotifyCollectorOfICEAnswer sends a WebSocket notification to a collector about a new ICE answer
func (h *DataHandler) NotifyCollectorOfICEAnswer(stationID, sessionID, answerSDP string) error {
	// We need to send this to the collector handler since collectors connect there
	if h.collectorHandler != nil {
		return h.collectorHandler.NotifyCollectorOfICEAnswer(stationID, sessionID, answerSDP)
	}
	
	h.logger.Debug("CollectorHandler not available to send ICE answer notification")
	return nil
}

// NotifyCollectorOfICECandidate sends a WebSocket notification to a collector about a new ICE candidate
func (h *DataHandler) NotifyCollectorOfICECandidate(stationID, sessionID string, candidate *models.ICECandidate) error {
	// We need to send this to the collector handler since collectors connect there
	if h.collectorHandler != nil {
		return h.collectorHandler.NotifyCollectorOfICECandidate(stationID, sessionID, candidate)
	}
	
	h.logger.Debug("CollectorHandler not available to send ICE candidate notification")
	return nil
}

// NotifyReceiverOfICECandidate sends a WebSocket notification to a receiver about a new ICE candidate
func (h *DataHandler) NotifyReceiverOfICECandidate(userID int, sessionID string, candidate *models.ICECandidate) error {
	userIDStr := fmt.Sprintf("%d", userID)
	
	h.connMutex.RLock()
	conn, exists := h.receiverConns[userIDStr]
	h.connMutex.RUnlock()

	if !exists {
		h.logger.Debug("No active WebSocket connection for user %d", userID)
		return nil
	}

	notification := map[string]interface{}{
		"type":          "ice_candidate",
		"session_id":    sessionID,
		"candidate":     candidate.Candidate,
		"sdp_mline_index": candidate.SDPMLineIndex,
		"sdp_mid":       candidate.SDPMid,
		"timestamp":     time.Now().Unix(),
	}

	// Set write deadline to avoid blocking
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	
	if err := conn.WriteJSON(notification); err != nil {
		h.logger.Error("Failed to send ICE candidate notification to user %d: %v", userID, err)
		// Remove the connection if it's broken
		h.connMutex.Lock()
		delete(h.receiverConns, userIDStr)
		h.connMutex.Unlock()
		return err
	}
	
	// Clear write deadline
	conn.SetWriteDeadline(time.Time{})

	h.logger.Info("Sent ICE candidate notification to user %d for session %s", userID, sessionID)
	return nil
}

// createICESessionForDataRequest creates an ICE session linked to a data request for direct P2P transfer
func (h *DataHandler) createICESessionForDataRequest(requestID string, userID int, stationID string, dataRequest shared.DataRequest) (string, error) {
	sessionID := uuid.New().String()
	
	// Create ICE session record - Type2 client (receiver) initiating session with Type1 client (collector)
	_, err := h.db.Exec(`
		INSERT INTO ice_sessions (session_id, initiator_user_id, initiator_client_type, target_client_type, status)
		VALUES (?, ?, 2, 1, 'pending')
	`, sessionID, userID)
	
	if err != nil {
		return "", fmt.Errorf("failed to create ICE session: %v", err)
	}
	
	// Create parameters JSON by combining the original parameters with ICE-specific data
	parametersJSON := fmt.Sprintf(`{
		"request_id": "%s",
		"request_type": "%s",
		"station_id": "%s",
		"ice_enabled": true,
		"original_parameters": %s
	}`, requestID, dataRequest.RequestType, stationID, dataRequest.Parameters)
	
	// Create file transfer record linked to the data request
	_, err = h.db.Exec(`
		INSERT INTO file_transfers (session_id, file_name, file_size, file_type, request_type, parameters)
		VALUES (?, ?, 0, 'application/octet-stream', ?, ?)
	`, sessionID, fmt.Sprintf("%s_data.npz", requestID), dataRequest.RequestType, parametersJSON)
	
	if err != nil {
		return "", fmt.Errorf("failed to create file transfer record: %v", err)
	}
	
	// Link the data request to the ICE session for future reference
	_, err = h.db.Exec(`
		UPDATE data_requests 
		SET status = 'ice_session_created'
		WHERE id = ?
	`, requestID)
	
	if err != nil {
		h.logger.Error("Failed to update data request status: %v", err)
		// Don't fail the entire operation for this
	}
	
	// If station ID is provided, notify that specific collector about the ICE session
	if stationID != "" && h.collectorHandler != nil {
		if err := h.collectorHandler.NotifyCollectorOfNewICESession(sessionID, dataRequest.RequestType, userID, parametersJSON); err != nil {
			h.logger.Error("Failed to notify collector %s about new ICE session: %v", stationID, err)
			// Don't fail the entire operation for this
		}
	}
	
	h.logger.Info("Created ICE session %s for data request %s targeting station %s", sessionID, requestID, stationID)
	return sessionID, nil
}