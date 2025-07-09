package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"argus-sdr/internal/shared"
	"argus-sdr/pkg/logger"

	"github.com/gorilla/websocket"
)

// Client represents a collector client instance
type Client struct {
	ID             string
	StationID      string
	APIServerURL   string
	DataDir        string
	ContainerImage string
	Logger         *logger.Logger

	conn           *websocket.Conn
	authToken      string
	activeRequests map[string]*shared.DataRequest
	mu             sync.RWMutex
	stopCh         chan struct{}
}

// Start initializes and starts the collector client
func (c *Client) Start() error {
	c.activeRequests = make(map[string]*shared.DataRequest)
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
	c.mu.Lock()
	c.activeRequests[request.ID] = &request
	c.mu.Unlock()

	c.Logger.Info("Received data request: %s", request.ID)

	go func() {
		if err := c.processRequest(request); err != nil {
			c.sendError(request.ID, err.Error())
			return
		}
	}()
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

	// Notify API server that file is ready
	response := shared.DataResponse{
		RequestID: request.ID,
		Status:    "ready",
		FilePath:  filePath,
		FileSize:  fileInfo.Size(),
		StationID: c.ID,
	}

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

	return c.sendWebSocketMessage(message)
}

// sendError sends an error response to the API server
func (c *Client) sendError(requestID, errorMsg string) {
	response := shared.DataResponse{
		RequestID: requestID,
		Status:    "error",
		Error:     errorMsg,
		StationID: c.ID,
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
		StationID: c.ID,
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
		StationID: c.ID,
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
