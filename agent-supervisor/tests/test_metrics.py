def test_metrics_module_imports_and_creates_instruments():
    from app import metrics

    assert metrics.publish_requests_total is not None
    assert metrics.sse_events_total is not None
    assert metrics.active_streams is not None
    assert metrics.publish_duration_seconds is not None

    # Smoke: recording shouldn't raise even without an exporter wired up.
    metrics.publish_requests_total.add(1, {"status": "complete"})
    metrics.active_streams.add(1)
    metrics.active_streams.add(-1)
    metrics.publish_duration_seconds.record(0.5)
