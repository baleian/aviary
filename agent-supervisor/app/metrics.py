"""OTel metrics for the supervisor — pushed via OTLP/HTTP.

Designed for the RED method (Rate / Errors / Duration) plus a few
up-down counters for in-flight load. Label sets are kept small to
avoid cardinality blow-up: no session_id / agent_id / stream_id labels.
SLO-relevant histogram bucket boundaries are pinned via Views in
``main._otel_views``.
"""

from opentelemetry.metrics import get_meter

_meter = get_meter("aviary.supervisor")


# ── Counters ────────────────────────────────────────────────────────────────

publish_requests_total = _meter.create_counter(
    "aviary_supervisor_publish_requests_total",
    description="Publish requests handled by the supervisor.",
)

a2a_requests_total = _meter.create_counter(
    "aviary_supervisor_a2a_requests_total",
    description="A2A sub-agent stream requests handled by the supervisor.",
)

sse_events_total = _meter.create_counter(
    "aviary_supervisor_sse_events_total",
    description="Runtime SSE events consumed by the supervisor.",
)

runtime_http_errors_total = _meter.create_counter(
    "aviary_supervisor_runtime_http_errors_total",
    description="Non-2xx HTTP responses from the runtime pool.",
)

abort_requests_total = _meter.create_counter(
    "aviary_supervisor_abort_requests_total",
    description="Abort requests received on /v1/streams/{id}/abort.",
)

redis_errors_total = _meter.create_counter(
    "aviary_supervisor_redis_errors_total",
    description="Redis operation failures.",
)


# ── Up-down counters (gauges) ───────────────────────────────────────────────

active_streams = _meter.create_up_down_counter(
    "aviary_supervisor_active_streams",
    description="Currently in-flight /message streams on this replica.",
)

active_a2a_streams = _meter.create_up_down_counter(
    "aviary_supervisor_active_a2a_streams",
    description="Currently in-flight /a2a sub-agent streams on this replica.",
)


# ── Histograms ──────────────────────────────────────────────────────────────

publish_duration_seconds = _meter.create_histogram(
    "aviary_supervisor_publish_duration_seconds",
    unit="s",
    description="Wall-clock time from publish request start to finish.",
)

a2a_duration_seconds = _meter.create_histogram(
    "aviary_supervisor_a2a_duration_seconds",
    unit="s",
    description="Wall-clock time for /a2a sub-agent streams.",
)

time_to_query_started_seconds = _meter.create_histogram(
    "aviary_supervisor_time_to_query_started_seconds",
    unit="s",
    description="Time from publish start to the runtime's first `query_started` event (TTFB).",
)

vault_fetch_duration_seconds = _meter.create_histogram(
    "aviary_supervisor_vault_fetch_duration_seconds",
    unit="s",
    description="Time spent fetching user credentials from Vault.",
)
