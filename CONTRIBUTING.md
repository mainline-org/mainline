# Contributing

Thanks for helping improve Mainline.

## Ground Rules

- Keep changes focused and reviewable.
- Add or update tests for behavior changes.
- Do not commit secrets, local caches, or generated hub exports.
- Follow the Mainline workflow (see the Mainline skill or `AGENTS.md` if installed) for intent-backed changes.

## Contributor Licensing

Mainline uses the layered licensing model described in [LICENSE](./LICENSE).
Local developer, agent, SDK, and protocol surfaces are intended to be licensed
under Apache-2.0; documentation and examples are intended to be available under
CC-BY-4.0 or Apache-2.0; hosted-service infrastructure and brand rights remain
separate protected surfaces.

By submitting a contribution, you confirm that you have the right to submit it
and license it to the project under the applicable license for the area you
change. For substantial contributions, maintainers may require a Contributor
License Agreement or Developer Certificate of Origin to keep the licensing
model clear across open local surfaces and hosted commercial surfaces.

## Development

```bash
go build -o mainline .
make quick-test
```

CI is split into fast and deep stages:

- `make hygiene` checks that local caches/configs, generated Hub exports, and
  high-signal credential patterns are not tracked.
- `make ci-quick` matches the required PR gate: hygiene, lint, build, and
  quick tests.
- `make ci-full` runs full rapid PBT coverage with race detection; it is for
  nightly/manual deep checks, not required for every PR.
- `make ci-release` runs the full release gate before publishing artifacts.

`make test` and `make test-pbt` are broader checks and may take longer.

## Pull Requests

- Use a clear Conventional Commit style title when practical.
- Include the relevant Mainline seal hash in the pull request template.
- Mention any behavior, CLI, config, or data-format compatibility impact.
- For documentation-only changes, say so explicitly in the PR body.

## Security Issues

Do not open public issues for vulnerabilities. Follow `SECURITY.md`.
