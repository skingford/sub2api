"""Fixed assertions for CC-20261010-007 findings; retain legacy differences.

Uses the same 128 inputs and 256 observations as 007. Custom-system expectations
come from separately captured interactive CLI fixtures, never gateway output.
This is deliberately not a claim that all CLI application state is reproduced.
"""
import argparse
import base64
import gzip
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("audit_root", type=Path)
    parser.add_argument("profiles", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--wire", action="store_true")
    args = parser.parse_args()
    inputs = {x["name"]: x for x in json.loads((args.audit_root / "inputs.json").read_text())}
    rows = json.loads((args.audit_root / "gateway/observations.json").read_text())
    profiles = {(x["version"], x["mode"]): x for x in json.loads(args.profiles.read_text())}
    references = json.loads((args.audit_root / "references.json").read_text())
    assert len(inputs) == 128 and len(rows) == 256
    wire = {}
    if args.wire:
        wire = {x["case"]: x for x in json.loads((args.audit_root / "wire/records-with-headers.json").read_text())}
        assert len(wire) == len(rows)
    failures = []
    counts = {"native": 0, "count": 0, "strict_omission": 0, "custom_system": 0, "tool_names": 0}
    seen = set()
    for row in rows:
        key = row["name"] + ("-preserve" if row["preserve_caller"] else "-legacy")
        assert key not in seen
        seen.add(key)
        source = inputs[row["name"]]
        body = row.get("body", {})
        headers = row.get("headers", {})
        if args.wire:
            raw = base64.b64decode(wire[key]["wire_body_base64"])
            if raw.startswith(b"\x1f\x8b"):
                raw = gzip.decompress(raw)
            body = json.loads(raw)
            headers = dict(wire[key]["headers_ordered"])
        reasons = []
        if row.get("error") or row["calls"] != 1 or row["status"] != 200:
            reasons.append("unexpected rejection or dispatch count")
        if any(k.lower().startswith("x-sub2api") for k in headers):
            reasons.append("gateway-only header leaked")
        if row["native"]:
            counts["native"] += 1
            reference = references[row["name"].removesuffix("-native")]["request"]
            if body != json.loads(reference["raw_body_utf8"]):
                reasons.append("native body changed")
        elif "count_tokens" in row["route"]:
            counts["count"] += 1
            if body != source["body"]:
                reasons.append("count input changed")
        else:
            payload = source["body"]
            if "chat/completions" in row["route"]:
                functions = [x["function"] for x in payload.get("tools", []) if x.get("type") == "function"]
                if len(functions) != len(body.get("tools", [])):
                    reasons.append("tool definition count changed")
                for original, actual in zip(functions, body.get("tools", [])):
                    counts["strict_omission"] += 1
                    if ("strict" in original) != ("strict" in actual) or original.get("strict") != actual.get("strict"):
                        reasons.append("strict omission/value changed")
            if row["preserve_caller"]:
                counts["custom_system"] += 1
                caller = payload.get("system", payload.get("instructions", ""))
                if "chat/completions" in row["route"]:
                    instructions = [x["content"] for x in payload["messages"] if x["role"] in ("system", "developer")]
                    assert len(instructions) <= 1
                    caller = instructions[0] if instructions else ""
                assert isinstance(caller, str)
                fixture = profiles[(row["version"], "replace" if caller else "empty")]
                assert fixture["caller_system"] == caller
                if body.get("system", [])[1:] != fixture["expected_system_after_attribution"]:
                    reasons.append("custom-system layout differs from interactive CLI capture")
                lower = {k.lower():v for k,v in headers.items()}
                if lower.get("user-agent") != fixture["user_agent"]:
                    reasons.append("custom-system profile has a mixed entrypoint")
                if "oauth-custom-mcp" in row["name"]:
                    counts["tool_names"] += 1
                    tools = payload.get("tools", [])
                    names = [x.get("function", x)["name"] for x in tools]
                    if [x["name"] for x in body.get("tools", [])] != names:
                        reasons.append("tool names changed")
        if reasons:
            failures.append({"case": key, "reasons": sorted(set(reasons))})
    assert counts == {"native": 124, "count": 72, "strict_omission": 24, "custom_system": 30, "tool_names": 6}, counts
    result = {"observations": len(rows), "wire_checked": args.wire, "checked": counts,
              "failed_cases": len(failures), "passed": not failures, "failures": failures,
              "scope": "count, tool omission/names, native preservation and interactive custom-system profile; legacy/default/append application differences retained"}
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({k:v for k,v in result.items() if k != "failures"}))
    return bool(failures)


if __name__ == "__main__":
    raise SystemExit(main())
