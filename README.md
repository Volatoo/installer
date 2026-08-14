# Volatoo installer

Installer for Volatoo live media and automated provisioning.

## Owns

- interactive and unattended installation flows;
- explicit disk selection, partition plans, bootloader installation, and state
  filesystem initialization;
- release-manifest verification before any target disk is modified;
- installation receipts, recovery UX, and destructive-operation tests.

The installer must require an explicit target device and must never guess a
disk to modify.

## Migration gate

The current installer stays in `Volatoo/Volatoo` until the live medium consumes
a versioned installer package and the disk contract has independent integration
tests. This repository must not become a second copy of the script.
