package shared

// DataRequest represents a request for data collection
type DataRequest struct {
	ID          string `json:"id"`
	RequestType string `json:"request_type"`
	Parameters  string `json:"parameters"`
	RequestedBy string `json:"requested_by"`
	Timestamp   int64  `json:"timestamp"`
}

// DataResponse represents the response from a collector
type DataResponse struct {
	RequestID   string `json:"request_id"`
	Status      string `json:"status"` // "processing", "ready", "error"
	FilePath    string `json:"file_path,omitempty"`
	DownloadURL string `json:"download_url,omitempty"` // URL for downloading the file
	FileSize    int64  `json:"file_size,omitempty"`
	Error       string `json:"error,omitempty"`
	StationID   string `json:"station_id"`
}

// FileReadyNotification is sent when a file is ready for download
type FileReadyNotification struct {
	RequestID string `json:"request_id"`
	StationID string `json:"station_id"`
	FilePath  string `json:"file_path"`
	FileSize  int64  `json:"file_size"`
}

// DataRequestStatus represents the status of a data request for clients
type DataRequestStatus struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	FilePath  string `json:"file_path,omitempty"`
	FileSize  int64  `json:"file_size,omitempty"`
	Error     string `json:"error,omitempty"`
	StationID string `json:"station_id,omitempty"`
}

// ICESessionInfo contains information about an ICE session for direct transfers
type ICESessionInfo struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	StationID string `json:"station_id"`
	ReceiverID  string `json:"receiver_id"`
	Status      string `json:"status"`
}

// WebSocketMessage is the base message type for WebSocket communication
type WebSocketMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// StationRegistration contains station registration information
type StationRegistration struct {
	StationID       string `json:"station_id"`
	Capabilities    string `json:"capabilities"`
	ContainerImage  string `json:"container_image,omitempty"`
}

// HeartbeatMessage for maintaining WebSocket connections
type HeartbeatMessage struct {
	StationID string `json:"station_id"`
	Timestamp int64  `json:"timestamp"`
	Status    string `json:"status"`
}