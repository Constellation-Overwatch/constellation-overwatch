# API key scopes

Constellation Overwatch accepts only the scope names listed below when an API
key is created. Scope comparison is exact and case-sensitive. Unknown names
are rejected before key material is generated.

## REST API

| Scope | Access |
|---|---|
| `organizations:read` | Read the key's organization |
| `organizations:write` | Mutate organizations when the key also has the required organization access |
| `entities:read` | Read entities in the key's organization |
| `entities:write` | Mutate entities in the key's organization |
| `admin` | Administrative access across organizations; implies all REST scopes |

`orgs:read` and `orgs:write` are deprecated aliases. New requests using either
name fail with `INVALID_SCOPE`. Startup migration rewrites stored aliases to
`organizations:read` and `organizations:write`.

## NATS

| Scope | Current access |
|---|---|
| `nats:telemetry` | Publish and subscribe to organization telemetry subjects |
| `nats:commands` | Subscribe to organization command subjects |
| `nats:commands:write` | Publish to organization command subjects |
| `nats:entities` | Publish and subscribe to organization entity subjects |
| `nats:events` | Subscribe to event subjects |
| `nats:all` | Full NATS access |

NATS scopes are validated by the same registry as REST scopes. The
least-privilege edge permission redesign is tracked separately in issue #20.
Until that contract lands, do not reinterpret an unknown stored NATS value as
one of the scopes above. Migration preserves unknown stored values without
granting them new permissions, and new keys cannot request them.
