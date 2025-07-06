# SDR API Project Plan

## Overview
A REST API for Software Defined Radio (SDR) data management with two types of clients:
- **Type 1 Clients**: SDR devices/software that provide data via persistent websocket connections
- **Type 2 Clients**: Data consumers that request calculated data via REST API

## Architecture

### Technology Stack
- **Backend**: Go (Golang)
- **Database**: SQLite for authentication data
- **SSL/TLS**: LetsEncrypt for automatic certificate management
- **Communication**: REST API + WebSockets
- **Authentication**: Email/Password based

### Client Types

#### Type 1 Clients (Data Providers)
- SDR devices or software that collect radio frequency data
- Authenticate via email/password
- Register with the API after authentication
- Maintain persistent websocket connections
- Respond to data requests from the server
- Minimum of 3 clients required for operation

#### Type 2 Clients (Data Consumers)
- Applications or users requesting processed SDR data
- Authenticate via email/password
- Make REST API requests for data
- Receive calculated/aggregated data from Type 1 clients

## Implementation Plan

### Phase 1: Core Infrastructure

#### 1.1 Project Setup
- Initialize Go module
- Set up project structure with clean architecture
- Configure development environment
- Set up Git repository with proper .gitignore

#### 1.2 Database Design
```sql
-- Users table for authentication
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    client_type INTEGER NOT NULL, -- 1 or 2
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Type 1 client registrations
CREATE TABLE type1_clients (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    client_name TEXT NOT NULL,
    status TEXT DEFAULT 'registered', -- registered, connected, disconnected
    last_seen DATETIME,
    capabilities TEXT, -- JSON string of client capabilities
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Active websocket connections
CREATE TABLE active_connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id INTEGER NOT NULL,
    connection_id TEXT UNIQUE NOT NULL,
    connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (client_id) REFERENCES type1_clients(id)
);
```

#### 1.3 Authentication System
- Implement bcrypt password hashing
- JWT token generation and validation
- Middleware for protected routes
- Email/password validation

### Phase 2: Core API Development

#### 2.1 REST API Endpoints

**Authentication Endpoints:**
- `POST /api/auth/register` - User registration
- `POST /api/auth/login` - User login
- `POST /api/auth/logout` - User logout
- `GET /api/auth/me` - Get current user info

**Type 1 Client Endpoints:**
- `POST /api/type1/register` - Register Type 1 client
- `GET /api/type1/status` - Get client status
- `PUT /api/type1/update` - Update client info

**Type 2 Client Endpoints:**
- `GET /api/data/spectrum` - Request spectrum data
- `GET /api/data/signal` - Request signal analysis
- `GET /api/data/availability` - Check Type 1 client availability

**Admin Endpoints:**
- `GET /api/admin/clients` - List all clients
- `GET /api/admin/stats` - System statistics

#### 2.2 WebSocket Implementation
- WebSocket connection handler for Type 1 clients
- Connection management and heartbeat
- Message routing and response handling
- Connection pooling and load balancing

### Phase 3: SSL/TLS with LetsEncrypt

#### 3.1 Certificate Management
- Integrate `golang.org/x/crypto/acme/autocert` for automatic certificates
- Configure certificate storage and renewal
- HTTPS redirect middleware
- Domain validation setup

#### 3.2 Security Enhancements
- Rate limiting middleware
- CORS configuration
- Security headers
- Input validation and sanitization

### Phase 4: Type 1 Client Management

#### 4.1 Client Selection Logic
- Maintain pool of connected Type 1 clients
- Random selection algorithm when > 3 clients available
- Fallback handling when < 3 clients available
- Health checking and automatic failover

#### 4.2 Data Request Coordination
- Request distribution to selected clients
- Response aggregation and processing
- Timeout handling
- Error recovery and retry logic

### Phase 5: Data Processing & API Logic

#### 5.1 Data Calculation Engine
- Implement data aggregation algorithms
- Signal processing utilities
- Statistical analysis functions
- Caching layer for computed results

#### 5.2 Real-time Communication
- WebSocket message protocols
- Request/response correlation
- Async operation handling
- Event-driven architecture

### Phase 6: Monitoring & Logging

#### 6.1 Logging System
- Structured logging with levels
- Request/response logging
- Error tracking and alerting
- Performance metrics

#### 6.2 Health Monitoring
- Health check endpoints
- Client connection monitoring
- Database connection pooling
- System resource monitoring

## File Structure