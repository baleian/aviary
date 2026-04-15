"""Prometheus metrics for stream proxying.

Exposed at GET /metrics in Prometheus exposition format. Labeled by `pool`
(and `event_type` / `status` where relevant) so dashboards can slice per-pool
traffic. Cardinality stays bounded by the pool catalog size.
"""

from __future__ import annotations

from prometheus_client import Counter, Gauge, Histogram

active_streams = Gauge(
    "agent_supervisor_active_streams",
    "Number of streams currently being proxied from the supervisor.",
    ["pool"],
)

stream_duration_seconds = Histogram(
    "agent_supervisor_stream_duration_seconds",
    "End-to-end duration of a /v1/stream call.",
    ["pool", "status"],
    buckets=(0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600, 1800),
)

events_total = Counter(
    "agent_supervisor_events_total",
    "Count of SSE events forwarded to Redis, by event type.",
    ["pool", "event_type"],
)

errors_total = Counter(
    "agent_supervisor_errors_total",
    "Failures while proxying a stream, by error kind (dns/http/redis/runtime).",
    ["pool", "error_kind"],
)

redis_publish_duration_seconds = Histogram(
    "agent_supervisor_redis_publish_duration_seconds",
    "Latency of Redis append + publish per event.",
    buckets=(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0),
)
