"""
Monkeypatch: fix Anthropic adapter streaming for non-Anthropic backends.

LiteLLM's experimental Anthropic Messages adapter (/v1/messages -> OpenAI
/v1/chat/completions) has two streaming issues when converting responses
back to Anthropic SSE format:

1. Block type detection: the block type detector only checks `thinking_blocks`
   to identify thinking content, missing `reasoning_content` used by providers
   like Ollama and OpenRouter. This causes thinking_delta events to be emitted
   inside a "text" content block instead of a separate "thinking" block, which
   breaks Claude Code CLI's stream_event forwarding.

2. Dropped trigger delta: when a block transition occurs (e.g. thinking -> text),
   LiteLLM drops the first delta of the new block. This loses the first token.

3. Thinking block flushing: Claude Code CLI emits one `assistant` snapshot per
   completed content block. Without periodic flushing, long thinking blocks
   arrive as a single chunk. This patch injects periodic block stop/start
   events at the SSE byte layer (not touching the iterator's internal state)
   to force intermediate snapshots for real-time thinking streaming.
   Only thinking blocks are flushed — text blocks are left intact because
   internal CLI calls (WebFetch, subagents) expect a single text block and
   would truncate results if text were split across multiple blocks.

Loaded at Python startup via .pth file. Remove when upstream issues are fixed.
"""

import json


def _apply():
    from litellm.llms.anthropic.experimental_pass_through.adapters.streaming_iterator import (
        AnthropicStreamWrapper,
    )
    from litellm.llms.anthropic.experimental_pass_through.adapters.transformation import (
        LiteLLMAnthropicMessagesAdapter,
    )

    # ── Fix 1: Block type detection ──────────────────────────────

    _orig_block_detect = (
        LiteLLMAnthropicMessagesAdapter
        ._translate_streaming_openai_chunk_to_anthropic_content_block
    )

    def _patched_block_detect(self, choices):
        from litellm.types.utils import StreamingChoices

        block_type, block_start = _orig_block_detect(self, choices)
        if block_type == "text":
            for choice in choices:
                if isinstance(choice, StreamingChoices) and hasattr(
                    choice.delta, "reasoning_content"
                ):
                    if choice.delta.reasoning_content is not None:
                        from litellm.types.llms.anthropic import (
                            ChatCompletionThinkingBlock,
                        )
                        return "thinking", ChatCompletionThinkingBlock(
                            type="thinking", thinking="", signature=""
                        )
        return block_type, block_start

    LiteLLMAnthropicMessagesAdapter._translate_streaming_openai_chunk_to_anthropic_content_block = (
        _patched_block_detect
    )

    # ── Fix 2: Save trigger delta on block transitions ───────────

    _orig_should_start = AnthropicStreamWrapper._should_start_new_content_block

    def _patched_should_start(self, chunk):
        result = _orig_should_start(self, chunk)
        if result:
            self._trigger_chunk = chunk
        return result

    AnthropicStreamWrapper._should_start_new_content_block = _patched_should_start

    _orig_anext = AnthropicStreamWrapper.__anext__

    async def _patched_anext(self):
        if getattr(self, "_pending_trigger_delta", None) is not None:
            delta = self._pending_trigger_delta
            self._pending_trigger_delta = None
            return delta

        result = await _orig_anext(self)

        if (
            isinstance(result, dict)
            and result.get("type") == "content_block_start"
            and getattr(self, "_trigger_chunk", None) is not None
        ):
            chunk = self._trigger_chunk
            self._trigger_chunk = None
            processed = LiteLLMAnthropicMessagesAdapter().translate_streaming_openai_response_to_anthropic(
                response=chunk,
                current_content_block_index=self.current_content_block_index,
            )
            if processed.get("type") == "content_block_delta":
                self._pending_trigger_delta = processed

        return result

    AnthropicStreamWrapper.__anext__ = _patched_anext

    _orig_next = AnthropicStreamWrapper.__next__

    def _patched_next(self):
        if getattr(self, "_pending_trigger_delta", None) is not None:
            delta = self._pending_trigger_delta
            self._pending_trigger_delta = None
            return delta

        result = _orig_next(self)

        if (
            isinstance(result, dict)
            and result.get("type") == "content_block_start"
            and getattr(self, "_trigger_chunk", None) is not None
        ):
            chunk = self._trigger_chunk
            self._trigger_chunk = None
            processed = LiteLLMAnthropicMessagesAdapter().translate_streaming_openai_response_to_anthropic(
                response=chunk,
                current_content_block_index=self.current_content_block_index,
            )
            if processed.get("type") == "content_block_delta":
                self._pending_trigger_delta = processed

        return result

    AnthropicStreamWrapper.__next__ = _patched_next

    # ── Fix 3: Periodic thinking block flush at the SSE byte layer ─
    # Injects stop/start events into the SSE output WITHOUT touching
    # the streaming iterator's internal state. Rewrites delta indices
    # so they match the virtual block structure.
    # Only thinking_delta is flushed — text blocks are left intact.

    FLUSH_EVERY = 10

    _orig_async_sse = AnthropicStreamWrapper.async_anthropic_sse_wrapper

    def _make_sse(event_type, data):
        return f"event: {event_type}\ndata: {json.dumps(data)}\n\n".encode()

    async def _patched_async_sse(self):
        counter = 0
        idx_offset = 0  # added to all indices for consistency

        async for chunk in _orig_async_sse(self):
            if not isinstance(chunk, bytes):
                yield chunk
                continue

            # Parse the SSE payload
            text = chunk.decode("utf-8", errors="replace")
            # Extract JSON from "data: {...}" line
            data_line = None
            for line in text.split("\n"):
                if line.startswith("data: "):
                    data_line = line[6:]
                    break
            if data_line is None:
                yield chunk
                continue

            try:
                d = json.loads(data_line)
            except (json.JSONDecodeError, ValueError):
                yield chunk
                continue

            rtype = d.get("type", "")

            # Apply index offset to all block-related events
            if "index" in d and idx_offset > 0:
                d = {**d, "index": d["index"] + idx_offset}

            # Flush logic — only for thinking_delta.
            # Text blocks are NOT flushed because internal CLI calls
            # (WebFetch, subagents) expect a single text block per response.
            # Flushing text would cause the CLI to keep only the last block,
            # truncating tool results.
            if rtype == "content_block_delta":
                delta_type = d.get("delta", {}).get("type", "")
                if delta_type == "thinking_delta":
                    counter += 1
                    if counter >= FLUSH_EVERY:
                        counter = 0
                        idx = d["index"]
                        new_idx = idx + 1
                        idx_offset += 1

                        yield _make_sse("content_block_delta", d)
                        yield _make_sse("content_block_stop", {
                            "type": "content_block_stop", "index": idx,
                        })
                        yield _make_sse("content_block_start", {
                            "type": "content_block_start",
                            "index": new_idx,
                            "content_block": {"type": "thinking", "thinking": ""},
                        })
                        continue
                else:
                    counter = 0
            elif rtype in ("content_block_start", "content_block_stop"):
                counter = 0

            # Re-serialize with potentially updated index
            event_type = str(d.get("type", "message"))
            yield _make_sse(event_type, d)

    AnthropicStreamWrapper.async_anthropic_sse_wrapper = _patched_async_sse

    # Sync version
    _orig_sync_sse = AnthropicStreamWrapper.anthropic_sse_wrapper

    def _patched_sync_sse(self):
        counter = 0
        idx_offset = 0

        for chunk in _orig_sync_sse(self):
            if not isinstance(chunk, bytes):
                yield chunk
                continue

            text = chunk.decode("utf-8", errors="replace")
            data_line = None
            for line in text.split("\n"):
                if line.startswith("data: "):
                    data_line = line[6:]
                    break
            if data_line is None:
                yield chunk
                continue

            try:
                d = json.loads(data_line)
            except (json.JSONDecodeError, ValueError):
                yield chunk
                continue

            rtype = d.get("type", "")

            if "index" in d and idx_offset > 0:
                d = {**d, "index": d["index"] + idx_offset}

            if rtype == "content_block_delta":
                delta_type = d.get("delta", {}).get("type", "")
                if delta_type == "thinking_delta":
                    counter += 1
                    if counter >= FLUSH_EVERY:
                        counter = 0
                        idx = d["index"]
                        new_idx = idx + 1
                        idx_offset += 1

                        yield _make_sse("content_block_delta", d)
                        yield _make_sse("content_block_stop", {
                            "type": "content_block_stop", "index": idx,
                        })
                        yield _make_sse("content_block_start", {
                            "type": "content_block_start",
                            "index": new_idx,
                            "content_block": {"type": "thinking", "thinking": ""},
                        })
                        continue
                else:
                    counter = 0
            elif rtype in ("content_block_start", "content_block_stop"):
                counter = 0

            event_type = str(d.get("type", "message"))
            yield _make_sse(event_type, d)

    AnthropicStreamWrapper.anthropic_sse_wrapper = _patched_sync_sse


_apply()
del _apply
