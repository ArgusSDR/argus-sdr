# Receiver Reimplementation Summary

## Overview
The receiver has been completely reimplemented to use command line arguments with cobra instead of interactive prompts, simplified to only handle data requests, and streamlined for the single data collection flow.

## Key Changes

### 1. Command Line Interface (CLI)
- **Before**: Interactive CLI with prompts (`sdr> request`, `sdr> status`, `sdr> list`, etc.)
- **After**: Command line arguments using cobra (`./receiver request --api-server URL --receiver-id ID`)

### 2. Commands Simplified
- **Before**: Multiple commands (request, status, list, help, quit)
- **After**: Only `request` command implemented (status and list removed as requested)

### 3. Request Types Removed
- **Before**: Multiple request types (`sample_collection`, etc.)
- **After**: Single data collection type (`data_collection`)

### 4. Automatic Operation
- **Before**: Manual polling and separate download commands
- **After**: Automatic polling and download when file is ready

## Implementation Details

### New Command Structure
```bash
# Usage
./receiver request --api-server http://localhost:8080 --receiver-id test-receiver-001 --download-dir ./downloads

# Help
./receiver --help
./receiver request --help
```

### Required Arguments
- `--api-server`: API server URL (required)
- `--receiver-id`: Unique receiver identifier (required)
- `--download-dir`: Download directory (optional, defaults to ./downloads)

### Flow Implementation
The receiver now follows the exact flow specified:

1. **Receiver requests file** → Sends POST to `/api/data/request`
2. **API receives it** → Stores request in database
3. **API sends websocket request to collector** → Via established websocket connection
4. **Collector runs docker command** → Generates file in nice_data directory
5. **Collector notifies API ready** → Sends status update via websocket
6. **API notifies receiver** → Updates database status to "ready"
7. **Receiver automatically downloads** → Polls status and downloads when ready

### Files Modified
- `cmd/receiver/main.go` - Complete rewrite for cobra CLI
- `internal/receiver/client.go` - Removed interactive CLI, added `RequestAndDownload()` method
- `internal/api/handlers/data.go` - Added `DownloadFile()` endpoint
- `internal/api/router.go` - Added `/api/data/download/:id` route
- `go.mod` - Made cobra a direct dependency

### Files Added
- `scripts/test-receiver.sh` - Test script demonstrating usage

## Usage Example

```bash
# Build the receiver
go build -o receiver ./cmd/receiver

# Request a data file
./receiver request \
  --api-server http://localhost:8080 \
  --receiver-id station-001 \
  --download-dir ./data

# The receiver will:
# 1. Send the request to the API
# 2. Poll for status until ready
# 3. Automatically download the file
# 4. Exit when complete
```

## Benefits
- **Simplified**: No interactive mode, just one command
- **Scriptable**: Can be easily automated in scripts
- **Focused**: Only does one thing - request and download data
- **Modern**: Uses cobra for professional CLI experience
- **Automated**: No manual intervention required

The receiver is now ready for use in automated data collection workflows.