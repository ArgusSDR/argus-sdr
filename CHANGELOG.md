# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.1] - 2025-07-30

### Added
- Initial release of Argus SDR system
- Three-mode architecture (API server, collector, receiver)
- Professional CLI interface with Cobra framework
- JWT-based authentication with bcrypt password hashing
- SQLite database with automatic migrations
- WebSocket communication for real-time coordination
- ICE/WebRTC integration for peer-to-peer file transfers
- Docker support with multi-container orchestration
- Comprehensive testing suite
- Complete API documentation
- Health and version endpoints

### Features
- **API Server Mode**: Central coordination hub with REST API
- **Collector Mode**: SDR data collection stations with Docker integration
- **Receiver Mode**: Interactive client for data requests and downloads
- **Dual Transfer Methods**: HTTP proxy and WebRTC P2P transfers
- **Real-time Communication**: WebSocket endpoints for all client types
- **Database Management**: Connection pooling and migration system
- **Security**: TLS support with LetsEncrypt integration
- **Monitoring**: Health checks and structured logging

### Technical Details
- Go 1.21+ with modern dependency management
- Gin web framework for HTTP API
- Gorilla WebSocket for real-time communication
- Pion WebRTC for peer-to-peer transfers
- SQLite with proper indexing and foreign keys
- Docker multi-stage builds with Alpine Linux
- Comprehensive error handling and graceful shutdown

[0.0.1]: https://github.com/example/argus-sdr/releases/tag/v0.0.1