# Argus SDR

A REST API for Software Defined Radio (SDR) data management with collector and receiver clients.

## Features

- Email/password authentication with JWT tokens
- SQLite database for user and client management
- WebSocket support for collector clients

## Quick Start

### Prerequisites

- Go 1.19 or later
- SQLite3

### Installation

1. Clone the repository
2. Install dependencies:
   ```bash
   go mod download
   ```

3. Run the server:
   ```bash
   go run cmd/server/main.go
   ```

The server will start on `http://localhost:8080` by default.

### Configuration

Set environment variables to configure the application:

- `ENVIRONMENT`: `development` or `production`
- `SERVER_ADDRESS`: Server bind address (default: `:8080`)
- `DATABASE_PATH`: SQLite database file path (default: `./sdr.db`)
- `JWT_SECRET`: Secret key for JWT tokens
- `SSL_ENABLED`: Enable HTTPS with LetsEncrypt (`true`/`false`)
- `SSL_DOMAIN`: Domain name for SSL certificates
- `SSL_EMAIL`: Email for LetsEncrypt registration

## API Endpoints

### Authentication

- `POST /api/auth/register` - Register a new user
- `POST /api/auth/login` - Login
- `POST /api/auth/logout` - Logout
- `GET /api/auth/me` - Get current user info

### Collector Clients (SDR Devices)

- `POST /api/type1/register` - Register collector client
- `GET /api/type1/status` - Get client status
- `PUT /api/type1/update` - Update client info
- `GET /ws` - WebSocket connection endpoint

### Receiver Clients (Data Consumers)

- `GET /api/data/availability` - Check collector client availability
- `GET /api/data/spectrum` - Request spectrum data
- `GET /api/data/signal` - Request signal analysis

### Health Check

- `GET /health` - Server health status

## Example Usage

### Register a Collector Client

```bash
# Register user
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email": "sdr1@example.com", "password": "password123", "client_type": 1}'

# Register Collector client
curl -X POST http://localhost:8080/api/type1/register \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"client_name": "SDR Device 1", "capabilities": "{\"frequency_range\": \"88-108MHz\"}"}'
```

### Register a Receiver Client and Request Data

```bash
# Register user
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email": "consumer@example.com", "password": "password123", "client_type": 2}'

# Check availability
curl -X GET http://localhost:8080/api/data/availability \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"

# Request spectrum data
curl -X GET http://localhost:8080/api/data/spectrum \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

## Development

### Project Structure

```
argus-sdr/
├── cmd/server/main.go          # Application entry point
├── internal/
│   ├── api/                    # API routes and handlers
│   ├── auth/                   # Authentication utilities
│   ├── database/               # Database connection and migrations
│   └── models/                 # Data models
├── pkg/
│   ├── config/                 # Configuration management
│   └── logger/                 # Logging utilities
└── README.md
```

### Building

```bash
go build -o argus-sdr cmd/server/main.go
```

### Testing

The API includes mock data for development. In a production environment, implement actual SDR data processing logic.
