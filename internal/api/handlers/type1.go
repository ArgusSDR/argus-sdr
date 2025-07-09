package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"argus-sdr/internal/models"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// WebSocketConnection represents an active WebSocket connection
type WebSocketConnection struct {
	ClientID     int
	ConnectionID string
	UserID       int
	Conn         *websocket.Conn
	Send         chan []byte
}

// ConnectionManager manages active WebSocket connections
type ConnectionManager struct {
	connections map[string]*WebSocketConnection
	mutex       sync.RWMutex
}

// Global connection manager instance
var connManager = &ConnectionManager{
	connections: make(map[string]*WebSocketConnection),
}

type Type1Handler struct {
	db       *sql.DB
	log      *logger.Logger
	cfg      *config.Config
	upgrader websocket.Upgrader
}

func NewType1Handler(db *sql.DB, log *logger.Logger, cfg *config.Config) *Type1Handler {
	return &Type1Handler{
		db:  db,
		log: log,
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for now
			},
		},
	}
}

// AddConnection adds a new WebSocket connection to the manager
func (cm *ConnectionManager) AddConnection(connID string, conn *WebSocketConnection) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.connections[connID] = conn
}

// RemoveConnection removes a WebSocket connection from the manager
func (cm *ConnectionManager) RemoveConnection(connID string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	if conn, exists := cm.connections[connID]; exists {
		close(conn.Send)
		delete(cm.connections, connID)
	}
}

// BroadcastToType1Clients sends a message to all connected Type 1 clients
func (cm *ConnectionManager) BroadcastToType1Clients(message []byte) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	for _, conn := range cm.connections {
		select {
		case conn.Send <- message:
		default:
			// Client's send channel is full, skip
		}
	}
}

// SendToClient sends a message to a specific client by connection ID
func (cm *ConnectionManager) SendToClient(connID string, message []byte) bool {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if conn, exists := cm.connections[connID]; exists {
		select {
		case conn.Send <- message:
			return true
		default:
			return false
		}
	}
	return false
}

// GetConnectedClients returns a list of all connected Type 1 client IDs
func (cm *ConnectionManager) GetConnectedClients() []int {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	var clientIDs []int
	for _, conn := range cm.connections {
		clientIDs = append(clientIDs, conn.ClientID)
	}
	return clientIDs
}

// NotifyType1Clients sends an ICE session notification to all Type 1 clients
func (h *Type1Handler) NotifyType1Clients(sessionID, requestType string, userID int) error {
	notification := map[string]interface{}{
		"type":         "ice_session_request",
		"session_id":   sessionID,
		"request_type": requestType,
		"from_user":    userID,
		"timestamp":    time.Now().UTC(),
	}

	messageBytes, err := json.Marshal(notification)
	if err != nil {
		return err
	}

	connManager.BroadcastToType1Clients(messageBytes)
	h.log.Info("Notified Type 1 clients about ICE session: %s", sessionID)
	return nil
}

func (h *Type1Handler) Register(c *gin.Context) {
	var req models.Type1RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")

	// Check if client already registered
	var existingID int
	err := h.db.QueryRow("SELECT id FROM type1_clients WHERE user_id = ?", userID).Scan(&existingID)
	if err != sql.ErrNoRows {
		c.JSON(http.StatusConflict, gin.H{"error": "Client already registered"})
		return
	}

	// Register the client
	result, err := h.db.Exec(
		"INSERT INTO type1_clients (user_id, client_name, capabilities, status) VALUES (?, ?, ?, 'registered')",
		userID, req.ClientName, req.Capabilities,
	)
	if err != nil {
		h.log.Error("Failed to register Type 1 client: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register client"})
		return
	}

	clientID, _ := result.LastInsertId()

	client := models.Type1Client{
		ID:           int(clientID),
		UserID:       userID.(int),
		ClientName:   req.ClientName,
		Status:       "registered",
		Capabilities: req.Capabilities,
	}

	c.JSON(http.StatusCreated, client)
}

func (h *Type1Handler) GetStatus(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var client models.Type1Client
	err := h.db.QueryRow(
		"SELECT id, user_id, client_name, status, last_seen, capabilities FROM type1_clients WHERE user_id = ?",
		userID,
	).Scan(&client.ID, &client.UserID, &client.ClientName, &client.Status, &client.LastSeen, &client.Capabilities)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not registered"})
		return
	}
	if err != nil {
		h.log.Error("Failed to get client status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get status"})
		return
	}

	c.JSON(http.StatusOK, client)
}

func (h *Type1Handler) Update(c *gin.Context) {
	var req models.Type1RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")

	_, err := h.db.Exec(
		"UPDATE type1_clients SET client_name = ?, capabilities = ? WHERE user_id = ?",
		req.ClientName, req.Capabilities, userID,
	)
	if err != nil {
		h.log.Error("Failed to update Type 1 client: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update client"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Client updated successfully"})
}

func (h *Type1Handler) WebSocketHandler(c *gin.Context) {
	userID, _ := c.Get("user_id")

	// Get client info
	var clientID int
	err := h.db.QueryRow("SELECT id FROM type1_clients WHERE user_id = ?", userID).Scan(&clientID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not registered"})
		return
	}
	if err != nil {
		h.log.Error("Failed to get client info: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// Upgrade connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("Failed to upgrade to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	// Generate connection ID and store in database
	connectionID := uuid.New().String()
	_, err = h.db.Exec(
		"INSERT INTO active_connections (client_id, connection_id) VALUES (?, ?)",
		clientID, connectionID,
	)
	if err != nil {
		h.log.Error("Failed to store connection: %v", err)
		return
	}

	// Update client status to connected
	_, err = h.db.Exec(
		"UPDATE type1_clients SET status = 'connected', last_seen = CURRENT_TIMESTAMP WHERE id = ?",
		clientID,
	)
	if err != nil {
		h.log.Error("Failed to update client status: %v", err)
	}

	// Create WebSocket connection object
	wsConn := &WebSocketConnection{
		ClientID:     clientID,
		ConnectionID: connectionID,
		UserID:       userID.(int),
		Conn:         conn,
		Send:         make(chan []byte, 256),
	}

	// Add to connection manager
	connManager.AddConnection(connectionID, wsConn)

	h.log.Info("Type 1 client connected: client_id=%d, connection_id=%s", clientID, connectionID)

	// Handle WebSocket messages with separate read/write goroutines
	defer func() {
		// Clean up connection when done
		connManager.RemoveConnection(connectionID)
		h.db.Exec("DELETE FROM active_connections WHERE connection_id = ?", connectionID)
		h.db.Exec(
			"UPDATE type1_clients SET status = 'disconnected', last_seen = CURRENT_TIMESTAMP WHERE id = ?",
			clientID,
		)
		h.log.Info("Type 1 client disconnected: client_id=%d", clientID)
	}()

	// Start write pump goroutine
	go h.writePump(wsConn)

	// Start read pump in current goroutine
	h.readPump(wsConn)
}

// readPump handles reading messages from the WebSocket connection
func (h *Type1Handler) readPump(wsConn *WebSocketConnection) {
	defer wsConn.Conn.Close()

	// Set read deadline and pong handler
	wsConn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	wsConn.Conn.SetPongHandler(func(string) error {
		wsConn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		// Read message from client
		_, message, err := wsConn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.log.Error("WebSocket error: %v", err)
			}
			break
		}

		h.log.Debug("Received message from Type 1 client %d: %s", wsConn.ClientID, string(message))

		// Handle incoming message (you can add message processing logic here)
		h.handleClientMessage(wsConn, message)
	}
}

// writePump handles writing messages to the WebSocket connection
func (h *Type1Handler) writePump(wsConn *WebSocketConnection) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		wsConn.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-wsConn.Send:
			wsConn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// The connection manager closed the channel
				wsConn.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := wsConn.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				h.log.Error("Failed to write message: %v", err)
				return
			}

		case <-ticker.C:
			wsConn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := wsConn.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.log.Error("Failed to send ping: %v", err)
				return
			}
		}
	}
}

// handleClientMessage processes incoming messages from Type 1 clients
func (h *Type1Handler) handleClientMessage(wsConn *WebSocketConnection, message []byte) {
	// Parse message to determine type and handle accordingly
	var msg map[string]interface{}
	if err := json.Unmarshal(message, &msg); err != nil {
		h.log.Error("Failed to parse message from client %d: %v", wsConn.ClientID, err)
		return
	}

	msgType, ok := msg["type"].(string)
	if !ok {
		h.log.Error("Message from client %d missing type field", wsConn.ClientID)
		return
	}

	switch msgType {
	case "ice_response":
		// Handle ICE session response from Type 1 client
		h.handleICEResponse(wsConn, msg)
	case "heartbeat":
		// Send heartbeat response
		response := map[string]interface{}{
			"type":      "heartbeat_ack",
			"timestamp": time.Now().UTC(),
		}
		responseBytes, _ := json.Marshal(response)
		select {
		case wsConn.Send <- responseBytes:
		default:
		}
	default:
		h.log.Debug("Unknown message type from client %d: %s", wsConn.ClientID, msgType)
	}
}

// handleICEResponse processes ICE session responses from Type 1 clients
func (h *Type1Handler) handleICEResponse(wsConn *WebSocketConnection, msg map[string]interface{}) {
	sessionID, ok := msg["session_id"].(string)
	if !ok {
		h.log.Error("ICE response missing session_id")
		return
	}

	accepted, ok := msg["accepted"].(bool)
	if !ok {
		h.log.Error("ICE response missing accepted field")
		return
	}

	if accepted {
		// Update session with the responding client
		_, err := h.db.Exec(`
			UPDATE ice_sessions
			SET target_user_id = ?, status = 'accepted', updated_at = CURRENT_TIMESTAMP
			WHERE session_id = ?
		`, wsConn.UserID, sessionID)

		if err != nil {
			h.log.Error("Failed to update ICE session: %v", err)
			return
		}

		h.log.Info("Type 1 client %d accepted ICE session %s", wsConn.ClientID, sessionID)
	} else {
		h.log.Info("Type 1 client %d declined ICE session %s", wsConn.ClientID, sessionID)
	}
}