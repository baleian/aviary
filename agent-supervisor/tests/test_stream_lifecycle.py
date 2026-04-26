"""Focused tests for _StreamLifecycle state transitions + metric bumps."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from app.services.stream_service import _StreamLifecycle


@pytest.mark.asyncio
async def test_begin_sets_status_keys_and_increments_active():
    lc = _StreamLifecycle("s1", "stream-1")
    with (
        patch("app.redis_client.set_stream_status", new_callable=AsyncMock) as stream_status,
        patch("app.redis_client.set_session_status", new_callable=AsyncMock) as session_status,
        patch("app.redis_client.set_session_latest_stream", new_callable=AsyncMock) as latest,
        patch("app.metrics.active_streams", new=MagicMock()) as active,
    ):
        await lc.begin()

    stream_status.assert_awaited_once_with("stream-1", "streaming")
    session_status.assert_awaited_once_with("s1", "streaming")
    latest.assert_awaited_once_with("s1", "stream-1")
    active.add.assert_called_once_with(1)


@pytest.mark.asyncio
async def test_end_decrements_active_and_flips_session_idle():
    lc = _StreamLifecycle("s2", "stream-2")
    with (
        patch("app.redis_client.set_session_status", new_callable=AsyncMock) as session_status,
        patch("app.metrics.active_streams", new=MagicMock()) as active,
    ):
        await lc.end()

    session_status.assert_awaited_once_with("s2", "idle")
    active.add.assert_called_once_with(-1)


@pytest.mark.asyncio
async def test_mark_complete_writes_status_and_bumps_counter():
    lc = _StreamLifecycle("s3", "stream-3")
    with (
        patch("app.redis_client.set_stream_status", new_callable=AsyncMock) as stream_status,
        patch("app.metrics.publish_requests_total", new=MagicMock()) as counter,
    ):
        await lc.mark_complete()

    stream_status.assert_awaited_once_with("stream-3", "complete")
    counter.add.assert_called_once_with(1, {"status": "complete"})


@pytest.mark.asyncio
async def test_mark_error_writes_status_and_bumps_counter():
    lc = _StreamLifecycle("s4", "stream-4")
    with (
        patch("app.redis_client.set_stream_status", new_callable=AsyncMock) as stream_status,
        patch("app.metrics.publish_requests_total", new=MagicMock()) as counter,
    ):
        await lc.mark_error()

    stream_status.assert_awaited_once_with("stream-4", "error")
    counter.add.assert_called_once_with(1, {"status": "error"})


@pytest.mark.asyncio
async def test_mark_aborted_writes_status_and_bumps_counter():
    lc = _StreamLifecycle("s5", "stream-5")
    with (
        patch("app.redis_client.set_stream_status", new_callable=AsyncMock) as stream_status,
        patch("app.metrics.publish_requests_total", new=MagicMock()) as counter,
    ):
        await lc.mark_aborted()

    stream_status.assert_awaited_once_with("stream-5", "aborted")
    counter.add.assert_called_once_with(1, {"status": "aborted"})
