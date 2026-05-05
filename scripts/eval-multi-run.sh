#!/usr/bin/env bash
# eval-multi-run.sh — Run eval N seeds × M models, aggregate results.
#
# Usage:
#   ./scripts/eval-multi-run.sh [--seeds N] [--models "model1,model2"] [--runner PATH] [--judge PATH]
#
# Defaults:
#   seeds=3, models from EVAL_MODELS env or "replay"
#   runner=./scripts/eval-runner-copilot.py
#   judge=./scripts/eval-judge-copilot.py
#
# Output:
#   docs/eval-runs/<timestamp>/
#     seed-1-<model>.json
#     seed-2-<model>.json
#     ...
#     aggregate.json

set -euo pipefail

SEEDS="${EVAL_SEEDS:-3}"
MODELS="${EVAL_MODELS:-replay}"
RUNNER="${EVAL_RUNNER:-./scripts/eval-runner-copilot.py}"
JUDGE="${EVAL_JUDGE:-./scripts/eval-judge-copilot.py}"
MAINLINE="${EVAL_MAINLINE:-mainline}"

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --seeds) SEEDS="$2"; shift 2 ;;
    --models) MODELS="$2"; shift 2 ;;
    --runner) RUNNER="$2"; shift 2 ;;
    --judge) JUDGE="$2"; shift 2 ;;
    --mainline) MAINLINE="$2"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUTPUT_BASE="docs/eval-runs/${TIMESTAMP}"
mkdir -p "$OUTPUT_BASE"

echo "═══ Eval multi-run: ${SEEDS} seeds × $(echo "$MODELS" | tr ',' ' ' | wc -w | tr -d ' ') models ═══"
echo "  runner: $RUNNER"
echo "  judge:  $JUDGE"
echo "  output: $OUTPUT_BASE"
echo ""

IFS=',' read -ra MODEL_LIST <<< "$MODELS"

ALL_RESULTS=()

for model in "${MODEL_LIST[@]}"; do
  for seed in $(seq 1 "$SEEDS"); do
    OUT_DIR="${OUTPUT_BASE}/seed-${seed}-${model}"
    mkdir -p "$OUT_DIR"
    
    echo "▸ Running seed=$seed model=$model ..."
    
    MODEL_FLAG=""
    if [[ "$model" != "replay" ]]; then
      MODEL_FLAG="--model $model"
    fi
    
    # shellcheck disable=SC2086 — intentional word-split on MAINLINE and MODEL_FLAG
    $MAINLINE eval agent \
      --runner "$RUNNER" \
      --judge "$JUDGE" \
      --seed "$seed" \
      $MODEL_FLAG \
      --output-dir "$OUT_DIR" \
      --json > /dev/null 2>&1 || true
    
    ALL_RESULTS+=("${OUT_DIR}/eval-run.json")
    
    # Extract summary line
    if [[ -f "${OUT_DIR}/eval-run.json" ]]; then
      python3 -c "
import json, sys
with open('${OUT_DIR}/eval-run.json') as f:
    d = json.load(f)
s = d.get('summary', {})
print(f\"    CF={s.get('code_first_violations',0)} IF={s.get('intent_first_violations',0)} Δ={s.get('delta',0)}\")
" 2>/dev/null || echo "    (parse error)"
    fi
  done
done

echo ""
echo "═══ Aggregating ${#ALL_RESULTS[@]} runs ═══"

# Aggregate all results
python3 << PYEOF
import json, os, sys

results_files = ${ALL_RESULTS[@]+"$(printf '"%s",' "${ALL_RESULTS[@]}" | sed 's/,$//')"}
results = []
for path in [${ALL_RESULTS[@]+"$(printf '"%s",' "${ALL_RESULTS[@]}" | sed 's/,$//')"}]:
    if not os.path.exists(path):
        continue
    try:
        with open(path) as f:
            results.append(json.load(f))
    except:
        pass

if not results:
    print("No valid results found.")
    sys.exit(1)

# Aggregate
total_cf = sum(r['summary']['code_first_violations'] for r in results)
total_if = sum(r['summary']['intent_first_violations'] for r in results)
n_runs = len(results)
avg_cf = total_cf / n_runs
avg_if = total_if / n_runs

aggregate = {
    "n_runs": n_runs,
    "total_code_first_violations": total_cf,
    "total_intent_first_violations": total_if,
    "avg_code_first_violations": round(avg_cf, 2),
    "avg_intent_first_violations": round(avg_if, 2),
    "delta": total_cf - total_if,
    "per_run": [r['summary'] for r in results],
}

out_path = "${OUTPUT_BASE}/aggregate.json"
with open(out_path, 'w') as f:
    json.dump(aggregate, f, indent=2)

print(f"  Runs:       {n_runs}")
print(f"  CF total:   {total_cf} (avg {avg_cf:.1f}/run)")
print(f"  IF total:   {total_if} (avg {avg_if:.1f}/run)")
print(f"  Δ total:    {total_cf - total_if}")
print(f"  Written:    {out_path}")
PYEOF

echo ""
echo "Done. Results in: $OUTPUT_BASE"
