# Linux package build and dependency check — 23 September 2026

**Oracle:** the Supported artifacts gate in the [finish line](../audits/2026-09-05/linux-finish-line.md): a Linux-first v1 owes dependency, installation and representative-deployment evidence for the Linux packages.
This record covers the first of those from this workstation and states why the second could not run here.

## Result

`wails3 task linux:create:rpm` on source `874f52205bd9e2daa403997f4f85c1ac046cf9f8` (the tree of master `df96e1ad`) built `aeronautes-0.0.0-dev.874f52205bd9-linux-amd64-UNSIGNED.rpm` (SHA-256 `6b80669a741ef591ea67b30443ced18ab19c7f276968839987880dc469cbb3e9`) and the 18.7 MB binary (`8bc87f14a5fc409a8b185ee19da4f4d379c9dd1f41b576ab8cd4cded0e407d4d`) with the pinned wails3 and nfpm v2.44.1 from the module cache, offline.
The package requires `gtk4`, `webkitgtk6.0` and `rclone`, unversioned; it installs four files (the binary under `/usr/local/bin`, the desktop entry, `build-identity.json` and the icon); its post-install script updates the desktop and MIME databases; it is unsigned and carries the dev version, because no release stamp was set.
Fedora 44's repositories offer `rclone-1.74.3-1.fc44` from `updates`, which is the pinned engine version, so a plain `dnf install` of this package on an updated Fedora 44 satisfies the pin; the base `fedora` repository lists `rclone-1.73.0-3.fc44`, and an installation that resolves to it would install but the application would refuse the engine by its version string (the repository query is retained as `rclone-repoquery-fedora44.txt` in the evidence directory, with its digest in `rpm-artifacts.sha256`).
On this Fedora 44 workstation, with `gtk4-4.22.5` and `webkitgtk6.0-2.52.5` installed, the built binary's dynamic dependencies all resolve (`ldd` reports none missing).

## Installation test: not possible from this workstation on 23 September

The intended test, installing the package into a fresh `registry.fedoraproject.org/fedora:44` container and running the binary's help, failed before installation: rootless podman inside this toolbox cannot unpack images because the user namespace has no subordinate id range.
The command to run on the host is retained beside the log; the follow-up below records the run that then succeeded here with ownership errors ignored.

## Evidence

`.aeronautes-work/20260923-linux-package/`: the build log, package metadata, requirements, file list and scripts, the dependency check and the failed container attempt, with digests in `rpm-artifacts.sha256`.

## Limits

This is a development build, not a release artifact: no version stamp, no signature, no representative deployment, and no native journey.
The rclone dependency is unversioned in the package while the application accepts only v1.74.3; whether the package should pin the dependency is a release decision this record does not make.

## Follow-up, 24 September 2026: the installation test

The test ran inside the toolbox after all, once podman was told to ignore ownership errors: rootless podman there maps a single id and has no subordinate ranges, so `--storage-opt overlay.ignore_chown_errors=true` lets it unpack an image whose files belong to other ids.
In a fresh `registry.fedoraproject.org/fedora:44` container, `dnf -y install` of the built rpm resolved and installed 345 packages: `aeronautes-0.0.0~dev.874f52205bd9-1`, `rclone-1.74.3-1.fc44` (the pinned engine version), `gtk4-4.22.5-2.fc44` and `webkitgtk6.0-2.52.5-1.fc44` all report installed, `rpm -V aeronautes` is clean, the binary's dynamic libraries all resolve, the installed build identity file names source `874f52205bd9`, and `dnf remove` leaves no file of the package behind.
The binary starts as far as the container allows: it reports that no OS keychain is reachable and that GTK cannot open a display, which are the container's limits, not the package's.
dnf nevertheless reported the transaction as failed, because five transitive dependencies of the desktop stack (polkit, polkit-pkla-compat, cups-filesystem, geoclue2, usbmuxd) could not be unpacked: rpm could not give their directories to the service users those packages create, since the single-id namespace cannot represent other owners ("cpio: chown failed").
None of them is a file of this package or of its three declared dependencies.
A run with subordinate id ranges, on the host, is the clean form of this evidence and is still owed; `install-test-host-command.sh` is retained beside the logs.

Evidence: `install-test-toolbox-fedora44.log` (first run, with removal; SHA-256 `95c11127ecee529094e1d17c9d432ebfdbdf268fb2cd741de22e14852dde7376`) and `install-test-toolbox-fedora44-full.log` (second run, full package-manager output; SHA-256 `7d11b9bb12632a96bf9fcb35fcb8154c6a806e16c2f5eae6802099752dae178f`), both listed in `rpm-artifacts.sha256`.
