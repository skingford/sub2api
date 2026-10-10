"""Verify all generation AND count paths, then compare captured native headers.

The older encoding checker selects only /v1/messages?beta=true and is not a
valid denominator for this mixed corpus. Never discard count requests to pass.
"""
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from verify_post_trace import EXPECTED, normalized, wire_bodies


def main(root):
    wire = root / "wire"
    records = json.loads((wire / "records-with-headers.json").read_text())
    references = json.loads((root / "references.json").read_text())
    command = ["tshark", "-r", str(wire / "capture.pcap"), "-o", "tls.keylog_file:" + str(wire / "server-reference.keys"),
               "-o", "http.decompress_body:FALSE"]
    paths = sorted({x["path"] for x in records})
    counts = {}
    for path in paths:
        actual = wire_bodies(command, path)
        expected = [base64.b64decode(x["wire_body_base64"]) for x in records if x["path"] == path]
        assert actual == expected, (path, len(actual), len(expected))
        counts[path] = len(actual)
    assert re.search(r"\b0 packets dropped by kernel\b", (wire / "tcpdump.log").read_text())
    packets = json.loads(subprocess.check_output(command + ["-Y", "tls.handshake.type == 1", "-T", "json", "-x"], stderr=subprocess.DEVNULL))
    hellos = [bytes.fromhex(p["_source"]["layers"]["tls"]["tls.record"]["tls.handshake_raw"][0]) for p in packets]
    hellos = [h for h in hellos if b"api.anthropic.com" in h]
    hello_matches = sum(normalized(h.hex()) == (1499, EXPECTED) for h in hellos)
    native = []
    leaks = []
    for record in records:
        actual = dict(record["headers_ordered"])
        leaks += [{"case": record["case"], "key": key} for key in actual if key.lower().startswith("x-sub2api")]
        stem = record["case"].rsplit("-", 1)[0]
        if not stem.endswith("-native"):
            continue
        expected = references[stem.removesuffix("-native")]["request"]["headers"]
        lower_actual = {k.lower(): v for k,v in actual.items()}
        lower_expected = {k.lower(): v for k,v in expected.items()}
        ignored_values = {"authorization", "x-api-key"}
        different = [k for k in sorted(set(lower_actual) | set(lower_expected)) if k not in ignored_values and lower_actual.get(k) != lower_expected.get(k)]
        native.append({"case": record["case"], "header_value_differences": different,
                       "header_order_equal": list(actual) == list(expected)})
    result = {"requests": len(records), "requests_by_path": counts, "pcap_bytes_match": True, "kernel_drops": 0,
              "client_hellos": len(hellos), "normalized_hello_matches_existing_linux_pin": hello_matches,
              "native_header_cases": len(native), "native_header_values_equal": sum(not x["header_value_differences"] for x in native),
              "native_header_order_equal": sum(x["header_order_equal"] for x in native),
              "sub2api_header_leaks": leaks, "native_headers": native}
    (wire / "alignment-verification.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({k:v for k,v in result.items() if k != "native_headers"}))
    assert len(hellos) == len(records) and hello_matches == len(hellos)
    assert not leaks and all(not x["header_value_differences"] and x["header_order_equal"] for x in native)


if __name__ == "__main__":
    main(Path(sys.argv[1]))
