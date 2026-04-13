"""Block reconstruction and tool result attachment — pure data transformation."""

# Tool result content larger than this is truncated before persisting / display.
# Avoids storing megabytes of grep/find output in Postgres JSONB.
MAX_TOOL_RESULT_BYTES = 10240


def truncate_tool_result(content: object) -> object:
    """Truncate string tool results above the persistence cap; pass-through otherwise."""
    if isinstance(content, str) and len(content) > MAX_TOOL_RESULT_BYTES:
        return content[:MAX_TOOL_RESULT_BYTES] + "\n... (truncated)"
    return content


def attach_tool_results(
    blocks: list[dict], results: dict[str, dict]
) -> None:
    """Attach tool_result content to matching tool_call blocks (in-place)."""
    for block in blocks:
        tid = block.get("tool_use_id")
        if block.get("type") == "tool_call" and tid and tid in results:
            tr = results[tid]
            block["result"] = tr["content"]
            if tr.get("is_error"):
                block["is_error"] = True


def rebuild_blocks_from_chunks(chunks: list[dict]) -> tuple[str, list[dict]]:
    """Reconstruct full_response text and blocks_meta from buffered Redis chunks."""
    full_text = ""
    blocks: list[dict] = []
    current_thinking = ""
    current_text = ""
    tool_results: dict[str, dict] = {}

    for chunk in chunks:
        ct = chunk.get("type")
        if ct == "chunk":
            current_text += chunk.get("content", "")
            full_text += chunk.get("content", "")
        elif ct == "thinking":
            current_thinking += chunk.get("content", "")
        elif ct == "tool_use":
            if current_thinking:
                blocks.append({"type": "thinking", "content": current_thinking})
                current_thinking = ""
            if current_text:
                blocks.append({"type": "text", "content": current_text})
                current_text = ""
            tool_block: dict = {
                "type": "tool_call",
                "name": chunk.get("name"),
                "input": chunk.get("input"),
                "tool_use_id": chunk.get("tool_use_id"),
            }
            if chunk.get("parent_tool_use_id"):
                tool_block["parent_tool_use_id"] = chunk["parent_tool_use_id"]
            blocks.append(tool_block)
        elif ct == "tool_result":
            tid = chunk.get("tool_use_id")
            if tid:
                tool_results[tid] = {
                    "content": truncate_tool_result(chunk.get("content", "")),
                    "is_error": chunk.get("is_error", False),
                }

    if current_thinking:
        blocks.append({"type": "thinking", "content": current_thinking})
    if current_text:
        blocks.append({"type": "text", "content": current_text})

    attach_tool_results(blocks, tool_results)
    return full_text, blocks
