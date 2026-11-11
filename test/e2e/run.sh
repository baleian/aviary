#!/usr/bin/env bash
set -euo pipefail

harness=${1:?usage: run.sh <harness>}
cd "$(dirname "$0")/../.."

kubectl="kubectl --context kind-aviary --namespace aviary-runtime"
out=.out/e2e/$harness

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

new_id() {
  cat /proc/sys/kernel/random/uuid
}

await() {
  local pod=$1 phase
  for _ in $(seq 300); do
    phase=$($kubectl get pod "$pod" --output jsonpath='{.status.phase}')
    case $phase in
      Succeeded) return 0 ;;
      Failed) return 1 ;;
    esac
    sleep 1
  done
  return 1
}

create_session() {
  SESSION=$1 envsubst < test/e2e/session-pod.yaml | $kubectl apply --filename -
  await "session-$1" || fail "session $1 was not created; pod session-$1 is kept"
  $kubectl delete pod "session-$1"
}

run_turn() {
  local session=$1 log=$out/$2.jsonl prompt=$3 turn succeeded=true
  turn=$(new_id)
  SESSION=$session TURN=$turn PROMPT=$prompt HARNESS_IMAGE=aviary-harness-$harness:dev \
    envsubst < test/e2e/turn-pod.yaml | $kubectl apply --filename -
  await "turn-$turn" || succeeded=false
  $kubectl logs "turn-$turn" > "$log"
  $succeeded || fail "turn did not succeed, see $log; pod turn-$turn is kept"
  $kubectl delete pod "turn-$turn"
}

[ -f .env ] || fail ".env is missing; it must set AVIARY_LLM_BASE_URL, AVIARY_LLM_API_KEY and AVIARY_LLM_MODEL"
$kubectl create secret generic aviary-llm --from-env-file .env --dry-run=client --output yaml |
  $kubectl apply --filename -

mkdir -p "$out"
session=$(new_id)

create_session "$session"
run_turn "$session" turn-1 "Reply with exactly: pong"

grep --quiet '"sessionUpdate":"agent_message_chunk"' "$out/turn-1.jsonl" ||
  fail "turn 1 produced no agent message, see $out/turn-1.jsonl"
echo "PASS: $harness answered one turn in a pod"
