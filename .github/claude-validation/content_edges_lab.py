"""Pinned native CLI serialization and equivalent gateway content fixtures.

All images, documents, tool calls, and signatures are synthetic. EXTRA_BODY
proves what the native client serializes, not provider validation or fetching.
"""
import base64
import copy
import json
import sys

MODELS = ["claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001",
          "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"]
PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aP9sAAAAASUVORK5CYII="
PDF = base64.b64encode(b"%PDF-1.4\n% Local inert content fixture\n%%EOF\n").decode()
URL = "https://content-audit.invalid/synthetic.png"
PROMPT = "LOCAL_CONTENT_AUDIT: 中文 😀"
SYSTEM = "LOCAL_DEVELOPER_INSTRUCTION: answer in JSON"


def fixtures():
    rows = []
    for control in ["system", "developer", "image-inline", "image-url", "document-inline",
                    "tool-text", "tool-image-inline", "tool-image-url", "tool-document-inline",
                    "tool-empty", "legacy-function", "temperature-zero", "top-p-zero",
                    "signed-thinking", "redacted-thinking", "invalid-tool-arguments"]:
        native = {"messages": [{"role": "user", "content": PROMPT}]}
        chat = copy.deepcopy(native)
        responses = {"input": [{"role": "user", "content": PROMPT}]}
        expected = {"control": control}
        if control in ("system", "developer"):
            native["system"] = SYSTEM
            chat["messages"].insert(0, {"role": control, "content": SYSTEM})
            responses["input"].insert(0, {"role": control, "content": SYSTEM})
            expected["system_text"] = SYSTEM
        elif control.startswith("image-") or control == "document-inline":
            document = control == "document-inline"
            source = {"type": "url", "url": URL} if control == "image-url" else {
                "type": "base64", "media_type": "application/pdf" if document else "image/png",
                "data": PDF if document else PNG}
            uri = URL if control == "image-url" else "data:" + source["media_type"] + ";base64," + source["data"]
            native["messages"][0]["content"] = [{"type": "text", "text": PROMPT},
                {"type": "document" if document else "image", "source": source}]
            chat["messages"][0]["content"] = [{"type": "text", "text": PROMPT},
                {"type": "file", "file": {"filename": "local.pdf", "file_data": uri}} if document else
                {"type": "image_url", "image_url": {"url": uri}}]
            responses["input"][0]["content"] = [{"type": "input_text", "text": PROMPT},
                {"type": "input_file", "filename": "local.pdf", "file_data": uri} if document else
                {"type": "input_image", "image_url": uri}]
            expected.update(block_type="document" if document else "image", source=source)
        elif control in ("temperature-zero", "top-p-zero"):
            key = "temperature" if control == "temperature-zero" else "top_p"
            for body in (native, chat, responses):
                body[key] = 0
            expected.update(sampling_key=key, sampling_value=0)
        else:
            native["tools"] = [{"name": "audit_tool", "input_schema": {"type": "object", "properties": {}}}]
            chat["tools"] = [{"type": "function", "function": {"name": "audit_tool", "parameters": {"type": "object", "properties": {}}}}]
            responses["tools"] = [{"type": "function", "name": "audit_tool", "parameters": {"type": "object", "properties": {}}}]
            native_call = {"type": "tool_use", "id": "toolu_content", "name": "audit_tool", "input": {"value": "中文"}}
            cc_call = {"id": "toolu_content", "type": "function", "function": {"name": "audit_tool", "arguments": '{"value":"中文"}'}}
            response_call = {"type": "function_call", "call_id": "toolu_content", "name": "audit_tool", "arguments": '{"value":"中文"}'}
            native_result = "LOCAL_TOOL_RESULT"
            response_result = native_result
            chat_result = native_result
            if control == "tool-empty":
                native_result = response_result = chat_result = ""
                expected["known_policy"] = "empty tool output becomes (empty) in converters"
            if control in ("tool-image-inline", "tool-image-url", "tool-document-inline"):
                document = control == "tool-document-inline"
                source = {"type": "url", "url": URL} if control == "tool-image-url" else {
                    "type": "base64", "media_type": "application/pdf" if document else "image/png",
                    "data": PDF if document else PNG}
                uri = URL if control == "tool-image-url" else "data:" + source["media_type"] + ";base64," + source["data"]
                native_result = [{"type": "text", "text": "LOCAL_TOOL_RESULT"}, {"type": "document" if document else "image", "source": source}]
                response_result = [{"type": "input_text", "text": "LOCAL_TOOL_RESULT"},
                    {"type": "input_file", "filename": "local.pdf", "file_data": uri} if document else
                    {"type": "input_image", "image_url": uri}]
                # Chat tool output supports text; use the existing compatible
                # user-message lifting representation for media.
                expected.update(block_type="document" if document else "image", source=source, tool_media=True)
            native["messages"] += [{"role": "assistant", "content": [native_call]},
                {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_content", "content": native_result}]}]
            chat["messages"] += [{"role": "assistant", "tool_calls": [cc_call]},
                {"role": "tool", "tool_call_id": "toolu_content", "content": chat_result}]
            responses["input"] += [response_call, {"type": "function_call_output", "call_id": "toolu_content", "output": response_result}]
            if expected.get("tool_media"):
                chat["messages"].append({"role": "user", "content": [
                    {"type": "file", "file": {"filename": "local.pdf", "file_data": uri}} if document else
                    {"type": "image_url", "image_url": {"url": uri}}]})
            if control == "legacy-function":
                chat["functions"] = [chat.pop("tools")[0]["function"]]
                chat["messages"][1] = {"role": "assistant", "function_call": cc_call["function"]}
                chat["messages"][2] = {"role": "function", "name": "audit_tool", "content": chat_result}
                expected["tool_round_trip"] = True
            if control in ("signed-thinking", "redacted-thinking"):
                block = {"type": "thinking", "thinking": "LOCAL_SYNTHETIC_THOUGHT", "signature": "LOCAL_SYNTHETIC_SIGNATURE"} if control == "signed-thinking" else {"type": "redacted_thinking", "data": "LOCAL_SYNTHETIC_REDACTED"}
                native["messages"][1]["content"].insert(0, block)
                envelope = "anthropic-thinking-v1:" + base64.b64encode(json.dumps(block).encode()).decode().rstrip("=")
                responses["input"].insert(1, {"type": "reasoning", "encrypted_content": envelope})
                chat = None  # No opaque Anthropic envelope in the Chat contract.
                expected["thinking_block"] = block
            if control == "invalid-tool-arguments":
                cc_call["function"]["arguments"] = '{"value":'
                response_call["arguments"] = '{"value":'
                native = None  # Native JSON cannot encode a malformed raw input.
                expected["reject_invalid_arguments"] = True
        rows.append({"name": control, "messages": native, "chat": chat, "responses": responses, "expected": expected})
    return rows


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--fixtures":
        from pathlib import Path
        Path(sys.argv[2]).write_text(json.dumps(fixtures(), ensure_ascii=False, indent=2) + "\n")
    else:
        import comprehensive_lab as lab
        lab.CASES = []
        for model in MODELS:
            for case in fixtures():
                if case["messages"] is None:
                    continue
                lab.CASES.append({"name": model + "-" + case["name"], "base": "api-arg", "model": model,
                    "env": {"CLAUDE_CODE_EXTRA_BODY": json.dumps(case["messages"], ensure_ascii=False)},
                    "content_contract": case["expected"]})
        lab.main()
