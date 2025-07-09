package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"argus-sdr/internal/shared"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type CollectorHandler struct {
	db             *sql.DB
	logger         *logger.Logger
	cfg            *config.Config
	dataHandler    *DataHandler
	upgrader       websocket.Upgrader
	connections    map[string]*CollectorConnection
	connectionsMux sync.RWMutex
}

type CollectorConnection struct {
	StationID   string
	Conn        *websocket.Conn
	LastSeen    time.Time
}

func NewCollectorHandler(db *sql.DB, log *logger.Logger, cfg *config.Config, dataHandler *DataHandler) *CollectorHandler {
	return &CollectorHandler{
		db:          db,
		logger:      log,
		cfg:         cfg,
		dataHandler: dataHandler,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for now
			},
		},
		connections: make(map[string]*CollectorConnection),
	}
}

// WebSocketHandler handles WebSocket connections from collector clients
func (h *CollectorHandler) WebSocketHandler(c *gin.Context) {
	// Upgrade HTTP connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	// Handle initial authentication/registration
	collectorConn, err := h.handleCollectorAuth(conn)
	if err != nil {
		h.logger.Error("Collector authentication failed: %v", err)
		return
	}

	// Register the connection
	h.connectionsMux.Lock()
	h.connections[collectorConn.StationID] = collectorConn
	h.connectionsMux.Unlock()

	// Register collector session in database
	if err := h.dataHandler.RegisterCollectorSession(collectorConn.StationID); err != nil {
		h.logger.Error("Failed to register collector session: %v", err)
	}

	h.logger.Info("Station connected: %s", collectorConn.StationID)

	// Handle messages
	defer h.cleanupConnection(collectorConn.StationID)
	h.handleMessages(collectorConn)
}

// handleCollectorAuth handles the initial authentication handshake
func (h *CollectorHandler) handleCollectorAuth(conn *websocket.Conn) (*CollectorConnection, error) {
	// Set read deadline for auth
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read initial message
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	if messageType != websocket.TextMessage {
		return nil, gin.Error{
			Err:  nil,
			Type: gin.ErrorTypePublic,
			Meta: "Expected text message for authentication",
		}
	}

	var authMsg shared.WebSocketMessage
	if err := json.Unmarshal(message, &authMsg); err != nil {
		return nil, err
	}

	if authMsg.Type != "collector_auth" {
		return nil, gin.Error{
			Err:  nil,
			Type: gin.ErrorTypePublic,
			Meta: "Expected collector_auth message",
		}
	}

	var registration shared.StationRegistration
	payload, _ := json.Marshal(authMsg.Payload)
	if err := json.Unmarshal(payload, &registration); err != nil {
		return nil, err
	}

	// Validate registration
	if registration.StationID == "" {
		return nil, gin.Error{
			Err:  nil,
			Type: gin.ErrorTypePublic,
			Meta: "StationID is required",
		}
	}

	// Send auth success response
	response := shared.WebSocketMessage{
		Type: "auth_success",
		Payload: map[string]string{
			"status": "authenticated",
		},
	}

	if err := h.sendMessage(conn, response); err != nil {
		return nil, err
	}

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	return &CollectorConnection{
		StationID:   registration.StationID,
		Conn:        conn,
		LastSeen:    time.Now(),
	}, nil
}

// handleMessages processes incoming messages from a collector
func (h *CollectorHandler) handleMessages(collectorConn *CollectorConnection) {
	for {
		messageType, message, err := collectorConn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Error("WebSocket error: %v", err)
			}
			break
		}

		if messageType == websocket.TextMessage {
			collectorConn.LastSeen = time.Now()
			h.processMessage(collectorConn, message)
		}
	}
}

// processMessage handles incoming messages from collectors
func (h *CollectorHandler) processMessage(collectorConn *CollectorConnection, message []byte) {
	var wsMsg shared.WebSocketMessage
	if err := json.Unmarshal(message, &wsMsg); err != nil {
		h.logger.Error("Failed to unmarshal message from collector %s: %v", collectorConn.StationID, err)
		return
	}

	switch wsMsg.Type {
	case "data_response":
		h.handleDataResponse(collectorConn, wsMsg)
	case "heartbeat":
		h.handleHeartbeat(collectorConn, wsMsg)
	case "heartbeat_response":
		h.handleHeartbeatResponse(collectorConn, wsMsg)
	default:
		h.logger.Warn("Unknown message type from collector %s: %s", collectorConn.StationID, wsMsg.Type)
	}
}

// handleDataResponse processes data collection responses from collectors
func (h *CollectorHandler) handleDataResponse(collectorConn *CollectorConnection, wsMsg shared.WebSocketMessage) {
	var response shared.DataResponse
	payload, _ := json.Marshal(wsMsg.Payload)
	if err := json.Unmarshal(payload, &response); err != nil {
		h.logger.Error("Failed to unmarshal data response: %v", err)
		return
	}

	h.logger.Info("Received data response from station %s: request %s, status %s",
		collectorConn.StationID, response.RequestID, response.Status)

	switch response.Status {
	case "ready":
		// Store the individual collector response
		if err := h.dataHandler.StoreCollectorResponse(response.RequestID, response.StationID, response.Status, response.FilePath, response.FileSize, ""); err != nil {
			h.logger.Error("Failed to store collector response: %v", err)
		} else {
			h.logger.Info("Stored collector response from station %s for request %s", response.StationID, response.RequestID)
		}
	case "error":
		// Store the error response
		errorMessage := response.Error
		if errorMessage == "" {
			errorMessage = "Unknown error from collector"
		}
		if err := h.dataHandler.StoreCollectorResponse(response.RequestID, response.StationID, "error", "", 0, errorMessage); err != nil {
			h.logger.Error("Failed to store collector error response: %v", err)
		}
	case "processing":
		// Store processing status update
		if err := h.dataHandler.StoreCollectorResponse(response.RequestID, response.StationID, "processing", "", 0, ""); err != nil {
			h.logger.Error("Failed to store collector processing status: %v", err)
		}
	}
}

// handleFileReady processes file ready notifications
func (h *CollectorHandler) handleFileReady(response shared.DataResponse) {
	// Update database with file ready status
	if err := h.dataHandler.UpdateDataRequestStatus(response.RequestID, "ready", response.FilePath, response.FileSize); err != nil {
		h.logger.Error("Failed to update data request status: %v", err)
		return
	}

	h.logger.Info("File ready for request %s: %s (%d bytes)", response.RequestID, response.FilePath, response.FileSize)

	// TODO: Notify waiting receiver clients
}

// handleCollectorError processes error responses from collectors
func (h *CollectorHandler) handleCollectorError(response shared.DataResponse) {
	// Update database with error status
	if err := h.dataHandler.UpdateDataRequestStatus(response.RequestID, "error", "", 0); err != nil {
		h.logger.Error("Failed to update data request status: %v", err)
		return
	}

	h.logger.Error("Collector error for request %s: %s", response.RequestID, response.Error)
}

// handleProcessingUpdate processes status updates from collectors
func (h *CollectorHandler) handleProcessingUpdate(response shared.DataResponse) {
	h.logger.Info("Processing update for request %s from station %s", response.RequestID, response.StationID)
}

// handleHeartbeat processes heartbeat messages
func (h *CollectorHandler) handleHeartbeat(collectorConn *CollectorConnection, wsMsg shared.WebSocketMessage) {
	// Update last heartbeat in database
	if err := h.dataHandler.UpdateCollectorHeartbeat(collectorConn.StationID); err != nil {
		h.logger.Error("Failed to update collector heartbeat: %v", err)
	}

	// Send heartbeat response
	response := shared.WebSocketMessage{
		Type: "heartbeat_response",
		Payload: shared.HeartbeatMessage{
			StationID: collectorConn.StationID,
			Timestamp: time.Now().Unix(),
			Status:    "active",
		},
	}

	if err := h.sendMessage(collectorConn.Conn, response); err != nil {
		h.logger.Error("Failed to send heartbeat response: %v", err)
	}
}

// handleHeartbeatResponse processes heartbeat responses
func (h *CollectorHandler) handleHeartbeatResponse(collectorConn *CollectorConnection, wsMsg shared.WebSocketMessage) {
	// Update last heartbeat in database
	if err := h.dataHandler.UpdateCollectorHeartbeat(collectorConn.StationID); err != nil {
		h.logger.Error("Failed to update collector heartbeat: %v", err)
	}
}

// SendDataRequest sends a data request to a specific station
func (h *CollectorHandler) SendDataRequest(stationID string, request shared.DataRequest) error {
	h.connectionsMux.RLock()
	conn, exists := h.connections[stationID]
	h.connectionsMux.RUnlock()

	if !exists {
		return gin.Error{
			Err:  nil,
			Type: gin.ErrorTypePublic,
			Meta: "Station not connected",
		}
	}

	message := shared.WebSocketMessage{
		Type:    "data_request",
		Payload: request,
	}

	return h.sendMessage(conn.Conn, message)
}

// sendMessage sends a WebSocket message
func (h *CollectorHandler) sendMessage(conn *websocket.Conn, message shared.WebSocketMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, data)
}

// cleanupConnection cleans up a collector connection
func (h *CollectorHandler) cleanupConnection(stationID string) {
	h.connectionsMux.Lock()
	delete(h.connections, stationID)
	h.connectionsMux.Unlock()

	// Update database status
	query := `
		UPDATE collector_sessions
		SET status = 'disconnected', last_heartbeat = CURRENT_TIMESTAMP
		WHERE station_id = ?
	`
	if _, err := h.db.Exec(query, stationID); err != nil {
		h.logger.Error("Failed to update collector session status: %v", err)
	}

	h.logger.Info("Station disconnected: %s", stationID)
}

// GetConnectedStations returns a list of currently connected stations
func (h *CollectorHandler) GetConnectedStations() []string {
	h.connectionsMux.RLock()
	defer h.connectionsMux.RUnlock()

	stations := make([]string, 0, len(h.connections))
	for stationID := range h.connections {
		stations = append(stations, stationID)
	}

	return stations
}