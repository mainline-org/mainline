# Changelog

Mainline is in public alpha. Until `v1.0.0`, release notes should optimize for
clear user impact over strict semver guarantees: call out workflow changes,
schema/config changes, migration notes, and known alpha limitations explicitly.

This project follows the spirit of [Keep a Changelog](https://keepachangelog.com/)
and uses semver-style versions once tags are published.

## [Unreleased]

### Added

- README/README.zh Hub instructions for generating, opening, exporting, and
  screenshotting local Hub output.
- README/README.zh license positioning for PolyForm Shield 1.0.0 and reserved
  commercial licensing.
- README/README.zh front-page positioning around preventing AI agents from
  repeating old engineering mistakes, plus workflow and vendor-memory
  boundaries.
- README/README.zh split the human quick start from the agent protocol contract
  and documented when agents must call context, how to append, recover, and
  write reviewable intent.

### Changed

- Project license changed from MIT to source-available PolyForm Shield 1.0.0.
- CONTRIBUTING now calls out contributor licensing expectations for future
  commercial licensing and possible component relicensing.

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
