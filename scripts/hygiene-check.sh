#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'hygiene-check: %s\n' "$*" >&2
  exit 1
}

tracked_local_artifacts="$({ git ls-files || true; } | awk '
  /^\.ml-cache\// { print; next }
  /^\.mainline\/local\.toml$/ { print; next }
  /^\.claude\// { print; next }
  /^\.codex\// { print; next }
  /^\.cursor\// && $0 != ".cursor/rules/mainline.md" { print; next }
  /^\.idea\// { print; next }
  /^\.vscode\// { print; next }
  /^\.env(\.|$)/ { print; next }
  /^dist\// { print; next }
  /^docs\/eval-runs\// { print; next }
  /^mainline$/ { print; next }
  /^mainline-hub\// { print; next }
  /^hub-export\// { print; next }
  /^data\/intents\.json$/ { print; next }
  /^(index|open|review|risks|graph|files|intents|coverage|digest)\.html$/ { print; next }
  /^zh\/(index|open|review|risks|graph|files|intents|coverage|digest)\.html$/ { print; next }
')"

if [ -n "$tracked_local_artifacts" ]; then
  printf '%s\n' "$tracked_local_artifacts" >&2
  fail "local cache/config or generated hub export files are tracked"
fi

required_public_files=(
  LICENSE
  README.md
  README.zh.md
  CONTRIBUTING.md
  SECURITY.md
  .github/PULL_REQUEST_TEMPLATE.md
  .github/ISSUE_TEMPLATE/bug_report.yml
  .github/ISSUE_TEMPLATE/feature_request.yml
  .github/ISSUE_TEMPLATE/design_partner_feedback.yml
  .github/ISSUE_TEMPLATE/confusing_concept.yml
  .github/ISSUE_TEMPLATE/use_case_report.yml
)

for path in "${required_public_files[@]}"; do
  [ -s "$path" ] || fail "required public repo file missing or empty: $path"
  git ls-files --error-unmatch "$path" >/dev/null 2>&1 || fail "required public repo file is not tracked: $path"
done

secret_pattern='(AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9_]{30,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|(^|[^A-Za-z0-9_])sk-((proj|ant-api03)-)?[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{20,}|AIza[0-9A-Za-z_-]{35}|-----BEGIN ([A-Z ]*)?PRIVATE KEY-----)'
secret_hits="$(git grep -I -n -E "$secret_pattern" -- . ':!go.sum' ':!docs/eval-layer2-baseline.json' ':!docs/eval-live-3seed.json' || true)"
if [ -n "$secret_hits" ]; then
  printf '%s\n' "$secret_hits" >&2
  fail "possible credential material found in tracked files"
fi

printf 'hygiene-check: ok\n'
