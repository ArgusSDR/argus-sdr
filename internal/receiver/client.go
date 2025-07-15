package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"argus-sdr/internal/models"
	"argus-sdr/internal/shared"
	"argus-sdr/pkg/logger"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// Client represents a receiver client instance
type Client struct {
	ID           string
	APIServerURL string
	DownloadDir  string
	Logger       *logger.Logger

	httpClient      *http.Client
	authToken       string
	wsConn          *websocket.Conn
	waitingForOffer map[string]chan webrtc.SessionDescription
	peerConnections map[string]*webrtc.PeerConnection
	mu              sync.RWMutex
}

// RequestAndDownload sends a data request and waits for completion, then downloads the file
func (c *Client) RequestAndDownload() error {
	// Initialize HTTP client
	c.httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}

	// Authenticate with API server
	if err := c.authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	c.Logger.Info("Authenticated with API server")

	// Initialize maps
	c.waitingForOffer = make(map[string]chan webrtc.SessionDescription)
	c.peerConnections = make(map[string]*webrtc.PeerConnection)

	// Connect to WebSocket for notifications - REQUIRED
	if err := c.connectWebSocket(); err != nil {
		return fmt.Errorf("WebSocket connection failed: %w", err)
	}

	c.Logger.Info("Connected to WebSocket for notifications")

	// Create and send data request
	request := shared.DataRequest{
		ID:          uuid.New().String(),
		RequestType: "data_collection", // Single request type
		Parameters:  "{}",
		RequestedBy: c.ID,
		Timestamp:   time.Now().Unix(),
	}

	c.Logger.Info("Sending data request with ID: %s", request.ID)

	// Send request to API
	if err := c.sendDataRequest(request); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	c.Logger.Info("Request submitted, waiting for data to be ready...")

	// Wait for data to be ready
	if err := c.waitForData(request.ID); err != nil {
		return fmt.Errorf("failed waiting for data: %w", err)
	}

	return nil
}

// authenticate performs authentication with the API server
func (c *Client) authenticate() error {
	// For demo purposes, use hardcoded credentials
	// In production, these would come from environment variables or config
	loginData := map[string]interface{}{
		"email":    "receiver@example.com",
		"password": "password123",
	}

	jsonData, err := json.Marshal(loginData)
	if err != nil {
		return fmt.Errorf("failed to marshal login data: %w", err)
	}

	req, err := http.NewRequest("POST", c.APIServerURL+"/api/auth/login", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// User doesn't exist, try to register first
		if err := c.register(); err != nil {
			return fmt.Errorf("failed to register user: %w", err)
		}
		// Try login again after registration - need to create a new request since the body was consumed
		resp.Body.Close()
		retryReq, err := http.NewRequest("POST", c.APIServerURL+"/api/auth/login", bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create retry login request: %w", err)
		}
		retryReq.Header.Set("Content-Type", "application/json")

		resp, err = c.httpClient.Do(retryReq)
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
	return nil
}

// register creates a new user account for the receiver
func (c *Client) register() error {
	registerData := map[string]interface{}{
		"email":       "receiver@example.com",
		"password":    "password123",
		"client_type": 2, // Type 2 for receiver clients
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send register request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	return nil
}

// connectWebSocket establishes a WebSocket connection for notifications
func (c *Client) connectWebSocket() error {
	// Parse API server URL to get host and build WebSocket URL
	apiURL, err := url.Parse(c.APIServerURL)
	if err != nil {
		return fmt.Errorf("failed to parse API server URL: %w", err)
	}

	// Build WebSocket URL
	wsScheme := "ws"
	if apiURL.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/receiver-ws", wsScheme, apiURL.Host)

	c.Logger.Debug("Connecting to WebSocket URL: %s", wsURL)

	// Set up headers with authentication
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.authToken)

	// Connect to WebSocket
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			c.Logger.Error("WebSocket connection failed with status: %d %s", resp.StatusCode, resp.Status)
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Set up ping/pong handler to respond to server pings
	conn.SetPongHandler(func(appData string) error {
		c.Logger.Debug("Received pong from server")
		return nil
	})

	conn.SetPingHandler(func(appData string) error {
		c.Logger.Debug("Received ping from server, sending pong")
		return conn.WriteMessage(websocket.PongMessage, []byte(appData))
	})

	c.wsConn = conn
	return nil
}

// sendDataRequest sends a data request to the API server
func (c *Client) sendDataRequest(request shared.DataRequest) error {
	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.APIServerURL+"/api/data/request", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}

// waitForData waits for WebSocket notifications when data is ready, then downloads it
func (c *Client) waitForData(requestID string) error {
	// WebSocket connection is required
	if c.wsConn == nil {
		return fmt.Errorf("WebSocket connection is required but not available")
	}

	timeout := time.After(10 * time.Minute) // Increased timeout for docker processing
	downloadedFromStations := make(map[string]bool) // Track which stations we've downloaded from
	firstDownloadTime := time.Time{}

	c.Logger.Info("Waiting for collectors to complete...")

	defer func() {
		if c.wsConn != nil {
			c.wsConn.Close()
		}
	}()

	// Channel to receive WebSocket notifications
	notifications := make(chan map[string]interface{}, 10)
	wsErrors := make(chan error, 1)

	// Start a goroutine to read WebSocket messages
	go func() {
		defer func() {
			close(notifications)
			if r := recover(); r != nil {
				c.Logger.Error("Recovered from panic in WebSocket reader: %v", r)
			}
		}()
		
		for {
			var notification map[string]interface{}
			
			// Don't set aggressive timeouts that could cause premature disconnection
			c.wsConn.SetReadDeadline(time.Time{}) // No deadline
			
			err := c.wsConn.ReadJSON(&notification)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					c.Logger.Debug("WebSocket connection closed normally: %v", err)
				} else {
					c.Logger.Error("WebSocket read error: %v", err)
				}
				wsErrors <- err
				return
			}
			
			c.Logger.Debug("Received WebSocket message: %+v", notification)
			
			// Check if this is an ICE signaling message
			if msgType, ok := notification["type"].(string); ok {
				switch msgType {
				case "ice_offer":
					c.handleICEOffer(notification)
				case "ice_candidate":
					c.handleICECandidate(notification)
				case "data_ready":
					// This is a data ready notification, not an ICE message
					// Fall through to the general notification channel
				default:
					// Unknown message type, could be other notifications
				}
			}
			
			select {
			case notifications <- notification:
			case <-time.After(5 * time.Second):
				// Channel is full, skip this notification
				c.Logger.Warn("Notification channel full, skipping message")
			}
		}
	}()

	for {
		select {
		case <-timeout:
			if len(downloadedFromStations) > 0 {
				c.Logger.Info("Timeout reached but successfully downloaded from %d collectors: %v",
					len(downloadedFromStations), getStationList(downloadedFromStations))
				return nil
			}
			return fmt.Errorf("timeout waiting for data (10 minutes)")
			
		case err := <-wsErrors:
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				return fmt.Errorf("WebSocket connection closed: %w", err)
			} else {
				return fmt.Errorf("WebSocket connection error: %w", err)
			}

		case notification := <-notifications:
			// Check if this notification is for our request
			if notification["type"] == "data_ready" && notification["request_id"] == requestID {
				stationID := notification["station_id"].(string)
				
				if !downloadedFromStations[stationID] {
					c.Logger.Info("Timestamp: Received WebSocket notification for station %s at %s", stationID, time.Now().Format("2006-01-02 15:04:05.000"))
					c.Logger.Info("New data available from station %s! Starting download...", stationID)

					// Get the download information
					downloads, err := c.checkAvailableDownloads(requestID)
					if err != nil {
						c.Logger.Error("Error checking available downloads: %v", err)
						continue
					}

					// Find the specific download for this station
					for _, download := range downloads {
						if download.StationID == stationID {
							// Create a DataRequestStatus object for compatibility with existing download function
							status := &shared.DataRequestStatus{
								RequestID: download.RequestID,
								Status:    download.Status,
								FilePath:  download.FilePath,
								FileSize:  download.FileSize,
								StationID: download.StationID,
							}

							if err := c.downloadFile(requestID, status); err != nil {
								c.Logger.Error("Failed to download from station %s: %v", stationID, err)
							} else {
								downloadedFromStations[stationID] = true
								c.Logger.Info("Successfully downloaded from station %s (%d total downloads)",
									stationID, len(downloadedFromStations))

								// Record the time of first download
								if firstDownloadTime.IsZero() {
									firstDownloadTime = time.Now()
								}
							}
							break
						}
					}
				}
			}

		case <-time.After(5 * time.Second):
			// Periodic check - continue waiting for additional collectors after first download
			if !firstDownloadTime.IsZero() {
				// If we've been waiting for additional collectors for more than 2 minutes after first download, stop
				if time.Since(firstDownloadTime) > 2*time.Minute {
					c.Logger.Info("Completed downloads from %d collectors: %v",
						len(downloadedFromStations), getStationList(downloadedFromStations))
					return nil
				}
			}
		}
	}
}

// waitForDataPolling is a fallback function that polls for data availability
func (c *Client) waitForDataPolling(requestID string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute)
	downloadedFromStations := make(map[string]bool)
	firstDownloadTime := time.Time{}

	c.Logger.Info("Polling for data availability...")

	for {
		select {
		case <-timeout:
			if len(downloadedFromStations) > 0 {
				c.Logger.Info("Timeout reached but successfully downloaded from %d collectors: %v",
					len(downloadedFromStations), getStationList(downloadedFromStations))
				return nil
			}
			return fmt.Errorf("timeout waiting for data (10 minutes)")
		case <-ticker.C:
			// Check for available downloads
			downloads, err := c.checkAvailableDownloads(requestID)
			if err != nil {
				c.Logger.Error("Error checking available downloads: %v", err)
				continue
			}

			// Download from any new stations that have completed
			newDownloads := 0
			for _, download := range downloads {
				if !downloadedFromStations[download.StationID] {
					c.Logger.Info("Timestamp: Detected new data from station %s via polling at %s", download.StationID, time.Now().Format("2006-01-02 15:04:05.000"))
					c.Logger.Info("New data available from station %s! Starting download...", download.StationID)

					// Create a DataRequestStatus object for compatibility with existing download function
					status := &shared.DataRequestStatus{
						RequestID: download.RequestID,
						Status:    download.Status,
						FilePath:  download.FilePath,
						FileSize:  download.FileSize,
						StationID: download.StationID,
					}

					if err := c.downloadFile(requestID, status); err != nil {
						c.Logger.Error("Failed to download from station %s: %v", download.StationID, err)
					} else {
						downloadedFromStations[download.StationID] = true
						newDownloads++
						c.Logger.Info("Successfully downloaded from station %s (%d total downloads)",
							download.StationID, len(downloadedFromStations))

						// Record the time of first download
						if firstDownloadTime.IsZero() {
							firstDownloadTime = time.Now()
						}
					}
				}
			}

			// If we had new downloads, log it
			if newDownloads > 0 {
				c.Logger.Info("Downloaded from %d new collectors this round", newDownloads)
			}

			// Continue polling for additional collectors, but with a shorter timeout after first download
			if !firstDownloadTime.IsZero() {
				// If we've been waiting for additional collectors for more than 2 minutes after first download, stop
				if time.Since(firstDownloadTime) > 2*time.Minute {
					c.Logger.Info("Completed downloads from %d collectors: %v",
						len(downloadedFromStations), getStationList(downloadedFromStations))
					return nil
				}
			}
		}
	}
}

// AvailableDownload represents a download available from a collector
type AvailableDownload struct {
	RequestID   string `json:"request_id"`
	StationID   string `json:"station_id"`
	Status      string `json:"status"`
	FilePath    string `json:"file_path"`
	FileSize    int64  `json:"file_size"`
	CompletedAt string `json:"completed_at"`
}

// checkAvailableDownloads checks for available downloads from collectors
func (c *Client) checkAvailableDownloads(requestID string) ([]AvailableDownload, error) {
	req, err := http.NewRequest("GET", c.APIServerURL+"/api/data/downloads/"+requestID, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No downloads available yet
		return []AvailableDownload{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var response struct {
		RequestID          string              `json:"request_id"`
		AvailableDownloads []AvailableDownload `json:"available_downloads"`
		TotalReady         int                 `json:"total_ready"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return response.AvailableDownloads, nil
}

// downloadFile initiates the download process for a ready file
func (c *Client) downloadFile(requestID string, status *shared.DataRequestStatus) error {
	c.Logger.Info("Downloading file from station %s...", status.StationID)

	// Ensure download directory exists
	if err := os.MkdirAll(c.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	// Use ICE WebRTC transfer for all stations (consistent behavior)
	return c.downloadViaICE(requestID, status)
}

// downloadViaICE downloads the file via ICE WebRTC for all stations (consistent behavior)
func (c *Client) downloadViaICE(requestID string, status *shared.DataRequestStatus) error {
	c.Logger.Info("Downloading file from station %s via ICE...", status.StationID)
	return c.requestFileViaICE(requestID, status)
}

// downloadViaHTTP downloads the file via HTTP endpoint with ICE fallback
func (c *Client) downloadViaHTTP(requestID string, status *shared.DataRequestStatus) error {
	// Request download URL from API - now includes station ID
	downloadURL := fmt.Sprintf("%s/api/data/download/%s/%s", c.APIServerURL, requestID, status.StationID)
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.Logger.Warn("HTTP download failed: %v, trying ICE fallback", err)
		return c.requestFileViaICE(requestID, status)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Logger.Warn("HTTP download failed with status %d, trying ICE fallback", resp.StatusCode)
		return c.requestFileViaICE(requestID, status)
	}

	// Create output file with station ID to avoid conflicts
	fileName := fmt.Sprintf("%s_%s_data.npz", requestID, status.StationID)
	filePath := filepath.Join(c.DownloadDir, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy response body to file
	c.Logger.Info("Downloading file from station %s...", status.StationID)
	bytesWritten, err := io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	c.Logger.Info("File downloaded successfully: %s (%d bytes)", filePath, bytesWritten)
	return nil
}

// requestFileViaICE initiates an ICE transfer session for a data request
func (c *Client) requestFileViaICE(requestID string, status *shared.DataRequestStatus) error {
	c.Logger.Info("Attempting ICE transfer for request %s from station %s", requestID, status.StationID)

	// Create file transfer request
	transferReq := models.FileTransferRequest{
		Parameters: fmt.Sprintf(`{"request_id": "%s", "station_id": "%s"}`, requestID, status.StationID),
	}

	// Initiate ICE session
	sessionID, err := c.initiateICESession(transferReq)
	if err != nil {
		return fmt.Errorf("failed to initiate ICE session: %w", err)
	}

	c.Logger.Info("ICE session initiated: %s", sessionID)

	// Wait for collector to accept and establish WebRTC connection
	if err := c.establishWebRTCConnection(sessionID, requestID, status.StationID); err != nil {
		return fmt.Errorf("failed to establish WebRTC connection: %w", err)
	}

	c.Logger.Info("ICE transfer completed successfully for request %s", requestID)
	return nil
}

// initiateICESession creates a new ICE session for file transfer
func (c *Client) initiateICESession(req models.FileTransferRequest) (string, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.APIServerURL+"/api/ice/request", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var response models.FileTransferResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return response.SessionID, nil
}

// establishWebRTCConnection sets up the WebRTC peer connection for file transfer
func (c *Client) establishWebRTCConnection(sessionID, requestID, stationID string) error {
	c.Logger.Debug("=== Starting WebRTC connection for session %s ===", sessionID)
	
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
	c.Logger.Debug("establishWebRTCConnection: acquiring lock for peerConnections")
	c.mu.Lock()
	c.peerConnections[sessionID] = peerConnection
	c.mu.Unlock()
	c.Logger.Debug("establishWebRTCConnection: released lock for peerConnections")

	defer func() {
		c.Logger.Debug("Closing peer connection for session %s", sessionID)
		peerConnection.Close()
		c.Logger.Debug("establishWebRTCConnection: acquiring lock for peerConnections (defer)")
		c.mu.Lock()
		delete(c.peerConnections, sessionID)
		c.mu.Unlock()
		c.Logger.Debug("establishWebRTCConnection: released lock for peerConnections (defer)")
		c.Logger.Debug("=== Finished WebRTC connection cleanup for session %s ===", sessionID)
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

	// Create completion channel for file transfer
	fileTransferComplete := make(chan struct{})

	// Handle incoming data channels from collector
	peerConnection.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
		c.Logger.Info("Data channel '%s' created for session %s", dataChannel.Label(), sessionID)
		c.Logger.Debug("Data channel state: %s, ready state: %s", dataChannel.ReadyState().String(), dataChannel.ReadyState().String())
		
		// Add data channel state monitoring
		dataChannel.OnOpen(func() {
			c.Logger.Info("Data channel '%s' opened for session %s", dataChannel.Label(), sessionID)
		})
		
		dataChannel.OnClose(func() {
			c.Logger.Info("Data channel '%s' closed for session %s", dataChannel.Label(), sessionID)
		})
		
		dataChannel.OnError(func(err error) {
			c.Logger.Error("Data channel error for session %s: %v", sessionID, err)
		})
		
		c.setupFileReception(dataChannel, requestID, stationID, sessionID, fileTransferComplete)
	})

	// Wait for offer from collector
	c.Logger.Debug("Waiting for offer from collector for session %s", sessionID)
	offer, err := c.waitForOffer(sessionID)
	if err != nil {
		c.Logger.Error("Failed to receive offer for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to get offer: %w", err)
	}

	c.Logger.Debug("Received offer for session %s, SDP length: %d", sessionID, len(offer.SDP))

	// Set remote description
	c.Logger.Debug("Setting remote description (offer) for session %s", sessionID)
	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		c.Logger.Error("Failed to set remote description for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	c.Logger.Debug("Remote description set successfully for session %s", sessionID)

	// Create answer
	c.Logger.Debug("Creating answer for session %s", sessionID)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		c.Logger.Error("Failed to create answer for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to create answer: %w", err)
	}

	c.Logger.Debug("Answer created for session %s, SDP length: %d", sessionID, len(answer.SDP))

	// Set local description
	c.Logger.Debug("Setting local description (answer) for session %s", sessionID)
	if err := peerConnection.SetLocalDescription(answer); err != nil {
		c.Logger.Error("Failed to set local description for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to set local description: %w", err)
	}

	c.Logger.Debug("Local description set successfully for session %s", sessionID)

	// Send answer to signaling server
	c.Logger.Debug("Sending answer to signaling server for session %s", sessionID)
	if err := c.sendAnswer(sessionID, answer); err != nil {
		c.Logger.Error("Failed to send answer for session %s: %v", sessionID, err)
		return fmt.Errorf("failed to send answer: %w", err)
	}

	c.Logger.Debug("Answer sent successfully for session %s", sessionID)

	// Wait for file transfer to complete
	transferComplete := make(chan error, 1)

	// We'll use a context with timeout for the transfer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Create a combined done channel that closes when either transfer completes or context times out
	combinedDone := make(chan struct{})
	go func() {
		select {
		case <-fileTransferComplete:
			c.Logger.Debug("File transfer completed for session %s, closing combined done channel", sessionID)
			close(combinedDone)
		case <-ctx.Done():
			c.Logger.Debug("Context timeout for session %s, closing combined done channel", sessionID)
			close(combinedDone)
		}
	}()

	// ICE candidates will be handled via WebSocket - no polling needed

	go func() {
		// Wait for either file transfer completion or timeout
		select {
		case <-fileTransferComplete:
			c.Logger.Debug("File transfer completed for session %s", sessionID)
			transferComplete <- nil
		case <-ctx.Done():
			c.Logger.Debug("Transfer timed out for session %s", sessionID)
			transferComplete <- ctx.Err()
		}
	}()

	return <-transferComplete
}

// setupFileReception handles receiving file data through the WebRTC data channel
func (c *Client) setupFileReception(dataChannel *webrtc.DataChannel, requestID, stationID, sessionID string, transferComplete chan<- struct{}) {
	var currentFile *os.File
	var currentFileSize int64
	var bytesReceived int64
	var mu sync.Mutex
	var completed bool

	fileName := fmt.Sprintf("%s_%s_data.npz", requestID, stationID)
	filePath := filepath.Join(c.DownloadDir, fileName)

	dataChannel.OnClose(func() {
		mu.Lock()
		defer mu.Unlock()
		if currentFile != nil && !completed {
			c.Logger.Error("Data channel closed unexpectedly! Received %d/%d bytes", bytesReceived, currentFileSize)
			currentFile.Close()
			currentFile = nil
		}
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		mu.Lock()
		defer mu.Unlock()

		if completed {
			return
		}

		if msg.IsString {
			// Handle metadata
			var metadata map[string]interface{}
			if err := json.Unmarshal(msg.Data, &metadata); err != nil {
				c.Logger.Error("Failed to unmarshal metadata: %v", err)
				return
			}

			if metadata["type"] == "file-metadata" {
				size := int64(metadata["size"].(float64))
				c.Logger.Info("Receiving file via ICE: %s (%d bytes)", fileName, size)

				// Create file
				file, err := os.Create(filePath)
				if err != nil {
					c.Logger.Error("Failed to create file: %v", err)
					return
				}

				currentFile = file
				currentFileSize = size
				bytesReceived = 0
			}
		} else {
			// Handle file data
			if currentFile == nil {
				c.Logger.Error("Received file data but no file prepared")
				return
			}

			chunkSize := len(msg.Data)
			c.Logger.Debug("Received chunk: %d bytes, total so far: %d/%d", chunkSize, bytesReceived, currentFileSize)

			n, err := currentFile.Write(msg.Data)
			if err != nil {
				c.Logger.Error("Failed to write file chunk: %v", err)
				return
			}

			if n != chunkSize {
				c.Logger.Error("Partial write! Expected %d bytes, wrote %d bytes", chunkSize, n)
			}

			bytesReceived += int64(n)
			progress := float64(bytesReceived) / float64(currentFileSize) * 100

			c.Logger.Debug("Progress: %.2f%% (%d/%d bytes)", progress, bytesReceived, currentFileSize)

			if bytesReceived%1048576 == 0 { // Log every MB
				c.Logger.Info("ICE transfer progress: %.2f%% (%d/%d bytes)",
					progress, bytesReceived, currentFileSize)
			}

			// Check if file is complete
			if bytesReceived >= currentFileSize {
				c.Logger.Info("ICE file transfer completed: %s (%d bytes)", fileName, bytesReceived)
				if err := currentFile.Sync(); err != nil {
					c.Logger.Error("Failed to sync file: %v", err)
				}
				currentFile.Close()
				currentFile = nil
				completed = true
				
				// Signal completion to stop ICE candidate polling
				c.Logger.Debug("Sending transfer completion signal for session %s", sessionID)
				select {
				case transferComplete <- struct{}{}:
					c.Logger.Debug("Transfer completion signal sent for session %s", sessionID)
				default:
					c.Logger.Debug("Transfer completion signal channel full or closed for session %s", sessionID)
				}
			}
		}
	})
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

// sendAnswer sends a WebRTC answer to the signaling server
func (c *Client) sendAnswer(sessionID string, answer webrtc.SessionDescription) error {
	signal := models.ICESignalRequest{
		SessionID: sessionID,
		Type:      "answer",
		SessionDescription: &models.SessionDescription{
			Type: answer.Type.String(),
			SDP:  answer.SDP,
		},
	}

	return c.sendSignal(signal)
}

// sendSignal sends a signal to the ICE signaling server
func (c *Client) sendSignal(signal models.ICESignalRequest) error {
	jsonData, err := json.Marshal(signal)
	if err != nil {
		return fmt.Errorf("failed to marshal signal: %w", err)
	}

	req, err := http.NewRequest("POST", c.APIServerURL+"/api/ice/signal", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}

// waitForOffer waits for a WebRTC offer from the collector via WebSocket - no HTTP polling
func (c *Client) waitForOffer(sessionID string) (webrtc.SessionDescription, error) {
	// Create a channel to wait for the offer
	offerChannel := make(chan webrtc.SessionDescription, 1)
	c.Logger.Debug("waitForOffer: acquiring lock for waitingForOffer")
	c.mu.Lock()
	c.waitingForOffer[sessionID] = offerChannel
	c.mu.Unlock()
	c.Logger.Debug("waitForOffer: released lock for waitingForOffer")
	
	var offer webrtc.SessionDescription
	select {
	case offer = <-offerChannel:
		// Offer received via WebSocket
		c.Logger.Debug("Received offer for session %s via WebSocket", sessionID)
	case <-time.After(30 * time.Second):
		c.Logger.Debug("waitForOffer: acquiring lock for waitingForOffer (timeout)")
		c.mu.Lock()
		delete(c.waitingForOffer, sessionID)
		c.mu.Unlock()
		c.Logger.Debug("waitForOffer: released lock for waitingForOffer (timeout)")
		return webrtc.SessionDescription{}, fmt.Errorf("timeout waiting for offer")
	}

	c.Logger.Debug("waitForOffer: acquiring lock for waitingForOffer (delete)")
	c.mu.Lock()
	delete(c.waitingForOffer, sessionID)
	c.mu.Unlock()
	c.Logger.Debug("waitForOffer: released lock for waitingForOffer (delete)")
	
	return offer, nil
}

// ICE candidates are now handled via WebSocket notifications - no polling needed

// ICE signaling now handled via WebSocket - no HTTP polling needed

// getStationList returns a slice of station IDs from the map
func getStationList(stationMap map[string]bool) []string {
	stations := make([]string, 0, len(stationMap))
	for station := range stationMap {
		stations = append(stations, station)
	}
	return stations
}

// handleICEOffer processes the ICE offer received via WebSocket
func (c *Client) handleICEOffer(notification map[string]interface{}) {
	sessionID, ok := notification["session_id"].(string)
	if !ok {
		return
	}
	offerSDP, ok := notification["offer_sdp"].(string)
	if !ok {
		return
	}

	c.Logger.Debug("Received WebRTC offer for session %s", sessionID)
	c.Logger.Debug("handleICEOffer: acquiring read lock for waitingForOffer")
	c.mu.RLock()
	offerChan, exists := c.waitingForOffer[sessionID]
	c.mu.RUnlock()
	c.Logger.Debug("handleICEOffer: released read lock for waitingForOffer")

	if exists {
		offer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  offerSDP,
		}
		select {
		case offerChan <- offer:
			c.Logger.Debug("Sent offer to waiting channel for session %s", sessionID)
		default:
			c.Logger.Warn("Offer channel full for session %s", sessionID)
		}
	}
}

// handleICECandidate processes the ICE candidate received via WebSocket
func (c *Client) handleICECandidate(notification map[string]interface{}) {
	sessionID, ok := notification["session_id"].(string)
	if !ok {
		return
	}

	c.Logger.Debug("handleICECandidate: acquiring read lock for peerConnections")
	c.mu.RLock()
	pc, exists := c.peerConnections[sessionID]
	c.mu.RUnlock()
	c.Logger.Debug("handleICECandidate: released read lock for peerConnections")

	if !exists {
		c.Logger.Warn("No peer connection found for session %s to add ICE candidate", sessionID)
		return
	}

	// The candidate in the payload is a string that needs to be unmarshaled
	candidate, ok := notification["candidate"].(string)
	if !ok {
		c.Logger.Error("Invalid ICE candidate format in notification")
		return
	}
	sdpmLineIndex, ok := notification["sdpMLineIndex"].(float64)
	if !ok {
		c.Logger.Error("Invalid sdpMLineIndex format in notification")
		return
	}
	sdpmid, ok := notification["sdpMid"].(string)
	if !ok {
		c.Logger.Error("Invalid sdpMid format in notification")
		return
	}

	sdpMLineIndexUint16 := uint16(sdpmLineIndex)

	candidateInit := webrtc.ICECandidateInit{
		Candidate:     candidate,
		SDPMLineIndex: &sdpMLineIndexUint16,
		SDPMid:        &sdpmid,
	}

	if err := pc.AddICECandidate(candidateInit); err != nil {
		c.Logger.Error("Failed to add ICE candidate for session %s: %v", sessionID, err)
	} else {
		c.Logger.Debug("Successfully added ICE candidate for session %s", sessionID)
	}
}
