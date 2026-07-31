# Independent Release Hypothesis

## Gate Status

Written before inspecting git history, branch commits, PR/Discord/crosstalk conclusions, existing release recommendations, or release artifacts.

## Expected Release Posture

- **INFERRED:** The branch should be treated as **not merge-ready** until there is attributable, reproducible cross-machine live-fire evidence that dashboard authentication works across the actual network boundary and runtime configuration intended for release.
- **INFERRED:** In the absence of that evidence, the strongest justified label is a **pre-release engineering build / RC0 at most**, not an RC1 or general release candidate. Passing source-level tests may justify code-review progression, but it cannot close the deployment/authentication acceptance gate.
- **INFERRED:** If the required live-fire evidence exists and the implementation plus automated regression coverage are sound, a merge with **RC1** status may be justified. A higher-confidence RC would additionally require repeatability, clean-start/restart behavior, and explicit residual-risk accounting.

## Evidence Required

1. **Source integrity:** an identifiable branch tip and commit set; no unexplained working-tree dependency; code review of authentication, session/cookie, proxy-header, host/origin, bind-address, and configuration behavior.
2. **Automated validation:** relevant unit/integration tests plus the repository-wide Go test and vet/build gates, recorded against the assessed commit.
3. **True cross-machine live fire:** a named server machine and distinct client machine; server bind/listen and advertised URL recorded; client access over a non-loopback network address; unauthenticated access denied or redirected; login/bootstrap succeeds; authenticated dashboard loads; protected requests succeed with the issued session; invalid or absent credentials remain rejected.
4. **Evidence provenance:** timestamps, commit SHA/build identity, commands or test script, observed HTTP status/redirect/cookie behavior, redacted logs, and enough detail for an independent operator to reproduce the result. Screenshots without request/runtime provenance are supporting evidence only.
5. **Release-operability checks:** secrets excluded from artifacts, cookies appropriate to the actual HTTP/HTTPS topology, restart or session-expiry behavior stated, failure modes documented, and no assertion that same-host/localhost success proves cross-machine behavior.

## Rejected Alternative

- **INFERRED:** I reject the alternative that passing automated tests and a same-machine browser login are sufficient for merge and RC1. Those checks do not exercise remote routing, host/origin differences, cookie transport, firewall/bind behavior, or the actual second-machine authentication path.

## Falsifier

This conservative hypothesis is falsified if the assessed branch provides: (a) clean source and automated test/build evidence tied to one commit, and (b) independently attributable live-fire records showing a distinct client machine completing the unauthenticated-to-authenticated dashboard flow over the intended non-loopback network path, including protected-resource success and negative-auth checks, with no material source/artifact/assertion drift.

## Initial Uncertainty

- **INFERRED:** I do not yet know whether such evidence exists, whether it is tied to the latest branch tip, or whether prior recommendations overstate what was actually exercised.
