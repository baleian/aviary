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

# run_turn <session> <node> <log-name> <runner-argument>...
run_turn() {
  local session=$1 node=$2 log=$out/$3.jsonl turn succeeded=true
  shift 3
  turn=$(new_id)
  SESSION=$session TURN=$turn NODE=$node HARNESS_IMAGE=aviary-harness-$harness:dev \
    ARGS=$(jq --null-input --compact-output '$ARGS.positional' --args -- "$@") \
    envsubst < test/e2e/turn-pod.yaml | $kubectl apply --filename -
  await "turn-$turn" || succeeded=false
  $kubectl logs "turn-$turn" > "$log"
  $succeeded || fail "turn did not succeed, see $log; pod turn-$turn is kept"
  $kubectl delete pod "turn-$turn"
}

# The harness session that the events of a turn belong to.
session_id() {
  jq --null-input --raw-output 'first(inputs | .sessionId // empty)' "$out/$1.jsonl"
}

# What the agent said in a turn.
answer() {
  jq --join-output \
    'select(.update.sessionUpdate == "agent_message_chunk") | .update.content.text // empty' "$out/$1.jsonl"
}

# The results of the tools that the agent ran in a turn.
tool_results() {
  jq --compact-output 'select(.update.sessionUpdate == "tool_call_update")' "$out/$1.jsonl"
}

[ -f .env ] || fail ".env is missing; it must set AVIARY_LLM_BASE_URL, AVIARY_LLM_API_KEY and AVIARY_LLM_MODEL"
$kubectl create secret generic aviary-llm --from-env-file .env --dry-run=client --output yaml |
  $kubectl apply --filename -

mkdir -p "$out"
file=note-$(new_id | cut --characters 1-8).txt
content=$(new_id | cut --characters 1-8)
recall="Read the file you created earlier and reply with its content."

# A session keeps its conversation and its files across pods and nodes.
session=$(new_id)
create_session "$session"
run_turn "$session" aviary-worker session-turn-1 \
  "Create a file named $file in the current directory with this content: $content"
resume=$(session_id session-turn-1)
[ -n "$resume" ] || fail "turn 1 reported no session, see $out/session-turn-1.jsonl"
run_turn "$session" aviary-worker2 session-turn-2 --resume "$resume" "$recall"

[ "$(session_id session-turn-2)" = "$resume" ] ||
  fail "turn 2 did not continue the session of turn 1, see $out/session-turn-2.jsonl"
[[ $(answer session-turn-2) == *"$content"* ]] ||
  fail "turn 2 did not answer with the file content, see $out/session-turn-2.jsonl"
[[ $(tool_results session-turn-2) == *"$content"* ]] ||
  fail "turn 2 did not read the file, see $out/session-turn-2.jsonl"

# Another session sees neither that conversation nor those files.
other=$(new_id)
create_session "$other"
run_turn "$other" aviary-worker2 other-turn-1 "$recall"
[ -n "$(answer other-turn-1)" ] || fail "the other session did not answer, see $out/other-turn-1.jsonl"
if grep --quiet --fixed-strings "$content" "$out/other-turn-1.jsonl"; then
  fail "the other session saw the file content, see $out/other-turn-1.jsonl"
fi

run_turn "$other" aviary-worker other-turn-2 --resume "$(session_id other-turn-1)" \
  'Run `ls -al` in the current directory and reply with its output.'
[ -n "$(tool_results other-turn-2)" ] || fail "the other session ran no tool, see $out/other-turn-2.jsonl"
if grep --quiet --fixed-strings "$file" "$out/other-turn-2.jsonl"; then
  fail "the other session saw the file, see $out/other-turn-2.jsonl"
fi

echo "PASS: $harness resumed a session in a new pod and kept sessions apart"
