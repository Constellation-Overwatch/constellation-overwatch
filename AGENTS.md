# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Commands

This project uses [Task](https://taskfile.dev/) instead of Make for task automation.

### Development
```bash
# List all available tasks
task --list

# Development mode (with templ auto-rebuild and server)
task dev

# Run the server
go run ./cmd/microlith/main.go

# Build the binary
task build

# Run the built binary
task run

# Run with TUI dashboard
task tui

# Build + run with TUI
task build-tui

# Run tests
go test ./...

# Install dependencies
go mod download

# Tidy dependencies
go mod tidy

# Format code
go fmt ./...

# Run go vet
go vet ./...

# Generate templ templates
task templ-generate

# Watch templ files for changes
task templ-watch

# Clean templ cache and regenerate
task templ-clean

# Clean build artifacts and data
task clean

# Generate development certificates
task generate-certs

# Docker commands
task docker-build
task docker-run
task docker-stop
```

### Environment Setup
Create a `.env` file from the example:
```bash
cp .env.example .env
```

Default ports:
- API Server & Web UI: 8080
- NATS Server: 4222 (embedded)

## Architecture

### Overview
Constellation Overwatch is an industrial data fabric and C4 toolbelt for agentic drones, robots, sensors, and video streams. It's built as a monolithic "microlith" with embedded NATS JetStream for messaging.

**Module:** `github.com/Constellation-Overwatch/constellation-overwatch` (Go 1.25)

### Core Components

#### 1. API Layer (`/api`)
- **handlers/**: HTTP endpoint handlers (entities, organizations, telemetry, commands)
- **middleware/**: Authentication (API key, session, WebAuthn passkeys) and CORS
- **responses/**: Standard JSON response utilities (`SendSuccess`, `SendError`)
- **services/**: Business logic for entity, organization, auth, and session management

#### 2. Database Layer (`/db`)
- SQLite database with auto-initialization (`MaxOpenConns=1`)
- Schema defined in embedded `schema.sql`
- Service pattern with connection management

#### 3. Event System (`/pkg/services/workers`)
- **manager.go**: Orchestrates all workers with graceful lifecycle
- **entity.go**: Processes entity CRUD events
- **command.go**: Handles command distribution
- **telemetry.go**: Processes telemetry streams
- **event.go**: General event processing
- **base.go**: Defines `Worker` interface

#### 4. NATS Integration (`/pkg/services/embedded-nats`)
- Embedded NATS server with JetStream
- Pull-based subscriptions (not drain-based)
- Streams:
  - CONSTELLATION_ENTITIES: Entity lifecycle events
  - CONSTELLATION_COMMANDS: Command messages
  - CONSTELLATION_TELEMETRY: Telemetry data
  - CONSTELLATION_EVENTS: General events

#### 5. Web UI (`/pkg/services/web`)
- Templ for type-safe server-side HTML templates
- Datastar for dynamic HTML updates
- SSE for real-time entity/telemetry updates

#### 6. TUI Dashboard (`/pkg/tui`)
- Bubbletea-based terminal UI
- Custom zap `TUIHook` captures logs for display

#### 7. Metrics (`/pkg/metrics`)
- Prometheus metrics collectors
- Entity registry with in-memory cache synced to NATS KV store

### Key Patterns

#### Service Lifecycle
All major components implement the `Service` interface:
```go
type Service interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Name() string
    HealthCheck() error
}
```
Services register with the Service Manager, start in registration order, stop in reverse order with 5-second timeout.

#### Event-Driven Architecture
All state changes publish events to NATS subjects:
- `constellation.entities.{org_id}.created`
- `constellation.entities.{org_id}.updated`
- `constellation.entities.{org_id}.deleted`
- `constellation.telemetry.{org_id}.{entity_id}`
- `constellation.commands.{org_id}.{entity_id}`

#### Authentication
Multiple auth mechanisms:
- **API Key**: Bearer token in `Authorization` header (for API clients)
- **Session**: In-memory session store with tokens (for web UI)
- **WebAuthn**: Passkey-based authentication (for web UI)
- **Bootstrap**: Creates admin user on first run with invite token

#### Standard Response Format
```go
type Response struct {
    Success bool        `json:"success"`
    Data    interface{} `json:"data,omitempty"`
    Error   *Error      `json:"error,omitempty"`
}
```

#### Database Auto-Initialization
The database service automatically:
- Creates the SQLite file if missing
- Initializes schema from embedded `schema.sql`
- Verifies schema on startup

### Key Dependencies
- `go-chi/chi/v5`: HTTP router
- `a-h/templ`: Type-safe templates
- `nats-io/nats-server/v2`: Embedded NATS
- `charmbracelet/bubbletea`: Terminal UI
- `go.uber.org/zap`: Structured logging
- `modernc.org/sqlite`: Pure-Go SQLite
- `starfederation/datastar-go`: Dynamic HTML
- `prometheus/client_golang`: Metrics
- `go-webauthn/webauthn`: Passkey auth

### Entity Types
Defined in `prd/design/CONSTELLATION_TAK_ONTOLOGY.md` and `pkg/ontology/`:
- `aircraft_multirotor`: Drones/UAVs
- `ground_vehicle`: Ground robots/vehicles
- `sensor_fixed`: Stationary sensors
- `control_station`: Command centers
- And many more...

### Important Files
- `cmd/microlith/main.go`: Entry point, initializes all services (keep `main` minimal)
- `pkg/shared/types.go`: Shared message types for NATS
- `pkg/shared/subjects.go`: NATS subject definitions
- `pkg/shared/errors.go`: Sentinel error definitions
- `pkg/services/service.go`: Service interface and manager
- `db/schema.sql`: Database schema definition

### Testing
- Standard Go testing: `go test ./...`
- Test files follow Go convention: `*_test.go`
- Test the public API, not implementation details
- Current test files: `pkg/metrics/registry_test.go`, `pkg/metrics/http_test.go`

## Go Style Guide

Follow [The Right Way to Write Go](https://bitfieldconsulting.com/posts/go-right-way) and these principles:

### Error Handling
- **Always check errors** — never discard with `_`
- **Use sentinel errors** defined in `pkg/shared/errors.go`: `var ErrNotFound = errors.New("...")`
- **Match with `errors.Is()`**: `if errors.Is(err, shared.ErrNotFound)`
- **Wrap with context**: `return fmt.Errorf("fetching entity: %w", err)` — preserves `errors.Is` matching
- **Reserve `panic` for bugs only** — never for predictable runtime errors
- **Don't panic or exit in packages** — return errors; only `main` may exit
- **Non-fatal errors**: log as warnings and continue gracefully

### Naming Conventions
- **Receiver names**: `h` (handlers), `s` (services), `w` (workers), `m` (managers)
- **Standard short names**: `err`, `ctx`, `req`, `resp`, `buf`, `i`
- **File naming**: `{domain}.{type}.go` (e.g., `entity.service.go`, `auth.service.go`)
- **Error codes**: Descriptive uppercase (`"MISSING_ORG_ID"`, `"NOT_FOUND"`, `"CREATE_FAILED"`)
- **Constants over magic values**: Use named constants, `iota` for enumerations

### Package Design
- Keep packages focused — one clear responsibility per package
- Don't print/log in library packages; return data and errors
- Don't use `os.Getenv` or `os.Args` in packages — only in `main`
- Configuration via constructor + `WithX` methods, not config structs
- Make zero values useful — design types preventing accidental invalid states

### Concurrency
- **Use sparingly** — don't introduce goroutines unless unavoidable
- **Confine goroutines** — ensure they terminate before enclosing functions exit
- **Use `sync.WaitGroup`** or `errgroup` for parallel work
- **Specify channel direction** in parameters: `chan<- Event` or `<-chan Event`
- **Avoid package-level mutable state** — use `sync.Mutex` or channel-based access
- **Create new instances** — don't use `http.DefaultServeMux` or `http.DefaultClient`

### Logging
- Use `zap` via the `logger` package (`logger.Infow`, `logger.Errorw`, `logger.Warnw`)
- Log only actionable errors needing fixes — avoid verbose info noise
- Use structured key-value pairs: `logger.Errorw("failed to fetch", "entity_id", id, "error", err)`
- Never log secrets or personal data

### Code Quality
- **Make it work first**, then refactor — get working code in front of users early
- **Consistent abstraction levels** within functions — extract low-level paperwork into named helpers
- **Comments explain _why_, not _what_** — refactor until code is self-documenting
- Don't over-engineer: solve the current problem, not hypothetical future ones
- Invest 10% extra in refactoring while understanding is fresh

### Security
- Use parameterized SQL queries — never string concatenation
- Safe response encoding: write to buffer before sending headers
- Don't require root or elevated privileges
- Use `os.OpenRoot` for filesystem access to prevent path traversal

### Development Workflow
1. Make changes to relevant services
2. Run `go fmt ./...` to format code
3. Run `go vet ./...` to check for issues
4. Run `go test ./...` to verify
5. Test locally with `task dev` or `go run ./cmd/microlith/main.go`
6. Build binary with `task build`

### Common Tasks
- **Add new entity type**: Update `pkg/ontology/` definitions and entity service
- **Add new API endpoint**: Add handler in `api/handlers/` and corresponding service in `api/services/`
- **Add new event type**: Define in `pkg/shared/types.go` and create worker in `pkg/services/workers/`
- **Add new web page**: Create templ component in `pkg/services/web/` and register route
- **Modify database schema**: Update `db/schema.sql` and migration logic