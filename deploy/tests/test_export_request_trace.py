import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("trace_export", Path(__file__).resolve().parents[1] / "export-request-trace.py")
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTraceTests(unittest.TestCase):
    def event(self, seq, kind, data):
        return {"schema": 1, "trace_id": "trace-1", "seq": seq, "time": f"2026-10-09T00:00:00.{seq:09d}Z", "event": kind, "data": data}

    def fixtures(self, folder, truncated=False):
        payload = b'data: {"message":"full synthetic evidence"}\n\n\xff'
        captured = payload[:10] if truncated else payload
        events = [self.event(1, "request.start", {"request_id": "req-test", "method": "POST", "url": "/v1/messages"}),
                  self.event(2, "upstream.request", {"upstream": {"account_id": 42}}),
                  self.event(3, "upstream.response", {"status": 403}),
                  self.event(4, "body.chunk", {"stream": "upstream.response", "attempt": 1, "offset": 0, "base64": base64.b64encode(captured).decode()}),
                  self.event(5, "body.end", {"stream": "upstream.response", "attempt": 1, "bytes": len(payload), "captured_bytes": len(captured), "complete": True, "truncated": truncated, "sha256": hashlib.sha256(payload).hexdigest()}),
                  self.event(6, "request.end", {"status": 403, "account_id": 42, "request_id": "req-test"})]
        for filename, batch in (("requests-2026-10-09T00-00-00.000.jsonl", events[:4]), ("requests.jsonl", events[4:])):
            (folder / filename).write_text("".join(json.dumps(e) + "\n" for e in batch))
        return captured

    def test_restore_across_rotation_and_filter(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            payload = self.fixtures(folder)
            listed = exporter.list_traces(folder, account_id=42, request_id="req-test", status=403)
            self.assertEqual(1, len(listed["traces"]))
            self.assertEqual([], exporter.list_traces(folder, account_id=43)["traces"])
            result = exporter.export_trace(folder, "trace-1", folder / "export")
            self.assertTrue(result["verified_complete"])
            self.assertEqual(payload, (folder / "export/0001-upstream.response.bin").read_bytes())
            self.assertEqual(0o600, (folder / "export/manifest.json").stat().st_mode & 0o777)
            with self.assertRaises(FileExistsError):
                exporter.export_trace(folder, "trace-1", folder / "export")

    def test_truncated_and_missing_records_never_claim_complete(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            self.fixtures(folder, truncated=True)
            self.assertFalse(exporter.export_trace(folder, "trace-1", folder / "truncated")["verified_complete"])
            next(folder.glob("requests-*.jsonl")).unlink()
            result = exporter.export_trace(folder, "trace-1", folder / "missing")
            self.assertFalse(result["verified_complete"])
            self.assertTrue(result["sequence_problems"])

    def test_partial_json_tail_is_reported(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            self.fixtures(folder)
            with (folder / "requests.jsonl").open("a") as dest:
                dest.write('{"schema":')
            result = exporter.export_trace(folder, "trace-1", folder / "export")
            self.assertFalse(result["verified_complete"])
            self.assertTrue(result["parse_problems"])


if __name__ == "__main__":
    unittest.main()
