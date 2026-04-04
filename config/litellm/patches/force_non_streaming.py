"""
Monkeypatch: force non-streaming for the Anthropic-to-ChatCompletions adapter.

vLLM's Gemma4 tool-call parser has a streaming bug that leaks raw special
tokens (<|"|>) into JSON argument deltas, producing invalid JSON. LiteLLM's
streaming Anthropic adapter also mishandles thinking blocks for non-Anthropic
backends (thinking_delta consumes max_tokens, no text block emitted).

This patch intercepts the adapter handler and forces non-streaming for
models matching FORCE_NON_STREAMING_PATTERNS, wrapping the response in a
fake Anthropic SSE stream when the client requested streaming. Other models
pass through to the original handler unchanged.

Loaded at Python startup via .pth file. Remove when upstream issues are fixed.

See: https://github.com/vllm-project/vllm/blob/main/vllm/tool_parsers/gemma4_tool_parser.py
"""

import re

# Glob-style patterns for models that should be forced to non-streaming.
# Matched against the full LiteLLM model name (e.g. "ollama/gemma4:26b").
FORCE_NON_STREAMING_PATTERNS = [
    "ollama/gemma4*",
    "vllm/gemma*",
]

_COMPILED = [re.compile(p.replace("*", ".*")) for p in FORCE_NON_STREAMING_PATTERNS]


def _should_force(model: str) -> bool:
    return any(r.fullmatch(model) for r in _COMPILED)


def _apply():
    from typing import AsyncIterator, Dict, List, Optional, Union, cast

    import litellm
    from litellm.llms.anthropic.experimental_pass_through.adapters import (
        handler as _handler,
    )
    from litellm.llms.anthropic.experimental_pass_through.adapters.transformation import (
        AnthropicAdapter,
    )
    from litellm.types.llms.anthropic_messages.anthropic_response import (
        AnthropicMessagesResponse,
    )
    from litellm.types.utils import ModelResponse

    _ADAPTER = AnthropicAdapter()
    _Cls = _handler.LiteLLMMessagesToCompletionTransformationHandler

    # Preserve the original handler for passthrough
    _original = _Cls.async_anthropic_messages_handler

    @staticmethod
    async def async_anthropic_messages_handler(
        max_tokens: int,
        messages: List[Dict],
        model: str,
        metadata: Optional[Dict] = None,
        stop_sequences: Optional[List[str]] = None,
        stream: Optional[bool] = False,
        system: Optional[str] = None,
        temperature: Optional[float] = None,
        thinking: Optional[Dict] = None,
        tool_choice: Optional[Dict] = None,
        tools: Optional[List[Dict]] = None,
        top_k: Optional[int] = None,
        top_p: Optional[float] = None,
        output_format: Optional[Dict] = None,
        **kwargs,
    ) -> Union[AnthropicMessagesResponse, AsyncIterator]:
        if not _should_force(model):
            return await _original(
                max_tokens=max_tokens, messages=messages, model=model,
                metadata=metadata, stop_sequences=stop_sequences, stream=stream,
                system=system, temperature=temperature, thinking=thinking,
                tool_choice=tool_choice, tools=tools, top_k=top_k, top_p=top_p,
                output_format=output_format, **kwargs,
            )

        # Force non-streaming for matched models
        completion_kwargs, tool_name_mapping = _Cls._prepare_completion_kwargs(
            max_tokens=max_tokens,
            messages=messages,
            model=model,
            metadata=metadata,
            stop_sequences=stop_sequences,
            stream=False,
            system=system,
            temperature=temperature,
            thinking=thinking,
            tool_choice=tool_choice,
            tools=tools,
            top_k=top_k,
            top_p=top_p,
            output_format=output_format,
            extra_kwargs=kwargs,
        )

        completion_response = await litellm.acompletion(**completion_kwargs)

        anthropic_response = _ADAPTER.translate_completion_output_params(
            cast(ModelResponse, completion_response),
            tool_name_mapping=tool_name_mapping,
        )
        if anthropic_response is None:
            raise ValueError("Failed to transform response to Anthropic format")

        if stream:
            from litellm.llms.anthropic.experimental_pass_through.messages.fake_stream_iterator import (
                FakeAnthropicMessagesStreamIterator,
            )
            return FakeAnthropicMessagesStreamIterator(anthropic_response)

        return anthropic_response

    _Cls.async_anthropic_messages_handler = async_anthropic_messages_handler


_apply()
del _apply
