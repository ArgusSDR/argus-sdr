# ICE Implementation Guide for SDR API

## Overview

This guide explains the Interactive Connectivity Establishment (ICE) implementation for direct peer-to-peer NPZ file transfers between Type1 (sender) and Type2 (receiver) clients in the SDR API system.

## Architecture

The implementation uses a three-tier approach:

1. **Server-side Signaling**: Centralized signaling server for ICE negotiation
2. **Type1 Client (Sender)**: Provides data files and creates WebRTC offers in response to requests
3. **Type2 Client (Receiver)**: Initiates file transfer requests and creates WebRTC answers

```
Type2 Client (Receiver)  SDR API Server (Signaling)    Type1 Client (Sender)
       |                            |                            |
       |-- POST /api/ice/initiate ->|                            |
       |   (request data)           |                            |
       |<-- Session created --------|                            |
       |                            |-- GET /api/ice/sessions -->|
       |                            |   (discover requests)      |
       |                            |<-- POST /api/ice/signal ---|
       |                            |   (SDP offer)              |
       |<-- GET /api/ice/signals ---|                            |
       |   (retrieve offer)         |                            |
       |-- POST /api/ice/signal --->|-- GET /api/ice/signals --->|
       |   (SDP answer)             |   (retrieve answer)        |
       |                            |                            |
       |<------ Direct WebRTC Data Channel Connection ---------->|
```

## Server-Side Implementation

### Database Schema

The server maintains several tables for ICE session management:

```sql
-- ICE sessions for coordinating transfers (initiated by Type2 clients)
CREATE TABLE ice_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT UNIQUE NOT NULL,
    initiator_user_id INTEGER NOT NULL,    -- Type2 client requesting data
    target_user_id INTEGER,                -- Type1 client providing data
    initiator_client_type INTEGER NOT NULL, -- Always 2 (Type2 initiates)
    target_client_type INTEGER NOT NULL,    -- Always 1 (Type1 responds)
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (initiator_user_id) REFERENCES users(id),
    FOREIGN KEY (target_user_id) REFERENCES users(id)
);

-- ICE candidates and SDP offers/answers
CREATE TABLE ice_candidates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    candidate TEXT NOT NULL,
    sdp_mline_index INTEGER NOT NULL,
    sdp_mid TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES ice_sessions(session_id),
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- File transfer tracking
CREATE TABLE file_transfers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    file_type TEXT,
    request_type TEXT NOT NULL,
    parameters TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    FOREIGN KEY (session_id) REFERENCES ice_sessions(session_id)
);
```

### API Endpoints

- `POST /api/ice/initiate` - Create new ICE session
- `POST /api/ice/signal` - Send ICE signals (offers, answers, candidates)
- `GET /api/ice/signals` - Retrieve pending signals
- `GET /api/ice/sessions` - Get active sessions

## Type2 Client (Receiver) Implementation - Initiator

### Dependencies

Add to your `go.mod`:

```go
require (
    github.com/pion/webrtc/v3 v3.3.5
    github.com/google/uuid v1.6.0
    github.com/gorilla/websocket v1.5.3
)
```

### Core Structure

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "time"

    "github.com/pion/webrtc/v3"
    "github.com/google/uuid"
)

type Type2Client struct {
    serverURL   string
    clientID    string
    authToken   string
    httpClient  *http.Client
    downloadDir string
}

type ICESession struct {
    ID            string `json:"session_id"`
    InitiatorID   string `json:"initiator_user_id"`
    TargetID      string `json:"target_user_id"`
    RequestType   string `json:"request_type"`
    Parameters    string `json:"parameters"`
    Status        string `json:"status"`
}

type SignalRequest struct {
    SessionID  string `json:"session_id"`
    SignalType string `json:"signal_type"`
    SignalData string `json:"signal_data"`
}

type SignalResponse struct {
    ID         int    `json:"id"`
    SessionID  string `json:"session_id"`
    SignalType string `json:"signal_type"`
    SignalData string `json:"signal_data"`
    FromClient string `json:"from_client_id"`
    ToClient   string `json:"to_client_id"`
}
```

### Session Initiation (Type2 Initiates)

```go
func (c *Type2Client) RequestFileTransfer(requestType string, requestData interface{}) (*ICESession, error) {
    requestDataBytes, err := json.Marshal(requestData)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal request data: %w", err)
    }

    payload := map[string]interface{}{
        "request_type": requestType,
        "parameters":   string(requestDataBytes),
        "file_name":    fmt.Sprintf("%s_data.npz", requestType),
        "file_size":    0, // Unknown at request time
        "file_type":    "application/octet-stream",
    }

    jsonData, err := json.Marshal(payload)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal payload: %w", err)
    }

    req, err := http.NewRequest("POST", c.serverURL+"/api/ice/initiate", bytes.NewBuffer(jsonData))
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.authToken)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to send request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
    }

    var session ICESession
    if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
        return nil, fmt.Errorf("failed to decode response: %w", err)
    }

    return &session, nil
}
```

### WebRTC Connection Setup (Type2 Waits for Offer)

```go
func (c *Type2Client) EstablishConnection(session *ICESession) error {
    // Create WebRTC configuration
    config := webrtc.Configuration{
        ICEServers: []webrtc.ICEServer{
            {
                URLs: []string{"stun:stun.l.google.com:19302"},
            },
        },
    }

    // Create peer connection
    peerConnection, err := webrtc.NewPeerConnection(config)
    if err != nil {
        return fmt.Errorf("failed to create peer connection: %w", err)
    }
    defer peerConnection.Close()

    // Set up ICE candidate handling
    peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
        if candidate == nil {
            return
        }

        candidateData, err := json.Marshal(candidate.ToJSON())
        if err != nil {
            log.Printf("Failed to marshal ICE candidate: %v", err)
            return
        }

        if err := c.sendSignal(session.ID, "candidate", string(candidateData)); err != nil {
            log.Printf("Failed to send ICE candidate: %v", err)
        }
    })

    // Handle incoming data channels from Type1 client
    peerConnection.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
        log.Printf("Data channel '%s' opened", dataChannel.Label())

        // Set up file reception
        c.setupFileReception(dataChannel)
    })

    // Wait for offer from Type1 client
    offer, err := c.waitForOffer(session.ID)
    if err != nil {
        return fmt.Errorf("failed to get offer: %w", err)
    }

    // Set remote description
    if err := peerConnection.SetRemoteDescription(offer); err != nil {
        return fmt.Errorf("failed to set remote description: %w", err)
    }

    // Create answer
    answer, err := peerConnection.CreateAnswer(nil)
    if err != nil {
        return fmt.Errorf("failed to create answer: %w", err)
    }

    // Set local description
    if err := peerConnection.SetLocalDescription(answer); err != nil {
        return fmt.Errorf("failed to set local description: %w", err)
    }

    // Send answer to server
    answerData, err := json.Marshal(answer)
    if err != nil {
        return fmt.Errorf("failed to marshal answer: %w", err)
    }

    if err := c.sendSignal(session.ID, "answer", string(answerData)); err != nil {
        return fmt.Errorf("failed to send answer: %w", err)
    }

    // Handle incoming ICE candidates
    go c.handleICECandidates(session.ID, peerConnection)

    // Keep connection alive for file reception
    select {}
}
```

### File Reception Implementation

```go
func (c *Type2Client) setupFileReception(dataChannel *webrtc.DataChannel) {
    var currentFile *os.File
    var currentFileSize int64
    var bytesReceived int64

    // Handle incoming messages
    dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
        if msg.IsString {
            // Handle metadata
            var metadata map[string]interface{}
            if err := json.Unmarshal(msg.Data, &metadata); err != nil {
                log.Printf("Failed to unmarshal metadata: %v", err)
                return
            }

            if metadata["type"] == "file-metadata" {
                filename := metadata["filename"].(string)
                size := int64(metadata["size"].(float64))

                log.Printf("Receiving file: %s (%d bytes)", filename, size)

                // Create file in download directory
                filepath := fmt.Sprintf("%s/%s", c.downloadDir, filename)
                file, err := os.Create(filepath)
                if err != nil {
                    log.Printf("Failed to create file: %v", err)
                    return
                }

                currentFile = file
                currentFileSize = size
                bytesReceived = 0
            }
        } else {
            // Handle file data
            if currentFile == nil {
                log.Printf("Received file data but no file prepared")
                return
            }

            n, err := currentFile.Write(msg.Data)
            if err != nil {
                log.Printf("Failed to write file chunk: %v", err)
                return
            }

            bytesReceived += int64(n)
            progress := float64(bytesReceived) / float64(currentFileSize) * 100
            log.Printf("Receive progress: %.2f%%", progress)

            // Check if file is complete
            if bytesReceived >= currentFileSize {
                log.Printf("File transfer completed: %d bytes received", bytesReceived)
                currentFile.Close()
                currentFile = nil
            }
        }
    })
}
```

### Signal Handling

```go
func (c *Type2Client) sendSignal(sessionID, signalType, signalData string) error {
    signal := SignalRequest{
        SessionID:  sessionID,
        SignalType: signalType,
        SignalData: signalData,
    }

    jsonData, err := json.Marshal(signal)
    if err != nil {
        return fmt.Errorf("failed to marshal signal: %w", err)
    }

    req, err := http.NewRequest("POST", c.serverURL+"/api/ice/signal", bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.authToken)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("failed to send signal: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("server returned status %d", resp.StatusCode)
    }

    return nil
}

func (c *Type2Client) waitForOffer(sessionID string) (webrtc.SessionDescription, error) {
    timeout := time.After(30 * time.Second)
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-timeout:
            return webrtc.SessionDescription{}, fmt.Errorf("timeout waiting for offer")
        case <-ticker.C:
            signals, err := c.getSignals(sessionID)
            if err != nil {
                continue
            }

            for _, signal := range signals {
                if signal.SignalType == "offer" {
                    var offer webrtc.SessionDescription
                    if err := json.Unmarshal([]byte(signal.SignalData), &offer); err != nil {
                        continue
                    }
                    return offer, nil
                }
            }
        }
    }
}

func (c *Type2Client) getSignals(sessionID string) ([]SignalResponse, error) {
    req, err := http.NewRequest("GET", c.serverURL+"/api/ice/signals/"+sessionID, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Authorization", "Bearer "+c.authToken)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to send request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
    }

    var signals []SignalResponse
    if err := json.NewDecoder(resp.Body).Decode(&signals); err != nil {
        return nil, fmt.Errorf("failed to decode signals: %w", err)
    }

    return signals, nil
}

func (c *Type2Client) handleICECandidates(sessionID string, peerConnection *webrtc.PeerConnection) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    processedCandidates := make(map[string]bool)

    for {
        select {
        case <-ticker.C:
            signals, err := c.getSignals(sessionID)
            if err != nil {
                continue
            }

            for _, signal := range signals {
                if signal.SignalType == "candidate" {
                    candidateKey := fmt.Sprintf("%s-%s", signal.SignalData, signal.FromClient)
                    if processedCandidates[candidateKey] {
                        continue
                    }
                    processedCandidates[candidateKey] = true

                    var candidate webrtc.ICECandidateInit
                    if err := json.Unmarshal([]byte(signal.SignalData), &candidate); err != nil {
                        log.Printf("Failed to unmarshal ICE candidate: %v", err)
                        continue
                    }

                    if err := peerConnection.AddICECandidate(candidate); err != nil {
                        log.Printf("Failed to add ICE candidate: %v", err)
                    }
                }
            }
        }
    }
}
```

## Type1 Client (Sender) Implementation - Responder

### Core Structure

```go
type Type1Client struct {
    serverURL   string
    clientID    string
    authToken   string
    httpClient  *http.Client
    dataDir     string
}
```

### Session Discovery and Connection

```go
func (c *Type1Client) ListenForRequests() error {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            sessions, err := c.getActiveSessions()
            if err != nil {
                log.Printf("Failed to get active sessions: %v", err)
                continue
            }

            for _, session := range sessions {
                go c.handleRequest(session)
            }
        }
    }
}

func (c *Type1Client) getActiveSessions() ([]ICESession, error) {
    req, err := http.NewRequest("GET", c.serverURL+"/api/ice/sessions", nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Authorization", "Bearer "+c.authToken)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to send request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
    }

    var sessions []ICESession
    if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
        return nil, fmt.Errorf("failed to decode sessions: %w", err)
    }

    return sessions, nil
}
```

### Request Handling (Type1 Creates Offer)

```go
func (c *Type1Client) handleRequest(session ICESession) error {
    // Parse request parameters
    var requestData map[string]interface{}
    if err := json.Unmarshal([]byte(session.Parameters), &requestData); err != nil {
        return fmt.Errorf("failed to parse request parameters: %w", err)
    }

    // Generate data based on request type
    filePath, err := c.generateData(session.RequestType, requestData)
    if err != nil {
        return fmt.Errorf("failed to generate data: %w", err)
    }

    // Create WebRTC configuration
    config := webrtc.Configuration{
        ICEServers: []webrtc.ICEServer{
            {
                URLs: []string{"stun:stun.l.google.com:19302"},
            },
        },
    }

    // Create peer connection
    peerConnection, err := webrtc.NewPeerConnection(config)
    if err != nil {
        return fmt.Errorf("failed to create peer connection: %w", err)
    }
    defer peerConnection.Close()

    // Create data channel for file transfer
    dataChannel, err := peerConnection.CreateDataChannel("file-transfer", nil)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %w", err)
    }

    // Set up ICE candidate handling
    peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
        if candidate == nil {
            return
        }

        candidateData, err := json.Marshal(candidate.ToJSON())
        if err != nil {
            log.Printf("Failed to marshal ICE candidate: %v", err)
            return
        }

        if err := c.sendSignal(session.ID, "candidate", string(candidateData)); err != nil {
            log.Printf("Failed to send ICE candidate: %v", err)
        }
    })

    // Create offer
    offer, err := peerConnection.CreateOffer(nil)
    if err != nil {
        return fmt.Errorf("failed to create offer: %w", err)
    }

    // Set local description
    if err := peerConnection.SetLocalDescription(offer); err != nil {
        return fmt.Errorf("failed to set local description: %w", err)
    }

    // Send offer to server
    offerData, err := json.Marshal(offer)
    if err != nil {
        return fmt.Errorf("failed to marshal offer: %w", err)
    }

    if err := c.sendSignal(session.ID, "offer", string(offerData)); err != nil {
        return fmt.Errorf("failed to send offer: %w", err)
    }

    // Wait for answer
    answer, err := c.waitForAnswer(session.ID)
    if err != nil {
        return fmt.Errorf("failed to get answer: %w", err)
    }

    // Set remote description
    if err := peerConnection.SetRemoteDescription(answer); err != nil {
        return fmt.Errorf("failed to set remote description: %w", err)
    }

    // Handle incoming ICE candidates
    go c.handleICECandidates(session.ID, peerConnection)

    // Wait for data channel to open
    dataChannelReady := make(chan struct{})
    dataChannel.OnOpen(func() {
        log.Println("Data channel opened")
        close(dataChannelReady)
    })

    // Wait for connection
    select {
    case <-dataChannelReady:
        log.Println("Data channel ready, starting file transfer")
    case <-time.After(30 * time.Second):
        return fmt.Errorf("timeout waiting for data channel")
    }

    // Send file
    return c.sendFile(dataChannel, filePath)
}
```

### File Sending (Type1 Sends Data)

```go
func (c *Type1Client) sendFile(dataChannel *webrtc.DataChannel, filePath string) error {
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
        "filename": fileInfo.Name(),
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

    // Send file in chunks
    buffer := make([]byte, 16384) // 16KB chunks
    totalSent := int64(0)

    for {
        n, err := file.Read(buffer)
        if err != nil {
            if err == io.EOF {
                break
            }
            return fmt.Errorf("failed to read file: %w", err)
        }

        if err := dataChannel.Send(buffer[:n]); err != nil {
            return fmt.Errorf("failed to send chunk: %w", err)
        }

        totalSent += int64(n)
        progress := float64(totalSent) / float64(fileInfo.Size()) * 100
        log.Printf("Transfer progress: %.2f%%", progress)
    }

    log.Printf("File transfer completed: %d bytes sent", totalSent)
    return nil
}

func (c *Type1Client) generateData(requestType string, parameters map[string]interface{}) (string, error) {
    switch requestType {
    case "spectrum":
        return c.generateSpectrum(parameters)
    case "signal":
        return c.generateSignal(parameters)
    default:
        return "", fmt.Errorf("unsupported request type: %s", requestType)
    }
}

func (c *Type1Client) generateSpectrum(params map[string]interface{}) (string, error) {
    // Extract parameters
    freqStart := int64(params["frequency_start"].(float64))
    freqEnd := int64(params["frequency_end"].(float64))
    sampleRate := int64(params["sample_rate"].(float64))

    // Generate spectrum data (mock implementation)
    filename := fmt.Sprintf("%s/spectrum_%d_%d_%d.npz", c.dataDir, freqStart, freqEnd, sampleRate)

    log.Printf("Generating spectrum: %d-%d Hz @ %d Hz", freqStart, freqEnd, sampleRate)

    // In a real implementation, you would generate actual spectrum data
    // For now, create a mock file
    file, err := os.Create(filename)
    if err != nil {
        return "", fmt.Errorf("failed to create spectrum file: %w", err)
    }
    defer file.Close()

    // Write mock data
    mockData := make([]byte, 1024*1024) // 1MB of mock data
    if _, err := file.Write(mockData); err != nil {
        return "", fmt.Errorf("failed to write mock data: %w", err)
    }

    return filename, nil
}

func (c *Type1Client) generateSignal(params map[string]interface{}) (string, error) {
    // Extract parameters
    duration := params["duration"].(float64)
    frequency := int64(params["frequency"].(float64))
    sampleRate := int64(params["sample_rate"].(float64))

    // Generate signal data (mock implementation)
    filename := fmt.Sprintf("%s/signal_%d_%d_%.2f.npz", c.dataDir, frequency, sampleRate, duration)

    log.Printf("Generating signal: %d Hz for %.2f seconds @ %d Hz", frequency, duration, sampleRate)

    // In a real implementation, you would generate actual signal data
    file, err := os.Create(filename)
    if err != nil {
        return "", fmt.Errorf("failed to create signal file: %w", err)
    }
    defer file.Close()

    // Write mock data
    mockData := make([]byte, int(duration*float64(sampleRate)*8)) // 8 bytes per sample
    if _, err := file.Write(mockData); err != nil {
        return "", fmt.Errorf("failed to write mock data: %w", err)
    }

    return filename, nil
}
```

## Error Handling and Fallback

Both Type1 and Type2 clients should implement fallback to WebSocket-based file transfer:

### Type2 Client (Receiver) Fallback

```go
func (c *Type2Client) RequestFileWithFallback(requestType string, requestData interface{}) error {
    // Try ICE first
    session, err := c.RequestFileTransfer(requestType, requestData)
    if err != nil {
        log.Printf("ICE initiation failed, falling back to WebSocket: %v", err)
        return c.RequestFileWebSocket(requestType, requestData)
    }

    // Try WebRTC connection
    if err := c.EstablishConnection(session); err != nil {
        log.Printf("WebRTC connection failed, falling back to WebSocket: %v", err)
        return c.RequestFileWebSocket(requestType, requestData)
    }

    return nil
}
```

### Type1 Client (Sender) Fallback

```go
func (c *Type1Client) HandleRequestWithFallback(session ICESession) error {
    // Try ICE first
    if err := c.handleRequest(session); err != nil {
        log.Printf("ICE transfer failed, falling back to WebSocket: %v", err)
        return c.handleRequestWebSocket(session)
    }

    return nil
}
```

## Usage Examples

### Type2 Client Usage (Initiator)

```go
func main() {
    client := &Type2Client{
        serverURL:   "http://localhost:8080",
        clientID:    "type2-client-456",
        authToken:   "your-jwt-token",
        httpClient:  &http.Client{Timeout: 30 * time.Second},
        downloadDir: "./downloads",
    }

    // Example spectrum request
    requestData := map[string]interface{}{
        "frequency_start": 100000000,
        "frequency_end":   200000000,
        "sample_rate":     1000000,
    }

    // Request file transfer
    session, err := client.RequestFileTransfer("spectrum", requestData)
    if err != nil {
        log.Fatalf("Failed to request file transfer: %v", err)
    }

    // Establish connection and receive file
    if err := client.EstablishConnection(session); err != nil {
        log.Fatalf("File transfer failed: %v", err)
    }
}
```

### Type1 Client Usage (Responder)

```go
func main() {
    client := &Type1Client{
        serverURL:  "http://localhost:8080",
        clientID:   "type1-client-123",
        authToken:  "your-jwt-token",
        httpClient: &http.Client{Timeout: 30 * time.Second},
        dataDir:    "./data",
    }

    // Start listening for data requests
    if err := client.ListenForRequests(); err != nil {
        log.Fatalf("Request listening failed: %v", err)
    }
}
```

## Best Practices

1. **Connection Timeouts**: Always implement reasonable timeouts for WebRTC connections
2. **Error Handling**: Gracefully handle connection failures and fallback to WebSocket
3. **File Chunking**: Use appropriate chunk sizes (16KB recommended) for file transfers
4. **Progress Reporting**: Implement progress callbacks for better user experience
5. **Resource Cleanup**: Always close peer connections and file handles properly
6. **Security**: Validate all incoming data and implement proper authentication
7. **Logging**: Implement comprehensive logging for debugging connection issues

## Security Considerations

- All signaling is authenticated via JWT tokens
- File transfers are direct peer-to-peer (no server intermediary)
- ICE candidates are exchanged through the secure signaling server
- WebRTC connections use DTLS encryption by default
- Implement proper input validation on both sides
- Consider implementing file integrity checks (checksums)

This implementation provides a robust foundation for WebRTC-based file transfers with proper fallback mechanisms and comprehensive error handling.