"""Wire event types for the session message stream.

Supervisor SSE emits events typed by the `type` key. The API relays a
subset to WebSocket clients and injects its own lifecycle events
(user_message, done, cancelled, error, replay_start/end, stream_complete).
"""

# Supervisor → API
QUERY_STARTED = "query_started"
CHUNK = "chunk"
THINKING = "thinking"
TOOL_USE = "tool_use"
TOOL_RESULT = "tool_result"
TOOL_PROGRESS = "tool_progress"

# API → WebSocket
USER_MESSAGE = "user_message"
DONE = "done"
CANCELLED = "cancelled"
ERROR = "error"
REPLAY_START = "replay_start"
REPLAY_END = "replay_end"
STREAM_COMPLETE = "stream_complete"

# Events relayed verbatim (supervisor → ws) + buffered for replay
BUFFERED_TYPES = frozenset({CHUNK, THINKING, TOOL_USE, TOOL_RESULT})

# Events relayed realtime but NOT buffered (noisy progress)
REALTIME_ONLY_TYPES = frozenset({TOOL_PROGRESS})
