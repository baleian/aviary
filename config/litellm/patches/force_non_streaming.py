"""
Monkeypatch: force non-streaming for the Anthropic-to-ChatCompletions adapter.

LiteLLM's experimental Anthropic Messages adapter (`/v1/messages` -> OpenAI
`/v1/chat/completions`) has streaming issues when converting responses back
to Anthropic SSE format:

  1. Thinking blocks: thinking_delta consumes max_tokens budget, leaving no
     room for the text block — results in "no response from model".
  2. Tool calls: vLLM's Gemma4 tool parser leaks raw special tokens into
     streaming JSON deltas, producing invalid JSON.

These only affect the adapter path (non-Anthropic backends accessed via
`/v1/messages`). Native Anthropic models go direct and are unaffected.

This patch replaces the adapter handler to always send non-streaming requests
to the backend and wrap the response in a fake Anthropic SSE stream when the
client requested streaming. This guarantees correct response translation at
the cost of slightly higher TTFT (time to first token).

Loaded at Python startup via .pth file. Remove when upstream issues are fixed.
"""


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
