# API key scopes

Constellation Overwatch accepts only the scope names listed below when an API
key is created. Scope comparison is exact and case-sensitive. Unknown names
are rejected before key or NKey material is generated.

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

## NATS data plane

| Scope | Access |
|---|---|
| `nats:telemetry:write` | Publish organization telemetry |
| `nats:commands:read` | Subscribe to organization commands |
| `nats:commands:write` | Publish organization commands |
| `nats:entities:read` | Subscribe to organization entity events |
| `nats:entities:write` | Publish organization entity events |
| `nats:events:read` | Subscribe to organization event subjects |
| `nats:events:write` | Publish organization event subjects |

No API-key scope grants unrestricted NATS subjects or JetStream administration.
`nats:all` and ambiguous legacy names such as `nats:telemetry` are rejected.
Migration never guesses at an unknown stored value: it preserves the value
without granting it new permissions, and new keys cannot request it.
