package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"argus-sdr/internal/models"
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
	clientIP := c.ClientIP()
	h.logger.Info("WebSocket connection attempt from collector at %s", clientIP)
	
	// Upgrade HTTP connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade WebSocket connection from %s: %v", clientIP, err)
		return
	}
	defer conn.Close()

	h.logger.Debug("WebSocket connection upgraded successfully for %s", clientIP)

	// Handle initial authentication/registration
	collectorConn, err := h.handleCollectorAuth(conn)
	if err != nil {
		h.logger.Error("Collector authentication failed from %s: %v", clientIP, err)
		return
	}

	// Register the connection
	h.connectionsMux.Lock()
	h.connections[collectorConn.StationID] = collectorConn
	activeConnections := len(h.connections)
	h.connectionsMux.Unlock()

	// Register collector session in database
	if err := h.dataHandler.RegisterCollectorSession(collectorConn.StationID); err != nil {
		h.logger.Error("Failed to register collector session for %s: %v", collectorConn.StationID, err)
	}

	h.logger.Info("Collector connected: station=%s ip=%s total_active=%d", 
		collectorConn.StationID, clientIP, activeConnections)

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
	h.logger.Debug("Starting message handling for collector %s", collectorConn.StationID)
	
	for {
		messageType, message, err := collectorConn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Error("WebSocket unexpected close from collector %s: %v", collectorConn.StationID, err)
			} else {
				h.logger.Debug("Collector %s connection closed: %v", collectorConn.StationID, err)
			}
			break
		}

		if messageType == websocket.TextMessage {
			collectorConn.LastSeen = time.Now()
			h.logger.Debug("Received message from collector %s: %s", collectorConn.StationID, string(message))
			h.processMessage(collectorConn, message)
		} else {
			h.logger.Warn("Received non-text message from collector %s (type: %d)", collectorConn.StationID, messageType)
		}
	}
	
	h.logger.Debug("Message handling ended for collector %s", collectorConn.StationID)
}

// processMessage handles incoming messages from collectors
func (h *CollectorHandler) processMessage(collectorConn *CollectorConnection, message []byte) {
	var wsMsg shared.WebSocketMessage
	if err := json.Unmarshal(message, &wsMsg); err != nil {
		h.logger.Error("Failed to unmarshal message from collector %s: %v", collectorConn.StationID, err)
		h.logger.Debug("Invalid message content: %s", string(message))
		return
	}

	h.logger.Debug("Processing message type '%s' from collector %s", wsMsg.Type, collectorConn.StationID)

	switch wsMsg.Type {
	case "data_response":
		h.logger.Debug("Handling data response from collector %s", collectorConn.StationID)
		h.handleDataResponse(collectorConn, wsMsg)
	case "heartbeat":
		h.logger.Debug("Handling heartbeat from collector %s", collectorConn.StationID)
		h.handleHeartbeat(collectorConn, wsMsg)
	case "heartbeat_response":
		h.logger.Debug("Handling heartbeat response from collector %s", collectorConn.StationID)
		h.handleHeartbeatResponse(collectorConn, wsMsg)
	default:
		h.logger.Warn("Unknown message type '%s' from collector %s", wsMsg.Type, collectorConn.StationID)
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
	h.logger.Info("Timestamp: Received data_response from station %s at %s", collectorConn.StationID, time.Now().Format("2006-01-02 15:04:05.000"))

	switch response.Status {
	case "ready":
		// Store the individual collector response
		h.logger.Info("Timestamp: Storing collector response at %s", time.Now().Format("2006-01-02 15:04:05.000"))
		if err := h.dataHandler.StoreCollectorResponse(response.RequestID,
			collectorConn.StationID, response.Status, response.FilePath, response.FileSize, ""); err != nil {
			h.logger.Error("Failed to store collector response: %v", err)
		} else {
			h.logger.Info("Timestamp: Collector response stored successfully at %s", time.Now().Format("2006-01-02 15:04:05.000"))
		}

		// Also store the download URL if provided
		if response.DownloadURL != "" {
			// Update the collector response with the download URL
			if err := h.dataHandler.UpdateCollectorResponseURL(response.RequestID,
				collectorConn.StationID, response.DownloadURL); err != nil {
				h.logger.Error("Failed to update collector response URL: %v", err)
			}
		}

		h.logger.Info("Stored ready response from station %s for request %s (file: %s, download: %s)",
			collectorConn.StationID, response.RequestID, response.FilePath, response.DownloadURL)

	case "error":
		// Store error response
		if err := h.dataHandler.StoreCollectorResponse(response.RequestID,
			collectorConn.StationID, response.Status, "", 0, response.Error); err != nil {
			h.logger.Error("Failed to store error response: %v", err)
		}

		h.logger.Error("Collector %s reported error for request %s: %s",
			collectorConn.StationID, response.RequestID, response.Error)

	default:
		h.logger.Warn("Unknown response status from station %s: %s",
			collectorConn.StationID, response.Status)
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
	remainingConnections := len(h.connections)
	h.connectionsMux.Unlock()

	h.logger.Info("Collector disconnected: station=%s remaining_active=%d", 
		stationID, remainingConnections)

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

// NotifyCollectorOfICEAnswer sends a WebSocket notification to a collector about a new ICE answer
func (h *CollectorHandler) NotifyCollectorOfICEAnswer(stationID, sessionID, answerSDP string) error {
	h.connectionsMux.RLock()
	conn, exists := h.connections[stationID]
	h.connectionsMux.RUnlock()

	if !exists {
		h.logger.Debug("No active collector connection for station %s", stationID)
		return nil
	}

	notification := shared.WebSocketMessage{
		Type: "ice_answer",
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"answer_sdp": answerSDP,
			"timestamp":  time.Now().Unix(),
		},
	}

	if err := h.sendMessage(conn.Conn, notification); err != nil {
		h.logger.Error("Failed to send ICE answer notification to station %s: %v", stationID, err)
		return err
	}

	h.logger.Info("Sent ICE answer notification to station %s for session %s", stationID, sessionID)
	return nil
}

// NotifyCollectorOfICECandidate sends a WebSocket notification to a collector about a new ICE candidate
func (h *CollectorHandler) NotifyCollectorOfICECandidate(stationID, sessionID string, candidate *models.ICECandidate) error {
	h.connectionsMux.RLock()
	conn, exists := h.connections[stationID]
	h.connectionsMux.RUnlock()

	if !exists {
		h.logger.Debug("No active collector connection for station %s", stationID)
		return nil
	}

	notification := shared.WebSocketMessage{
		Type: "ice_candidate",
		Payload: map[string]interface{}{
			"session_id":      sessionID,
			"candidate":       candidate.Candidate,
			"sdp_mline_index": candidate.SDPMLineIndex,
			"sdp_mid":         candidate.SDPMid,
			"timestamp":       time.Now().Unix(),
		},
	}

	if err := h.sendMessage(conn.Conn, notification); err != nil {
		h.logger.Error("Failed to send ICE candidate notification to station %s: %v", stationID, err)
		return err
	}

	h.logger.Info("Sent ICE candidate notification to station %s for session %s", stationID, sessionID)
	return nil
}

// NotifyCollectorOfNewICESession sends a WebSocket notification to collectors about a new ICE session
func (h *CollectorHandler) NotifyCollectorOfNewICESession(sessionID, requestType string, userID int, parameters string) error {
	h.connectionsMux.RLock()
	connections := make([]*CollectorConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.connectionsMux.RUnlock()

	if len(connections) == 0 {
		h.logger.Debug("No active collector connections to notify about ICE session %s", sessionID)
		return nil
	}

	notification := shared.WebSocketMessage{
		Type: "new_ice_session",
		Payload: map[string]interface{}{
			"session_id":   sessionID,
			"request_type": requestType,
			"from_user":    userID,
			"parameters":   parameters,
			"timestamp":    time.Now().Unix(),
		},
	}

	successCount := 0
	for _, conn := range connections {
		if err := h.sendMessage(conn.Conn, notification); err != nil {
			h.logger.Error("Failed to send new ICE session notification to station %s: %v", conn.StationID, err)
		} else {
			h.logger.Debug("Sent new ICE session notification to station %s for session %s", conn.StationID, sessionID)
			successCount++
		}
	}

	h.logger.Info("Notified %d collectors about new ICE session: %s", successCount, sessionID)
	return nil
}