# Constellation Overwatch Auth and Release Assessment

Assessment date: 2026-07-27
Repository: `Constellation-Overwatch/constellation-overwatch`
PR: `#30` (`feat/fleet-integration-baseline` into `main`)
Candidate: `b54e383344441a814856286614fd3f4f24a3b316`
Published prerelease: `v0.1.6-rc.1`

## Bottom Line

- **INFERRED — HOLD / DO NOT MERGE:** keep PR #30 Draft.
- **INFERRED — RELEASE POSTURE:** retain `v0.1.6-rc.1` as an
  acceptance-pending canary. Do not call it acceptance-cleared or promote it to
  GA.
- **EXTRACTED:** PR #30 remains open and Draft. The `v0.1.6-rc.1` tag resolves
  to `b54e383`, which is outside `main`.
- **EXTRACTED:** exact-head CI, release provenance, SIM install/migration,
  health, unauthenticated redirect, and GCS reconnect are established.
- **EXTRACTED:** authenticated Windows-to-Mac passkey acceptance, deployed
  replay/revoke behavior, third-authenticator scope, rollback/forward restore,
  and soak are not established.
- **EXTRACTED:** exact-head local validation passed `go test ./...` and
  `go vet ./...`.

## What “RC” Means Here

`rc` means release candidate: a prerelease build intended to be a plausible
final build if acceptance finds no release-blocking defect. `rc.1` is the first
candidate iteration, not a quality level awarded merely because an artifact was
published.

Semantic Versioning defines `-rc.1` only as a prerelease identifier with lower
precedence than the associated normal version. It does not define an acceptance
process. Under the team's release contract, the artifact is mechanically named
RC1 but the stack remains beta/canary in maturity because known acceptance work
is still open.

## Latest Candidate State

The shared checkout remains clean at `fbf33ed` and is thirteen commits behind
the remote PR/tag tip. A detached clean worktree was used for exact-head tests.
The post-`fbf33ed` range contains the multi-device passkey, recovery, scoped API
key, runtime-auth, release-provenance, installer-preservation, recovery-audit,
and bootstrap-hygiene work.

The local Pulsar checkout is clean at `d55d750` on
`feat/fleet-integration-baseline`, but the configured remote has no branch of
that name. This is a source-reproduction gap, not proof that the deployed
Pulsars are unhealthy.

## Tobalo Design Intent and Architecture Decision

Decision tier: **Tier 3** because the decision crosses browser identity,
machine-to-machine credentials, network topology, deployment, recovery, and
release governance.

The implementation direction matches the extracted design intent:

1. One singleton Hub on SIM presents one stable HTTPS origin and WebAuthn RP ID.
2. Browser operators authenticate with passkeys and receive separate
   server-side sessions on each machine; sessions are not copied between
   machines.
3. Multiple independent credentials belong to one account. A Windows Hello
   private key is not exported to a Mac.
4. REST API keys and NATS edge credentials remain separate from browser
   session/passkey authentication.
5. Tailscale Serve supplies the network/TLS front door while application
   authentication and authorization remain independent controls.
6. The CLI install and Pulsar binding experience remains stable.

Component contracts:

| Component | Accepts | Produces/guarantees | Must not know or grant |
|---|---|---|---|
| Browser WebAuthn | Exact HTTPS origin, RP-bound assertion | Authenticated user and fresh browser session | NATS/API-key authority |
| Enrollment/recovery | Authenticated admin or narrow grant, target user/org | Short, single-use enrollment authority and audit transition | Role/org changes |
| Tailscale Serve | Tailnet HTTPS/TCP traffic | TLS proxy to loopback services | Application user identity |
| NATS edge auth | Scoped edge credentials | Machine messaging authority | Browser dashboard session |

The architecture is hybrid: stable RP/origin, loopback listeners, separate
credentials, and purpose-bound grants are structural controls; WebAuthn,
session, audit, and revocation decisions are computed controls.

## Exact-Head Security Findings

### Merge blockers

1. **CRITICAL — browser mutation integrity:** cookie-authenticated unsafe
   routes have no centralized CSRF or exact-origin enforcement. `SameSite=Lax`
   does not separate cross-origin sibling nodes that are same-site under the
   `tailnet.ts.net` boundary. Tracked in issue #31.
2. **CRITICAL — live acceptance:** no attributable authenticated
   Windows-to-Mac journey exists for the exact candidate.
3. **IMPORTANT — WebAuthn UV policy:** registration/login leave user
   verification implicit/preferred. Require UV or approve and document a
   possession-only authenticator exception. Tracked in issue #32.

### Time-bounded follow-ups

1. Dashboard session bearer values are stored directly, authorization state is
   snapshotted for up to 24 hours, and there is no user-wide revoke-all path.
   Tracked in issue #33 and overlaps issue #28 recovery/session requirements.
2. Durable audit coverage exists for recovery issue/redeem/revoke but not the
   complete login, credential, session, logout, and revocation lifecycle.
3. Account → Passkeys remains incomplete: credential naming, metadata,
   add-device UI, step-up, and user-visible revocation remain open in issue #28.

### Withdrawn stale findings

Two findings from the stale local checkout do not apply to `b54e383`:

- WebAuthn ceremony state is consumed transactionally and exactly once.
- Invite redemption is transactionally guarded, replay-safe, and audited.

## Live-Fire State

Read-only reachability through the canonical SIM HTTPS origin returned TLS
verified HTTP 200 from PC-1, Mac, PC-Home, GCS1, and GCS2. The login page is
present. This proves fleet routing and TLS reachability; it does not prove
authenticated WebAuthn behavior.

Completed from the prior operation:

- exact candidate publication and commit-bound attestation;
- SIM backup/install/restart/migration;
- health and SQLite integrity;
- unauthenticated dashboard redirect;
- GCS1/GCS2 Pulsar reconnect after Hub restart.

Remaining single-driver sequence:

1. Windows Hello login on PC-1; load authenticated dashboard and SSE.
2. Issue the first live audited `admin_recovery` grant.
3. Redeem on Mac and register a distinct credential.
4. Prove the enrollment-only session cannot act as a dashboard session.
5. Log in normally on Mac; load dashboard and SSE; log out and verify rejection.
6. Log in again on Windows and prove the original credential still works.
7. Prove credential count/coexistence, replay failure, and revocation.
8. Exercise a third authenticator or obtain an explicit scope reduction.
9. Run rollback/forward restore and confirm session/recovery behavior.
10. Run the authenticated soak.

No passkey gesture, email, recovery URL, token, credential identifier, or
session value has been entered or recorded during this assessment. The Mac
driver was asked for an explicit GO/NO-GO or transfer of the standing handoff.

## Discord Review

The channel contains roughly forty GitHub webhook messages from the July 24 CI
and release sequence, including per-matrix success, transient failure/rerun
churn, tag creation, publication, and final green results. This obscures the
release decision.

Yeet's question is answered as follows:

- No, PR #30 was not merged; it is still Draft and `main` is unchanged.
- Yes, `v0.1.6-rc.1` was cut from the feature branch at exact `b54e383` so the
  published/attested artifact could be exercised in live acceptance.
- Yeet is correct about maturity: the stack remains beta/canary until the
  multi-device passkey and recovery gates pass. If team convention reserves
  `rc` for main-based, acceptance-ready cuts, future interim tags should use
  `-beta.N`; cut a new RC only after blockers close.

One consolidated reply is staged in the Discord composer under the team
single-poster rule. Browser automation cannot emit Discord's trusted Enter
event; the operator must press Enter once to send it and clear the duplicate
draft in the older agent tab. No stray message was posted.

Recommended webhook repair: publish one workflow conclusion and one
release/promotion event, suppress per-matrix successes and cancelled/retried
job churn, and route failures to a thread or separate CI channel.

## Issue and Artifact Updates

Created:

- #31 — CSRF and exact-origin enforcement
- #32 — explicit WebAuthn user-verification policy
- #33 — hashed sessions and user-wide revocation

Repaired status:

- #22 now records the implemented/deployed partial production-hardening state
  and links #31.
- #28 now records the landed P0 source work, incomplete live acceptance, P1
  lifecycle work, and links #32/#33.

Independent artifacts:

- `adversary-auth-hypothesis.md`
- `adversary-auth-evaluation.md`
- `adversary-release-hypothesis.md`
- `adversary-release-evaluation.md`

## Reconciliation

Incorporated:

- exact-head test and source verification;
- withdrawal of stale race findings;
- HOLD/keep-Draft release decision;
- explicit CSRF, UV, and session-lifecycle issues;
- parent-issue status repair;
- single-poster Discord noise discipline;
- single-driver live ceremony with no secret-bearing crosstalk.

Deferred:

- product-code fixes for #31–#33, pending maintainer priority and design review;
- authenticated live-fire, pending the user-attached Windows gesture and Mac
  driver's authoritative handoff;
- third-authenticator scope decision;
- webhook configuration mutation.

## WHAT I CANNOT VERIFY

- A user-attended Windows Hello or macOS WebAuthn ceremony.
- Whether SIM still runs exact `b54e383` today; the public health body lacks
  build identity and current identity relies on the prior deployment record.
- Any credential, session, recovery token, or audit row; these were
  intentionally not inspected.
- Rollback/forward restore, authenticated soak, and lost-machine revocation.
- Formal maintainer review/approval.
- The final Discord send until the operator supplies one trusted Enter action.
