package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"sdr-api/internal/models"
	"sdr-api/pkg/config"
	"sdr-api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

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

	h.log.Info("Type 1 client connected: client_id=%d, connection_id=%s", clientID, connectionID)

	// Handle WebSocket messages
	defer func() {
		// Clean up connection when done
		h.db.Exec("DELETE FROM active_connections WHERE connection_id = ?", connectionID)
		h.db.Exec(
			"UPDATE type1_clients SET status = 'disconnected', last_seen = CURRENT_TIMESTAMP WHERE id = ?",
			clientID,
		)
		h.log.Info("Type 1 client disconnected: client_id=%d", clientID)
	}()

	// Keep connection alive with ping/pong
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.log.Error("Failed to send ping: %v", err)
				return
			}
		default:
			// Read messages from client
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					h.log.Error("WebSocket error: %v", err)
				}
				return
			}

			h.log.Debug("Received message from Type 1 client: %s", string(message))

			// Echo back for now (implement actual data request handling later)
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				h.log.Error("Failed to write message: %v", err)
				return
			}
		}
	}
}