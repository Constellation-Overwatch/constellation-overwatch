# Open Mothership

Open source Edge C2 (Command & Control) Server Mesh Fabric designed for hybrid drone/robotic pub/sub communication, sensor data streaming, and video stream management.

## Overview

Open Mothership provides a distributed, event-driven architecture for managing fleets of autonomous systems including drones, robots, IoT sensors, and edge computing devices. Built on NATS JetStream for reliable, low-latency messaging with atomic operations and durable streams.

## Key Features

- **Real-time Pub/Sub Messaging** - Low-latency communication between edge devices and control systems
- **Durable Event Streams** - Reliable message delivery with JetStream persistence
- **Multi-Entity Support** - Manage drones, robots, sensors, and other autonomous systems
- **RESTful API** - Simple HTTP interface with bearer token authentication
- **Embedded NATS** - Self-contained messaging system with no external dependencies
- **Telemetry Streaming** - Efficient handling of high-frequency sensor data
- **Command & Control** - Secure command distribution to edge devices

## Architecture

### API Service Diagram

```mermaid
graph TB
    subgraph "Client Layer"
        C1[Web Dashboard]
        C2[Mobile App]
        C3[CLI Tools]
        C4[Edge Devices]
    end
    
    subgraph "API Gateway"
        API[REST API<br/>:8080]
        AUTH[Bearer Auth<br/>Middleware]
    end
    
    subgraph "Core Services"
        OS[Organization<br/>Service]
        ES[Entity<br/>Service]
    end
    
    subgraph "Data Layer"
        DB[(SQLite DB)]
        NATS[NATS JetStream<br/>:4222]
    end
    
    subgraph "NATS Streams"
        S1[CONSTELLATION_ENTITIES]
        S2[CONSTELLATION_EVENTS]
        S3[CONSTELLATION_TELEMETRY]
        S4[CONSTELLATION_COMMANDS]
    end
    
    C1 & C2 & C3 --> API
    C4 <--> NATS
    API --> AUTH
    AUTH --> OS & ES
    OS & ES --> DB
    ES --> NATS
    NATS --> S1 & S2 & S3 & S4
    
    style API fill:#4CAF50
    style NATS fill:#2196F3
    style DB fill:#FF9800
```

### Data Flow Sequence Diagram

```mermaid
sequenceDiagram
    participant D as Drone/Robot
    participant N as NATS JetStream
    participant A as API Service
    participant DB as Database
    participant C as Control Client
    
    Note over D,C: Entity Registration Flow
    C->>A: POST /api/v1/entities
    A->>DB: Store Entity
    A->>N: Publish entity.created
    A-->>C: Return Entity
    
    Note over D,C: Telemetry Flow
    D->>N: Publish telemetry data
    N->>N: Store in TELEMETRY stream
    C->>A: Subscribe to telemetry
    N-->>C: Stream telemetry
    
    Note over D,C: Command Flow
    C->>A: Send command
    A->>N: Publish to COMMANDS stream
    N->>D: Deliver command
    D->>N: Publish command.ack
    N-->>A: Command acknowledged
    A-->>C: Command status
    
    Note over D,C: Event Processing
    D->>N: Publish status change
    N->>N: Store in EVENTS stream
    A->>DB: Update entity status
    N-->>C: Notify subscribers
```

## Quick Start

### Prerequisites

- Go 1.21 or higher
- SQLite3

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/open-mothership.git
cd open-mothership

# Install dependencies
go mod download

# Run the server
go run ./cmd/microlith/main.go
```

The server will start on port 8080 with an embedded NATS server on port 4222.

### Configuration

Set the following environment variables:

- `API_BEARER_TOKEN` - Bearer token for API authentication (default: `constellation-dev-token`)
- `PORT` - HTTP server port (default: `8080`)

### API Authentication

All API endpoints require Bearer token authentication:

```bash
curl -H "Authorization: Bearer constellation-dev-token" \
     http://localhost:8080/api/v1/organizations
```

## API Endpoints

### Organizations
- `POST /api/v1/organizations` - Create organization
- `GET /api/v1/organizations` - List organizations
- `GET /api/v1/organizations?org_id=xxx` - Get organization

### Entities
- `POST /api/v1/entities?org_id=xxx` - Create entity
- `GET /api/v1/entities?org_id=xxx` - List entities
- `GET /api/v1/entities?org_id=xxx&entity_id=yyy` - Get entity
- `PUT /api/v1/entities?org_id=xxx&entity_id=yyy` - Update entity
- `DELETE /api/v1/entities?org_id=xxx&entity_id=yyy` - Delete entity

### Health Check
- `GET /health` - Service health status

## NATS Subjects

### Entity Events
- `constellation.entities.{org_id}.created`
- `constellation.entities.{org_id}.updated`
- `constellation.entities.{org_id}.deleted`
- `constellation.entities.{org_id}.status`

### Telemetry
- `constellation.telemetry.{org_id}.{entity_id}`

### Commands
- `constellation.commands.{org_id}.{entity_id}`
- `constellation.commands.{org_id}.broadcast`

## Development

### Project Structure

```
open-mothership/
├── cmd/
│   └── microlith/         # Main application entry point
├── api/
│   ├── middleware/        # HTTP middleware (auth, CORS, logging)
│   ├── services/          # Business logic services
│   └── handlers.go        # HTTP request handlers
├── pkg/
│   ├── ontology/          # Core domain models
│   ├── shared/            # Shared types and constants
│   └── services/
│       └── embedded-nats/ # Embedded NATS server
├── nats.conf              # NATS configuration
└── constellation.db       # SQLite database
```

### Building

```bash
# Build the binary
go build -o mothership ./cmd/microlith/main.go

# Run tests
go test ./...
```

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Roadmap

- [ ] WebSocket support for real-time updates
- [ ] Kubernetes deployment manifests
- [ ] Prometheus metrics integration
- [ ] Video stream proxy support
- [ ] Multi-region mesh networking
- [ ] Edge device SDK (Go, Python, Rust)
- [ ] Web dashboard UI
- [ ] Mobile control application
