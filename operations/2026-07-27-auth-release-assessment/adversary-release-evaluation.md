# Tier-3 Source-Integrity and Release Evaluation

Assessment date: 2026-07-27
Repository: `Constellation-Overwatch/constellation-overwatch`
PR: `#30` — `feat/fleet-integration-baseline` into `main`
Authoritative candidate: `b54e383344441a814856286614fd3f4f24a3b316`
Published candidate: `v0.1.6-rc.1`

## Verdict

- **INFERRED — MERGE: HOLD / DO NOT MERGE.** Keep PR #30 Draft. The implementation, exact-head CI, release provenance, and partial SIM canary are strong, but the branch's own release-defining `multi-machine-live-acceptance` gate is still `in_progress`.
- **INFERRED — RELEASE LEVEL: retain `v0.1.6-rc.1` as a prerelease canary under test.** The evidence justifies a first published candidate for live acceptance. It does **not** justify describing RC1 as acceptance-cleared, promoting it to a higher RC/GA level, or using its existence as merge approval. If local terminology uses RC levels as approval stages rather than candidate iterations, the branch remains pre-acceptance/RC0.
- **INFERRED — CONFIDENCE:** HIGH that merge should remain blocked on the currently attributable record; HYPOTHESIS only that authenticated cross-machine WebAuthn behavior will pass once exercised.

## Severity-Ranked Findings

### BLOCKER — No attributable cross-machine authenticated dashboard live fire

- **EXTRACTED:** Operation `1d58b75a-4037-46c8-a7f1-85727461ceed` records gate `multi-machine-live-acceptance` as `in_progress`, operation status `active`, and last authoritative update `2026-07-24T16:49:07Z`.
- **EXTRACTED:** Context version 65 proves exact `b54e383` was installed on SIM from the published Linux archive; migration and SQLite `quick_check` passed; `/health` returned 200; a Mac browser reached the SIM origin and was redirected unauthenticated to `/login`.
- **EXTRACTED:** That same context explicitly leaves authenticated dashboard/SSE, recovery/audit, Windows/Mac/additional authenticator, rollback/forward restore, and soak pending. It records one existing credential and **no passkey audit actions** after migration.
- **EXTRACTED:** Context version 66 proves both GCS Pulsars disconnected and reconnected around the Hub restart, but explicitly says authenticated application-level consumption/SSE was pending.
- **EXTRACTED:** A fresh unauthenticated probe on 2026-07-27 returned `200` from `https://galaxysim-system.tail8e4fe5.ts.net:8443/health` and `302` from `/overwatch` to `/login`.
- **INFERRED:** Health, redirect, migration, and NATS reconnect prove deployment reachability and fail-closed access. They do not prove that a Windows credential can authenticate, issue an audited recovery grant, enroll a distinct Mac passkey, preserve the Windows passkey, or authenticate the dashboard from both machines.
- **Required closure evidence:** exact-candidate identity; Windows login; authenticated dashboard and SSE; audited 15-minute `admin_recovery` issuance; Mac registration and login; subsequent Windows login; credential-count preservation/increase; negative replay and revoke behavior; one additional authenticator path or an explicit approved scope reduction; rollback/forward restore; soak; redacted audit aggregates. Evidence must name the distinct machines, timestamps, origin, and exact candidate.

### HIGH — The local checkout is not the PR tip

- **EXTRACTED:** The shared local checkout is at `fbf33ed2c50753279b1cdc9797464ba836b4fd84`.
- **EXTRACTED:** GitHub PR #30 head, the remote feature branch, and tag `v0.1.6-rc.1` all resolve to `b54e383344441a814856286614fd3f4f24a3b316`.
- **EXTRACTED:** The PR-tip range adds fourteen commits after the local checkout, including the multi-device enrollment, recovery-audit, runtime-auth, release-provenance, and bootstrap-hygiene changes.
- **INFERRED:** Any local test, source citation, or release recommendation made from the current checkout without an explicit remote-SHA source is stale and cannot validate PR #30. This is an assessment/reproduction integrity problem, not evidence that the remote PR itself is corrupt.

### HIGH — Status assertions have drifted across PR, issue, and operation records

- **EXTRACTED:** PR #30 still says publication, installation, and live acceptance “will” occur, although `v0.1.6-rc.1` was published and partially deployed later that day. Its statement that live acceptance remains required is still correct.
- **EXTRACTED:** Issue #28 remains open appropriately, but its validation section says no patch has been implemented or deployed, which is now false.
- **EXTRACTED:** The operation's `current_focus` still says “Phase 0” even though source, publication, and partial deployment advanced; its owner registry row is `STALE`.
- **INFERRED:** There is no single current status artifact that cleanly distinguishes completed publication/SIM canary from incomplete authenticated acceptance. This invites a future reader to over-credit or under-credit the candidate. Before merge review, update the PR/issue with the exact partial-live result and a concise remaining checklist.

### MEDIUM — Green automated tests do not cover the missing browser/device boundary

- **EXTRACTED:** Exact-head PR run `30108142169`, job `89533577108`, succeeded at `b54e383` for modules, generated templates, formatting, `go test ./...`, `go test -race -count=1 ./...`, vet, GoReleaser validation, backup/restore drill, and installer-preservation drill. All portable build jobs also succeeded.
- **EXTRACTED:** The inspected exact-head source uses `WithExclusions`, requires the unique credential-ID index, maps duplicate insert conflicts, binds ceremonies to the current user, consumes registration sessions once, destroys scoped enrollment sessions after registration, and atomically audits invite issue/redeem/revoke transitions.
- **EXTRACTED:** The exact-head tests cover exclusions, duplicate credential IDs, missing unique-index fail-closed behavior, one-time ceremony consumption, short-lived scoped sessions, recovery role preservation, audit rollback, atomic replay rejection, revoke authorization, and lifecycle auditing.
- **EXTRACTED:** These are Go service/handler tests; no test performs a real `navigator.credentials.create/get` ceremony against the production Tailscale HTTPS origin with Windows Hello and macOS authenticators.
- **INFERRED:** Automated evidence makes the implementation a credible candidate and reduces code-level risk, but cannot close RP-ID/origin, browser cookie, authenticator selection, user-presence, and real credential-coexistence risk.

### MEDIUM — Merge-governance review is incomplete for a broad security/release change

- **EXTRACTED:** PR #30 is Draft, has no GitHub review decision and no recorded GitHub reviews or requested reviewers.
- **EXTRACTED:** The PR is 28 commits ahead of `main`, changes 115 files, and includes authentication, authorization, production configuration, embedded NATS, state persistence, generated web output, installers, and release workflows.
- **EXTRACTED:** The maintainer's PR comment specifically requires explicit review of the release-policy/workflow change and says no ready-for-review transition or merge is planned before prerelease and live acceptance results.
- **INFERRED:** Independent agent reviews and green CI are useful engineering evidence but do not replace the explicit maintainer review the PR itself requires.

## What Is Positively Established

- **EXTRACTED:** Remote feature branch, PR head, and RC tag agree on exact SHA `b54e383`.
- **EXTRACTED:** Exact-head CI and portable builds are green; the release workflow published 13 assets: six archives, six SBOMs, and one checksum manifest.
- **EXTRACTED:** GitHub release metadata exposes digests for all assets. The Linux-amd64 archive digest is `deae825d6155d56629a78e4d486918b131774ed0bf333c0c705b805085d79d5b`, matching the operation's deployed-archive record.
- **EXTRACTED:** The tag-push release workflow was the sole publisher. Its GoReleaser and same-workflow provenance-attestation steps succeeded; the dispatch-only “Attest existing release” job was correctly skipped for that event.
- **EXTRACTED:** The candidate installed and started on SIM at the exact version/commit recorded by the driver; production migration and health passed; the existing credential count remained one; GCS Pulsars reconnected after restart.
- **INFERRED:** Source, test, packaging, publication, provenance, migration, and basic network/canary readiness are sufficient to retain RC1 as a live-test artifact.

## Assertion/Source Drift Map

| Assertion | Authoritative source | State |
|---|---|---|
| “Local feature branch is latest” | local `HEAD=fbf33ed`; remote PR/tag=`b54e383` | **DRIFTED / false locally** |
| “RC1 has not been published” | GitHub release `v0.1.6-rc.1`, published 2026-07-24 16:37:49Z | **DRIFTED / false** |
| “Nothing has been deployed” | operation contexts v65–v66 | **DRIFTED / false; partial deploy occurred** |
| “Authenticated multi-machine acceptance passed” | operation gate and contexts v65–v66 | **UNSUPPORTED / no evidence** |
| “Green CI proves Windows-to-Mac authentication” | exact-head CI plus source/test inspection | **OVERCLAIM; CI stops below browser/device boundary** |
| “RC1 is release-accepted” | release marked prerelease; PR Draft; live gate in progress | **UNSUPPORTED** |

## Residual Risk

- **INFERRED:** The production RP ID, exact HTTPS origin, Secure/SameSite cookies, and proxy topology are correctly validated in source but remain unproven through a real Windows/Mac ceremony.
- **INFERRED:** Recovery-link possession is an account-takeover boundary. Transactional tests reduce risk, but only live issue/redeem/replay/revoke evidence confirms the deployed contract and audit shape.
- **INFERRED:** The current SIM health response contains no commit identity, so the fresh 2026-07-27 network probe cannot prove the node still runs `b54e383`; that identity rests on the 2026-07-24 driver record.
- **INFERRED:** No attributable rollback/forward-restore or post-auth soak record exists for the deployed RC.
- **INFERRED:** The full Account → Passkeys management UI and credential deletion/reset remain follow-up scope; issue #28 should not close merely because P0 source landed.

## Independent Hypothesis Delta

- **My hypothesis was:** do not merge without attributable cross-machine authenticated live fire; absent that evidence, automated tests alone cannot close the acceptance gate.
- **Existing implementation/recommendation was:** source and CI were considered ready; the PR and operation explicitly kept live acceptance pending.
- **Agreement:** the prior record correctly separated publication/provenance from live acceptance.
- **Delta:** more than my initial hypothesis assumed was proven—exact artifact publication, attestation, SIM deployment, migration, current unauthenticated reachability, and GCS reconnect all have attributable evidence. That supports retaining an RC1 canary rather than reducing the result to an unpublished RC0 engineering build.
- **What prior work caught that I initially lacked:** single-publisher release provenance, exact archive digest, real SIM migration, operator-unit preservation, and GCS reconnect evidence.
- **What this assessment catches:** the shared local checkout is fourteen commits stale; PR/issue/operation status text has drifted; and no later artifact closes the authentication gate despite RC1 publication and deployment.

## Self-Adversary Gate

> [ADVERSARY] **Proposal:** hold PR #30 as Draft and retain `v0.1.6-rc.1` only as an acceptance-pending canary.
>
> **Challenge 1 — Lazy path:** This could be a formulaic “absence of evidence” rejection that ignores substantial live work.
>
> **Rebuttal/refinement:** The verdict credits the exact published artifact, attestation, SIM install, migration, unauthenticated browser redirect, GCS reconnect, and a fresh 2026-07-27 health/redirect probe. The hold is narrowly tied to the branch's explicit authenticated Windows→Mac acceptance criterion, not to an assertion that all live testing is absent.
>
> **Challenge 2 — Fragility path:** The local operation replica may be stale and a private Discord/browser record may contain a later successful ceremony.
>
> **Rebuttal/refinement:** That is possible, but it is precisely a source-integrity failure until the evidence is attributable to `b54e383` and registered in the PR/issue/operation record. The operation remains active, the live gate remains `in_progress`, its owner is stale, the PR is Draft, and neither PR nor issue contains a closure update. The verdict is therefore HIGH on the available authoritative record, not a claim that the ceremony definitely failed.
>
> **Challenge 3 — Alternative path:** Merge the green, reviewed code now and perform live authentication after merge, treating the remaining work as deployment validation.
>
> **Rebuttal/refinement:** That approach is viable only if maintainers explicitly reclassify the acceptance contract. The current PR body, issue #28, operation Definition of Done, Draft state, and maintainer comment all make deployed multi-machine authentication a pre-merge gate for this security change. No authoritative scope change exists, so moving it post-merge would silently weaken the agreed gate.
>
> **Confidence verdict:** HIGH for HOLD / DO NOT MERGE; CONFIRMED for current unauthenticated SIM health and redirect; HYPOTHESIS for successful authenticated cross-machine behavior.

## WHAT I CANNOT VERIFY

- Whether an unregistered Discord, private browser, or operator-local artifact after 2026-07-24 contains a completed Windows/Mac ceremony.
- Whether SIM currently still runs exact `b54e383`; the public health endpoint reports only `{"status":"ok"}`.
- Any credential assertion, recovery token, session, or audit row directly; none was accessed, and secrets/PII were intentionally not inspected.
- Whether the Mac and Windows authenticators will accept the configured RP/origin and coexist until the user-attended ceremony is performed.
- Rollback/forward restore, authenticated SSE/counters, replay/revocation on the deployed service, the additional-authenticator path, and soak.
- A clean local reproduction of PR tip from the shared checkout, because its local branch is at `fbf33ed`, not `b54e383`.
