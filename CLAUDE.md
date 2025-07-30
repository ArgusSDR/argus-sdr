# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Argus SDR is a comprehensive Software Defined Radio system with a three-mode architecture:

1. **API Server Mode** (`./argus-sdr api`) - Central coordination hub with REST API and WebSocket endpoints
2. **Collector Mode** (`./argus-sdr collector`) - SDR data collection stations that process requests using Docker containers
3. **Receiver Mode** (`./argus-sdr receiver`) - Interactive clients for data requests and file downloads

The system supports two data transfer methods:
- **Traditional HTTP Proxy**: Server-mediated transfers through the API server
- **ICE Direct P2P**: WebRTC-based peer-to-peer transfers bypassing the server

## Common Commands

### Building and Running
```bash
# Build the unified binary
go build -o argus-sdr .

# Run different modes
./argus-sdr api                                                    # API server (default)
STATION_ID=station-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr collector
RECEIVER_ID=receiver-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr receiver

# Docker deployment (recommended)
docker-compose up -d                                               # Full system
docker-compose up -d api-server collector-station-1               # Minimal setup
```

### Testing
```bash
# Comprehensive test suite
./scripts/test-modes.sh                 # Test all three operational modes
./scripts/test-ice-integration.sh       # Test WebRTC P2P transfers
./scripts/test-docker-setup.sh         # Test Docker deployment
./scripts/test-logging.sh               # Test enhanced logging system
./scripts/test-api.sh                   # Test API endpoints
./scripts/test-receiver.sh              # Test receiver functionality

# Run Go tests
go test ./...

# Manual testing with different server ports
SERVER_ADDRESS=:8081 ./argus-sdr api

# Debug logging for development
ENVIRONMENT=development LOG_LEVEL=debug ./argus-sdr api
```

### Database Operations
```bash
# Database automatically migrates on API server startup
# For manual access via Docker:
docker-compose --profile admin run db-admin sqlite3 /data/sdr.db

# Check database path in configuration (default: ./sdr.db)
DATABASE_PATH=/custom/path/sdr.db ./argus-sdr api
```

## High-Level Architecture

### Core Components
- **main.go**: Unified entry point with Cobra CLI framework, handles all three modes
- **internal/api/**: REST API server with Gin framework
- **internal/collector/**: Data collection client with Docker integration
- **internal/receiver/**: Interactive data request client
- **internal/shared/**: Common message types and data structures
- **internal/database/**: SQLite database management with automatic migrations

### Handler Architecture
The API server uses a layered handler approach in `internal/api/handlers/`:
- **AuthHandler**: JWT-based authentication with bcrypt password hashing
- **DataHandler**: New data request system with WebSocket notifications
- **CollectorHandler**: WebSocket management for collector connections
- **ICEHandler**: WebRTC signaling for peer-to-peer transfers
- **Type1Handler/Type2Handler**: Legacy client support (preserved for compatibility)

### Data Flow Patterns
1. **Traditional Flow**: Receiver → API Server → Collector → API Server → Receiver
2. **ICE Direct Flow**: Receiver → API Server (signaling only) → Direct P2P → Collector
3. **WebSocket Communication**: Real-time bidirectional messaging between components

### Database Schema
SQLite database with automatic migrations containing:
- `users`: Authentication and client type (1=collector, 2=receiver)
- `data_requests`: Request tracking with status management
- `collector_responses`: Individual responses from each collector station
- `ice_sessions`: WebRTC session management for P2P transfers
- `file_transfers`: File metadata linked to ICE sessions
- Connection management tables for WebSocket state

### Configuration System
Environment-based configuration in `pkg/config/`:
- **API Server**: `SERVER_ADDRESS`, `DATABASE_PATH`, `JWT_SECRET`, SSL settings
- **Collector**: `STATION_ID`, `API_SERVER_URL`, `DATA_DIR`, `CONTAINER_IMAGE`
- **Receiver**: `RECEIVER_ID`, `API_SERVER_URL`, `DOWNLOAD_DIR`

## Key Integration Points

### WebSocket Communication
- **Collector WebSocket** (`/collector-ws`): Bidirectional data request/response
- **Receiver WebSocket** (`/receiver-ws`): File ready notifications
- **Legacy WebSocket** (`/ws`): Type1 client compatibility

### ICE/WebRTC Integration
The ICE system bridges data requests with WebRTC signaling:
- `POST /api/data/request-ice`: Creates both data request and ICE session
- ICE sessions link to data requests via `file_transfers` table
- WebSocket notifications coordinate offer/answer/candidate exchange

### Docker Integration
Collectors execute SDR processing in Docker containers:
- Default image: `argussdr/sdr-tdoa-df:release-0.4`
- Data directory mounting for file persistence
- Docker-in-Docker for collector containers

## Development Patterns

### Adding New API Endpoints
1. Define handler method in appropriate handler file (`internal/api/handlers/`)
2. Add route in `internal/api/router.go`
3. Update message types in `internal/shared/messages.go` if needed
4. Add database migrations in `internal/database/database.go` if schema changes

### WebSocket Message Handling
Messages use the `shared.WebSocketMessage` structure with `Type` and `Payload` fields. Handler dispatch is based on message type strings.

### Database Changes
All schema changes go through the migration system in `database.Migrate()`. Add new migrations to the `migrations` slice - never modify existing migrations.

### Logging System
The project uses structured logging with multiple levels:
- **Development Mode**: Detailed request/response logging via `RequestLogger` middleware
- **Production Mode**: Standard request logging via `Logger` middleware
- **Log Levels**: DEBUG, INFO, WARN, ERROR with contextual information
- **Security**: Sensitive data (passwords) automatically sanitized in logs
- **Context**: User ID, client type, and IP addresses included in relevant logs

Key logging features:
- Authentication events (successful/failed logins, registrations)
- WebSocket connection lifecycle (connect, disconnect, message handling)
- API request timing and status codes
- Data request processing and collector interactions
- ICE session management activities

### Error Handling
Use structured logging via `pkg/logger` with severity levels. Database errors, WebSocket connection issues, and Docker execution failures should be logged with context.

### Authentication Flow
JWT tokens contain `user_id`, `client_type`, and expiration. Middleware `RequireAuth()` validates tokens and `RequireClientType()` enforces access control.

## Important Implementation Details

### Mode Switching
The unified binary uses Cobra subcommands. Default behavior (no args) runs API server mode. Environment variables override CLI flags.

### Connection Management
- Database connections use connection pooling with automatic cleanup
- WebSocket connections tracked in handler maps with mutex protection
- Graceful shutdown handles SIGINT/SIGTERM with 30-second timeout

### File Transfer Architecture
Two parallel systems:
1. **HTTP Proxy**: Files served through API server with proxied download URLs
2. **ICE Direct**: WebRTC data channels with ICE session coordination

### Collector Selection
Multiple collectors can respond to requests. The system supports load balancing and automatic failover through WebSocket connection health monitoring.

### Testing Infrastructure
Comprehensive test scripts validate:
- All three operational modes
- ICE P2P transfer workflow
- Docker deployment scenarios
- API endpoint functionality
- Database migrations and cleanup