"""Independently check intent against serialized production request content.

Exit 1 means a listed constraint failed. Explicit model-policy rejections and
documented empty-output substitution are recorded separately from equality.
"""
import collections
import json
from pathlib import Path
import sys


def blocks(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from blocks(child)
    elif isinstance(value, list):
        for child in value:
            yield from blocks(child)


def check(row):
    expected, body = row["expected"], row.get("body", {})
    if expected.get("reject_invalid_arguments"):
        return [] if row["status"] == 400 and row["calls"] == 0 else ["invalid tool arguments were silently discarded or dispatched"]
    if row.get("error") or row["calls"] != 1:
        if row["status"] == 400 and row["calls"] == 0 and expected.get("sampling_key") and row["model"] == "claude-sonnet-5-5" and row.get("error") == "claude-sonnet-5-5 does not support non-default " + expected["sampling_key"]:
            return []
        return ["unexpected rejection: " + row.get("error", "no dispatch")]
    failures = []
    message_blocks = list(blocks(body.get("messages", [])))
    if "system_text" in expected:
        system = body.get("system", "")
        texts = [system] if isinstance(system, str) else [x.get("text", "") for x in blocks(system)]
        if not any(expected["system_text"] in text for text in texts):
            failures.append("instruction lost system role")
        if any(expected["system_text"] in x.get("text", "") or expected["system_text"] == x.get("content") for x in message_blocks):
            failures.append("instruction demoted into conversation")
    if "block_type" in expected:
        scope = message_blocks
        if expected.get("tool_media") and row["route"] != "/v1/chat/completions":
            scope = [inner for block in message_blocks if block.get("type") == "tool_result" for inner in blocks(block.get("content"))]
        if not any(x.get("type") == expected["block_type"] and x.get("source") == expected["source"] for x in scope):
            failures.append("image/document source lost")
    if row["control"].startswith("tool-") or row["control"] in ("legacy-function", "signed-thinking", "redacted-thinking"):
        if not any(x.get("type") == "tool_use" and x.get("name") == "audit_tool" and x.get("input") == {"value": "中文"} for x in message_blocks):
            failures.append("tool call or arguments lost")
        results = [x.get("content") for x in message_blocks if x.get("type") == "tool_result"]
        if row["control"] == "tool-empty":
            valid = any(value in ("", "(empty)") for value in results)
        else:
            valid = any(value == "LOCAL_TOOL_RESULT" or any(x.get("text") == "LOCAL_TOOL_RESULT" for x in blocks(value)) for value in results)
        if not valid:
            failures.append("tool result text lost")
    if "thinking_block" in expected:
        block = expected["thinking_block"]
        if not any(all(x.get(k) == v for k, v in block.items()) for x in message_blocks):
            failures.append("opaque signed thinking lost")
    if "sampling_key" in expected and body.get(expected["sampling_key"]) != expected["sampling_value"]:
        failures.append("explicit sampling value changed")
    return failures


def main():
    rows = json.loads(Path(sys.argv[1]).read_text())
    # Six models, two accounts, 16 controls; two Chat opaque controls and one
    # Messages malformed-JSON control do not have equivalent inputs.
    assert len(rows) == 540, len(rows)
    failures, policies = [], []
    for row in rows:
        keys = {k: row[k] for k in ("model", "account", "route", "control", "status", "calls")}
        reasons = check(row)
        if reasons:
            policy = None
            if not row.get("preserve_caller", False) and row["account"] == "oauth" and row["control"] in ("system", "developer"):
                messages = row.get("body", {}).get("messages", [])
                marker = "[System Instructions]\n" + row["expected"]["system_text"]
                # A correctly converted Chat developer now takes this same
                # policy path. Its old bare user text must still fail.
                if (len(messages) >= 2 and messages[0].get("role") == "user" and
                        any(x.get("text") == marker for x in blocks(messages[0].get("content"))) and
                        messages[1].get("role") == "assistant" and
                        any(x.get("text") == "Understood. I will follow these instructions." for x in blocks(messages[1].get("content")))):
                    policy = "existing ordinary OAuth system-instruction conversation wrapper"
            failures.append({**keys, "reasons": reasons, "known_policy": policy})
        elif row.get("error"):
            policies.append({**keys, "policy": row["error"]})
        elif row["expected"].get("known_policy") and any(x.get("type") == "tool_result" and x.get("content") == "(empty)" for x in blocks(row.get("body", {}))):
            policies.append({**keys, "policy": row["expected"]["known_policy"]})
    result = {"cases": len(rows), "successful_dispatches": sum(x["calls"] == 1 for x in rows),
              "explicit_rejections": sum(x["status"] == 400 and x["calls"] == 0 for x in rows),
              "failed_cases": len(failures), "failures_by_control": dict(collections.Counter(x["control"] for x in failures)),
              "known_policy_differences": sum(bool(x["known_policy"]) for x in failures),
              "unresolved_content_differences": sum(not x["known_policy"] for x in failures),
              "raw_equal": not failures,
              "contract_passed": not any(not x["known_policy"] for x in failures),
              "failures": failures, "policies": policies}
    Path(sys.argv[2]).write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k not in ("failures", "policies")}, ensure_ascii=False))
    return not result["contract_passed"]


if __name__ == "__main__":
    sys.exit(main())
