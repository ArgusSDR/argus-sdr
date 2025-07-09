package receiver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"argus-sdr/internal/shared"
	"argus-sdr/pkg/logger"

	"github.com/google/uuid"
)

// Client represents a receiver client instance
type Client struct {
	ID           string
	APIServerURL string
	DownloadDir  string
	Logger       *logger.Logger

	httpClient *http.Client
	authToken  string
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

// waitForData polls the API server until data is ready, then downloads it
func (c *Client) waitForData(requestID string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute) // Increased timeout for docker processing
	downloadedFromStations := make(map[string]bool) // Track which stations we've downloaded from
	firstDownloadTime := time.Time{}

	c.Logger.Info("Waiting for collectors to complete...")

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

			// If we had new downloads, reset the timeout timer slightly
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

			// Check if any downloads failed
			if len(downloads) > 0 && len(downloadedFromStations) == 0 {
				// There are available downloads but we haven't successfully downloaded any
				c.Logger.Warn("Found %d available downloads but none succeeded yet", len(downloads))
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

	// For now, download via HTTP endpoint
	// In the full implementation, this would use ICE for direct transfer
	return c.downloadViaHTTP(requestID, status)
}

// downloadViaHTTP downloads the file via HTTP endpoint
func (c *Client) downloadViaHTTP(requestID string, status *shared.DataRequestStatus) error {
	// Request download URL from API
	req, err := http.NewRequest("GET", c.APIServerURL+"/api/data/download/"+requestID, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to request download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download request failed with status %d", resp.StatusCode)
	}

	// Create output file with station ID to make it unique
	fileName := fmt.Sprintf("%s_%s_data.npz", requestID, status.StationID)
	filePath := fmt.Sprintf("%s/%s", c.DownloadDir, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Copy response body to file
	if _, err := file.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	c.Logger.Info("File downloaded successfully: %s (%d bytes)", filePath, status.FileSize)
	return nil
}

// getStationList returns a slice of station IDs from the map
func getStationList(stationMap map[string]bool) []string {
	stations := make([]string, 0, len(stationMap))
	for station := range stationMap {
		stations = append(stations, station)
	}
	return stations
}
