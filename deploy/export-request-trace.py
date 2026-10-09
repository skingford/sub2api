#!/usr/bin/env python3
"""List gateway traces or export one trace and validate its body evidence (stdlib only)."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import sys


STREAMS = {"client.request", "client.response", "upstream.request", "upstream.response", "upstream.response_decoded"}


def records(directory, problems):
    # Lumberjack timestamped backups sort before the current requests.jsonl.
    files = sorted(Path(directory).glob("requests*.jsonl"))
    if not files:
        raise ValueError("No requests*.jsonl files found")
    for path in files:
        with path.open("rb") as source:
            for line_number, line in enumerate(source, 1):
                try:
                    event = json.loads(line)
                    if not isinstance(event, dict) or event.get("schema") != 1:
                        raise ValueError("unsupported event schema")
                    yield event
                except (ValueError, UnicodeError) as error:
                    problems.append(f"{path.name}:{line_number}: {error}")


def list_traces(directory, account_id=None, request_id=None, status=None):
    traces, problems = {}, []
    for event in records(directory, problems):
        if event.get("event") == "body.chunk":
            continue
        trace_id = event.get("trace_id")
        row = traces.setdefault(trace_id, {"trace_id": trace_id, "time": event.get("time"), "account_ids": [], "upstream_statuses": [], "request_id": None, "client_request_id": None, "status": None, "has_start": False, "has_end": False})
        data = event.get("data") or {}
        if event["event"] == "request.start":
            row.update(time=event.get("time"), method=data.get("method"), url=data.get("url"), has_start=True)
        if event["event"] in ("request.start", "request.end"):
            for key in ("request_id", "client_request_id", "model", "user_id", "api_key_id", "group_id"):
                if data.get(key) is not None:
                    row[key] = data[key]
        if event["event"] == "request.end":
            row.update(status=data.get("status"), elapsed_ms=event.get("elapsed_ms"), has_end=True)
        if event["event"] == "upstream.response":
            row["upstream_statuses"].append(data.get("status"))
        account = data.get("account_id") or (data.get("upstream") or {}).get("account_id")
        if account and account not in row["account_ids"]:
            row["account_ids"].append(account)
    rows = [row for row in traces.values() if
            (account_id is None or account_id in row["account_ids"]) and
            (request_id is None or request_id in (row["request_id"], row["client_request_id"])) and
            (status is None or status == row["status"] or status in row["upstream_statuses"])]
    return {"traces": sorted(rows, key=lambda row: row["time"] or ""), "parse_problems": problems}


def export_trace(directory, trace_id, output):
    output = Path(output)
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.chmod(output, 0o700)
    streams, problems, integrity = {}, [], []
    count, last_seq, has_start, has_end, write_failures = 0, 0, False, False, 0
    # Files use fixed stream names and numeric attempt IDs, never URL/session paths.
    try:
        with (output / "events.jsonl").open("x", encoding="utf-8") as timeline:
            os.chmod(output / "events.jsonl", 0o600)
            for event in records(directory, problems):
                if event.get("trace_id") != trace_id:
                    continue
                timeline.write(json.dumps(event, ensure_ascii=False) + "\n")
                count += 1
                seq = event.get("seq", 0)
                if seq != last_seq + 1:
                    integrity.append(f"event sequence gap/order: {last_seq} -> {seq}")
                last_seq = seq
                write_failures = max(write_failures, event.get("trace_write_failures", 0))
                kind, data = event.get("event"), event.get("data") or {}
                has_start |= kind == "request.start"
                has_end |= kind == "request.end"
                if kind not in ("body.chunk", "body.end"):
                    continue
                name, attempt = data.get("stream"), data.get("attempt")
                if name not in STREAMS or not isinstance(attempt, int) or not 0 <= attempt <= 1000000000:
                    integrity.append("invalid stream identifier")
                    continue
                key = f"{attempt:04d}-{name}"
                if key not in streams:
                    path = output / (key + ".bin")
                    handle = path.open("xb")
                    os.chmod(path, 0o600)
                    streams[key] = {"file": path.name, "handle": handle, "hash": hashlib.sha256(), "bytes": 0, "gaps": [], "end": None}
                stream = streams[key]
                if kind == "body.end":
                    if stream["end"] is not None:
                        stream["gaps"].append("duplicate body.end")
                    stream["end"] = data
                    continue
                if stream["end"] is not None:
                    stream["gaps"].append("chunk after body.end")
                offset = data.get("offset")
                if offset != stream["bytes"]:
                    stream["gaps"].append(f"offset {offset}, expected {stream['bytes']}")
                chunk = base64.b64decode(data["base64"], validate=True)
                stream["handle"].write(chunk)
                stream["hash"].update(chunk)
                stream["bytes"] += len(chunk)
    finally:
        for stream in streams.values():
            stream.pop("handle").close()
    if count == 0:
        raise ValueError("Trace ID not found; output contains no evidence")
    for stream in streams.values():
        digest = stream.pop("hash").hexdigest()
        stream["export_sha256"] = digest
        end = stream["end"] or {}
        stream["verified_complete"] = bool(end.get("complete") and not end.get("truncated") and
                                          not stream["gaps"] and end.get("sha256") == digest and
                                          end.get("bytes") == stream["bytes"] == end.get("captured_bytes"))
    manifest = {"trace_id": trace_id, "event_count": count, "has_start": has_start, "has_end": has_end,
                "trace_write_failures": write_failures, "sequence_problems": integrity, "parse_problems": problems,
                "streams": streams,
                "verified_complete": bool(has_start and has_end and not integrity and not problems and not write_failures and streams and all(s["verified_complete"] for s in streams.values()))}
    path = output / "manifest.json"
    with path.open("x", encoding="utf-8") as dest:
        os.chmod(path, 0o600)
        json.dump(manifest, dest, indent=2, ensure_ascii=False)
        dest.write("\n")
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", help="Private request-traces directory, or a copied snapshot")
    parser.add_argument("--account-id", type=int)
    parser.add_argument("--request-id", help="Server request ID or client request ID")
    parser.add_argument("--status", type=int, help="Downstream or any upstream HTTP status")
    parser.add_argument("--trace-id", help="Export exactly one trace UUID")
    parser.add_argument("--output", help="New private output directory, required with --trace-id")
    args = parser.parse_args()
    if bool(args.trace_id) != bool(args.output):
        parser.error("--trace-id and --output must be supplied together")
    try:
        if args.trace_id:
            result = export_trace(args.directory, args.trace_id, args.output)
        else:
            result = list_traces(args.directory, args.account_id, args.request_id, args.status)
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except (OSError, ValueError, KeyError) as error:
        print(f"Trace export failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
