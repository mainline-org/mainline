# Contributing

Thanks for helping improve Mainline.

## Ground Rules

- Keep changes focused and reviewable.
- Add or update tests for behavior changes.
- Do not commit secrets, local caches, or generated hub exports.
- Follow the Mainline workflow in `AGENTS.md` for intent-backed changes.

## Development

```bash
go build -o mainline .
make quick-test
```

`make test` and `make test-pbt` are broader checks and may take longer.

## Pull Requests

- Use a clear Conventional Commit style title when practical.
- Include the relevant Mainline seal hash in the pull request template.
- Mention any behavior, CLI, config, or data-format compatibility impact.
- For documentation-only changes, say so explicitly in the PR body.

## Security Issues

Do not open public issues for vulnerabilities. Follow `SECURITY.md`.
