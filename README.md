# Volatoo installer

`volatoo-installer` is the official installer for Volatoo live media and
automated provisioning. It is a self-contained Linux executable; release
selection, authentication, destructive-device planning and installation
receipts are product contracts rather than shell-script conventions.

## Owns

- interactive and unattended installation flows;
- explicit disk selection, partition plans, bootloader installation, and state
  filesystem initialization;
- release-manifest verification before any target disk is modified;
- installation receipts, recovery UX, and destructive-operation tests.

The installer must require an explicit target device and must never guess a
disk to modify.

## Migration gate

The Bash writer stays in `Volatoo/Volatoo` only for the historical developer
preview. This repository owns the formal installer and must not become a
second copy of that script. Formal release migration completes when live media
consume versioned installer and keyring packages and cross-repository QA pins
released revisions.

The first implementation milestone and remaining distribution gates are
tracked in [`docs/installer-v1.md`](docs/installer-v1.md). The old script is a
developer-preview disk writer and must not be described as the official
installer.

## Development

The installer intentionally uses only the Go standard library. Build and run
the contract tests with the pinned container wrapper:

```sh
scripts/test-docker.sh
scripts/test-install-docker.sh
```

The wrapper refuses every Docker context except `orbstack`.

Build the static amd64 executable reproducibly:

```sh
scripts/build-docker.sh --version 0.1.0-dev out/volatoo-installer
scripts/test-reproducible-build.sh
```

Verify an offline release bundle without touching a block device:

```sh
volatoo-installer verify \
  --index release-index.json \
  --trusted-key /usr/share/volatoo/keyring/release/current.pub \
  --init-system openrc \
  --archive volatoo-openrc-amd64.img.zst \
  --manifest volatoo-openrc-amd64.img.manifest
```

When `--archive` and `--manifest` are omitted, the installer obtains both from
the content-addressed HTTPS or local paths authenticated by the signed index.
The detached signature defaults to `<index>.sig`. Explicit offline artifacts
are copied into a root-private workspace and authenticated there before device
confirmation, so replacing an operator-supplied path cannot change the bytes
used after the target is opened.

Install only to one explicit device:

```sh
sudo volatoo-installer install \
  --index https://dist.volatoo.org/releases/amd64/channels/v0.1-dev/index.json \
  --init-system openrc \
  --device /dev/DEVICE \
  --ssh-authorized-key "$HOME/.ssh/id_ed25519.pub"
```

The installer downloads and validates everything before inspecting or opening
the target for writing. It then prints the exact plan and requires the complete
device path as confirmation. A successful run reads the installed disk back,
expands the state filesystem and writes `receipt-v1.json` there.

On authenticated Volatoo live media, the installer loads every digest-named
public key from `/usr/share/volatoo/keyring/release`. `--trusted-key` remains
available for an explicit offline key, while `--trusted-key-dir` selects a
different fail-closed keyring during development or recovery.
