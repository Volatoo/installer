# Installer v1 contract

Status: installer and cross-repository disk gates validated; live-media
packaging and released-revision pinning remain.

## Product boundary

`volatoo-installer` is the only supported user-facing installer. Release
engineering publishes signed metadata and immutable payloads; the installer
selects and authenticates one payload, presents an exact destructive plan,
writes only the explicitly selected device and records what it installed.

The legacy `install-volatoo.sh` in the main repository remains a development
and compatibility disk writer until the migration gates below pass. It is not
copied into this repository.

## Trust chain

The v1 chain is:

```text
public release key from the live medium
  -> OpenBSD signify detached signature
  -> exact release-index JSON bytes
  -> channel, sequence, validity window and target identity
  -> archive and release-media manifest size and SHA-256
  -> uncompressed disk size and SHA-256
  -> installed-disk readback and installation receipt
```

Public keys are supplied by the separately versioned Volatoo keyring package
inside the authenticated live medium. The installer loads only digest-named,
regular non-symlink `.pub` files from that package's release directory. GitHub,
a CDN and a mirror are all
untrusted transports. A checksum downloaded beside an image is not a trust
root.

The installer accepts a release only when exactly one trusted key verifies the
detached signature, the index is within its signed validity window and one
entry exactly matches the requested architecture and init system. Missing,
duplicate, expired, unknown-schema or ambiguously matching data fails closed.

## Release index v1

The signed document is UTF-8 JSON with this shape:

```json
{
  "schema": "org.volatoo.release-index/v1",
  "channel": "v0.1-dev",
  "sequence": 1,
  "published_at": "2026-08-15T00:00:00Z",
  "expires_at": "2026-08-22T00:00:00Z",
  "releases": [
    {
      "id": "v0.1.0-dev.20260815-openrc-amd64",
      "architecture": "amd64",
      "init_system": "openrc",
      "archive": {
        "url": "objects/sha256/aa/aabb...",
        "size": 123,
        "sha256": "aabb...",
        "format": "zstd"
      },
      "manifest": {
        "url": "objects/sha256/bb/bbcc...",
        "size": 456,
        "sha256": "bbcc...",
        "format": "release-media-v2"
      },
      "disk": {
        "file": "volatoo-v0.1-dev-20260815-openrc-amd64.img",
        "size": 789,
        "sha256": "ccdd...",
        "format": "raw-gpt"
      }
    }
  ]
}
```

The signature covers the exact bytes, so JSON key ordering is not a verifier
assumption. The parser rejects unknown fields to prevent a publisher and an
older installer from assigning different meaning to one document. Digests are
lowercase SHA-256. Artifact URLs are either relative to the index or absolute
HTTPS URLs; credentials, fragments, plain HTTP and local-file URLs are
forbidden in a published index.

`sequence` is monotonic within a channel. The installer receipt stores the
accepted channel and sequence. Reusing an older signed index therefore
requires an explicit rollback action once persistent receipt enforcement is
enabled; wall-clock expiry is an additional freshness bound, not the only
rollback control.

## Destructive plan

Before opening a target for writing, the installer must have:

1. authenticated and parsed the release index;
2. selected exactly one architecture and init-system target;
3. downloaded and verified the complete compressed archive and manifest;
4. cross-checked release-media v2 identity against the signed index;
5. resolved one explicit non-symlink block device that is large enough, is not
   mounted and does not contain the running root filesystem;
6. displayed the stable device identity, current size, selected release and
   every partition mutation;
7. received the exact device path as confirmation, unless an unattended file
   explicitly authorizes that same stable device identity.

The installer never scans for a plausible writable target and never chooses a
default disk.

## Installation receipt

Successful installation writes
`VOLATOO-STATE:/volatoo/install/receipt-v1.json` atomically. It records the
release ID, channel, index sequence and digest, signing-key ID, archive,
manifest and disk digests, target stable identity, installation time and
installer version. Secrets, private SSH keys and access tokens are forbidden.

## Migration gates

- [x] Strict release-index and signify verification tests pass.
- [x] Explicit-device planning and negative destructive-operation tests pass.
- [x] Privileged loop-device installation reproduces and reads back the signed
      disk digest, expands state safely and writes receipt v1.
- [x] OpenRC and systemd installations boot under BIOS and UEFI in the
      OrbStack QEMU runner.
- [x] The Volatoo overlay packages the standalone installer.
- [x] A live image consumes a versioned installer and keyring package.
- [ ] Cross-repository QA pins released installer, releng and image revisions.
- [x] Formal releng packaging excludes `install-volatoo.sh`; the main
      repository retains it only for the historical developer preview.
