"""Build controlled replays, then report differences without treating them as PASS.

Generic replays copy captured messages/tools but remove client attribution and
select the original caller system text. They isolate gateway policy; they are
not a reconstruction of every state or option of an interactive CLI session.
"""
import copy
import hashlib
import json
from pathlib import Path
import sys

from caller_cli_alignment_lab import INSTRUCTION


def prepare(root):
    cases, references = [], {}
    for capture in sorted(root.glob("capture-*cli_alignment")):
        version = json.loads((capture / "summary.json").read_text())["cli_version"]
        for path in sorted(capture.glob("*/requests.json")):
            scenario = path.parent.name
            for index, request in enumerate(json.loads(path.read_text())):
                if not request["path"].startswith("/v1/messages"):
                    continue
                body = json.loads(request["raw_body_utf8"])
                count = "count_tokens" in request["path"]
                name = version + "-" + scenario + "-" + str(index)
                references[name] = {"capture": str(path), "index": index, "request": request}
                cases.append({"name": name + "-native", "version": version, "native": True,
                              "route": request["path"], "body": body, "headers": request["headers"]})
                # A separate raw file preserves byte layout for native inputs.
                cases[-1]["body"] = body
                if count:
                    generic = copy.deepcopy(body)
                    generic.pop("metadata", None)
                    cases.append({"name": name + "-count", "version": version, "native": False,
                                  "route": "/v1/messages/count_tokens", "body": generic, "headers": {}})
                    continue
                if scenario not in {"oauth-default-no-tools", "oauth-custom-system", "oauth-append-system", "oauth-custom-mcp", "interactive-default"}:
                    continue
                if scenario == "interactive-default" and request["headers"].get("x-claude-code-request-class", request["headers"].get("X-Claude-Code-Request-Class")) != "main":
                    continue
                generic = {"model": body["model"], "messages": body["messages"], "tools": body.get("tools", [])}
                if scenario not in {"oauth-default-no-tools", "interactive-default"}:
                    generic["system"] = INSTRUCTION
                cases.append({"name": name + "-messages", "version": version, "native": False,
                              "route": "/v1/messages", "body": generic, "headers": {}})
                chat = {"model": body["model"], "messages": copy.deepcopy(body["messages"]), "tools": []}
                responses = {"model": body["model"], "input": [], "tools": []}
                if "system" in generic:
                    chat["messages"].insert(0, {"role": "system", "content": INSTRUCTION})
                    responses["instructions"] = INSTRUCTION
                for message in body["messages"]:
                    assert message["role"] == "user", "selected adapter fixtures must be initial user turns"
                    content = message["content"]
                    if isinstance(content, str):
                        content = [{"type": "text", "text": content}]
                    assert all(block["type"] == "text" for block in content)
                    responses["input"].append({"role": "user", "content": [dict(block, type="input_text") for block in content]})
                for tool in body.get("tools", []):
                    assert "name" in tool and "input_schema" in tool, "do not silently drop unsupported tool definitions"
                    function = {"name": tool["name"], "description": tool.get("description", ""), "parameters": tool["input_schema"]}
                    chat["tools"].append({"type": "function", "function": function})
                    responses["tools"].append(dict(function, type="function"))
                for route, payload, label in [("/v1/chat/completions", chat, "chat"), ("/v1/responses", responses, "responses")]:
                    cases.append({"name": name + "-" + label, "version": version, "native": False,
                                  "route": route, "body": payload, "headers": {}})
    # Write native body values as captured JSON, not a Python re-serialization.
    chunks = []
    for case in cases:
        raw = json.dumps(case, ensure_ascii=False)
        if case["native"]:
            name = case["name"].removesuffix("-native")
            marker = json.dumps(case["body"], ensure_ascii=False)
            raw = raw.replace('"body": ' + marker, '"body": ' + references[name]["request"]["raw_body_utf8"], 1)
        chunks.append(raw)
    (root / "inputs.json").write_text("[" + ",\n".join(chunks) + "]\n")
    (root / "references.json").write_text(json.dumps(references, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"inputs": len(cases), "native": sum(c["native"] for c in cases), "observations_expected": len(cases)*2}))


def verify(root):
    references = json.loads((root / "references.json").read_text())
    rows = json.loads((root / "gateway/observations.json").read_text())
    outputs = []
    ignored_headers = {"host", "content-length", "connection", "authorization", "x-api-key"}
    for row in rows:
        suffix = next(s for s in ("native", "messages", "chat", "responses", "count") if row["name"].endswith("-"+s))
        reference = references[row["name"].removesuffix("-"+suffix)]["request"]
        native = json.loads(reference["raw_body_utf8"])
        body = row.get("body", {})
        result = {k: row[k] for k in ("name", "version", "native", "route", "preserve_caller", "calls", "status")}
        result["error"] = row.get("error")
        result["body_different_fields"] = [key for key in sorted(set(native) | set(body)) if native.get(key) != body.get(key) or (key in native) != (key in body)]
        expected = {k.lower(): v for k,v in reference["headers"].items() if k.lower() not in ignored_headers}
        actual = {k.lower(): v for k,v in row.get("headers", {}).items() if k.lower() not in ignored_headers}
        result["header_different_fields"] = [key for key in sorted(set(expected) | set(actual)) if expected.get(key) != actual.get(key)]
        result["sub2api_header_leaks"] = [key for key in actual if key.startswith("x-sub2api")]
        result["profile"] = row.get("profile")
        if row["native"] and row["calls"] == 1:
            export = root / "gateway/exports" / (row["name"] + ("-preserve" if row["preserve_caller"] else "-legacy") + ".json")
            wire = json.loads(export.read_text())["raw_body_utf8"]
            result["native_logical_bytes_equal"] = wire == reference["raw_body_utf8"]
        result["system_summary"] = [{"chars": len(x.get("text", "")), "sha256": hashlib.sha256(x.get("text", "").encode()).hexdigest(), "cache_control": x.get("cache_control")} for x in body.get("system", [])] if isinstance(body.get("system", []), list) else []
        result["tool_names"] = [x.get("name", x.get("type")) for x in body.get("tools", [])]
        outputs.append(result)
    summary = {"observations": len(outputs), "native_observations": sum(x["native"] for x in outputs),
               "native_logical_bytes_equal": sum(x.get("native_logical_bytes_equal", False) for x in outputs),
               "errors": sum(bool(x["error"]) for x in outputs), "sub2api_header_leaks": sum(bool(x["sub2api_header_leaks"]) for x in outputs),
               "generic_full_bodies_equal": sum(not x["native"] and not x["body_different_fields"] for x in outputs)}
    (root / "comparison.json").write_text(json.dumps({"summary": summary, "comparisons": outputs}, ensure_ascii=False, indent=2)+"\n")
    print(json.dumps(summary))


if __name__ == "__main__":
    operation, directory = sys.argv[1:]
    {"prepare": prepare, "verify": verify}[operation](Path(directory))
