# Production deployment profile

Constellation Overwatch has two explicit runtime modes. `development` keeps a
local checkout convenient. `production` refuses to start unless its network,
identity, secret, storage, and bootstrap settings are complete.

## Recommended singleton Hub topology

Run one Hub per connectivity boundary. Put the web listener on loopback behind
an HTTPS reverse proxy and bind embedded NATS to the Hub's specific tailnet
address. Operators on Windows, macOS, and Linux use a browser at the Hub's HTTPS
name; do not install independent Hub processes as viewers.

For the SIM canary, the intended shape is:

- Web: `127.0.0.1:8090`, published as one HTTPS tailnet origin.
- NATS: the SIM's exact Tailscale IPv4 address on port `4223`, reachable only by
  named Pulsar devices under tailnet ACLs.
- Persistent database/JetStream data and backups in separate absolute paths.
- One service identity owning the data, backup, configuration, and bootstrap
  directories.

## Required environment

Provide production variables through the service supervisor (for example a
root-owned systemd `EnvironmentFile=`). The binary deliberately refuses to load
an application `.env` file in production, including one selected with `-env` or
found through `OVERWATCH_HOME`; this prevents checkout-local configuration from
silently changing a deployed service.

```dotenv
OVERWATCH_ENV=production
HOST=127.0.0.1
PORT=8090
NATS_HOST=100.x.y.z
NATS_PORT=4223

OVERWATCH_BASE_URL=https://constellation.example-tailnet.ts.net
OVERWATCH_RPID=constellation.example-tailnet.ts.net
OVERWATCH_ALLOWED_ORIGINS=https://constellation.example-tailnet.ts.net
OVERWATCH_TRUSTED_PROXIES=127.0.0.1/32,::1/128

OVERWATCH_KEY_HASH_SECRET=<at-least-32-random-characters>
OVERWATCH_ADMIN_EMAIL=operator@your-real-domain.com

OVERWATCH_DATA_DIR=/var/lib/constellation-overwatch
OVERWATCH_BACKUP_DIR=/var/backups/constellation-overwatch
OVERWATCH_BOOTSTRAP_FILE=/var/lib/constellation-overwatch-secrets/bootstrap.txt
```

The base URL and every allowed origin must be an exact HTTPS origin: no path,
query, fragment, credentials, or wildcard. The RP ID must equal the base URL's
host or be its parent DNS domain. Production rejects wildcard bind addresses,
demo email addresses, weak/example key secrets, relative storage, insecure
cookies, and development hot-reload mode.

`OVERWATCH_TRUSTED_PROXIES` is optional. Add only the CIDRs of reverse proxies
that directly connect to the Hub. Forwarding headers from every other peer are
ignored. Never enter the whole tailnet CIDR merely for convenience.

## HTTPS and WebAuthn

Terminate HTTPS at a host-local proxy such as Tailscale Serve and forward to
`http://127.0.0.1:8090`. Configure the single public HTTPS origin in
`OVERWATCH_BASE_URL`, `OVERWATCH_RPID`, and
`OVERWATCH_ALLOWED_ORIGINS` before the first start. Changing those values later
changes the WebAuthn relying party and can make registered passkeys unusable.

Production responses include HSTS, CSP, frame denial, MIME-sniffing denial,
referrer policy, and a restrictive permissions policy. Session and WebAuthn
ceremony cookies are always `Secure`, `HttpOnly`, and `SameSite=Lax` under the
production profile.

## Secure first-run bootstrap

The first start creates the admin and writes its one-time setup URL to
`OVERWATCH_BOOTSTRAP_FILE` with create-once semantics. The URL is never printed
to ordinary logs. Read the file as the service owner through an authenticated
administrative channel, complete passkey enrollment, then securely remove the
file. A pre-existing file is never overwritten.

On Linux and macOS, create the parent directory as the service user with mode
`0700`; the file is created with mode `0600`. On Windows, place it under a
service-owned directory (for example under `C:\ProgramData`) and remove inherited
ACL entries before first start; NTFS ACLs, not POSIX mode bits, are authoritative.

If delivery fails after the admin row is created, the next start detects the
incomplete passkey setup, revokes the prior pending invitation, and mints a new
one. It still refuses to overwrite an existing bootstrap file.

## Operational cautions

- Generate the API-key hash secret with a cryptographic random generator and
  store it in the platform secret/config facility with owner-only access.
- Do not use the development Docker Compose file for production.
- Restrict NATS at both the host firewall and tailnet policy. Pulsars receive
  per-organization NKey credentials and least-privilege directional scopes.
- Back up both SQLite and JetStream state as a coordinated set. Restore and
  restart/replay behavior must be proven during canary characterization before
  promotion beyond the SIM lab Hub.
- Deploy only provenance-stamped artifacts built from an exact commit SHA.
