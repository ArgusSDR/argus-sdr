package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"argus-sdr/internal/models"
	"argus-sdr/pkg/config"
	"argus-sdr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
)

type ICEHandler struct {
	db               *sql.DB
	log              *logger.Logger
	cfg              *config.Config
	api              *webrtc.API
	type1Handler     *Type1Handler
	dataHandler      *DataHandler
	collectorHandler *CollectorHandler
}

func NewICEHandler(db *sql.DB, log *logger.Logger, cfg *config.Config, type1Handler *Type1Handler, dataHandler *DataHandler, collectorHandler *CollectorHandler) *ICEHandler {
	// Create a MediaEngine object to configure the supported codec
	m := &webrtc.MediaEngine{}

	// Create a InterceptorRegistry
	i := &interceptor.Registry{}

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))

	return &ICEHandler{
		db:               db,
		log:              log,
		cfg:              cfg,
		api:              api,
		type1Handler:     type1Handler,
		dataHandler:      dataHandler,
		collectorHandler: collectorHandler,
	}
}

// InitiateSession creates a new ICE session for file transfer
// Only Type2 clients can initiate sessions (they request data from Type1 clients)
func (h *ICEHandler) InitiateSession(c *gin.Context) {
	var req models.FileTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	clientType, _ := c.Get("client_type")

	// Only Type2 clients can initiate sessions (they request data from Type1 clients)
	if clientType.(int) != 2 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only Type2 clients can initiate file transfer sessions"})
		return
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Type2 clients always target Type1 clients for data requests
	targetClientType := 1

	// Create session record
	_, err := h.db.Exec(`
		INSERT INTO ice_sessions (session_id, initiator_user_id, initiator_client_type, target_client_type, status)
		VALUES (?, ?, ?, ?, 'pending')
	`, sessionID, userID, clientType, targetClientType)

	if err != nil {
		h.log.Error("Failed to create ICE session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	// Create file transfer record - simplified to just one file type
	_, err = h.db.Exec(`
		INSERT INTO file_transfers (session_id, file_name, file_size, file_type, request_type, parameters)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, "data_file.bin", 0, "application/octet-stream", "data", req.Parameters)

	if err != nil {
		h.log.Error("Failed to create file transfer record: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create file transfer"})
		return
	}

	// Notify Type 1 clients about the new session request
	if err := h.type1Handler.NotifyType1Clients(sessionID, "data", userID.(int)); err != nil {
		h.log.Error("Failed to notify Type 1 clients: %v", err)
		// Don't fail the request if notification fails
	}

	// Also notify collectors via the CollectorHandler
	if err := h.collectorHandler.NotifyCollectorOfNewICESession(sessionID, "data", userID.(int), req.Parameters); err != nil {
		h.log.Error("Failed to notify collectors about new ICE session: %v", err)
		// Don't fail the request if notification fails
	}

	h.log.Info("ICE session initiated: session_id=%s, user_id=%v, request_type=data", sessionID, userID)

	c.JSON(http.StatusCreated, models.FileTransferResponse{
		SessionID: sessionID,
		Success:   true,
		Message:   "Session initiated successfully",
	})
}

// Signal handles ICE signaling messages (offers, answers, candidates)
func (h *ICEHandler) Signal(c *gin.Context) {
	var req models.ICESignalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	clientType, _ := c.Get("client_type")

	// Verify session exists and user has permission
	var sessionExists bool
	var initiatorUserID, targetUserID sql.NullInt64
	var initiatorClientType, targetClientType int

	// For Type 1 clients (collectors), allow them to participate in sessions that target their client type
	var query string
	var args []interface{}

	if clientType.(int) == 1 {
		// Type 1 clients can participate in sessions targeting Type 1 clients
		query = `
			SELECT 1, initiator_user_id, target_user_id, initiator_client_type, target_client_type
			FROM ice_sessions
			WHERE session_id = ? AND target_client_type = 1
		`
		args = []interface{}{req.SessionID}
	} else {
		// Type 2 clients can only participate in sessions they initiated or are targeted for
		query = `
			SELECT 1, initiator_user_id, target_user_id, initiator_client_type, target_client_type
			FROM ice_sessions
			WHERE session_id = ? AND (initiator_user_id = ? OR target_user_id = ?)
		`
		args = []interface{}{req.SessionID, userID, userID}
	}

	err := h.db.QueryRow(query, args...).Scan(&sessionExists, &initiatorUserID, &targetUserID, &initiatorClientType, &targetClientType)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found or access denied"})
		return
	}
	if err != nil {
		h.log.Error("Failed to verify session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// For Type 1 clients responding to a session, set them as the target
	if clientType.(int) == 1 && !targetUserID.Valid {
		_, err := h.db.Exec(`
			UPDATE ice_sessions
			SET target_user_id = ?, updated_at = CURRENT_TIMESTAMP
			WHERE session_id = ?
		`, userID, req.SessionID)
		if err != nil {
			h.log.Error("Failed to set target user for ICE session: %v", err)
			// Don't fail the request, just log the error
		}
	}

	switch req.Type {
	case "offer":
		err = h.handleOffer(req, userID.(int), clientType.(int))
	case "answer":
		err = h.handleAnswer(req, userID.(int), clientType.(int))
	case "candidate":
		err = h.handleICECandidate(req, userID.(int))
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid signal type"})
		return
	}

	if err != nil {
		h.log.Error("Failed to handle signal: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process signal"})
		return
	}

	c.JSON(http.StatusOK, models.ICESignalResponse{
		SessionID: req.SessionID,
		Success:   true,
		Message:   "Signal processed successfully",
	})
}

func (h *ICEHandler) handleOffer(req models.ICESignalRequest, userID, clientType int) error {
	if req.SessionDescription == nil {
		return errors.New("session description required for offer")
	}

	// Store the offer
	_, err := h.db.Exec(`
		UPDATE ice_sessions
		SET status = 'offer_received', offer_sdp = ?, updated_at = CURRENT_TIMESTAMP
		WHERE session_id = ?
	`, req.SessionDescription.SDP, req.SessionID)

	if err != nil {
		return err
	}

	// Send WebSocket notification to receiver about the new offer
	if h.dataHandler != nil {
		if err := h.notifyReceiverOfOffer(req.SessionID, req.SessionDescription.SDP); err != nil {
			h.log.Error("Failed to notify receiver of offer: %v", err)
		}
	}

	h.log.Info("Offer received for session %s from user %d", req.SessionID, userID)

	return nil
}

func (h *ICEHandler) handleAnswer(req models.ICESignalRequest, userID, clientType int) error {
	if req.SessionDescription == nil {
		return errors.New("session description required for answer")
	}

	// Store the answer
	_, err := h.db.Exec(`
		UPDATE ice_sessions
		SET target_user_id = ?, status = 'answer_received', answer_sdp = ?, updated_at = CURRENT_TIMESTAMP
		WHERE session_id = ?
	`, userID, req.SessionDescription.SDP, req.SessionID)

	if err != nil {
		return err
	}

	// Send WebSocket notification to collector about the new answer
	if h.dataHandler != nil {
		if err := h.notifyCollectorOfAnswer(req.SessionID, req.SessionDescription.SDP); err != nil {
			h.log.Error("Failed to notify collector of answer: %v", err)
		}
	}

	h.log.Info("Answer received for session %s from user %d", req.SessionID, userID)

	return nil
}

func (h *ICEHandler) handleICECandidate(req models.ICESignalRequest, userID int) error {
	if req.ICECandidate == nil {
		return errors.New("ICE candidate required")
	}

	// Store the ICE candidate
	_, err := h.db.Exec(`
		INSERT INTO ice_candidates (session_id, user_id, candidate, sdp_mline_index, sdp_mid)
		VALUES (?, ?, ?, ?, ?)
	`, req.SessionID, userID, req.ICECandidate.Candidate, req.ICECandidate.SDPMLineIndex, req.ICECandidate.SDPMid)

	if err != nil {
		return err
	}

	// Send WebSocket notification to the other party about the new ICE candidate
	if h.dataHandler != nil {
		if err := h.notifyPeerOfICECandidate(req.SessionID, userID, req.ICECandidate); err != nil {
			h.log.Error("Failed to notify peer of ICE candidate: %v", err)
		}
	}

	h.log.Info("ICE candidate received for session %s from user %d", req.SessionID, userID)

	return nil
}

// GetSignals retrieves pending signals for a session
func (h *ICEHandler) GetSignals(c *gin.Context) {
	sessionID := c.Param("session_id")
	userID, _ := c.Get("user_id")
	clientType, _ := c.Get("client_type")

	// Verify session access - allow Type 1 clients to access sessions targeting their client type
	var sessionExists bool
	var query string
	var args []interface{}

	if clientType.(int) == 1 {
		// Type 1 clients can access sessions targeting Type 1 clients
		query = `SELECT 1 FROM ice_sessions WHERE session_id = ? AND target_client_type = 1`
		args = []interface{}{sessionID}
	} else {
		// Type 2 clients can only access sessions they initiated or are targeted for
		query = `SELECT 1 FROM ice_sessions WHERE session_id = ? AND (initiator_user_id = ? OR target_user_id = ?)`
		args = []interface{}{sessionID, userID, userID}
	}

	err := h.db.QueryRow(query, args...).Scan(&sessionExists)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found or access denied"})
		return
	}
	if err != nil {
		h.log.Error("Failed to verify session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// Get offer SDP if available and user is Type 2 (receiver)
	var offerSDP sql.NullString
	if clientType.(int) == 2 {
		err = h.db.QueryRow(`
			SELECT offer_sdp FROM ice_sessions WHERE session_id = ?
		`, sessionID).Scan(&offerSDP)
		if err != nil && err != sql.ErrNoRows {
			h.log.Error("Failed to fetch offer SDP: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
	}

	// Get answer SDP if available and user is Type 1 (collector)
	var answerSDP sql.NullString
	if clientType.(int) == 1 {
		err = h.db.QueryRow(`
			SELECT answer_sdp FROM ice_sessions WHERE session_id = ?
		`, sessionID).Scan(&answerSDP)
		if err != nil && err != sql.ErrNoRows {
			h.log.Error("Failed to fetch answer SDP: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
	}

	// Get ICE candidates for this session (excluding the current user's candidates)
	rows, err := h.db.Query(`
		SELECT candidate, sdp_mline_index, sdp_mid, created_at
		FROM ice_candidates
		WHERE session_id = ? AND user_id != ?
		ORDER BY created_at ASC
	`, sessionID, userID)

	if err != nil {
		h.log.Error("Failed to fetch ICE candidates: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	var candidates []models.ICECandidate
	for rows.Next() {
		var candidate models.ICECandidate
		var createdAt time.Time
		err := rows.Scan(&candidate.Candidate, &candidate.SDPMLineIndex, &candidate.SDPMid, &createdAt)
		if err != nil {
			h.log.Error("Failed to scan ICE candidate: %v", err)
			continue
		}
		candidates = append(candidates, candidate)
	}

	response := gin.H{
		"session_id": sessionID,
		"candidates": candidates,
	}
	
	if offerSDP.Valid {
		response["offer_sdp"] = offerSDP.String
	}
	
	if answerSDP.Valid {
		response["answer_sdp"] = answerSDP.String
	}

	c.JSON(http.StatusOK, response)
}

// GetActiveSessions returns sessions that need peer connections
func (h *ICEHandler) GetActiveSessions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	clientType, _ := c.Get("client_type")

	// Get sessions where this user is involved (as initiator or target)
	var query string
	var args []interface{}
	
	if clientType.(int) == 1 {
		// Type 1 clients (collectors) see sessions targeting them
		query = `
			SELECT s.session_id, s.initiator_user_id, s.status, s.created_at,
				   ft.request_type, ft.parameters
			FROM ice_sessions s
			JOIN file_transfers ft ON s.session_id = ft.session_id
			WHERE s.target_client_type = 1 AND s.status IN ('pending', 'offer_received')
			AND (s.target_user_id IS NULL OR s.target_user_id = ?)
			ORDER BY s.created_at DESC
		`
		args = []interface{}{userID}
	} else {
		// Type 2 clients (receivers) see sessions they initiated
		query = `
			SELECT s.session_id, s.initiator_user_id, s.status, s.created_at,
				   ft.request_type, ft.parameters
			FROM ice_sessions s
			JOIN file_transfers ft ON s.session_id = ft.session_id
			WHERE s.initiator_user_id = ? AND s.status IN ('pending', 'offer_received', 'answer_received')
			ORDER BY s.created_at DESC
		`
		args = []interface{}{userID}
	}
	
	rows, err := h.db.Query(query, args...)

	if err != nil {
		h.log.Error("Failed to fetch active sessions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	var sessions []gin.H
	for rows.Next() {
		var sessionID string
		var initiatorUserID int
		var status string
		var createdAt time.Time
		var requestType, parameters string

		err := rows.Scan(&sessionID, &initiatorUserID, &status, &createdAt, &requestType, &parameters)
		if err != nil {
			h.log.Error("Failed to scan session: %v", err)
			continue
		}

		sessions = append(sessions, gin.H{
			"session_id":        sessionID,
			"initiator_user_id": initiatorUserID,
			"status":            status,
			"created_at":        createdAt,
			"request_type":      requestType,
			"parameters":        parameters,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
	})
}

// notifyReceiverOfOffer sends a WebSocket notification to the receiver about a new ICE offer
func (h *ICEHandler) notifyReceiverOfOffer(sessionID, offerSDP string) error {
	// Get the initiator user ID for this session (the receiver who initiated the request)
	var initiatorUserID int
	err := h.db.QueryRow(`
		SELECT initiator_user_id FROM ice_sessions WHERE session_id = ?
	`, sessionID).Scan(&initiatorUserID)
	
	if err != nil {
		return err
	}
	
	// Send WebSocket notification to the receiver
	return h.dataHandler.NotifyReceiverOfICEOffer(initiatorUserID, sessionID, offerSDP)
}

// notifyCollectorOfAnswer sends a WebSocket notification to the collector about a new ICE answer
func (h *ICEHandler) notifyCollectorOfAnswer(sessionID, answerSDP string) error {
	// We need to find which collector should receive this answer
	// The collector is identified by the station_id from the session parameters
	var parameters string
	err := h.db.QueryRow(`
		SELECT ft.parameters FROM ice_sessions s
		JOIN file_transfers ft ON s.session_id = ft.session_id
		WHERE s.session_id = ?
	`, sessionID).Scan(&parameters)
	
	if err != nil {
		return err
	}
	
	// Parse parameters to get station_id
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(parameters), &params); err != nil {
		return err
	}
	
	stationID, ok := params["station_id"].(string)
	if !ok {
		return errors.New("station_id not found in session parameters")
	}
	
	// Send WebSocket notification to the collector
	return h.dataHandler.NotifyCollectorOfICEAnswer(stationID, sessionID, answerSDP)
}

// notifyPeerOfICECandidate sends a WebSocket notification about ICE candidates to the appropriate peer
func (h *ICEHandler) notifyPeerOfICECandidate(sessionID string, senderUserID int, candidate *models.ICECandidate) error {
	// Get session info to determine who should receive the candidate
	var initiatorUserID, targetUserID sql.NullInt64
	var initiatorClientType, targetClientType int
	var parameters string
	
	err := h.db.QueryRow(`
		SELECT s.initiator_user_id, s.target_user_id, s.initiator_client_type, s.target_client_type, ft.parameters
		FROM ice_sessions s
		JOIN file_transfers ft ON s.session_id = ft.session_id
		WHERE s.session_id = ?
	`, sessionID).Scan(&initiatorUserID, &targetUserID, &initiatorClientType, &targetClientType, &parameters)
	
	if err != nil {
		return err
	}
	
	// Determine who should receive this candidate (the other party)
	if initiatorUserID.Valid && initiatorUserID.Int64 == int64(senderUserID) {
		// Sender is initiator (receiver), so notify the target (collector)
		if targetClientType == 1 { // Collector
			// Parse parameters to get station_id
			var params map[string]interface{}
			if err := json.Unmarshal([]byte(parameters), &params); err != nil {
				return err
			}
			
			stationID, ok := params["station_id"].(string)
			if !ok {
				return errors.New("station_id not found in session parameters")
			}
			
			return h.dataHandler.NotifyCollectorOfICECandidate(stationID, sessionID, candidate)
		}
	} else if targetUserID.Valid && targetUserID.Int64 == int64(senderUserID) {
		// Sender is target (collector), so notify the initiator (receiver)
		if initiatorClientType == 2 { // Receiver
			return h.dataHandler.NotifyReceiverOfICECandidate(int(initiatorUserID.Int64), sessionID, candidate)
		}
	}
	
	return nil
}