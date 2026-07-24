# Production deployment

Production mode is fail-closed. `overwatch start` validates the complete
security configuration before it opens the database, starts NATS, creates an
admin, or binds a listener.

## Required configuration

Set every value explicitly:

```dotenv
OVERWATCH_ENV=production
HOST=127.0.0.1
PORT=8080
OVERWATCH_BASE_URL=https://hub.example-operator.net
OVERWATCH_RPID=example-operator.net
ALLOWED_ORIGINS=https://hub.example-operator.net
OVERWATCH_TRUSTED_PROXIES=127.0.0.1/32
OVERWATCH_KEY_HASH_SECRET=<at-least-32-random-bytes>
OVERWATCH_DATA_DIR=/var/lib/constellation-overwatch
OVERWATCH_BACKUP_DIR=/var/backups/constellation-overwatch
OVERWATCH_ADMIN_EMAIL=operations@example-operator.net
OVERWATCH_BOOTSTRAP_FILE=/run/constellation-overwatch/bootstrap.txt
```

`OVERWATCH_BASE_URL` and each `ALLOWED_ORIGINS` entry are exact origins:
scheme, hostname, and optional port only. Paths, wildcards, credentials,
queries, and fragments are rejected. The base URL must be HTTPS and its
hostname must equal `OVERWATCH_RPID` or be below it.

The data and backup directories must be absolute, distinct paths. Startup can
verify that the paths are explicit and separate; the operator must still place
them on durable storage and schedule/monitor backups.

## TLS reverse proxy

Terminate TLS at a controlled reverse proxy and bind Overwatch to a private
listener such as `127.0.0.1:8080`. Preserve the original `Host` and scheme.
List only the proxy IP/CIDR in `OVERWATCH_TRUSTED_PROXIES`. Direct clients are
not allowed to influence rate limiting with `X-Forwarded-For` or `X-Real-IP`.

The browser-visible origin, WebAuthn origin, and `ALLOWED_ORIGINS` entry must
match exactly. A change from hostname to IP, HTTP to HTTPS, or one port to
another is a different WebAuthn origin and must be deployed as an intentional
configuration change.

Overwatch emits HSTS in production and a CSP on application responses. The
current Datastar/templates require inline scripts, inline styles, and dynamic
expression evaluation, so the CSP explicitly includes `unsafe-inline` and
`unsafe-eval`. It also names the existing CDN-hosted code-highlighting and
Carto map resources. Removing those allowances or hosts requires a separate
nonce/hash, asset-vendoring, and Datastar-expression migration; tightening a
proxy CSP first will break the reactive dashboard.

## First-admin bootstrap

On an empty database, production startup creates the admin and its one-time
invite in one database transaction. Invite material is never printed to normal
logs. It is written to `OVERWATCH_BOOTSTRAP_FILE` with exclusive creation and
mode `0600`; startup fails without changing the database if the file cannot be
created.

Retrieve the file through the host's privileged administration channel,
complete passkey enrollment at the HTTPS URL, and securely delete the file.
If an operator-owned file already exists at that path, Overwatch will not
overwrite it.

Development mode remains convenient and is visibly logged as non-production.
It may print the local bootstrap URL to the terminal and use the SHA-256
development fallback when no API-key HMAC secret is configured.
