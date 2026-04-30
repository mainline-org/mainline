#!/usr/bin/env bash
# eval-runner-anthropic.sh — minimal runner for `mainline eval agent --runner`
#
# Reads the JSON envelope from stdin, constructs a prompt, calls the
# Anthropic Messages API, and writes the response to stdout.
#
# Requirements:
#   - ANTHROPIC_API_KEY env var set
#   - jq installed
#   - curl installed
#
# Usage:
#   export ANTHROPIC_API_KEY=sk-ant-...
#   mainline eval agent --runner ./scripts/eval-runner-anthropic.sh
#
# The runner produces an AgentRunResult JSON on stdout so the harness
# can parse ContextRetrieved and DurationMillis directly.

set -euo pipefail

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo '{"error":"ANTHROPIC_API_KEY not set","output":"","context_retrieved":false}' 
  exit 1
fi

MODEL="${EVAL_MODEL:-claude-sonnet-4-5-20250514}"
MAX_TOKENS="${EVAL_MAX_TOKENS:-2048}"

# Read the full envelope from stdin
ENVELOPE=$(cat)

PROMPT_TEXT=$(echo "$ENVELOPE" | jq -r '.prompt')
PROMPT_KEY=$(echo "$ENVELOPE" | jq -r '.prompt_key')
FIXTURE_TASK=$(echo "$ENVELOPE" | jq -r '.fixture.Task')
FIXTURE_NAME=$(echo "$ENVELOPE" | jq -r '.fixture.Name')

# Build forbidden bullets
FORBIDDEN_BULLETS=$(echo "$ENVELOPE" | jq -r '.fixture.Forbidden[]' | sed 's/^/- /')

# Substitute template placeholders
FULL_PROMPT="${PROMPT_TEXT//\{\{TASK\}\}/$FIXTURE_TASK}"
FULL_PROMPT="${FULL_PROMPT//\{\{FORBIDDEN_BULLETS\}\}/$FORBIDDEN_BULLETS}"

# For intent-first, prepend the intent context
if [[ "$PROMPT_KEY" == "intent_first" ]]; then
  INTENT_CONTEXT=$(echo "$ENVELOPE" | jq -r '
    .fixture.Intents[]
    | "Intent \(.ID) (status: \(.Status)):\n  Title: \(.Title)\n  What: \(.What)\n  Why: \(.Why)\n  Anti-patterns: \((.AntiPatterns // []) | map("[\(.Severity)] \(.What) — \(.Why)") | join("; "))\n  Decisions: \((.Decisions // []) | map("\(.Point): \(.Chose) (\(.Rationale))") | join("; "))\n"
  ')
  FULL_PROMPT="## Mainline Context (retrieved via mainline context --query)\n\n${INTENT_CONTEXT}\n\n---\n\n${FULL_PROMPT}"
fi

# Call Anthropic API
START_MS=$(($(date +%s%N 2>/dev/null || python3 -c 'import time; print(int(time.time()*1000))') / 1000000))

RESPONSE=$(curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d "$(jq -n \
    --arg model "$MODEL" \
    --argjson max_tokens "$MAX_TOKENS" \
    --arg prompt "$FULL_PROMPT" \
    '{
      model: $model,
      max_tokens: $max_tokens,
      messages: [{role: "user", content: $prompt}]
    }'
  )")

END_MS=$(($(date +%s%N 2>/dev/null || python3 -c 'import time; print(int(time.time()*1000))') / 1000000))
DURATION_MS=$(( END_MS - START_MS ))

# Extract text content from response
OUTPUT=$(echo "$RESPONSE" | jq -r '.content[0].text // .error.message // "unknown error"')
HAS_ERROR=$(echo "$RESPONSE" | jq -r 'if .error then .error.message else "" end')

# Detect context_retrieved for intent-first
CONTEXT_RETRIEVED="false"
if [[ "$PROMPT_KEY" == "intent_first" ]]; then
  CONTEXT_RETRIEVED="true"
fi

# Emit AgentRunResult JSON
jq -n \
  --arg prompt "$PROMPT_KEY" \
  --arg output "$OUTPUT" \
  --argjson duration_ms "$DURATION_MS" \
  --argjson context_retrieved "$CONTEXT_RETRIEVED" \
  --arg error "$HAS_ERROR" \
  '{
    prompt: $prompt,
    output: $output,
    duration_ms: $duration_ms,
    context_retrieved: $context_retrieved,
    error: (if $error == "" then null else $error end)
  }'
