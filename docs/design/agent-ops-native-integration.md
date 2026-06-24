# Agent Ops Native Integration

## Direction

Agent operations should become a Constellation domain, not a transplanted
`tools/ao-ops` subtree. The durable target is:

- SQLite tables in `db/schema.sql` for agent nodes, sessions, and events.
- NATS subjects under `constellation.agentops.>`.
- A dedicated `CONSTELLATION_AGENTOPS` stream and `agentops-processor`.
- A native web feature under `pkg/services/web/features/agentops`.
- Operator routes under `/agent-ops` and `/api/agent-ops/*`.
- A lifecycle-managed tmux-compatible observer in
  `pkg/services/agentops.Observer`.
- Durable launch requests in `agent_launch_requests` with commands published
  under `constellation.commands.<org>.agentops.launch`.
- A native launch executor consuming `agentops.launch` commands, updating
  request lifecycle state, and creating tmux-compatible team panes in executor
  mode.
- Tool-call events recorded as Agent Ops `tool.call` envelopes under
  `constellation.agentops.<node>.tool.<tool>`.
- CLI transcript/autolog entries recorded as Agent Ops `session.entry`
  envelopes under `constellation.agentops.<node>.session.<provider>`.
- Event-backed knowledge gradients for hot topics, participants, suggested
  queries, and query hits through `/api/agent-ops/knowledge`.

This lets AO behavior move into the same service manager, embedded NATS,
worker, API, and Templ/Datastar topology as fleet, map, streams, metrics,
video, and Overwatch.

## Legacy To Native Map

| Legacy AO domain | Legacy package | Native Constellation owner | UI parity |
|---|---|---|---|
| Observe agent workspaces | `tools/ao-ops/internal/tmux`, `tools/ao-ops/internal/autolog` | `pkg/services/agentops.Observer`, `agent_sessions`, `agent_events`, `AgentOpsWorker` | `/agent-ops` nodes, sessions, events, last output |
| Connect nodes | `tools/ao-ops/internal/gossip`, `tools/ao-ops/internal/events` | Embedded NATS, JetStream streams, global KV | `/streams`, `/agent-ops` |
| Learn from histories | `tools/ao-ops/internal/store`, `tools/ao-ops/internal/neuralpulse`, `tools/ao-ops/internal/reflection` | SQLite domain tables, `session.entry` events, and native knowledge gradient | `/agent-ops` knowledge gradient, event history, and session entries |
| CLI autolog | `tools/ao-ops/internal/autolog`, `tools/ao-ops/internal/exchange` | Agent Ops `session.entry` envelopes, provider metadata, and transcript previews | `/agent-ops` recent session entries |
| Launch teams | `tools/ao-ops/internal/orchestrator`, `tools/ao-ops/internal/mcpserver` | `agent_launch_requests`, Constellation command subjects, native launch executor | `/agent-ops` launch requests |
| MCP/tool call telemetry | `tools/ao-ops/internal/mcpserver/toolevents` | Agent Ops `tool.call` events and tool-specific NATS subjects | `/agent-ops` recent tool calls |
| Operate through dashboard | `tools/ao-ops/internal/api`, `tools/ao-ops/internal/ui`, `tools/ao-ops/static` | `pkg/services/web/features/agentops` | `/agent-ops` |
| Tray/bootstrap/update | `cmd/ao-tray`, `internal/tray`, release scripts | `cmd/microlith` bootstrap/update and `pkg/updater` | `/agent-ops` parity map |

## Removal Sequence

1. Keep the legacy AO tree read-only while Constellation records native agent
   events from `constellation.agentops.>`.
2. Keep the native tmux-compatible observer as the replacement for the AO tmux
   pane/session observer. CLI autolog producers should emit `session.entry`
   envelopes through `RecordSessionEntry`, `/api/agent-ops/session-entries`, or
   `constellation.agentops.<node>.session.<provider>` instead of writing
   directly to AO SQLite tables.
3. Keep launch/team requests behind Constellation command subjects and web admin
   role checks. The native executor consumes `agentops.launch`, records
   accepted/running/completed/failed state, and creates tmux-compatible team
   panes when `AGENTOPS_LAUNCH_EXECUTOR_MODE=tmux`. Model CLI startup is
   explicit via `AGENTOPS_LAUNCH_CLI_ENABLED=true`.
4. Port only the required retrieval/reflection logic into Constellation-native
   Agent Ops services. The first native owner is event-backed knowledge
   gradient over `agent_events`; external API reflection remains opt-in future
   work if needed.
5. Delete legacy AO packages after the matching native owner has tests and UI
   evidence.

Implemented slices:

- Native seam: schema, service, stream, worker, web page, JSON summary, and
  Datastar SSE refresh.
- Native tmux-compatible observer: lifecycle-managed service that polls
  `tmux`/`psmux`-compatible pane state and records nodes, sessions, current
  command, role/model hints, workspace, and last output under Agent Ops.
- Native launch request path: admin-gated `/api/agent-ops/launch` persists
  `agent_launch_requests`, publishes `agentops.launch` commands to the
  Constellation command stream when NATS is available, and shows queued requests
  in `/agent-ops`.
- Native launch executor: `CommandWorker` routes
  `constellation.commands.<org>.agentops.launch` to
  `pkg/services/agentops.LaunchExecutor`, which updates request lifecycle
  state, records launch lifecycle events, creates tmux-compatible team panes in
  tmux mode, sets observer-readable pane metadata, and keeps model CLI startup
  opt-in.
- Native tool-call ingestion: `/api/agent-ops/tool-calls` and the Agent Ops
  stream accept MCP-style tool call envelopes, normalize tool subjects, store
  them as `tool.call` events, and show recent calls in `/agent-ops`.
- Native session-entry ingestion: `/api/agent-ops/session-entries` and the
  Agent Ops stream accept CLI/autolog transcript envelopes, normalize provider
  subjects, store them as `session.entry` events, update session last output,
  and show recent entries in `/agent-ops`.
- Native knowledge gradient: `/api/agent-ops/knowledge` computes local topic
  heat, related hits, participants, suggested queries, and a compact capsule
  from Agent Ops events without depending on the legacy AO exchanges database.
- Bootstrap/update mapping: first-run admin bootstrap and release replacement
  already live in `cmd/microlith` and `pkg/updater`; the Agent Ops parity map
  treats the legacy tray as an operator shell over those native service/update
  owners, not as a second embedded AO runtime.

## GUS Package Removal Evidence

The sibling `gus-agent-overwatch` checkout now treats Agent Ops as a native
Constellation domain. The old package and operation surfaces have been removed
there or rewritten as migration notes:

- Removed `tools/ao-ops` and the quarantined `tools/agent-overwatch` runtime
  tree.
- Removed standalone AO release surfaces: `.goreleaser.yaml`,
  `.github/workflows/release.yml`, `scripts/release-ao-ops.sh`,
  `scripts/fetch-mux-binaries.sh`, and the old GCS1 AO monitor script.
- Removed old AO model skills under `claude/skills/ao-ops-*` and
  `codex/skills/ao-ops-live-awareness`.
- Removed dedicated AO operation folders, including AO genesis, AO release,
  AO psmux, AO identity, phase, tray, harness, mesh/gossip/dashboard,
  sensory-fabric, intern-readiness, and hotfix operation records.
- Removed stale operation-owned AO helper code and runbooks that imported or
  invoked the deleted `tools/ao-ops` runtime.
- Replaced GUS root routing docs with `MIGRATION_TO_CONSTELLATION_AGENT_OPS.md`
  and a `reference/PRODUCT_BOUNDARY.md` that points all active Agent Ops work
  to this Constellation-native topology.
- Replaced live-facing `operations` handoff, queue, backlog, continuity, and
  team-launch docs with legacy-boundary notes that explicitly prohibit
  `ao-ops`, `ao-mcp`, `mcp__agent-overwatch__*`, `agent-overwatch.db`, and the
  old AO release/tray/dashboard path.

Validation evidence from the removal pass:

- `gus-agent-overwatch`: `git diff --check`
- `gus-agent-overwatch`: no working-tree files remain at the removed package,
  release, and AO skill paths; the tracked entries are deleted pending commit
- `gus-agent-overwatch`: no AO-named operation directories remain at
  `operations` depth two
- `gus-agent-overwatch`: remaining AO references under `operations` are either
  explicit retirement/boundary text or historical report/log evidence, not live
  launch prerequisites or package owners
- `constellation-overwatch`: `git diff --check`
- `constellation-overwatch`: focused Agent Ops package tests
- `constellation-overwatch`: `go test ./...`
