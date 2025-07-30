# Argus SDR

A comprehensive Software Defined Radio (SDR) system with three operational modes: API server, data collector clients, and data receiver clients. Features include traditional HTTP-based data transfers and advanced WebRTC-based peer-to-peer direct transfers.

## 🚀 Features

### Core Capabilities
- **Three Operation Modes**: API server, collector clients, and receiver clients
- **Dual Transfer Methods**: Traditional HTTP proxy and direct P2P via WebRTC/ICE
- **Professional CLI**: Built with Cobra framework for intuitive command-line interface
- **Real-time Communication**: WebSocket connections for live data coordination
- **Authentication & Security**: JWT-based authentication with bcrypt password hashing
- **Database Management**: SQLite with automatic migrations and connection pooling
- **Docker Support**: Complete containerization with multi-service orchestration

### Advanced Features
- **ICE Direct Transfers**: WebRTC-based peer-to-peer file transfers
- **Load Balancing**: Automatic collector selection and failover
- **Health Monitoring**: Real-time connection status and heartbeat monitoring
- **Graceful Shutdown**: Proper cleanup and connection management
- **Configuration Management**: Environment-based configuration with validation

## 📋 Quick Start

### Prerequisites
- **Go 1.21+** for building from source
- **Docker & Docker Compose** for containerized deployment
- **SQLite3** for database functionality

### Installation Options

#### Option 1: Docker Compose (Recommended)
```bash
# Clone the repository
git clone <repository-url>
cd argus-sdr

# Start the complete system (API + 3 collectors)
docker-compose up -d

# Check system status
docker-compose ps

# View logs
docker-compose logs -f api-server
```

#### Option 2: Build from Source
```bash
# Clone and build
git clone <repository-url>
cd argus-sdr
go mod download
go build -o argus-sdr .

# Run API server
./argus-sdr api

# Run collector (in separate terminal)
STATION_ID=station-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr collector

# Run receiver (in separate terminal)
RECEIVER_ID=receiver-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr receiver
```

## 🎯 Operation Modes

### 1. API Server Mode
**Purpose**: Central coordination hub for collectors and receivers

```bash
./argus-sdr api
```

**Environment Variables**:
- `SERVER_ADDRESS`: Bind address (default: `:8080`)
- `DATABASE_PATH`: SQLite database path (default: `./sdr.db`)
- `JWT_SECRET`: Secret key for JWT tokens
- `SSL_ENABLED`: Enable HTTPS (`true`/`false`)
- `SSL_DOMAIN`: Domain for SSL certificates
- `SSL_EMAIL`: Email for LetsEncrypt

### 2. Collector Mode
**Purpose**: SDR data collection stations that process requests

```bash
STATION_ID=station-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr collector
```

**Environment Variables**:
- `STATION_ID`: Unique station identifier (required)
- `API_SERVER_URL`: API server URL (required)
- `DATA_DIR`: Data storage directory (default: `./nice_data`)
- `CONTAINER_IMAGE`: Docker image for SDR processing

### 3. Receiver Mode
**Purpose**: Interactive client for requesting and downloading data

```bash
RECEIVER_ID=receiver-001 API_SERVER_URL=http://localhost:8080 ./argus-sdr receiver
```

**Environment Variables**:
- `RECEIVER_ID`: Unique receiver identifier (required)
- `API_SERVER_URL`: API server URL (required)
- `DOWNLOAD_DIR`: Download directory (default: `./downloads`)

## 🔌 API Endpoints

### Authentication
- `POST /api/auth/register` - Register new user
- `POST /api/auth/login` - User login
- `GET /api/auth/me` - Get current user info

### Data Requests (New System)
- `POST /api/data/request` - Submit data collection request
- `POST /api/data/request-ice` - Submit ICE-enabled P2P request
- `GET /api/data/status/:id` - Check request status
- `GET /api/data/requests` - List user requests
- `GET /api/data/downloads/:id` - Get available downloads
- `GET /api/data/download/:id/:station_id` - Download file

### ICE/WebRTC Direct Transfers
- `POST /api/ice/request` - Initiate ICE session
- `POST /api/ice/signal` - ICE signaling (offers/answers/candidates)
- `GET /api/ice/signals/:session_id` - Get pending signals
- `GET /api/ice/sessions` - Get active ICE sessions

### WebSocket Endpoints
- `GET /collector-ws` - Collector WebSocket connection
- `GET /receiver-ws` - Receiver WebSocket notifications
- `GET /ws` - Legacy Type1 client connections

### Legacy Endpoints (Preserved)
- `POST /api/type1/register` - Register collector client
- `GET /api/data/spectrum` - Legacy spectrum data request
- `GET /api/data/availability` - Check collector availability

## 📊 Complete Workflow Examples

### Traditional HTTP Workflow
```bash
# 1. Register receiver user
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "receiver@example.com",
    "password": "password123",
    "client_type": 2
  }'

# 2. Login and get token
TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "receiver@example.com",
    "password": "password123"
  }' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# 3. Submit data request
curl -X POST http://localhost:8080/api/data/request \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "request_type": "samples",
    "parameters": "{\"frequency\": 100.0, \"sample_rate\": 1000000, \"duration\": 10}"
  }'

# 4. Check status and download when ready
curl -X GET http://localhost:8080/api/data/status/REQUEST_ID \
  -H "Authorization: Bearer $TOKEN"
```

### ICE Direct Transfer Workflow
```bash
# 1. Submit ICE-enabled request
curl -X POST http://localhost:8080/api/data/request-ice \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "request_type": "samples",
    "parameters": "{\"frequency\": 100.0, \"sample_rate\": 1000000, \"duration\": 10}",
    "use_ice": true,
    "station_id": "station-001"
  }'

# 2. Get ICE sessions
curl -X GET http://localhost:8080/api/ice/sessions \
  -H "Authorization: Bearer $TOKEN"

# 3. Handle ICE signaling for P2P connection
curl -X POST http://localhost:8080/api/ice/signal \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "session_id": "SESSION_ID",
    "type": "offer",
    "session_description": {"type": "offer", "sdp": "..."}
  }'
```

## 🐳 Docker Deployment

### Production Deployment
```bash
# Start full system
docker-compose up -d

# Scale collectors
docker-compose up -d --scale collector-station-1=2

# View system status
docker-compose ps
docker-compose logs -f

# Stop system
docker-compose down
```

### Development with Interactive Receiver
```bash
# Start core services
docker-compose up -d api-server collector-station-1 collector-station-2

# Run interactive receiver
docker-compose --profile testing run receiver-client
```

### Database Administration
```bash
# Access database
docker-compose --profile admin run db-admin sqlite3 /data/sdr.db

# Backup database
docker run --rm -v argus-sdr_api_data:/data alpine tar czf - /data > backup.tar.gz
```

## 🧪 Testing

### Automated Tests
```bash
# Test all modes
./scripts/test-modes.sh

# Test ICE integration
./scripts/test-ice-integration.sh

# Test Docker setup
./scripts/test-docker-setup.sh
```

### Manual Testing
```bash
# Register test users
./scripts/test-api.sh

# Test receiver functionality
./scripts/test-receiver.sh
```

## 🏗️ Architecture

### System Components
```
┌─────────────────┐    ┌─────────────────┐     ┌─────────────────┐
│   Receiver      │    │   API Server    │     │   Collector     │
│   Clients       │◄──►│                 │◄───►│   Stations      │
│                 │    │  - Auth/DB      │     │                 │
│ - Data Requests │    │  - WebSockets   │     │  - Data Proc    │
│ - File Downloads│    │  - ICE Sessions │     │  - File Serving │
│ - ICE P2P       │◄───┼─────────────────┼────►│  - ICE P2P      │
└─────────────────┘    └─────────────────┘     └─────────────────┘
```

### Data Flow Options

**Traditional Flow**:
Receiver → API Server → Collector → API Server → Receiver

**ICE Direct Flow**:
Receiver → API Server (signaling) → Direct P2P → Collector

### Directory Structure
```
argus-sdr/
├── main.go                     # Unified entry point with Cobra CLI
├── internal/
│   ├── api/                   # REST API and WebSocket handlers
│   │   ├── handlers/          # HTTP request handlers
│   │   ├── middleware/        # Authentication, logging, CORS
│   │   └── router.go          # Route definitions
│   ├── auth/                  # JWT authentication utilities
│   ├── collector/             # Collector client implementation
│   ├── receiver/              # Receiver client implementation
│   ├── database/              # SQLite database and migrations
│   ├── models/                # Data structures and types
│   └── shared/                # Shared message types
├── pkg/
│   ├── config/                # Configuration management
│   └── logger/                # Structured logging
├── scripts/                   # Testing and utility scripts
├── docker-compose.yml         # Multi-container orchestration
├── Dockerfile                 # Container build definition
└── README.md
```

## 🔧 Configuration

### Environment Variables Reference

**Common**:
- `MODE`: Operation mode (`api`/`collector`/`receiver`)
- `ENVIRONMENT`: Environment (`development`/`production`)
- `LOG_LEVEL`: Logging level (`debug`/`info`/`warn`/`error`)

**API Server**:
- `SERVER_ADDRESS`: Bind address (default: `:8080`)
- `DATABASE_PATH`: SQLite file path (default: `./sdr.db`)
- `JWT_SECRET`: JWT signing secret
- `SSL_ENABLED`: Enable HTTPS (default: `false`)
- `SSL_DOMAIN`: Domain for SSL certificates
- `SSL_EMAIL`: Email for LetsEncrypt
- `TOKEN_EXPIRY_HOURS`: JWT expiry time (default: `24`)
- `BCRYPT_COST`: Password hashing cost (default: `12`)

**Collector**:
- `STATION_ID`: Unique station identifier (required)
- `API_SERVER_URL`: API server URL (required)
- `DATA_DIR`: Data storage directory (default: `./nice_data`)
- `CONTAINER_IMAGE`: Docker image for SDR processing (default: `argussdr/sdr-tdoa-df:release-0.4`)

**Receiver**:
- `RECEIVER_ID`: Unique receiver identifier (required)
- `API_SERVER_URL`: API server URL (required)
- `DOWNLOAD_DIR`: Download directory (default: `./downloads`)

## 🚀 Production Deployment

### System Requirements
- **CPU**: Multi-core recommended for concurrent processing
- **Memory**: 4GB+ RAM for multiple collectors
- **Storage**: SSD recommended for database and file operations
- **Network**: Low-latency connection for real-time coordination

### Security Considerations
- Change default JWT secret in production
- Use environment variables for sensitive configuration
- Enable SSL/TLS for production deployments
- Implement rate limiting for public APIs
- Use Docker secrets for sensitive data

### Monitoring & Maintenance
- Monitor collector connection health via WebSocket heartbeats
- Set up log aggregation for distributed troubleshooting
- Implement backup strategies for SQLite database
- Monitor disk space usage in data directories
- Use health check endpoints for load balancer configuration

## 📚 Development

### Building from Source
```bash
# Install dependencies
go mod download

# Run tests
go test ./...

# Build binary
go build -o argus-sdr .

# Build with specific OS/architecture
GOOS=linux GOARCH=amd64 go build -o argus-sdr-linux .
```

### Contributing
1. Fork the repository
2. Create feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open Pull Request

## 🐛 Troubleshooting

### Common Issues

**Database locked errors**:
```bash
# Check for stale connections
./argus-sdr api  # Automatic cleanup on startup
```

**Collector connection failures**:
```bash
# Check API server accessibility
curl http://api-server:8080/health

# Verify authentication
curl -X POST http://api-server:8080/api/auth/login ...
```

**ICE session establishment problems**:
```bash
# Check WebSocket connections
curl --upgrade-insecure-requests http://api-server:8080/receiver-ws
```

### Debug Mode
```bash
# Enable debug logging
LOG_LEVEL=debug ./argus-sdr api
```

## 📄 License

This project is licensed under the terms specified in the LICENSE file.

## 🤝 Support

For issues, feature requests, or questions:
1. Check existing issues in the repository
2. Create a new issue with detailed description
3. Include logs and configuration details
4. Specify environment and version information
