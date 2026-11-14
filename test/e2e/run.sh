#!/usr/bin/env bash
set -euo pipefail

# The e2e runs against a model server of its own, mock-llm, which answers by
# a script. A message to a session is an instruction to it: "say <text>",
# "run <command>" or "recall". See test/mock-llm/main.go.

harness=${1:?usage: run.sh <harness>}
cd "$(dirname "$0")/../.."

kubectl="kubectl --context kind-aviary"
helm="helm --kube-context kind-aviary --namespace aviary-system"
api=http://127.0.0.1:18080
out=.out/e2e/$harness

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

new_id() {
  cat /proc/sys/kernel/random/uuid
}

# connect forwards a local port to the Supervisor. A restart of the
# Supervisor breaks the forwarding, so it is then called again.
forward=
connect() {
  [ -z "$forward" ] || kill "$forward" 2> /dev/null || true
  $kubectl --namespace aviary-system port-forward service/aviary-supervisor 18080:80 > /dev/null &
  forward=$!
  curl --silent --output /dev/null --retry 20 --retry-connrefused --retry-delay 1 "$api/healthz" ||
    fail "the Supervisor does not answer; run make deploy"
}

# install [<option>...] installs the chart again, with the values that it was
# installed with and the given options. A change restarts the Supervisor.
install() {
  $helm upgrade aviary deploy/charts/aviary --values "$out/values.yaml" "$@" --wait > /dev/null
}

# The chart is put back as it was installed however the script ends.
changed=
cleanup() {
  kill "$forward" 2> /dev/null || true
  [ -z "$changed" ] || install
}
trap cleanup EXIT

# create <path> [<body>] creates a resource and prints its ID.
create() {
  curl --silent --show-error --fail --request POST --data "${2:-}" "$api$1" | jq --raw-output .id
}

# field <path> <name> prints a field of a resource.
field() {
  curl --silent --show-error --fail "$api$1" | jq --raw-output ".$2"
}

# send <session> <message> sends the message and prints the ID of its turn.
send() {
  create "/v1/sessions/$1/messages" "$(jq --null-input --arg text "$2" '{text: $text}')"
}

# follow <turn> <log-name> follows the turn to its end. The events are stored
# one per line, as {"event", "data"}.
follow() {
  curl --silent --show-error --fail --no-buffer --max-time 300 "$api/v1/turns/$1/events" |
    awk '/^event: / { event = substr($0, 8) }
         /^data: /  { printf "{\"event\":\"%s\",\"data\":%s}\n", event, substr($0, 7) }' > "$out/$2.jsonl"
}

# ended <log-name> prints the event that ended a turn.
ended() {
  jq --slurp --raw-output 'last.event' "$out/$1.jsonl"
}

# run_turn <session> <log-name> <message> runs a turn that must complete and
# leaves its ID in $turn.
run_turn() {
  turn=$(send "$1" "$3")
  follow "$turn" "$2"
  [ "$(ended "$2")" = turn.completed ] || fail "turn $turn did not complete, see $out/$2.jsonl"
}

# What the agent said in a turn.
answer() {
  jq --join-output 'select(.event == "message.delta") | .data.text // empty' "$out/$1.jsonl"
}

# What the tools did that the agent ran in a turn.
tool_events() {
  jq --compact-output 'select(.event | startswith("tool."))' "$out/$1.jsonl"
}

# The positions in the queue that a turn was told, in order.
positions() {
  jq --slurp --compact-output 'map(select(.event == "turn.queued") | .data.position)' "$out/$1.jsonl"
}

# in_cluster <turn> prints what the cluster holds of a turn: its Job and pod.
in_cluster() {
  $kubectl --namespace aviary-runtime get jobs,pods --selector "aviary.io/turn=$1" --output name
}

# wait_for <seconds> <command>... waits for the command to succeed.
wait_for() {
  local seconds=$1
  shift
  for _ in $(seq "$seconds"); do
    "$@" && return 0
    sleep 1
  done
  return 1
}

cleaned_up() { [ -z "$(in_cluster "$1")" ]; }
running() { [ "$(field "/v1/turns/$1" state)" = running ]; }

# For the e2e the turn pods use mock-llm, and one turn runs at a time, which
# the check of the queue needs.
mkdir -p "$out"
$kubectl apply --filename test/e2e/mock-llm.yaml > /dev/null
$kubectl --namespace aviary-e2e rollout status deployment/mock-llm --timeout 120s > /dev/null ||
  fail "mock-llm did not start; run make images"
$helm get values aviary --output yaml > "$out/values.yaml" || fail "Aviary is not installed; run make deploy"
changed=yes
install --set runtime.llmSecretName=aviary-llm-mock --set runtime.maxConcurrentTurns=1
connect

file=note-$(new_id | cut --characters 1-8).txt
content=$(new_id | cut --characters 1-8)
write="run printf '$content' > $file"

project=$(create /v1/projects '{"name": "e2e"}')
agent=$(create "/v1/projects/$project/agents" "$(jq --null-input --arg harness "$harness" '{harness: $harness}')")

# A message runs as a turn in a pod of its own, and nothing is left of the
# pod afterwards.
session=$(create "/v1/agents/$agent/sessions")
run_turn "$session" session-turn-1 "$write"
[ "$(field "/v1/turns/$turn" state)" = completed ] || fail "turn $turn is not recorded as completed"
wait_for 60 cleaned_up "$turn" || fail "the Job or the pod of turn $turn was not deleted"

# A session keeps its conversation and its files from one turn to the next.
run_turn "$session" session-turn-2 recall
[[ $(answer session-turn-2) == *"$write"* ]] ||
  fail "turn 2 did not know what was said in turn 1, see $out/session-turn-2.jsonl"
run_turn "$session" session-turn-3 "run cat $file"
[[ $(answer session-turn-3) == *"$content"* ]] ||
  fail "turn 3 did not answer with the file content, see $out/session-turn-3.jsonl"
[[ $(tool_events session-turn-3) == *"$content"* ]] ||
  fail "the tool events of turn 3 lack the file content, see $out/session-turn-3.jsonl"

# Another session sees neither that conversation nor those files.
other=$(create "/v1/agents/$agent/sessions")
run_turn "$other" other-turn-1 recall
[[ $(answer other-turn-1) == *nothing* ]] ||
  fail "the other session recalled something, see $out/other-turn-1.jsonl"
run_turn "$other" other-turn-2 "run ls -a"
[ -n "$(tool_events other-turn-2)" ] || fail "the other session ran no tool, see $out/other-turn-2.jsonl"
if grep --quiet --fixed-strings "$file" "$out/other-turn-2.jsonl"; then
  fail "the other session saw the file, see $out/other-turn-2.jsonl"
fi

# The records outlast the Supervisor.
$kubectl --namespace aviary-system rollout restart deployment/aviary-supervisor > /dev/null
$kubectl --namespace aviary-system rollout status deployment/aviary-supervisor --timeout 120s > /dev/null
connect
[ "$(field "/v1/projects/$project" name)" = e2e ] || fail "the project is gone after a restart of the Supervisor"
[ "$(field "/v1/agents/$agent" harness)" = "$harness" ] || fail "the agent is gone after a restart of the Supervisor"
[ "$(field "/v1/sessions/$session" agent_id)" = "$agent" ] || fail "the session is gone after a restart of the Supervisor"
[ "$(field "/v1/turns/$turn" state)" = completed ] || fail "turn $turn is gone after a restart of the Supervisor"

# Turns beyond the limit wait in the order in which they were sent. A turn
# that loses its pod fails, and the next one runs.
killed=$(create "/v1/agents/$agent/sessions")
first=$(send "$killed" "run sleep 30")
second=$(send "$(create "/v1/agents/$agent/sessions")" "say done")
third=$(send "$(create "/v1/agents/$agent/sessions")" "say done")
[ "$(field "/v1/turns/$second" state)$(field "/v1/turns/$third" state)" = queuedqueued ] ||
  fail "turns $second and $third did not wait for turn $first"
wait_for 60 running "$first" || fail "turn $first did not start"

# A session takes no message while a turn of it has not ended.
status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --request POST --data '{"text": "say again"}' "$api/v1/sessions/$killed/messages")
[ "$status" = 409 ] || fail "a session with a running turn answered a message with $status, not 409"

$kubectl --namespace aviary-runtime delete pod --selector "aviary.io/turn=$first" --wait=false > /dev/null
follow "$first" queue-turn-1
[ "$(ended queue-turn-1)" = turn.failed ] || fail "turn $first did not fail without its pod, see $out/queue-turn-1.jsonl"
[ "$(field "/v1/turns/$third" state)" = queued ] || fail "turn $third did not wait for turn $second"
follow "$second" queue-turn-2
[ "$(ended queue-turn-2)" = turn.completed ] || fail "turn $second did not complete, see $out/queue-turn-2.jsonl"
follow "$third" queue-turn-3
[ "$(ended queue-turn-3)" = turn.completed ] || fail "turn $third did not complete, see $out/queue-turn-3.jsonl"
[[ $(positions queue-turn-3) =~ ^\[[2-9].*,1\]$ ]] ||
  fail "turn $third was not told its way up the queue: $(positions queue-turn-3)"

# The session whose turn failed goes on with its next message.
run_turn "$killed" queue-turn-4 "say done"
[[ $(answer queue-turn-4) == *done* ]] ||
  fail "the session of the failed turn did not answer, see $out/queue-turn-4.jsonl"

echo "PASS: $harness ran turns through the API, continued a session, kept sessions apart and queued turns"
