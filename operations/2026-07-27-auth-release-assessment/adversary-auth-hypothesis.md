# Independent Auth Architecture Hypothesis

Date: 2026-07-27

Gate state at authorship: **CLOSED**. I had not read git history, prior operation
documents, PR discussion, crosstalk conclusions, or existing recommendations. I
used only the operator problem statement, repository `AGENTS.md`, filenames, and
the exposed auth/config/router interfaces.

## Answer

The intended secure design should be one canonical HTTPS relying-party origin
and DNS RP ID, WebAuthn/passkeys for interactive operator authentication,
persistent server-side opaque browser sessions, and role checks at every
privileged handler. Moving across machines should mean authenticating each
browser with a synced or independently enrolled passkey and receiving a fresh
machine-local session; it should not mean copying a bearer session between
machines.

The browser cookie should be host-only, `Secure`, `HttpOnly`, and an appropriate
`SameSite` value. Session identifiers should be high-entropy, stored as keyed
hashes rather than reusable plaintext, rotated on authentication and privilege
change, revocable, and bounded by both idle and absolute expiry. WebAuthn
ceremony state must be server-side, short-lived, bound to ceremony and user,
single-use, and consumed atomically. Production startup should fail closed
unless the base URL and all allowed origins are exact HTTPS origins, the RP ID
matches the canonical DNS suffix, a stable key-hash secret is present, proxy
trust is explicit, and secure cookies are enabled.

API keys remain a distinct machine-to-machine mechanism and must not silently
grant dashboard sessions. Authorization must cover HTML, JSON mutation, SSE,
metrics, debug, and admin routes—not only navigation visibility.

## Evidence From Raw Constraints and Code Interfaces

- **EXTRACTED:** Operators require authenticated dashboard access while moving
  across multiple machines.
- **EXTRACTED:** Repository guidance identifies API-key, persisted session, and
  WebAuthn/passkey mechanisms, including first-run bootstrap/invite behavior.
- **EXTRACTED:** Runtime interfaces expose RP ID, allowed origins, base URL,
  secure-cookie, key-hash-secret, and trusted-proxy concepts.
- **EXTRACTED:** Router interfaces distinguish public passkey/invite endpoints,
  session-protected dashboard routes, role-protected mutations, admin routes,
  SSE streams, metrics, and debug surfaces.
- **EXTRACTED:** Auth interfaces expose persistent SQLite sessions, random
  WebAuthn ceremony keys, credential lookup, sign-count updates, and cookie
  creation.
- **INFERRED:** A stable canonical RP boundary plus reauthentication on each
  machine is the smallest design compatible with both passkey phishing
  resistance and clean multi-machine use.
- **INFERRED:** Because a browser session is a bearer credential, portability
  of sessions would enlarge theft and replay risk; portable authentication
  should come from the passkey, not the cookie.

## Live-Fire Release/Merge Gate

The merge/release decision should be **FAIL CLOSED** unless all of the following
are demonstrated against the production-shaped binary through the real TLS
reverse-proxy path, using two distinct physical machines or independently
administered browser profiles:

1. **Configuration gate:** invalid HTTP base URL, mismatched RP ID, wildcard or
   malformed origins, absent production key-hash secret, unsafe proxy trust, or
   insecure cookies prevent startup. Evidence includes exact effective config
   with secrets redacted and negative startup transcripts.
2. **Bootstrap and enrollment gate:** a fresh database permits only the intended
   bootstrap flow; an invite is single-use and expiring; the first passkey
   enrollment succeeds; replay, token alteration, and use after expiry fail.
3. **Cross-machine gate:** the same operator authenticates on machine A and
   machine B using a synced passkey or separately enrolled credentials; each
   receives a distinct session; neither session token is transferred; both
   retain the correct role and organization scope.
4. **Authentication-abuse gate:** wrong RP/origin, stale or replayed ceremony,
   cross-user ceremony substitution, malformed payload, parallel finish
   requests, cloned/replayed assertion where detectable, and rate-limit bypass
   attempts fail without creating a session. Ceremony consumption is proven
   atomic under concurrency.
5. **Session gate:** authentication rotates/creates a fresh token; database
   restart preserves only valid sessions; logout revokes immediately; expiry
   rejects; cookie attributes are verified over HTTPS; no token appears in URL,
   response body, logs, telemetry, or plaintext database inspection.
6. **Authorization matrix gate:** unauthenticated, viewer, operator, and admin
   identities are exercised against every protected route class (HTML, JSON,
   mutation, SSE, metrics, debug). Expected allow/deny results are captured,
   including cross-organization object IDs and direct requests that bypass UI
   controls.
7. **Browser-request integrity gate:** cross-origin mutation and login requests
   from an untrusted origin fail; allowed-origin behavior is exact, not suffix
   or reflection based; CSRF defenses are demonstrated for every state-changing
   cookie-authenticated request.
8. **Proxy and availability gate:** spoofed forwarding headers do not evade
   throttling outside configured proxies; legitimate proxy chains resolve the
   client correctly; login throttling is shared or otherwise effective for the
   deployed topology; protected SSE connections do not outlive logout or
   session expiry beyond a documented bounded interval.
9. **Evidence gate:** targeted auth/security tests, race-enabled concurrency
   tests, `go vet`, full Go tests, and a production build pass at the exact
   candidate commit. The evidence bundle records commit, binary digest,
   environment shape, commands, timestamps, expected results, actual results,
   and residual exceptions.

Any CRITICAL authorization bypass, credential/session disclosure, replay that
creates a session, cross-organization access, production fail-open
configuration, or inability to reproduce the two-machine path is a hard stop.
IMPORTANT findings require correction or a named owner, time-bounded exception,
and compensating control approved before release. Suggestions may be deferred
with rationale.

## One Rejected Alternative

**Rejected:** make an external OIDC identity provider or VPN identity the sole
dashboard authentication system now. It could centralize multi-machine access,
but it adds a new availability and trust dependency, does not by itself prove
route-level authorization or secure browser sessions, and discards the
passkey/session interfaces already exposed by the local design. It should be
reconsidered only if fleet-wide lifecycle requirements exceed what local
passkeys, invites, and revocable sessions can safely administer.

## Falsifier

This hypothesis is falsified if the required deployment cannot present one
stable HTTPS RP ID/origin to all operator machines, if operators must roam among
independent dashboard instances that cannot share identity state, or if the
actual requirement is centralized enterprise account lifecycle and immediate
global revocation across many instances. In those cases, a central federation
layer (for example OIDC with phishing-resistant upstream authentication) is the
better trust boundary, and local WebAuthn should not be treated as the primary
cross-machine identity plane.

## Pre-Commit Self-Adversary

> [ADVERSARY]
>
> **Challenge 1 — Lazy path:** “Passkeys + secure cookies + RBAC” is generic
> security prose unless the gate proves binding, atomic consumption, complete
> route coverage, and production configuration.
>
> **Response/refinement:** The gate names ceremony substitution/concurrency,
> cookie/database inspection, role/org matrices across every route class, and
> negative startup cases as evidence-producing tests.
>
> **Challenge 2 — Fragility path:** The design can still fail behind a proxy,
> across concurrent finish requests, on long-lived SSE connections, or when a
> user moves to a second machine without a synced authenticator.
>
> **Response/refinement:** The gate requires the real proxy path, atomic replay
> testing, bounded SSE revocation behavior, and explicitly permits separately
> enrolled per-machine credentials instead of assuming passkey sync.
>
> **Challenge 3 — Alternative path:** Central OIDC could provide cleaner
> lifecycle and cross-machine identity.
>
> **Response/refinement:** OIDC is the one rejected alternative above, but the
> falsifier makes central federation mandatory if the deployment lacks a stable
> RP boundary or requires global multi-instance lifecycle.
>
> **Confidence verdict:** **HYPOTHESIS** until current implementation and
> production-shaped live-fire evidence are inspected.
