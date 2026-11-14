#!/usr/bin/env bash
set -euo pipefail

harness=${1:?usage: run.sh <harness>}
cd "$(dirname "$0")/../.."

api=http://127.0.0.1:18080
out=.out/e2e/$harness

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

new_id() {
  cat /proc/sys/kernel/random/uuid
}

# create <path> [<body>] creates a resource and prints its ID.
create() {
  curl --silent --show-error --fail --request POST --data "${2:-}" "$api$1" | jq --raw-output .id
}

# run_turn <session> <log-name> <message> sends the message and follows the
# turn to its end. The events are stored one per line, as {"event", "data"}.
run_turn() {
  local session=$1 log=$out/$2.jsonl turn
  turn=$(create "/v1/sessions/$session/messages" "$(jq --null-input --arg text "$3" '{text: $text}')")
  curl --silent --show-error --fail --no-buffer --max-time 300 "$api/v1/turns/$turn/events" |
    awk '/^event: / { event = substr($0, 8) }
         /^data: /  { printf "{\"event\":\"%s\",\"data\":%s}\n", event, substr($0, 7) }' > "$log"
  [ "$(jq --slurp --raw-output 'last.event' "$log")" = turn.completed ] ||
    fail "turn $turn did not complete, see $log"
}

# What the agent said in a turn.
answer() {
  jq --join-output 'select(.event == "message.delta") | .data.text // empty' "$out/$1.jsonl"
}

# What the tools did that the agent ran in a turn.
tool_events() {
  jq --compact-output 'select(.event | startswith("tool."))' "$out/$1.jsonl"
}

kubectl --context kind-aviary --namespace aviary-system \
  port-forward service/aviary-supervisor 18080:80 > /dev/null &
trap "kill $!" EXIT
curl --silent --output /dev/null --retry 20 --retry-connrefused --retry-delay 1 "$api/healthz" ||
  fail "the Supervisor does not answer; run make deploy"

mkdir -p "$out"
file=note-$(new_id | cut --characters 1-8).txt
content=$(new_id | cut --characters 1-8)

project=$(create /v1/projects '{"name": "e2e"}')
agent=$(create "/v1/projects/$project/agents" "$(jq --null-input --arg harness "$harness" '{harness: $harness}')")

# A session keeps its files from one turn to the next. Each turn runs in a
# new pod.
session=$(create "/v1/agents/$agent/sessions")
run_turn "$session" session-turn-1 \
  "Create a file named $file in the current directory with this content: $content"
run_turn "$session" session-turn-2 \
  "Read the file $file in the current directory and reply with its content."
[[ $(answer session-turn-2) == *"$content"* ]] ||
  fail "turn 2 did not answer with the file content, see $out/session-turn-2.jsonl"
[[ $(tool_events session-turn-2) == *"$content"* ]] ||
  fail "turn 2 did not read the file, see $out/session-turn-2.jsonl"

# Another session does not see those files.
other=$(create "/v1/agents/$agent/sessions")
run_turn "$other" other-turn-1 \
  'Run `ls -al` in the current directory and reply with its output.'
[ -n "$(tool_events other-turn-1)" ] || fail "the other session ran no tool, see $out/other-turn-1.jsonl"
if grep --quiet --fixed-strings "$file" "$out/other-turn-1.jsonl"; then
  fail "the other session saw the file, see $out/other-turn-1.jsonl"
fi

echo "PASS: $harness ran turns through the API, kept a session's files and kept sessions apart"
