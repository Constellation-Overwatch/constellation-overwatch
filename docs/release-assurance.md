# Release assurance and fleet installation

Releases are built from a clean Git tag with GoReleaser. Each release contains
six CGO-disabled targets (Linux, macOS, and Windows on amd64 and arm64), a
SHA-256 checksum manifest, an SPDX JSON SBOM for each archive, and exact source
commit metadata visible through `overwatch version`. GitHub Actions attaches
build-provenance attestations to the archives, checksums, and SBOMs.

Verify an archive before installation:

```sh
sha256sum --check overwatch_TAG_checksums.txt
gh attestation verify overwatch_TAG_linux_amd64.tar.gz \
  --repo Constellation-Overwatch/constellation-overwatch
```

## Linux fleet install

Extract the published archive and run:

```sh
./packaging/install-linux.sh TAG
```

The installer creates an immutable release directory under
`~/.local/opt/constellation-overwatch/releases/`, atomically updates the
`current` symlink, installs a user-systemd unit, and stops. It never starts an
unconfigured service or writes over a production environment file.

Copy `~/.config/constellation-overwatch/overwatch.env.example` to
`overwatch.env`, replace every placeholder, and set mode `0600`. The embedded
NATS listener defaults to loopback port `4224`; tailnet TLS is separately
published on `4223`. Port `4222` remains reserved for ao-ops.

After configuring the service:

```sh
systemctl --user enable --now constellation-overwatch.service
curl --fail http://127.0.0.1:8090/health
~/.local/opt/constellation-overwatch/current/overwatch version
```

Rollback activates an already-installed release and restarts only an active
service:

```sh
./packaging/rollback-linux.sh PREVIOUS_TAG
```

`uninstall-linux.sh` disables activation but deliberately preserves releases,
configuration, secrets, databases, JetStream state, and backups.

## Containers

The container bases are pinned by OCI index digest. The runtime uses UID/GID
`10001`, stores mutable data only under `/data`, listens for HTTP on `8080`,
keeps embedded NATS on loopback `4224`, and declares a `/health` check. CI
builds and runs the image, verifies the non-root user, and waits for health.
That development-profile smoke test does not replace production-mode fleet
acceptance.
