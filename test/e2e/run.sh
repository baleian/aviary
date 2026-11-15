#!/usr/bin/env bash
set -euo pipefail

# The e2e runs against a model server of its own, mock-llm, which answers by
# a script. A message to a session is an instruction to it: "say <text>",
# "drawl <text>", "run <command>", "recall" or "fail <text>". See
# test/mock-llm/main.go.

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
# Supervisor breaks the forwarding, so it is then called again. The
# forwarding complains of every stream of events that is not read to its end.
forward=
connect() {
  [ -z "$forward" ] || kill "$forward" 2> /dev/null || true
  $kubectl --namespace aviary-system port-forward service/aviary-supervisor 18080:80 > /dev/null 2>&1 &
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

# written <turn> prints what the record of a turn has the agent say.
written() {
  field "/v1/turns/$1" items | jq --join-output '.[] | select(.type == "message") | .text'
}

# ending <turn> prints how the record of a turn says that it ended: its
# state, why the harness ended it and why it failed, with - for none.
ending() {
  curl --silent --show-error --fail "$api/v1/turns/$1" |
    jq --raw-output '[.state, .stop_reason // "-", .failure.reason // "-"] | join(" ")'
}

# listed <session> [<query>] prints the sequence numbers of the session's
# turns in the order in which the API lists them.
listed() {
  curl --silent --show-error --fail "$api/v1/sessions/$1/turns${2:+?$2}" | jq --compact-output '[.turns[].seq]'
}

# in_events <turn> <text> tells whether the events of the turn so far have
# the text. The stream of a turn that runs does not end, so it is read for a
# second.
in_events() {
  local events
  events=$(curl --silent --max-time 1 "$api/v1/turns/$1/events" || true)
  grep --quiet --fixed-strings "$2" <<< "$events"
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

# cancel <turn> asks for the turn to be cancelled and prints the status of
# the answer.
cancel() {
  curl --silent --output /dev/null --write-out '%{http_code}' --request POST "$api/v1/turns/$1/cancel"
}

# reason <log-name> prints why the turn of that log failed.
reason() {
  jq --slurp --raw-output 'last.data.reason' "$out/$1.jsonl"
}

cleaned_up() { [ -z "$(in_cluster "$1")" ]; }
running() { [ "$(field "/v1/turns/$1" state)" = running ]; }

# recorded <turn> <route> <host> <status> tells whether the Egress Proxy
# recorded a request of the turn to the host that ended with the status. The
# route is llm for a request to the model endpoint and forward for any other.
# There may be several proxies, and any of them may have served the request.
recorded() {
  local log
  log=$($kubectl --namespace aviary-system logs --selector app.kubernetes.io/component=egress-proxy \
    --tail -1 --since 10m)
  awk -v turn="\"turn\":\"$1\"" -v route="\"route\":\"$2\"" -v host="\"host\":\"$3\"" -v status="\"status\":$4," \
    'index($0, turn) && index($0, route) && index($0, host) && index($0, status) { found = 1 }
     END { exit !found }' <<< "$log"
}

# Commands for the agent to run in its pod. fetch gets a URL the way the
# pod is set up to, which is through the Egress Proxy, and prints the status
# of the answer. connect opens a connection to port 80 of an address, without
# the proxy, and prints whether the address answered.
fetch="node -e \"fetch(process.argv[1]).then(r => console.log('status', r.status), () => console.log('failed'))\""
connect="node -e \"const s = require('net').connect({host: process.argv[1], port: 80, timeout: 3000}); \
s.on('connect', () => { console.log('connected'); s.destroy() }); \
s.on('timeout', () => { console.log('no answer'); s.destroy() }); s.on('error', () => console.log('no answer'))\""

# reported tells whether its input is the token usage that mock-llm reports.
# Every answer of mock-llm reports 11 tokens in and 7 out, and a harness
# reports the usage of one answer of a turn or of all of them.
reported() {
  jq --exit-status '((.total_tokens // 0) / 18) as $answers
    | $answers >= 1 and $answers == ($answers | floor)
      and .input_tokens == 11 * $answers and .output_tokens == 7 * $answers' > /dev/null
}

# For the e2e the model endpoint is mock-llm, and one turn runs at a time,
# which the check of the queue needs. mock-llm is a service in the cluster,
# which the Egress Proxy reaches only if its network policy names it. It is
# also the only host that the proxy lets requests through to.
mock_llm_egress='[{"to": [{"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "aviary-e2e"}}}]}]'
mkdir -p "$out"
$kubectl apply --filename test/e2e/mock-llm.yaml > /dev/null
$kubectl --namespace aviary-e2e rollout status deployment/mock-llm --timeout 120s > /dev/null ||
  fail "mock-llm did not start; run make images"
$helm get values aviary --output yaml > "$out/values.yaml" || fail "Aviary is not installed; run make deploy"
changed=yes
settings=(
  --set llm.url=http://mock-llm.aviary-e2e/v1 --set llm.model=mock
  --set llm.apiKey.secretName=aviary-llm-mock --set runtime.maxConcurrentTurns=1
  --set-json "egressProxy.extraEgress=$mock_llm_egress"
  --set 'egressProxy.allowedHosts={mock-llm.aviary-e2e}'
)
install "${settings[@]}"
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

# The conversation can be read back, newest turn first and a few turns at a
# time. The record of a turn has what its events told.
[ "$(listed "$session")" = "[3,2,1]" ] || fail "the turns of the session are listed as $(listed "$session")"
[ "$(listed "$session" "limit=2")" = "[3,2]" ] ||
  fail "the two newest turns of the session are listed as $(listed "$session" "limit=2")"
[ "$(listed "$session" "limit=2&before=2")" = "[1]" ] ||
  fail "the turns before turn 2 are listed as $(listed "$session" "limit=2&before=2")"
[ "$(written "$turn")" = "$(answer session-turn-3)" ] ||
  fail "the record of turn $turn does not have what the agent said, see $out/session-turn-3.jsonl"
field "/v1/turns/$turn" items | jq --exit-status --arg content "$content" \
  'any(.[]; .type == "tool_call" and .status == "completed" and (tostring | contains($content)))' > /dev/null ||
  fail "the record of turn $turn does not have the tool call with the file content"

# The record does not need the events, which Valkey keeps only for a while.
valkey_password=$($kubectl --namespace aviary-data get secret valkey-users \
  --output jsonpath='{.data.default}' | base64 --decode)
deleted=$($kubectl --namespace aviary-data exec deployment/valkey --container valkey -- \
  env REDISCLI_AUTH="$valkey_password" valkey-cli del "turn:$turn")
[ "$deleted" = 1 ] || fail "the events of turn $turn were not found in Valkey"
[ "$(written "$turn")" = "$(answer session-turn-3)" ] ||
  fail "the record of turn $turn is gone with its events"

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

# The agent does not hold the key of the model endpoint. Its pod has the
# Egress Proxy as the endpoint and the token of its turn as the key, and the
# proxy records which turn a model request came from. mock-llm answers only
# requests with its key, so every turn that completes got it from the proxy.
llm_key=$($kubectl --namespace aviary-system get secret aviary-llm-mock \
  --output jsonpath='{.data.api-key}' | base64 --decode)
run_turn "$other" other-turn-3 "run tr '\\0' '\\n' < /proc/1/environ"
grep --quiet --fixed-strings AVIARY_LLM_BASE_URL=http://aviary-egress-proxy "$out/other-turn-3.jsonl" ||
  fail "the pod of turn $turn does not have the Egress Proxy as its model endpoint, see $out/other-turn-3.jsonl"
if grep --quiet --fixed-strings "$llm_key" "$out/other-turn-3.jsonl"; then
  fail "the pod of turn $turn holds the key of the model endpoint, see $out/other-turn-3.jsonl"
fi
wait_for 30 recorded "$turn" llm mock-llm.aviary-e2e 200 ||
  fail "the Egress Proxy recorded no model request of turn $turn"

# The pod reaches the outside only through the Egress Proxy, which lets a
# request through to an allowed host and refuses any other, and records both
# with the turn. Without the proxy the pod reaches nothing: not even the
# address of the host that the proxy lets it reach.
run_turn "$other" egress-turn-1 "run $fetch http://mock-llm.aviary-e2e/healthz"
[[ $(answer egress-turn-1) == *"status 200"* ]] ||
  fail "the agent did not reach an allowed host through the Egress Proxy, see $out/egress-turn-1.jsonl"
wait_for 30 recorded "$turn" forward mock-llm.aviary-e2e 200 ||
  fail "the Egress Proxy did not record the request of turn $turn that it let through"
run_turn "$other" egress-turn-2 "run $fetch http://example.com/"
[[ $(answer egress-turn-2) == *"status 403"* ]] ||
  fail "the Egress Proxy did not refuse a host that is not allowed, see $out/egress-turn-2.jsonl"
wait_for 30 recorded "$turn" forward example.com 403 ||
  fail "the Egress Proxy did not record the request of turn $turn that it refused"
mock_llm_ip=$($kubectl --namespace aviary-e2e get service mock-llm --output jsonpath='{.spec.clusterIP}')
run_turn "$other" egress-turn-3 "run $connect $mock_llm_ip"
[[ $(answer egress-turn-3) == *"no answer"* ]] ||
  fail "the pod of turn $turn connected to an address without the Egress Proxy, see $out/egress-turn-3.jsonl"

# The record has a long answer whole. A turn with more events than are read
# at once is the check.
words=$(seq --format 'w%g' --separator ' ' 1 1200)
run_turn "$other" record-turn-long "say $words"
[ "$(wc --lines < "$out/record-turn-long.jsonl")" -gt 1000 ] ||
  fail "turn $turn has too few events for the check, see $out/record-turn-long.jsonl"
[[ $(written "$turn") == "$(answer record-turn-long)" && $(written "$turn") == *"$words"* ]] ||
  fail "the record of turn $turn does not have the whole answer, see $out/record-turn-long.jsonl"

# A tool may return the character U+0000, which the database cannot keep.
# The turn ends all the same, and its record has a mark in that place.
run_turn "$other" record-turn-nul "run printf 'a\\0b'"
grep --quiet --fixed-strings 'a\u0000b' "$out/record-turn-nul.jsonl" ||
  fail "the events of turn $turn lack the character, see $out/record-turn-nul.jsonl"
field "/v1/turns/$turn" items | jq --exit-status 'tostring | contains("a\ufffdb")' > /dev/null ||
  fail "the record of turn $turn lacks what the tool returned"

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
fourth=$(send "$(create "/v1/agents/$agent/sessions")" "say never")
[ "$(field "/v1/turns/$second" state)$(field "/v1/turns/$third" state)" = queuedqueued ] ||
  fail "turns $second and $third did not wait for turn $first"
wait_for 60 running "$first" || fail "turn $first did not start"

# A turn that waits is cancelled at once and never runs.
[ "$(cancel "$fourth")" = 200 ] || fail "cancelling the waiting turn $fourth was not answered with 200"
follow "$fourth" queue-turn-cancelled
[ "$(ended queue-turn-cancelled)" = turn.cancelled ] ||
  fail "turn $fourth did not end as cancelled, see $out/queue-turn-cancelled.jsonl"
[ "$(ending "$fourth") $(field "/v1/turns/$fourth" started_at) $(field "/v1/turns/$fourth" "items | length")" = "cancelled - - null 0" ] ||
  fail "the record of turn $fourth, which never ran, says that it ended as $(ending "$fourth")"

# A session takes no message while a turn of it has not ended.
status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --request POST --data '{"text": "say again"}' "$api/v1/sessions/$killed/messages")
[ "$status" = 409 ] || fail "a session with a running turn answered a message with $status, not 409"

# A pod that is stopped while its agent works gets the harness to end the
# turn first. The record keeps what the agent did until then and tells how
# the turn ended.
wait_for 60 in_events "$first" "event: tool.call" || fail "turn $first ran no tool"
$kubectl --namespace aviary-runtime delete pod --selector "aviary.io/turn=$first" --wait=false > /dev/null
follow "$first" queue-turn-1
[ "$(ended queue-turn-1)" = turn.failed ] || fail "turn $first did not fail without its pod, see $out/queue-turn-1.jsonl"
[ "$(reason queue-turn-1)" = pod_lost ] ||
  fail "turn $first, whose pod was deleted, failed for the reason $(reason queue-turn-1)"
[ "$(ending "$first")" = "failed cancelled pod_lost" ] ||
  fail "the record of turn $first, whose pod was deleted, says that it ended as $(ending "$first")"
field "/v1/turns/$first" items | jq --exit-status 'any(.[]; .type == "tool_call")' > /dev/null ||
  fail "the record of turn $first lacks the tool call that was cut off"
[ "$(field "/v1/turns/$third" state)" = queued ] || fail "turn $third did not wait for turn $second"
follow "$second" queue-turn-2
[ "$(ended queue-turn-2)" = turn.completed ] || fail "turn $second did not complete, see $out/queue-turn-2.jsonl"
follow "$third" queue-turn-3
[ "$(ended queue-turn-3)" = turn.completed ] || fail "turn $third did not complete, see $out/queue-turn-3.jsonl"
[[ $(positions queue-turn-3) =~ ^\[[2-9].*,1\]$ ]] ||
  fail "turn $third was not told its way up the queue: $(positions queue-turn-3)"
if grep --quiet turn.started "$out/queue-turn-cancelled.jsonl" || ! cleaned_up "$fourth"; then
  fail "the cancelled turn $fourth ran"
fi

# The session whose turn failed goes on with its next message.
run_turn "$killed" queue-turn-4 "say done"
[[ $(answer queue-turn-4) == *done* ]] ||
  fail "the session of the failed turn did not answer, see $out/queue-turn-4.jsonl"

# A turn records the token usage that its harness reported, and its last
# event carries it.
field "/v1/turns/$turn" usage | reported ||
  fail "turn $turn does not record the token usage that mock-llm reported"
jq --slurp 'last.data.usage' "$out/queue-turn-4.jsonl" | reported ||
  fail "the last event of turn $turn lacks the token usage, see $out/queue-turn-4.jsonl"

# A turn that fails says why. The model endpoint refuses this one, and the
# pod of the one above was deleted.
refused=$(send "$killed" "fail the model is out of order")
follow "$refused" refused-turn
[ "$(ended refused-turn)" = turn.failed ] ||
  fail "turn $refused did not fail when the model endpoint refused it, see $out/refused-turn.jsonl"
[ "$(reason refused-turn)" = harness_error ] ||
  fail "turn $refused, which the model endpoint refused, failed for the reason $(reason refused-turn)"

# No more turns wait than may, a turn does not wait for longer than it may,
# and a turn that runs is stopped when it is cancelled.
install "${settings[@]}" --set runtime.maxQueuedTurns=1 --set runtime.maxQueueWaitSeconds=5
connect
stopped=$(create "/v1/agents/$agent/sessions")
running=$(send "$stopped" "run sleep 120")
wait_for 60 running "$running" || fail "turn $running did not start"
waiting=$(send "$(create "/v1/agents/$agent/sessions")" "say late")
status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --request POST --data '{"text": "say more"}' "$api/v1/sessions/$(create "/v1/agents/$agent/sessions")/messages")
[ "$status" = 429 ] || fail "a message beyond the most that may wait was answered with $status, not 429"
follow "$waiting" queue-turn-expired
[ "$(ended queue-turn-expired)" = turn.expired ] ||
  fail "turn $waiting did not expire, see $out/queue-turn-expired.jsonl"
[ "$(cancel "$running")" = 202 ] || fail "cancelling the running turn $running was not answered with 202"
follow "$running" cancelled-turn
[ "$(ended cancelled-turn)" = turn.cancelled ] ||
  fail "turn $running did not end as cancelled, see $out/cancelled-turn.jsonl"
wait_for 60 cleaned_up "$running" || fail "the Job or the pod of the cancelled turn $running was not deleted"
[ "$(ending "$running")" = "cancelled cancelled -" ] ||
  fail "the record of the cancelled turn $running says that it ended as $(ending "$running")"
run_turn "$stopped" cancelled-turn-next "say done"

# An answer that is cut off is kept as far as it got.
cut=$(send "$stopped" "drawl $(seq --format 'w%g' --separator ' ' 1 40)")
wait_for 60 in_events "$cut" "w2 " || fail "turn $cut did not begin its answer"
[ "$(cancel "$cut")" = 202 ] || fail "cancelling the running turn $cut was not answered with 202"
follow "$cut" cut-turn
[ "$(ended cut-turn)" = turn.cancelled ] || fail "turn $cut did not end as cancelled, see $out/cut-turn.jsonl"
[[ $(written "$cut") == "$(answer cut-turn)" && $(written "$cut") == *"w1 w2 "* && $(written "$cut") != *w40* ]] ||
  fail "the record of turn $cut does not have the answer as far as it got, see $out/cut-turn.jsonl"

echo "PASS: $harness ran turns through the API, continued a session, kept sessions apart," \
  "reached the model without holding its key and the outside only through the Egress Proxy," \
  "queued, cancelled and expired turns, gave failed turns a reason, recorded token usage" \
  "and kept what was said, also in turns that were cut off"
