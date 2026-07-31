# Tier-3 Auth Logic and Security Evaluation

Assessment date: 2026-07-27
Repository: `Constellation-Overwatch/constellation-overwatch`
PR: `#30` — `feat/fleet-integration-baseline` into `main`
Authoritative candidate: `b54e383344441a814856286614fd3f4f24a3b316`
Published candidate: `v0.1.6-rc.1`

## Verdict

**REJECT / HOLD MERGE.** Retain `v0.1.6-rc.1` only as an
acceptance-pending canary.

The architecture direction is sound and aligned with the local design intent:
one production Hub behind a canonical Tailscale HTTPS origin, WebAuthn
authentication, separate per-browser server-side sessions, multiple credentials
per account, scoped recovery enrollment, and role/organization enforcement.
Exact-head automated evidence is materially stronger than the stale local
checkout suggested.

The candidate is not merge-ready for two independent reasons:

1. **CRITICAL — code:** cookie-authenticated mutation routes have no CSRF or
   exact-origin enforcement. `SameSite=Lax` does not protect against an HTTPS
   sibling origin within the same tailnet site, and multiple operator/admin
   handlers accept simple form POSTs.
2. **CRITICAL — acceptance evidence:** the attributable live record proves
   exact-candidate deployment, health, unauthenticated redirect, and
   multi-machine reachability, but not an authenticated Windows-to-Mac
   passkey/recovery/enrollment/login path.

**Confidence:** HIGH for the source findings and HOLD decision; HYPOTHESIS for
actual authenticator interoperability until live ceremonies run.

## Source Correction and Withdrawn Findings

The shared checkout is stale at `fbf33ed`; PR/tag head is `b54e383`, thirteen
commits later. All final findings below use `git show b54e383:<path>` or a
detached `b54e383` worktree.

I withdraw two provisional findings produced from the stale checkout:

- **WITHDRAWN:** non-atomic WebAuthn ceremony consumption. At `b54e383`,
  `GetWebAuthnSession` uses a transaction, deletes exactly one row, checks
  `RowsAffected`, fails closed on malformed expiry, and commits only after
  decoding (`api/services/auth.service.go:426-494`). The exact-head test
  `TestGetWebAuthnSessionConsumesExactlyOnce` covers one-time use.
- **WITHDRAWN:** non-atomic invitation redemption. At `b54e383`,
  `AcceptInvite` transitions only `pending`, unexpired rows, verifies the target
  user, checks one affected row, writes the audit record in the same
  transaction, and commits (`api/services/invite.service.go:251-323`). Exact-head
  tests cover concurrent single use, revoke, actor/organization checks, and
  audit rollback.

These are incorporated controls, not residual findings.

## Severity-Ranked Findings

### CRITICAL C-1 — Same-tailnet CSRF can exercise privileged form mutations

**Evidence**

- **EXTRACTED:** session and ceremony cookies are host-only, `Secure`,
  `HttpOnly`, and `SameSite=Lax`, but there is no CSRF-token, `Origin`,
  `Referer`, or Fetch Metadata middleware at `b54e383`
  (`api/middleware/auth.go:229-251`,
  `pkg/services/web/handlers/auth.go:340-363`; exact-head grep finds no CSRF
  enforcement).
- **EXTRACTED:** the session-protected router exposes state-changing form
  endpoints, including admin organization creation and operator entity/fleet
  creation (`pkg/services/web/router.go:117-138`).
- **EXTRACTED:** those handlers call `r.ParseForm()` and mutate state before
  response rendering (`pkg/services/web/handlers/datastar.go:81-128`,
  `397-495`, `707-784`). An ordinary cross-origin HTML form can send this
  request; CORS response rules do not prevent the form submission.
- **EXTRACTED:** the intended production hostname is of the form
  `node.tailnet.ts.net`. The current Public Suffix List contains `ts.net`, so
  HTTPS sibling nodes under one `tailnet.ts.net` registered domain are
  same-site. The current cookie specification states that `Lax` cookies are sent
  on same-site requests.
- **INFERRED:** a compromised or attacker-controlled HTTPS application on a
  sibling tailnet node can cause an authenticated operator/admin browser to
  submit a form to the Hub. The target Hub receives its host-only session cookie
  because the request is same-site even though it is cross-origin. This can
  create or alter operational state under the victim's role.

**Why CRITICAL**

The target is a C4 dashboard and the reachable handlers create organization and
entity state. Tailnet membership and application RBAC are separate security
boundaries; a compromised peer must not inherit a browser operator's authority.

**Required correction**

Apply a centralized unsafe-method guard to every cookie-authenticated web route:
require the exact validated `OVERWATCH_BASE_URL` origin (and use a synchronizer
or well-designed double-submit token where origin is absent or insufficient).
Reject unsafe requests before handler parsing. Make logout POST-only. Add
negative tests for an HTTPS sibling origin, missing/malformed origin, form POST,
JSON POST, PUT, and DELETE, plus positive exact-origin tests. Do not treat CORS
headers or `SameSite` as the complete control.

Primary external references:

- Public Suffix List Tailscale entries:
  `https://raw.githubusercontent.com/publicsuffix/list/master/public_suffix_list.dat`
  (Tailscale section includes `ts.net`).
- HTTP Working Group cookie draft:
  `https://httpwg.org/http-extensions/draft-ietf-httpbis-rfc6265bis.html`
  (`Lax` cookies are sent on same-site requests).

### CRITICAL C-2 — The release-defining authenticated multi-machine path is not proven

- **EXTRACTED:** the independent source-integrity evaluation records exact
  `b54e383` installation on SIM, migration, SQLite quick-check, HTTP health,
  unauthenticated `/overwatch` redirect, and GCS reconnect.
- **EXTRACTED:** current telemetry records successful TLS/HTTP reachability from
  five machines but says no passkey or secret action was taken.
- **EXTRACTED:** the authoritative live-acceptance operation remains
  `in_progress`; no attributable record proves Windows login, recovery grant,
  Mac credential enrollment, Mac login, subsequent Windows login, authenticated
  dashboard/SSE, or deployed replay/revoke behavior.
- **INFERRED:** reachability and fail-closed redirect are prerequisites, not
  evidence that the RP ID, origin, authenticator, enrollment, and persistent
  credential contracts work across the actual machines.

**Required correction:** close the live-fire gate below against exact
`b54e383` (or a newer frozen candidate) and attach redacted, reproducible
evidence to the PR/operation. A private or unregistered operator claim does not
close this gate.

### IMPORTANT I-1 — User verification is preferred, not required

- **EXTRACTED:** `NewWebAuthnWithOrigins` sets RP name, RP ID, and origins but no
  `AuthenticatorSelection.UserVerification`
  (`api/services/auth.service.go:94-108`).
- **EXTRACTED:** login uses `BeginLogin` without a
  `WithUserVerification(VerificationRequired)` option
  (`pkg/services/web/handlers/auth.go:66-74`).
- **EXTRACTED:** browser fallbacks explicitly use `"preferred"` for login and
  registration (`pkg/services/web/static/js/passkey.js:108,208`), and the fake
  response does the same (`pkg/services/web/handlers/auth.go:313-325`).
- **INFERRED:** the server does not require the WebAuthn UV flag. An authenticator
  that permits user-presence-only assertions may authenticate without a local
  PIN/biometric verification. Synced platform passkeys will often perform UV,
  but that ecosystem behavior is not an application policy.

**Recommendation:** require UV for registration and authentication, test a
no-UV assertion rejection, and prove Windows/macOS/roaming-authenticator
compatibility. If possession-only security keys are an explicit operational
requirement, record the threat-model exception and compensating physical-token
policy rather than relying on an implicit default.

### IMPORTANT I-2 — Dashboard bearer sessions remain plaintext and stale for up to 24 hours

- **EXTRACTED:** random session tokens are inserted directly into
  `sessions.session_token` and queried by the cookie value
  (`api/middleware/auth.go:54-81,100-125,157-183`;
  `db/schema.sql:257-266`).
- **EXTRACTED:** role and organization are copied into the session at creation
  and trusted from that row for the fixed 24-hour lifetime. There is no
  user-wide session revocation method; logout deletes only the presented token
  (`api/middleware/auth.go:128-133`).
- **EXTRACTED:** enrollment sessions are substantially better: they expire
  after ten minutes and are destroyed after successful registration
  (`api/middleware/auth.go:18-20,64-70`;
  `pkg/services/web/handlers/auth.go:262-277`).
- **INFERRED:** theft of a live SQLite copy or backup yields directly reusable
  dashboard bearer tokens, and a lost machine or emergency role change lacks an
  application-level revoke-all path. Private data-directory permissions reduce
  likelihood but do not remove backup/exfiltration risk.

**Recommendation:** store a keyed hash of each session token, provide
user/session inventory and revoke-one/revoke-all operations, and either
re-resolve authorization from current user state or invalidate all sessions on
role/org/status changes. Define idle and absolute expiry. For this merge, these
controls require correction or a named, time-bounded exception plus an
exercised incident runbook.

### IMPORTANT I-3 — Authentication observability is incomplete

- **EXTRACTED:** `b54e383` adds transactional audit records for recovery invite
  issue/redeem/revoke and correctly rolls back state when audit writes fail.
- **EXTRACTED:** no equivalent durable audit events exist for successful/failed
  WebAuthn login, credential registration, session issuance/logout, or
  credential removal. Credential removal is not yet a complete user-facing
  lifecycle.
- **INFERRED:** operators cannot fully answer which credential authenticated on
  which machine, which sessions remain live, or whether a recovery enrollment
  was followed by expected login behavior without correlating ephemeral logs.

**Recommendation:** add privacy-minimized security events with stable action
names and no credential/session secrets. Include source IP only under a
documented retention policy. The live gate should validate aggregate event
shape, not expose identifiers or tokens in evidence.

### IMPORTANT I-4 — Automated coverage is strong but omits the remaining policy boundaries

- **EXTRACTED:** exact-head CI runs full tests, `go test -race -count=1 ./...`,
  vet, release validation, install tests, and backup/restore drills.
- **EXTRACTED:** exact-head unit/service tests cover credential exclusions and
  uniqueness, ceremony single use, short enrollment sessions, recovery role
  preservation, atomic invite replay/revoke, authorization, and audit rollback.
- **EXTRACTED:** there are no exact-head CSRF/origin, required-UV, session
  hashing, user-wide revocation, or real browser/authenticator tests.
- **INFERRED:** green CI supports candidate quality but cannot close C-1, I-1,
  I-2, or C-2.

### SUGGESTION S-1 — Reduce invite/auth response and logging exposure

Auth and invite pages do not consistently emit `Cache-Control: no-store`.
Invite tokens occur in URLs, and the global referrer policy permits full
same-origin referrers. Use `no-store` and a stricter referrer policy on
token-bearing pages. Remove personal email values from routine production logs
or document and enforce their retention.

### SUGGESTION S-2 — Make session cleanup lifecycle-bound

`NewSessionAuthWithCookieSecurity` starts a cleanup goroutine with no
context/stop path (`api/middleware/auth.go:45-51,136-143`). This is not an auth
bypass, but it complicates repeated server construction and graceful lifecycle
proof. Bind cleanup to the service context.

## Incorporated Recommendations

The exact PR head already incorporates the following independent-hypothesis
requirements:

- one canonical production Hub/origin and validated HTTPS/RP configuration;
- loopback listeners behind the intended Tailscale proxy;
- production `Secure`, `HttpOnly`, host-only session and ceremony cookies;
- persisted server-side sessions, distinct per browser;
- random opaque WebAuthn user handles and ceremony keys;
- exact-origin WebAuthn validation;
- transactionally single-use WebAuthn ceremony state;
- credential exclusion plus global unique credential IDs;
- multiple credentials per user;
- short, enrollment-only recovery sessions that never become full sessions;
- recovery grants that preserve the existing role and organization;
- atomic, expiring, single-use, audited recovery invite lifecycle;
- trusted-proxy-chain parsing from the right;
- role and organization enforcement across protected route classes;
- exact-head race tests, portable builds, release provenance, install, and
  backup/restore automation.

These controls materially improve the verdict from “architecture unproven” to
“credible RC canary with specific unresolved gates.”

## Deferred Recommendations

The following may be deferred from this merge only with explicit owner,
deadline, and compensating control:

- hashed-at-rest dashboard session tokens;
- user-visible session inventory and revoke-one/revoke-all;
- full login/session/credential security-event auditing;
- auth/invite `no-store` and stricter token-page referrer policy;
- cleanup-goroutine lifecycle binding.

The following are **not deferrable** under the current secure-auth and
multi-machine acceptance intent:

- centralized CSRF/exact-origin enforcement for unsafe cookie-authenticated
  requests;
- an explicit UV policy (required, or an approved threat-model exception);
- attributable exact-candidate multi-machine authenticated live fire.

Migration to an external OIDC provider remains deferred. The local
WebAuthn/session architecture is appropriate for the current singleton Hub;
federation should be reconsidered only if identity lifecycle must span multiple
independent Hubs.

## Merge Blockers Versus Follow-Ups

- **MERGE BLOCKER — C-1 CSRF:** this is an unowned exact-head application
  boundary, not merely missing live evidence. It needs centralized
  unsafe-method origin/CSRF enforcement and route-level tests before merge.
- **MERGE BLOCKER — C-2 multi-machine live acceptance:** this is explicitly
  required by issue #28 and the existing operation/PR contract.
- **MERGE DECISION BLOCKER — I-1 UV policy:** the candidate silently inherits
  `preferred`. Before merge, maintainers must either require UV in source and
  prove the supported authenticators, or explicitly approve a possession-only
  authenticator threat model and record its controls. Leaving the policy
  implicit is not an acceptable follow-up.
- **TIME-BOUNDED FOLLOW-UPS — I-2/I-3:** keyed session-token storage,
  revoke-one/revoke-all, current-role invalidation, and fuller auth auditing can
  follow this merge only with owners, deadlines, and an exercised lost-machine
  incident procedure. Issue #28 already owns much of that lifecycle; do not
  close it after P0 source/live acceptance alone.
- **ORDINARY FOLLOW-UPS — S-1/S-2:** no-store/referrer/log minimization and
  cleanup lifecycle binding.

## Existing-Issue Overlap

### Issue #22 — fail-closed production configuration and secure bootstrap

- **Direct overlap already implemented at `b54e383`:** explicit production
  configuration, exact HTTPS base/RP/origin validation, required key-hash
  secret, secure bootstrap file, loopback/proxy deployment, secure cookies,
  CSP/HSTS, trusted-proxy parsing, and negative production tests.
- **C-1 is adjacent but not actually owned by #22's current wording.** Its
  “origin behavior” criterion and discussion concern WebAuthn/CORS/proxy
  configuration. They do not specify application-layer CSRF protection for
  unsafe cookie-authenticated form routes. Update #22 with an explicit
  unsafe-method exact-origin/CSRF criterion or open a dedicated security issue;
  do not mark #22 complete while treating CORS as this control.
- **I-1 is not currently owned by #22.** Exact RP/origin configuration does not
  decide WebAuthn user-verification policy.
- **Status implication:** #22 should remain open through the production-shaped
  auth gate; its stale “not merged/deployed” update should be reconciled with the
  published and partially deployed RC.

### Issue #28 — secure multi-device enrollment and recovery

- **Direct overlap substantially implemented at `b54e383`:** removal of the
  second-device deadlock, `WithExclusions`, global credential-ID uniqueness,
  current-user ceremony binding, one-time ceremony consumption, short scoped
  enrollment sessions, role/org-preserving recovery, atomic audit lifecycle,
  and preservation of the original credential.
- **C-2 is exactly #28 closure scope.** Its acceptance criteria require a live
  Windows-existing → Mac-new credential journey, login with either credential,
  safe replay/expiry/user/org behavior, and live two-device closure evidence.
- **I-2/I-3 overlap #28 P1 and security notes:** credential listing/naming,
  lost-credential revocation, add-device step-up, recovery-session review, and
  audit visibility remain follow-up scope. Existing dashboard session inventory
  and revoke-all are natural extensions but are not fully specified.
- **I-1 is adjacent, not explicit.** #28 requires synced-provider/hardware-key
  compatibility and fresh passkey assertions, but does not say whether UV is
  `required` or `preferred`. Add that decision to #28 or a focused auth-policy
  issue before merge.
- **Status implication:** do not close #28 on the P0 implementation alone. At
  minimum the live two-device gate must pass; remaining P1 items should stay
  open in #28 or be split into linked issues with preserved acceptance criteria.

## Live-Fire Merge/Release Gate

All evidence must identify the candidate SHA, archive digest, binary version,
machines, origin, commands/actions, timestamps, expected result, actual result,
and redaction method. Screenshots are supporting evidence, not sufficient
provenance.

### G0 — Candidate and source integrity

- Freeze one candidate; remote PR head, tag, release asset, installed binary,
  and evidence bundle must resolve to the same SHA.
- Use a clean worktree. Record `git status`, version output, archive digest,
  checksum, and provenance verification.
- Any source/tag/binary drift is **FAIL**.

### G1 — Automated security gate

- Full tests, Linux race tests, vet, generated-template check, release config,
  installer, and backup/restore tests pass.
- Add and pass: unsafe-method origin/CSRF matrix; no-UV assertion rejection;
  one-time ceremony concurrency; one-time recovery accept/revoke; session
  revocation; role/org session invalidation; route-level role/org matrix.
- A test that reaches only a helper without the production router/middleware
  chain does not close the route gate.

### G2 — Production topology and negative configuration

- Deploy the exact candidate as one singleton SIM Hub behind the real
  host-local Tailscale HTTPS proxy. Backend web and NATS listeners remain
  loopback-only.
- Record the exact HTTPS origin, RP ID, allowed origins, trusted proxy CIDRs,
  and redacted effective configuration.
- Prove startup rejects HTTP origin, RP mismatch, wildcard/malformed origin,
  wildcard bind, missing/weak hash secret, insecure mode, and unsafe
  storage/bootstrap paths.
- Prove direct non-proxy access is unavailable from client machines and
  forwarding-header spoofing does not change the rate-limit identity.

### G3 — Fresh bootstrap and recovery integrity

- On a disposable production-shaped database, retrieve the bootstrap URL only
  through the protected bootstrap file, enroll the first passkey, remove the
  file, and log in.
- Prove expired, modified, replayed, revoked, wrong-user, and concurrently
  redeemed grants create no second enrollment session or audit transition.
- Prove recovery preserves the account's current role/org and issues a
  ten-minute enrollment-only session; completing enrollment destroys that
  session and requires a normal WebAuthn login.
- Redacted audit aggregates must show issue/redeem/revoke exactly once without
  exposing tokens, session IDs, credential IDs, or email.

### G4 — Two-machine operator journey

Use two distinct named machines/browser profiles (the required Windows and Mac
path) through the real HTTPS origin:

1. Authenticate the existing Windows credential; load `/overwatch` and one
   authenticated SSE stream.
2. Issue the short audited recovery enrollment grant.
3. Redeem on Mac and register a distinct Mac/synced/roaming credential.
4. Confirm the enrollment session redirects to normal login and cannot access
   the dashboard.
5. Log in normally on Mac; load the dashboard and SSE.
6. Log out Mac and prove its session is rejected immediately.
7. Log in again on Windows and prove the original credential still works.
8. Confirm credential count increased without duplicate IDs.
9. Exercise the additional authenticator path required by the acceptance
   contract, or record an explicit approved scope reduction.

No session cookie may be copied between machines.

### G5 — Authentication-abuse and browser integrity

- Wrong RP ID/origin, stale challenge, ceremony replay, cross-user ceremony
  substitution, malformed assertion, and concurrent finish requests fail
  without issuing a session.
- A malicious form and fetch from an HTTPS sibling
  `*.tailnet.ts.net` origin are rejected before handler execution for every
  unsafe method. Exact-origin requests continue to work.
- Verify the returned WebAuthn options require UV and a no-UV assertion fails,
  unless the approved threat model explicitly permits possession-only keys.
- Inspect response headers/cookies: host-only, `Secure`, `HttpOnly`, correct
  `SameSite`, no secret in URL/body/log/telemetry, and no sensitive response
  caching.

### G6 — Authorization and organization matrix

Exercise unauthenticated, enrollment-only, viewer, operator, and admin sessions
against HTML, form mutation, JSON mutation, SSE, metrics, debug, and admin
routes. For non-admin roles, test both same-org and cross-org object IDs and
direct requests that bypass navigation controls. Unknown/missing roles fail
closed. Any cross-org read/write or privilege bypass is **FAIL**.

### G7 — Session, restart, restore, and incident response

- Distinct machines receive distinct sessions; logout revokes only the selected
  session as documented.
- Prove user-wide revocation or execute the approved lost-machine incident
  runbook; the lost session must stop working within the stated bound.
- Prove role/org/status change invalidates or re-evaluates existing sessions
  within the approved bound.
- Restart the Hub and verify only live sessions survive; expired/revoked
  sessions fail. Perform rollback/forward restore and document whether restoring
  an older database can resurrect a previously live session.
- Run the required authenticated soak with bounded SSE behavior through session
  expiry/revocation.

### G8 — Decision rule

- Any CSRF state mutation, authentication/authorization bypass, credential or
  session disclosure, replay that issues another session, cross-org access,
  production fail-open, exact-candidate drift, or failure of the required
  Windows/Mac journey is a hard **NO-GO**.
- IMPORTANT findings require correction or a named owner, expiry date,
  compensating control, and explicit approver.
- Only SUGGESTION findings may be deferred by ordinary rationale.
- Update PR #30, issue #28, and the live operation with one reconciled gate
  record before changing Draft/merge status.

## Residual Risks

- Browser and authenticator behavior varies across Windows Hello, macOS
  platform credentials, synced passkey providers, roaming keys, and hybrid
  transport.
- Recovery-link possession remains an account-takeover boundary even with
  strong transactionality; delivery and operator handling matter.
- A restored SQLite backup may restore sessions or recovery state unless the
  restore procedure explicitly invalidates it.
- Tailscale ACLs reduce exposure but do not replace origin, CSRF, application
  authorization, or session controls.
- Local WebAuthn is tied to one stable RP boundary. Renaming or splitting the Hub
  can strand credentials.

## Independent Hypothesis Delta

- **My hypothesis was:** canonical HTTPS WebAuthn plus fresh per-machine
  server-side sessions is the right local architecture; release must fail closed
  on replay, cross-role/org access, production misconfiguration, and an
  unproven two-machine journey.
- **Existing implementation is:** substantially aligned, with stronger recovery
  transactionality, multi-device enrollment, audit rollback, release
  provenance, and race coverage than the stale checkout exposed.
- **Agreement:** the singleton Hub and passkey-not-session portability model is
  correct. The independent source adversary independently reached the same
  HOLD decision because authenticated live acceptance is unfinished.
- **Delta:** prior work caught and fixed the ceremony/invite races and proxy
  parsing I initially flagged. My exact-head review adds the remaining
  same-tailnet CSRF boundary, explicit UV-policy gap, and dashboard-session
  at-rest/revocation gap.
- **Final verdict:** **REJECT / HOLD**, with the architecture approved in
  direction but not the current merge.

## Pre-Close Self-Adversary

> [ADVERSARY]
>
> **Challenge 1 — Lazy path:** “Add CSRF, require UV, run two machines” could be
> generic security advice detached from this topology.
>
> **Rebuttal/refinement:** C-1 traces the exact RC router and form handlers to
> `SameSite=Lax` cookies and the current `ts.net` public-suffix boundary. G4 names
> the existing Windows→recovery→Mac→Windows contract rather than a generic
> browser smoke test.
>
> **Challenge 2 — Fragility path:** The CSRF scenario requires an attacker or
> compromised HTTPS sibling inside the same tailnet, and some authenticators
> always perform UV even when the server says preferred.
>
> **Rebuttal/refinement:** Those conditions reduce likelihood but do not remove
> either application-policy gap. A C4 dashboard explicitly separates tailnet
> reachability from operator/admin authority, and the server must validate UV
> rather than infer it from common clients. UV remains IMPORTANT, while
> privileged same-site state mutation remains CRITICAL.
>
> **Challenge 3 — Alternative path:** Merge the green exact-head RC behind
> Tailscale ACLs, then fix application controls and perform live acceptance
> post-merge.
>
> **Rebuttal/refinement:** That silently weakens the PR's own pre-merge
> multi-machine acceptance contract and leaves a known privileged browser
> boundary open. If maintainers intentionally reclassify the contract, they must
> do so explicitly; no current authoritative scope change exists.
>
> **Confidence verdict:** **HIGH** for exact-head source findings and HOLD;
> **HYPOTHESIS** for runtime browser/authenticator outcomes.

## WHAT I CANNOT VERIFY

- Whether user-attended passkey live fire completed after the last attributable
  operation/telemetry record.
- Whether SIM currently still runs exact `b54e383`; the public health response
  has no build identity.
- Actual Windows Hello, macOS, synced-provider, roaming-key, or hybrid-transport
  UV behavior.
- The installed Hub's database/bootstrap/backup filesystem permissions and
  Windows ACLs.
- Direct session, credential, recovery-token, or audit rows; secrets and PII
  were intentionally not inspected.
- Whether a specific sibling Tailscale node currently serves attacker-controlled
  HTTPS content; C-1 is a source-proven missing boundary with a deployment-shaped
  exploit precondition, not a claim that exploitation occurred.
- Local `go test -race` on Windows: the local Go toolchain reported that race
  requires CGO and no GCC was installed. Exact-head CI reportedly passed Linux
  race tests; locally, detached `b54e383` `go test ./...` and `go vet ./...`
  both passed.
- PR maintainer review/approval, rollback-forward restore, authenticated soak,
  and the final registered live-fire evidence.
