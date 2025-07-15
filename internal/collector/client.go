package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"argus-sdr/internal/models"
	"argus-sdr/internal/shared"
	"argus-sdr/pkg/logger"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// Client represents a collector client instance
type Client struct {
	ID             string
	StationID      string
	APIServerURL   string
	DataDir        string
	ContainerImage string
	Logger         *logger.Logger

	conn              *websocket.Conn
	authToken         string
	activeRequests    map[string]*shared.DataRequest
	waitingForAnswer  map[string]chan webrtc.SessionDescription
	peerConnections   map[string]*webrtc.PeerConnection
	mu                sync.RWMutex
	stopCh            chan struct{}
}

// Start initializes and starts the collector client
func (c *Client) Start() error {
	c.activeRequests = make(map[string]*shared.DataRequest)
	c.waitingForAnswer = make(map[string]chan webrtc.SessionDescription)
	c.peerConnections = make(map[string]*webrtc.PeerConnection)
	c.stopCh = make(chan struct{})

	// Authenticate with API server
	if err := c.authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Connect WebSocket
	if err := c.connectWebSocket(); err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}

	// Start message handler
	go c.handleMessages()

	// Start heartbeat
	go c.heartbeat()

	c.Logger.Info("Collector client started successfully")

	// Block main goroutine
	select {
	case <-c.stopCh:
		return nil
	}
}


// authenticate performs authentication with the API server
func (c *Client) authenticate() error {
	// For demo purposes, use hardcoded credentials
	// In production, these would come from environment variables or config
	loginData := map[string]interface{}{
		"email":    "collector@example.com",
		"password": "password123",
	}

	jsonData, err := json.Marshal(loginData)
	if err != nil {
		return fmt.Errorf("failed to marshal login data: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", c.APIServerURL+"/api/auth/login", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// User doesn't exist, try to register first
		if err := c.register(httpClient); err != nil {
			return fmt.Errorf("failed to register user: %w", err)
		}
		// Try login again after registration - need to create a new request since the body was consumed
		resp.Body.Close()
		retryReq, err := http.NewRequest("POST", c.APIServerURL+"/api/auth/login", bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create retry login request: %w", err)
		}
		retryReq.Header.Set("Content-Type", "application/json")

		resp, err = httpClient.Do(retryReq)
		if err != nil {
			return fmt.Errorf("failed to send login request after registration: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed with status %d", resp.StatusCode)
	}

	var authResponse struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&authResponse); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	c.authToken = authResponse.Token
	c.Logger.Info("Authentication completed")
	return nil
}

// register creates a new user account for the collector
func (c *Client) register(httpClient *http.Client) error {
	registerData := map[string]interface{}{
		"email":       "collector@example.com",
		"password":    "password123",
		"client_type": 1, // Type 1 for collector clients
	}

	jsonData, err := json.Marshal(registerData)
	if err != nil {
		return fmt.Errorf("failed to marshal register data: %w", err)
	}

	req, err := http.NewRequest("POST", c.APIServerURL+"/api/auth/register", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create register request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send register request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	return nil
}

// stripProtocolAndSlash removes http:// or https:// prefix and trailing slash from URL
func (c *Client) stripProtocolAndSlash(url string) string {
	// Remove protocol prefix
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")

	// Remove trailing slash
	url = strings.TrimSuffix(url, "/")

	return url
}

// connectWebSocket establishes WebSocket connection to the API server
func (c *Client) connectWebSocket() error {
	// Strip protocol and trailing slash from API server URL
	cleanURL := c.stripProtocolAndSlash(c.APIServerURL)
	url := fmt.Sprintf("ws://%s/collector-ws", cleanURL)

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	c.conn = conn

	// Send authentication message
	if err := c.sendAuthMessage(); err != nil {
		conn.Close()
		return fmt.Errorf("authentication failed: %w", err)
	}

	c.Logger.Info("WebSocket connection established and authenticated")
	return nil
}

// sendAuthMessage sends the initial authentication message
func (c *Client) sendAuthMessage() error {
	authMsg := shared.WebSocketMessage{
		Type: "collector_auth",
		Payload: shared.StationRegistration{
			StationID:      c.StationID,
			Capabilities:   "{}",
			ContainerImage: c.ContainerImage,
		},
	}

	data, err := json.Marshal(authMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal auth message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send auth message: %w", err)
	}

	// Wait for auth response
	c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	messageType, message, err := c.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read auth response: %w", err)
	}
	c.conn.SetReadDeadline(time.Time{})

	if messageType != websocket.TextMessage {
		return fmt.Errorf("expected text message for auth response")
	}

	var response shared.WebSocketMessage
	if err := json.Unmarshal(message, &response); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	if response.Type != "auth_success" {
		return fmt.Errorf("authentication failed: unexpected response type %s", response.Type)
	}

	c.Logger.Info("Authentication successful")
	return nil
}

// handleMessages processes incoming WebSocket messages
func (c *Client) handleMessages() {
	defer c.conn.Close()

	for {
		select {
		case <-c.stopCh:
			return
		default:
			messageType, message, err := c.conn.ReadMessage()
			if err != nil {
				c.Logger.Error("Failed to read WebSocket message: %v", err)
				return
			}

			if messageType == websocket.TextMessage {
				c.processMessage(message)
			}
		}
	}
}

// processMessage handles incoming messages from the API server
func (c *Client) processMessage(message []byte) {
	var wsMsg shared.WebSocketMessage
	if err := json.Unmarshal(message, &wsMsg); err != nil {
		c.Logger.Error("Failed to unmarshal message: %v", err)
		return
	}

	switch wsMsg.Type {
	case "data_request":
		var request shared.DataRequest
		payload, _ := json.Marshal(wsMsg.Payload)
		if err := json.Unmarshal(payload, &request); err != nil {
			c.Logger.Error("Failed to unmarshal data request: %v", err)
			return
		}
		c.handleDataRequest(request)

	case "ice_answer":
		c.handleICEAnswer(wsMsg)

	case "ice_candidate":
		c.handleICECandidate(wsMsg)

	case "new_ice_session":
		c.handleNewICESession(wsMsg)

	case "heartbeat":
		c.sendHeartbeatResponse()

	case "heartbeat_response":
		// Handle heartbeat response from server (acknowledgment of our heartbeat)
		var heartbeat shared.HeartbeatMessage
		payload, _ := json.Marshal(wsMsg.Payload)
		if err := json.Unmarshal(payload, &heartbeat); err != nil {
			c.Logger.Error("Failed to unmarshal heartbeat response: %v", err)
			return
		}
		c.Logger.Debug("Received heartbeat response from server")

	default:
		c.Logger.Warn("Unknown message type: %s", wsMsg.Type)
	}
}

// handleDataRequest processes a data collection request
func (c *Client) handleDataRequest(request shared.DataRequest) {
	c.Logger.Debug("handleDataRequest: acquiring lock for activeRequests")
	c.mu.Lock()
	c.activeRequests[request.ID] = &request
	c.mu.Unlock()
	c.Logger.Debug("handleDataRequest: released lock for activeRequests")

	c.Logger.Info("Received data request: %s", request.ID)

	go func() {
		if err := c.processRequest(request); err != nil {
			c.sendError(request.ID, err.Error())
			return
		}
	}()
}

// handleICEAnswer processes ICE answer messages received via WebSocket
func (c *Client) handleICEAnswer(wsMsg shared.WebSocketMessage) {
	// Extract the answer data from the message
	var answerData struct {
		SessionID string `json:"session_id"`
		AnswerSDP string `json:"answer_sdp"`
	}
	
	payload, _ := json.Marshal(wsMsg.Payload)
	if err := json.Unmarshal(payload, &answerData); err != nil {
		c.Logger.Error("Failed to unmarshal ICE answer: %v", err)
		return
	}
	
	c.Logger.Debug("Received WebRTC answer for session %s", answerData.SessionID)
	
	// Find the waiting channel for this session
	c.Logger.Debug("handleICEAnswer: acquiring read lock for waitingForAnswer")
	c.mu.RLock()
	answerChan, exists := c.waitingForAnswer[answerData.SessionID]
	c.mu.RUnlock()
	c.Logger.Debug("handleICEAnswer: released read lock for waitingForAnswer")
	
	if exists {
		answer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  answerData.AnswerSDP,
		}
		select {
		case answerChan <- answer:
			c.Logger.Debug("Sent answer to waiting channel for session %s", answerData.SessionID)
		default:
			c.Logger.Warn("Answer channel full for session %s", answerData.SessionID)
		}
	} else {
		c.Logger.Warn("No waiting channel found for session %s", answerData.SessionID)
	}
}

// handleICECandidate processes ICE candidate messages received via WebSocket
func (c *Client) handleICECandidate(wsMsg shared.WebSocketMessage) {
	var signalData struct {
		SessionID     string  `json:"session_id"`
		Candidate     string  `json:"candidate"`
		SDPMLineIndex float64 `json:"sdpMLineIndex"`
		SDPMid        string  `json:"sdpMid"`
	}

	payloadBytes, err := json.Marshal(wsMsg.Payload)
	if err != nil {
		c.Logger.Error("Failed to marshal ICE candidate payload: %v", err)
		return
	}

	if err := json.Unmarshal(payloadBytes, &signalData); err != nil {
		c.Logger.Error("Failed to unmarshal ICE candidate payload: %v", err)
		return
	}

	c.Logger.Debug("handleICECandidate: acquiring read lock for peerConnections")
	c.mu.RLock()
	pc, exists := c.peerConnections[signalData.SessionID]
	c.mu.RUnlock()
	c.Logger.Debug("handleICECandidate: released read lock for peerConnections")

	if !exists {
		c.Logger.Warn("No peer connection found for session %s to add ICE candidate", signalData.SessionID)
		return
	}

	sdpMLineIndexUint16 := uint16(signalData.SDPMLineIndex)

	candidateInit := webrtc.ICECandidateInit{
		Candidate:     signalData.Candidate,
		SDPMLineIndex: &sdpMLineIndexUint16,
		SDPMid:        &signalData.SDPMid,
	}

	if err := pc.AddICECandidate(candidateInit); err != nil {
		c.Logger.Error("Failed to add ICE candidate for session %s: %v", signalData.SessionID, err)
	} else {
		c.Logger.Debug("Successfully added ICE candidate for session %s", signalData.SessionID)
	}
}

// processRequest executes the data collection process
func (c *Client) processRequest(request shared.DataRequest) error {
	// Run Docker command to generate data
	filePath, err := c.runDataCollection(request)
	if err != nil {
		return fmt.Errorf("data collection failed: %w", err)
	}

	// Get file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Notify API server that file is ready for ICE transfer
	response := shared.DataResponse{
		RequestID: request.ID,
		Status:    "ready",
		FilePath:  filePath, // Local path for ICE transfer
		FileSize:  fileInfo.Size(),
		StationID: c.StationID,
	}

	c.Logger.Info("Timestamp: Sending data_response message at %s", time.Now().Format("2006-01-02 15:04:05.000"))
	return c.sendResponse(response)
}


// runDataCollection executes the Docker command to collect data
func (c *Client) runDataCollection(request shared.DataRequest) (string, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(c.DataDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	// Build Docker command with station ID as argument
	dockerArgs := []string{"run", "-i", "--rm",
		"--device", "/dev/bus/usb",
		"--mount", fmt.Sprintf("type=bind,src=%s,dst=/SDR-TDOA-DF/nice_data", c.DataDir),
		c.ContainerImage,
		"./sync_collect_samples.py", c.StationID}

	cmd := exec.Command("docker", dockerArgs...)

	// Debug: Log the exact command being executed
	c.Logger.Debug("Executing Docker command: docker %s", strings.Join(dockerArgs, " "))
	c.Logger.Debug("Data directory: %s", c.DataDir)
	c.Logger.Debug("Container image: %s", c.ContainerImage)
	c.Logger.Debug("Station ID: %s", c.StationID)

	// Set up output capture
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the command
	c.Logger.Info("Starting data collection for request %s", request.ID)
	if err := cmd.Run(); err != nil {
		// Debug: Log detailed error information
		c.Logger.Error("Docker command failed for request %s", request.ID)
		c.Logger.Error("Exit error: %v", err)
		c.Logger.Error("Stdout: %s", stdout.String())
		c.Logger.Error("Stderr: %s", stderr.String())
		return "", fmt.Errorf("docker command failed: %w, stderr: %s", err, stderr.String())
	}

	// Debug: Log successful execution
	c.Logger.Debug("Docker command completed successfully for request %s", request.ID)
	c.Logger.Debug("Stdout: %s", stdout.String())
	if stderr.Len() > 0 {
		c.Logger.Debug("Stderr: %s", stderr.String())
	}

	// Find the generated file (latest file in data directory)
	filePath, err := c.findLatestFile()
	if err != nil {
		c.Logger.Error("Failed to find generated file in directory %s: %v", c.DataDir, err)
		return "", fmt.Errorf("failed to find generated file: %w", err)
	}

	c.Logger.Info("Data collection completed for request %s, file: %s", request.ID, filePath)
	c.Logger.Info("Timestamp: Data collection completed at %s", time.Now().Format("2006-01-02 15:04:05.000"))
	return filePath, nil
}

// findLatestFile locates the most recently created file in the data directory
func (c *Client) findLatestFile() (string, error) {
	files, err := filepath.Glob(filepath.Join(c.DataDir, "*"))
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no files found in data directory")
	}

	// Find the most recently modified file
	var latestFile string
	var latestTime time.Time

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestFile = file
		}
	}

	return latestFile, nil
}

// sendResponse sends a response to the API server
func (c *Client) sendResponse(response shared.DataResponse) error {
	message := shared.WebSocketMessage{
		Type:    "data_response",
		Payload: response,
	}

	c.Logger.Info("Timestamp: data_response message being sent at %s", time.Now().Format("2006-01-02 15:04:05.000"))
	err := c.sendWebSocketMessage(message)
	if err != nil {
		c.Logger.Error("Failed to send data_response message: %v", err)
	} else {
		c.Logger.Info("Timestamp: data_response message sent successfully at %s", time.Now().Format("2006-01-02 15:04:05.000"))
	}
	return err
}

// sendError sends an error response to the API server
func (c *Client) sendError(requestID, errorMsg string) {
	response := shared.DataResponse{
		RequestID: requestID,
		Status:    "error",
		Error:     errorMsg,
		StationID: c.StationID,
	}

	message := shared.WebSocketMessage{
		Type:    "data_response",
		Payload: response,
	}

	if err := c.sendWebSocketMessage(message); err != nil {
		c.Logger.Error("Failed to send error response: %v", err)
	}
}

// heartbeat sends periodic heartbeat messages
func (c *Client) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.sendHeartbeat()
		}
	}
}

// sendHeartbeat sends a heartbeat message
func (c *Client) sendHeartbeat() {
	heartbeat := shared.HeartbeatMessage{
		StationID: c.StationID,
		Timestamp: time.Now().Unix(),
		Status:    "active",
	}

	message := shared.WebSocketMessage{
		Type:    "heartbeat",
		Payload: heartbeat,
	}

	if err := c.sendWebSocketMessage(message); err != nil {
		c.Logger.Error("Failed to send heartbeat: %v", err)
	}
}

// sendHeartbeatResponse responds to heartbeat requests
func (c *Client) sendHeartbeatResponse() {
	heartbeat := shared.HeartbeatMessage{
		StationID: c.StationID,
		Timestamp: time.Now().Unix(),
		Status:    "active",
	}

	message := shared.WebSocketMessage{
		Type:    "heartbeat_response",
		Payload: heartbeat,
	}

	if err := c.sendWebSocketMessage(message); err != nil {
		c.Logger.Error("Failed to send heartbeat response: %v", err)
	}
}

// sendWebSocketMessage sends a message over the WebSocket connection
func (c *Client) sendWebSocketMessage(message shared.WebSocketMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Stop gracefully shuts down the collector client
func (c *Client) Stop() {
	close(c.stopCh)

	if c.conn != nil {
		c.conn.Close()
	}
}

// handleNewICESession handles a new ICE session notification from the server
func (c *Client) handleNewICESession(wsMsg shared.WebSocketMessage) {
	var sessionData map[string]interface{}
	payloadBytes, err := json.Marshal(wsMsg.Payload)
	if err != nil {
		c.Logger.Error("Failed to marshal new ICE session payload: %v", err)
		return
	}
	if err := json.Unmarshal(payloadBytes, &sessionData); err != nil {
		c.Logger.Error("Failed to unmarshal new ICE session payload: %v", err)
		return
	}

	sessionID, ok := sessionData["session_id"].(string)
	if !ok {
		c.Logger.Error("No session_id found in new_ice_session message")
		return
	}

	// Now that we have the session, we can handle it
	go c.handleICESession(sessionID, sessionData)
}

// handleICESession handles an ICE transfer session
func (c *Client) handleICESession(sessionID string, sessionData map[string]interface{}) {
	c.Logger.Info("Handling ICE session: %s", sessionID)

	// Extract request parameters
	parameters, ok := sessionData["parameters"].(string)
	if !ok {
		c.Logger.Error("No parameters found in ICE session")
		return
	}

	var params map[string]interface{}
	if err := json.Unmarshal([]byte(parameters), &params); err != nil {
		c.Logger.Error("Failed to parse session parameters: %v", err)
		return
	}

	requestID, ok := params["request_id"].(string)
	if !ok {
		c.Logger.Error("No request_id found in session parameters")
		return
	}

	stationID, ok := params["station_id"].(string)
	if !ok {
		c.Logger.Error("No station_id found in session parameters")
		return
	}

	// Check if this station matches our ID
	if stationID != c.StationID {
		// This session is not for us
		return
	}

	// Find the generated file for this request
	filePath, err := c.findFileForRequest(requestID)
	if err != nil {
		c.Logger.Error("Failed to find file for request %s: %v", requestID, err)
		return
	}

	// Start WebRTC transfer
	if err := c.sendFileViaWebRTC(sessionID, filePath); err != nil {
		c.Logger.Error("Failed to send file via WebRTC: %v", err)
		return
	}

	c.Logger.Info("Successfully completed ICE transfer for session %s", sessionID)
}

// findFileForRequest finds the generated file for a specific request
func (c *Client) findFileForRequest(requestID string) (string, error) {
	// Look for files in the data directory that might match this request
	// This is a simplified approach - in a real implementation, you'd want to
	// track the mapping between requests and generated files more precisely
	files, err := filepath.Glob(filepath.Join(c.DataDir, "*"))
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no files found in data directory")
	}

	// For now, return the most recent file
	var latestFile string
	var latestTime time.Time

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestFile = file
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no valid files found")
	}

	return latestFile, nil
}

// sendFileViaWebRTC sends a file using WebRTC data channels
func (c *Client) sendFileViaWebRTC(sessionID, filePath string) error {
	c.Logger.Debug("=== Starting WebRTC file transfer for session %s ===", sessionID)
	c.Logger.Debug("File to send: %s", filePath)
	
	// Create WebRTC configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	c.Logger.Debug("Creating peer connection with STUN server: stun:stun.l.google.com:19302")

	// Create peer connection
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		c.Logger.Error("Failed to create peer connection for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to create peer connection: %w", err)
	}

	c.Logger.Debug("Peer connection created successfully for session %s", sessionID)

	// Store peer connection
	c.Logger.Debug("sendFileViaWebRTC: acquiring lock for peerConnections")
	c.mu.Lock()
	c.peerConnections[sessionID] = peerConnection
	c.mu.Unlock()
	c.Logger.Debug("sendFileViaWebRTC: released lock for peerConnections")

	defer func() {
		c.Logger.Debug("Closing peer connection for session %s", sessionID)
		peerConnection.Close()
		c.Logger.Debug("sendFileViaWebRTC: acquiring lock for peerConnections (defer)")
		c.mu.Lock()
		delete(c.peerConnections, sessionID)
		c.mu.Unlock()
		c.Logger.Debug("sendFileViaWebRTC: released lock for peerConnections (defer)")
		c.Logger.Debug("=== Finished WebRTC file transfer cleanup for session %s ===", sessionID)
	}()

	// Add ICE connection state monitoring
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		c.Logger.Info("ICE connection state changed for session %s: %s", sessionID, connectionState.String())
		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			c.Logger.Info("ICE connection established for session %s", sessionID)
		case webrtc.ICEConnectionStateDisconnected:
			c.Logger.Warn("ICE connection disconnected for session %s", sessionID)
		case webrtc.ICEConnectionStateFailed:
			c.Logger.Error("ICE connection failed for session %s", sessionID)
		case webrtc.ICEConnectionStateClosed:
			c.Logger.Debug("ICE connection closed for session %s", sessionID)
		}
	})

	// Add connection state monitoring
	peerConnection.OnConnectionStateChange(func(connectionState webrtc.PeerConnectionState) {
		c.Logger.Info("Peer connection state changed for session %s: %s", sessionID, connectionState.String())
	})

	// Create data channel for file transfer
	c.Logger.Debug("Creating data channel for session %s", sessionID)
	dataChannel, err := peerConnection.CreateDataChannel("file-transfer", nil)
	if err != nil {
		c.Logger.Error("Failed to create data channel for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to create data channel: %w", err)
	}

	c.Logger.Debug("Data channel created successfully for session %s", sessionID)

	// Set up data channel ready channel IMMEDIATELY after creation
	dataChannelReady := make(chan struct{})
	dataChannel.OnOpen(func() {
		c.Logger.Info("Data channel opened for session %s", sessionID)
		close(dataChannelReady)
	})

	// Add data channel state monitoring
	dataChannel.OnClose(func() {
		c.Logger.Info("Data channel closed for session %s", sessionID)
	})

	dataChannel.OnError(func(err error) {
		c.Logger.Error("Data channel error for session %s: %v", sessionID, err)
	})

	// Set up ICE candidate handling
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			c.Logger.Debug("ICE gathering complete for session %s", sessionID)
			return
		}

		c.Logger.Debug("Generated ICE candidate for session %s: %s", sessionID, candidate.String())

		// Send ICE candidate to signaling server
		if err := c.sendICECandidate(sessionID, candidate); err != nil {
			c.Logger.Error("Failed to send ICE candidate for session %s: %v", sessionID, err)
		} else {
			c.Logger.Debug("Successfully sent ICE candidate for session %s", sessionID)
		}
	})

	// Create offer
	c.Logger.Debug("Creating offer for session %s", sessionID)
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		c.Logger.Error("Failed to create offer for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to create offer: %w", err)
	}

	c.Logger.Debug("Offer created for session %s, SDP length: %d", sessionID, len(offer.SDP))

	// Set local description
	c.Logger.Debug("Setting local description (offer) for session %s", sessionID)
	if err := peerConnection.SetLocalDescription(offer); err != nil {
		c.Logger.Error("Failed to set local description for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to set local description: %w", err)
	}

	c.Logger.Debug("Local description set successfully for session %s", sessionID)

	// Send offer to signaling server
	c.Logger.Debug("Sending offer to signaling server for session %s", sessionID)
	if err := c.sendOffer(sessionID, offer); err != nil {
		c.Logger.Error("Failed to send offer for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to send offer: %w", err)
	}

	c.Logger.Debug("Offer sent successfully for session %s", sessionID)

	// Answer will be received via WebSocket - no polling needed
	// Create a channel to wait for the answer
	answerChannel := make(chan webrtc.SessionDescription, 1)
	c.Logger.Debug("sendFileViaWebRTC: acquiring lock for waitingForAnswer")
	c.mu.Lock()
	c.waitingForAnswer[sessionID] = answerChannel
	c.mu.Unlock()
	c.Logger.Debug("sendFileViaWebRTC: released lock for waitingForAnswer")

	c.Logger.Debug("Waiting for answer from receiver for session %s", sessionID)
	var answer webrtc.SessionDescription
	select {
	case answer = <-answerChannel:
		c.Logger.Debug("Received answer from receiver for session %s, SDP length: %d", sessionID, len(answer.SDP))
	case <-time.After(30 * time.Second):
		c.Logger.Error("Timeout waiting for answer from receiver for session %s", sessionID)
		c.Logger.Debug("sendFileViaWebRTC: acquiring lock for waitingForAnswer (timeout)")
		c.mu.Lock()
		delete(c.waitingForAnswer, sessionID)
		c.mu.Unlock()
		c.Logger.Debug("sendFileViaWebRTC: released lock for waitingForAnswer (timeout)")
		return fmt.Errorf("timeout waiting for answer")
	}
	c.Logger.Debug("sendFileViaWebRTC: acquiring lock for waitingForAnswer (delete)")
	c.mu.Lock()
	delete(c.waitingForAnswer, sessionID)
	c.mu.Unlock()
	c.Logger.Debug("sendFileViaWebRTC: released lock for waitingForAnswer (delete)")

	// Set remote description
	c.Logger.Debug("Setting remote description (answer) for session %s", sessionID)
	if err := peerConnection.SetRemoteDescription(answer); err != nil {
		c.Logger.Error("Failed to set remote description for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	c.Logger.Debug("Remote description set successfully for session %s", sessionID)

	// ICE candidates will be handled via WebSocket - no polling needed

	// Wait for connection with timeout
	c.Logger.Debug("Waiting for data channel to open for session %s", sessionID)
	select {
	case <-dataChannelReady:
		c.Logger.Info("Data channel ready, starting file transfer for session %s", sessionID)
	case <-time.After(30 * time.Second):
		c.Logger.Error("Timeout waiting for data channel to open for session %s", sessionID)
		return fmt.Errorf("timeout waiting for data channel")
	}

	// Send file
	c.Logger.Debug("Starting file data transfer for session %s", sessionID)
	err = c.sendFileData(dataChannel, filePath)
	if err != nil {
		c.Logger.Error("File data transfer failed for session %s: %v", sessionID, err)
	} else {
		c.Logger.Info("File data transfer completed successfully for session %s", sessionID)
	}
	return err
}

// sendICECandidate sends an ICE candidate to the signaling server
func (c *Client) sendICECandidate(sessionID string, candidate *webrtc.ICECandidate) error {
	candidateInit := candidate.ToJSON()

	// Handle potential nil values and convert pointers to values
	var sdpMLineIndex int
	var sdpMid string

	if candidateInit.SDPMLineIndex != nil {
		sdpMLineIndex = int(*candidateInit.SDPMLineIndex)
	}

	if candidateInit.SDPMid != nil {
		sdpMid = *candidateInit.SDPMid
	}

	signal := models.ICESignalRequest{
		SessionID: sessionID,
		Type:      "candidate",
		ICECandidate: &models.ICECandidate{
			Candidate:     candidateInit.Candidate,
			SDPMLineIndex: sdpMLineIndex,
			SDPMid:        sdpMid,
		},
	}

	return c.sendSignal(signal)
}

// sendOffer sends a WebRTC offer to the signaling server
func (c *Client) sendOffer(sessionID string, offer webrtc.SessionDescription) error {
	signal := models.ICESignalRequest{
		SessionID: sessionID,
		Type:      "offer",
		SessionDescription: &models.SessionDescription{
			Type: offer.Type.String(),
			SDP:  offer.SDP,
		},
	}

	return c.sendSignal(signal)
}

// sendSignal sends a signal to the ICE signaling server
func (c *Client) sendSignal(signal models.ICESignalRequest) error {
	c.Logger.Debug("Sending %s signal for session %s", signal.Type, signal.SessionID)
	
	jsonData, err := json.Marshal(signal)
	if err != nil {
		c.Logger.Error("Failed to marshal %s signal for session %s: %v", signal.Type, signal.SessionID, err)
		return fmt.Errorf("failed to marshal signal: %w", err)
	}

	req, err := http.NewRequest("POST", c.APIServerURL+"/api/ice/signal", bytes.NewBuffer(jsonData))
	if err != nil {
		c.Logger.Error("Failed to create HTTP request for %s signal (session %s): %v", signal.Type, signal.SessionID, err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.Logger.Error("Failed to send HTTP request for %s signal (session %s): %v", signal.Type, signal.SessionID, err)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Logger.Error("Server returned status %d for %s signal (session %s)", resp.StatusCode, signal.Type, signal.SessionID)
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	c.Logger.Debug("Successfully sent %s signal for session %s", signal.Type, signal.SessionID)
	return nil
}

// WebSocket-based signaling - no polling needed

// ICE candidates now handled via WebSocket - no polling needed

// All signaling now handled via WebSocket - no HTTP polling needed

// sendFileData sends file data through the WebRTC data channel
func (c *Client) sendFileData(dataChannel *webrtc.DataChannel, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Send file metadata
	metadata := map[string]interface{}{
		"filename": filepath.Base(filePath),
		"size":     fileInfo.Size(),
		"type":     "file-metadata",
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := dataChannel.SendText(string(metadataJSON)); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	c.Logger.Info("Sending file via ICE: %s (%d bytes)", filepath.Base(filePath), fileInfo.Size())

	// Send file in chunks
	buffer := make([]byte, 16384) // 16KB chunks
	totalSent := int64(0)

	chunkNum := 0
	for {
		n, err := file.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read file: %w", err)
		}

		chunkNum++
		c.Logger.Debug("Sending chunk %d: %d bytes", chunkNum, n)

		if err := dataChannel.Send(buffer[:n]); err != nil {
			c.Logger.Error("Failed to send chunk %d: %v", chunkNum, err)
			return fmt.Errorf("failed to send chunk: %w", err)
		}

		totalSent += int64(n)
		c.Logger.Debug("Sent chunk %d successfully, total: %d/%d bytes", chunkNum, totalSent, fileInfo.Size())

		// Add flow control - wait for buffer to drain
		for dataChannel.BufferedAmount() > 65536 { // Wait if buffer > 64KB
			time.Sleep(10 * time.Millisecond)
		}

		if totalSent%1048576 == 0 { // Log every MB
			progress := float64(totalSent) / float64(fileInfo.Size()) * 100
			c.Logger.Info("ICE transfer progress: %.2f%% (%d/%d bytes)",
				progress, totalSent, fileInfo.Size())
		}
	}

	// Wait for final buffer to drain completely
	for dataChannel.BufferedAmount() > 0 {
		time.Sleep(10 * time.Millisecond)
	}

	// Give receiver time to process final chunk
	time.Sleep(100 * time.Millisecond)

	c.Logger.Info("ICE file transfer completed: %d bytes sent", totalSent)
	return nil
}
