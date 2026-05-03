# Changelog

Mainline is in public alpha. Until `v1.0.0`, release notes should optimize for
clear user impact over strict semver guarantees: call out workflow changes,
schema/config changes, migration notes, and known alpha limitations explicitly.

This project follows the spirit of [Keep a Changelog](https://keepachangelog.com/)
and uses semver-style versions once tags are published.

## [Unreleased]

### Added

- _Template: new user-visible capabilities, commands, workflows, docs, or
  integrations._

### Changed

- _Template: behavior changes, defaults, UX copy, storage semantics, or workflow
  guidance._

### Fixed

- _Template: bug fixes, correctness fixes, reliability improvements, or reduced
  false positives._

### Security

- _Template: vulnerability fixes, hardening, dependency updates, secret-handling
  changes, or disclosure-process updates._

### Migration Notes

- _Template: actions users or maintainers should take after upgrading._

### Known Alpha Limits

- _Template: intentionally accepted limitations, unstable schemas, incomplete
  integrations, or non-blocking risks relevant to this release._

## Release Notes Template

Use this checklist when drafting a GitHub Release:

````markdown
## Mainline <version>

One sentence: what changed for users and why this release exists.

### Highlights

- ...

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_VERSION=<version> bash
```

Prebuilt archives and `checksums.txt` are attached to this release.

### Upgrade Notes

- ...

### Validation

- `make ci-release`
- `govulncheck ./...`
- install script smoke test on macOS/Linux

### Known Alpha Limits

- ...
````
