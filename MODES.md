# SDR API Modes Implementation Plan

## Overview

This plan implements three operational modes for the SDR API system:
- `api`: Server mode (existing functionality)
- `collector`: SDR data collection client mode
- `receiver`: Data request client mode

## Architecture

```
Receiver Client  ←→  API Server  ←→  Collector Client
     |                   |                |
     |                   |                |
     └─── ICE Direct Transfer ────────────┘
```

### Workflow Process

1. **Receiver** makes a data request to the API
2. **API** sends request across WebSocket to connected collector(s)
3. **Collector** runs Python script in Docker to generate file
4. **Collector** notifies API when file is ready
5. **API** notifies receiver that data is available
6. **Receiver** initiates ICE connection to download file directly from collector

## Implementation Plan

### Phase 1: Project Structure Updates

#### 1.1 Directory Structure
```
argus-sdr/
├── cmd/
│   ├── server/          # API server mode
│   │   └── main.go
│   ├── collector/       # Collector client mode
│   │   └── main.go
│   └── receiver/        # Receiver client mode
│       └── main.go
├── internal/
│   ├── api/             # Existing API server code
│   ├── collector/       # Collector client implementation
│   │   ├── client.go
│   │   ├── docker.go
│   │   └── websocket.go
│   ├── receiver/        # Receiver client implementation
│   │   ├── client.go
│   │   ├── ice.go
│   │   └── api.go
│   └── shared/          # Shared utilities
│       ├── messages.go
│       ├── config.go
│       └── ice.go
└── nice_data/           # Data directory for collector
```

#### 1.2 Shared Message Types
Create `internal/shared/messages.go`:
```go
type DataRequest struct {
    ID           string `json:"id"`
    RequestType  string `json:"request_type"`
    Parameters   string `json:"parameters"`
    RequestedBy  string `json:"requested_by"`
    Timestamp    int64  `json:"timestamp"`
}

type DataResponse struct {
    RequestID    string `json:"request_id"`
    Status       string `json:"status"` // "processing", "ready", "error"
    FilePath     string `json:"file_path,omitempty"`
    FileSize     int64  `json:"file_size,omitempty"`
    Error        string `json:"error,omitempty"`
    CollectorID  string `json:"collector_id"`
}

type FileReadyNotification struct {
    RequestID   string `json:"request_id"`
    CollectorID string `json:"collector_id"`
    FilePath    string `json:"file_path"`
    FileSize    int64  `json:"file_size"`
}
```

### Phase 2: Mode Argument Implementation

#### 2.1 Root Command Structure
Update `cmd/server/main.go` to support mode selection:
```go
func main() {
    var mode string
    flag.StringVar(&mode, "mode", "api", "Operating mode: api, collector, receiver")
    flag.StringVar(&mode, "m", "api", "Operating mode: api, collector, receiver")
    flag.Parse()

    switch mode {
    case "api":
        runAPIServer()
    case "collector":
        runCollectorClient()
    case "receiver":
        runReceiverClient()
    default:
        log.Fatalf("Invalid mode: %s. Valid modes: api, collector, receiver", mode)
    }
}
```

#### 2.2 Configuration Updates
Update `pkg/config/config.go` to support all modes:
```go
type Config struct {
    // Common
    Mode        string `env:"MODE" default:"api"`
    LogLevel    string `env:"LOG_LEVEL" default:"info"`

    // API Server
    Port        int    `env:"PORT" default:"8080"`
    DatabaseURL string `env:"DATABASE_URL" default:"./sdr.db"`
    JWTSecret   string `env:"JWT_SECRET" required:"true"`

    // Collector Client
    StationID       string `env:"STATION_ID" required:"true"`
    DataDir         string `env:"DATA_DIR" default:"./nice_data"`
    ContainerImage  string `env:"CONTAINER_IMAGE" default:"sdr-collector:latest"`
    APIServerURL    string `env:"API_SERVER_URL" required:"true"`

    // Receiver Client
    ReceiverID      string `env:"RECEIVER_ID" required:"true"`
    DownloadDir     string `env:"DOWNLOAD_DIR" default:"./downloads"`
}
```

### Phase 3: API Server Updates

#### 3.1 WebSocket Message Handling
Update `internal/api/handlers/ice.go` to handle collector communications:
```go
func (h *ICEHandler) HandleCollectorMessage(ws *websocket.Conn, message []byte) {
    var msg shared.DataResponse
    if err := json.Unmarshal(message, &msg); err != nil {
        log.Printf("Invalid message from collector: %v", err)
        return
    }

    switch msg.Status {
    case "ready":
        h.handleFileReady(msg)
    case "error":
        h.handleCollectorError(msg)
    }
}

func (h *ICEHandler) handleFileReady(response shared.DataResponse) {
    // Update database with file ready status
    h.db.UpdateDataRequestStatus(response.RequestID, "ready", response.FilePath, response.FileSize)

    // Notify waiting receiver
    h.notifyReceiver(response.RequestID, response)
}
```

#### 3.2 Data Request Endpoint
Add new endpoint `POST /api/data/request`:
```go
func (h *DataHandler) RequestData(c *gin.Context) {
    var request shared.DataRequest
    if err := c.ShouldBindJSON(&request); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    // Generate unique request ID
    request.ID = uuid.New().String()
    request.RequestedBy = c.GetString("user_id")
    request.Timestamp = time.Now().Unix()

    // Store request in database
    if err := h.db.CreateDataRequest(&request); err != nil {
        c.JSON(500, gin.H{"error": "Failed to create request"})
        return
    }

    // Forward to available collectors
    if err := h.forwardToCollectors(request); err != nil {
        c.JSON(500, gin.H{"error": "No collectors available"})
        return
    }

    c.JSON(202, gin.H{
        "request_id": request.ID,
        "status": "processing"
    })
}
```

#### 3.3 Database Schema Updates
Add tables for data requests:
```sql
CREATE TABLE data_requests (
    id TEXT PRIMARY KEY,
    request_type TEXT NOT NULL,
    parameters TEXT,
    requested_by INTEGER NOT NULL,
    assigned_collector TEXT,
    status TEXT DEFAULT 'pending',
    file_path TEXT,
    file_size INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    FOREIGN KEY (requested_by) REFERENCES users(id)
);

CREATE TABLE collector_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collector_id TEXT UNIQUE NOT NULL,
    station_id TEXT NOT NULL,
    connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT DEFAULT 'connected'
);
```

### Phase 4: Collector Client Implementation

#### 4.1 Collector Main (`cmd/collector/main.go`)
```go
func main() {
    config := loadCollectorConfig()

    collector := &internal.Collector{
        ID:             config.CollectorID,
        StationID:      config.StationID,
        APIServerURL:   config.APIServerURL,
        DataDir:        config.DataDir,
        ContainerImage: config.ContainerImage,
    }

    if err := collector.Start(); err != nil {
        log.Fatalf("Failed to start collector: %v", err)
    }
}
```

#### 4.2 Collector Client (`internal/collector/client.go`)
```go
type Collector struct {
    ID             string
    StationID      string
    APIServerURL   string
    DataDir        string
    ContainerImage string

    conn           *websocket.Conn
    authToken      string
    activeRequests map[string]*DataRequest
    mu             sync.RWMutex
}

func (c *Collector) Start() error {
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

    // Block main goroutine
    select {}
}

func (c *Collector) handleDataRequest(request shared.DataRequest) {
    c.mu.Lock()
    c.activeRequests[request.ID] = &request
    c.mu.Unlock()

    go func() {
        if err := c.processRequest(request); err != nil {
            c.sendError(request.ID, err.Error())
            return
        }
    }()
}

func (c *Collector) processRequest(request shared.DataRequest) error {
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
        RequestID:   request.ID,
        Status:      "ready",
        FilePath:    filePath,
        FileSize:    fileInfo.Size(),
        CollectorID: c.ID,
    }

    return c.sendResponse(response)
}
```

#### 4.3 Docker Integration (`internal/collector/docker.go`)
```go
func (c *Collector) runDataCollection(request shared.DataRequest) (string, error) {
    // Ensure data directory exists
    if err := os.MkdirAll(c.DataDir, 0755); err != nil {
        return "", fmt.Errorf("failed to create data directory: %w", err)
    }

    // Build Docker command with station ID as argument
    cmd := exec.Command("docker", "run", "-it", "--rm",
        "--device", "/dev/bus/usb",
        "--mount", fmt.Sprintf("type=bind,src=%s,dst=/SDR-TDOA-DF/nice_data", c.DataDir),
        c.ContainerImage,
        "./sync_collect_samples.py", c.StationID)

    // Set up output capture
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    // Run the command
    log.Printf("Starting data collection for request %s", request.ID)
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("docker command failed: %w, stderr: %s", err, stderr.String())
    }

    // Find the generated file (latest file in data directory)
    filePath, err := c.findLatestFile()
    if err != nil {
        return "", fmt.Errorf("failed to find generated file: %w", err)
    }

    log.Printf("Data collection completed for request %s, file: %s", request.ID, filePath)
    return filePath, nil
}

func (c *Collector) findLatestFile() (string, error) {
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
```

### Phase 5: Receiver Client Implementation

#### 5.1 Receiver Main (`cmd/receiver/main.go`)
```go
func main() {
    config := loadReceiverConfig()

    receiver := &internal.Receiver{
        ID:           config.ReceiverID,
        APIServerURL: config.APIServerURL,
        DownloadDir:  config.DownloadDir,
    }

    if err := receiver.Start(); err != nil {
        log.Fatalf("Failed to start receiver: %v", err)
    }
}
```

#### 5.2 Receiver Client (`internal/receiver/client.go`)
```go
type Receiver struct {
    ID           string
    APIServerURL string
    DownloadDir  string

    httpClient   *http.Client
    authToken    string
}

func (r *Receiver) Start() error {
    // Authenticate with API server
    if err := r.authenticate(); err != nil {
        return fmt.Errorf("authentication failed: %w", err)
    }

    // Start interactive CLI
    return r.startCLI()
}

func (r *Receiver) startCLI() error {
    scanner := bufio.NewScanner(os.Stdin)

    for {
        fmt.Print("sdr> ")
        if !scanner.Scan() {
            break
        }

        command := strings.TrimSpace(scanner.Text())
        if command == "" {
            continue
        }

        if err := r.handleCommand(command); err != nil {
            fmt.Printf("Error: %v\n", err)
        }
    }

    return scanner.Err()
}

func (r *Receiver) handleCommand(command string) error {
    switch {
    case command == "request":
        return r.requestData()
    case command == "status":
        return r.showStatus()
    case command == "quit" || command == "exit":
        os.Exit(0)
    default:
        fmt.Println("Available commands: request, status, quit")
    }
    return nil
}

func (r *Receiver) requestData() error {
    fmt.Println("Requesting data collection...")

    request := shared.DataRequest{
        RequestType: "sample_collection",
        Parameters:  "{}",
    }

    // Send request to API
    requestID, err := r.sendDataRequest(request)
    if err != nil {
        return fmt.Errorf("failed to send request: %w", err)
    }

    fmt.Printf("Request submitted with ID: %s\n", requestID)
    fmt.Println("Waiting for data to be ready...")

    // Poll for completion
    return r.waitForData(requestID)
}

func (r *Receiver) waitForData(requestID string) error {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    timeout := time.After(5 * time.Minute)

    for {
        select {
        case <-timeout:
            return fmt.Errorf("timeout waiting for data")
        case <-ticker.C:
            status, err := r.checkRequestStatus(requestID)
            if err != nil {
                continue
            }

            switch status.Status {
            case "ready":
                fmt.Println("Data is ready! Starting download...")
                return r.downloadFile(requestID, status)
            case "error":
                return fmt.Errorf("data collection failed: %s", status.Error)
            case "processing":
                fmt.Println("Still processing...")
            }
        }
    }
}
```

#### 5.3 ICE Download (`internal/receiver/ice.go`)
```go
func (r *Receiver) downloadFile(requestID string, status DataRequestStatus) error {
    // Initialize ICE session
    session, err := r.initiateICESession(requestID, status.CollectorID)
    if err != nil {
        return fmt.Errorf("failed to initiate ICE session: %w", err)
    }

    // Establish WebRTC connection
    return r.establishICEConnection(session, status.FilePath)
}

func (r *Receiver) initiateICESession(requestID, collectorID string) (*ICESession, error) {
    payload := map[string]interface{}{
        "request_id":   requestID,
        "collector_id": collectorID,
        "file_type":    "data_collection",
    }

    jsonData, err := json.Marshal(payload)
    if err != nil {
        return nil, err
    }

    req, err := http.NewRequest("POST", r.APIServerURL+"/api/ice/initiate", bytes.NewBuffer(jsonData))
    if err != nil {
        return nil, err
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+r.authToken)

    resp, err := r.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
    }

    var session ICESession
    if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
        return nil, err
    }

    return &session, nil
}
```

### Phase 6: Docker and Deployment

#### 6.1 Docker Compose
Create `docker-compose.yml`:
```yaml
version: '3.8'

services:
  api:
    build: .
    command: ["-mode", "api"]
    ports:
      - "8080:8080"
    environment:
      - JWT_SECRET=your-secret-key
      - DATABASE_URL=/data/sdr.db
    volumes:
      - ./data:/data

  collector:
    build: .
    command: ["-mode", "collector"]
    environment:
      - STATION_ID=station-001
      - API_SERVER_URL=http://api:8080
      - DATA_DIR=/nice_data
    volumes:
      - ./nice_data:/nice_data
      - /var/run/docker.sock:/var/run/docker.sock
    devices:
      - /dev/bus/usb:/dev/bus/usb
    privileged: true

  receiver:
    build: .
    command: ["-mode", "receiver"]
    environment:
      - RECEIVER_ID=receiver-001
      - API_SERVER_URL=http://api:8080
      - DOWNLOAD_DIR=/downloads
    volumes:
      - ./downloads:/downloads
    stdin_open: true
    tty: true
```

#### 6.2 Dockerfile Updates
```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o sdr-api ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates docker
WORKDIR /root/

COPY --from=builder /app/sdr-api .

ENTRYPOINT ["./sdr-api"]
```

### Phase 7: Testing Plan

#### 7.1 Unit Tests
- Test Docker command generation
- Test WebSocket message handling
- Test ICE session management
- Test file transfer completion

#### 7.2 Integration Tests
- End-to-end workflow test
- Collector failure handling
- ICE fallback to WebSocket
- Multiple concurrent requests

#### 7.3 Test Scripts
Create `scripts/test-modes.sh`:
```bash
#!/bin/bash

echo "Starting API server..."
./sdr-api -mode api &
API_PID=$!

sleep 5

echo "Starting collector..."
./sdr-api -mode collector &
COLLECTOR_PID=$!

sleep 5

echo "Testing data request..."
./sdr-api -mode receiver << EOF
request
quit
EOF

# Cleanup
kill $API_PID $COLLECTOR_PID
```

### Phase 8: Configuration Examples

#### 8.1 Environment Files

**API Server (.env.api)**
```bash
MODE=api
PORT=8080
DATABASE_URL=./sdr.db
JWT_SECRET=your-secret-key
LOG_LEVEL=info
```

**Collector (.env.collector)**
```bash
MODE=collector
STATION_ID=station-001
STATION_ID=station-001
API_SERVER_URL=http://localhost:8080
DATA_DIR=./nice_data
CONTAINER_IMAGE=sdr-collector:latest
LOG_LEVEL=info
```

**Receiver (.env.receiver)**
```bash
MODE=receiver
RECEIVER_ID=receiver-001
API_SERVER_URL=http://localhost:8080
DOWNLOAD_DIR=./downloads
LOG_LEVEL=info
```

## Implementation Progress

### Phase 1: Foundation ✅
- [x] Add Cobra for mode argument parsing
- [x] Create shared message types
- [x] Update configuration system
- [x] Set up project structure

### Phase 2: Mode Structure ✅
- [x] Create cmd/collector/main.go
- [x] Create cmd/receiver/main.go
- [x] Update cmd/server/main.go with Cobra
- [x] Test mode switching

### Phase 3: API Server Updates ✅
- [x] Add data request endpoints
- [x] Implement collector WebSocket handling
- [x] Update database schema
- [ ] Add ICE session management for direct transfers

### Phase 4: Collector Client ✅
- [x] Implement collector WebSocket client
- [x] Add Docker integration
- [x] Implement data request processing
- [x] Add file ready notifications

### Phase 5: Receiver Client ✅
- [x] Implement receiver API client
- [x] Add interactive CLI
- [ ] Implement ICE download client
- [x] Add progress tracking

### Phase 6: Integration & Testing 🔄
- [x] Basic integration testing (test script)
- [ ] End-to-end testing with real Docker
- [ ] Docker compose setup
- [x] Error handling improvements
- [x] Documentation and examples

## Implementation Timeline

### Week 1: Foundation
- [x] Implement mode argument parsing
- [x] Create shared message types
- [x] Update configuration system
- [x] Set up project structure

### Week 2: API Server Updates
- [ ] Add data request endpoints
- [ ] Implement collector WebSocket handling
- [ ] Update database schema
- [ ] Add ICE session management for direct transfers

### Week 3: Collector Client
- [ ] Implement collector WebSocket client
- [ ] Add Docker integration
- [ ] Implement data request processing
- [ ] Add file ready notifications

### Week 4: Receiver Client
- [ ] Implement receiver API client
- [ ] Add interactive CLI
- [ ] Implement ICE download client
- [ ] Add progress tracking

### Week 5: Integration & Testing
- [ ] End-to-end testing
- [ ] Docker compose setup
- [ ] Error handling improvements
- [ ] Documentation and examples

## Success Criteria

1. **Mode Selection**: Application can run in api, collector, or receiver mode
2. **Data Flow**: Complete workflow from receiver request to file download
3. **Docker Integration**: Collector successfully runs Python script in Docker
4. **ICE Transfer**: Direct file transfer between collector and receiver
5. **Error Handling**: Graceful handling of failures with appropriate fallbacks
6. **Monitoring**: Proper logging and status reporting throughout the process

This implementation provides a complete SDR data collection and distribution system with efficient direct file transfers and proper separation of concerns between the different operational modes.