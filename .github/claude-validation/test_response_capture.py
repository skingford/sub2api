"""Negative controls for the PCAP verifier's explicit empty-body exception."""
import contextlib
import io
import json
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch


class ResponseCaptureTest(unittest.TestCase):
    def verify(self, http, body):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            case = root / 'case'
            case.mkdir()
            (case / 'responses.json').write_text(json.dumps([{'body': body}]))
            (case / 'tcpdump.log').write_text('0 packets dropped by kernel\n')
            packets = json.dumps([{'_source': {'layers': {'http': http}}}]).encode()
            script = Path(__file__).with_name('verify_response_capture.py')
            output = root / 'verified.json'
            with patch('sys.argv', [str(script), str(root), str(output)]), \
                    patch('subprocess.check_output', return_value=packets), \
                    contextlib.redirect_stdout(io.StringIO()):
                runpy.run_path(str(script), run_name='__main__')
            self.assertTrue(json.loads(output.read_text())[0]['response_bytes_match'])

    def test_explicit_empty_body(self):
        self.verify({'http.content_length_header': '0'}, '')

    def test_nonempty_wire_bytes(self):
        self.verify({'http.file_data_raw': ['616263']}, 'abc')

    def test_missing_nonempty_or_unspecified_body(self):
        for http in ({'http.content_length_header': '3'}, {}):
            with self.subTest(http=http), self.assertRaises(AssertionError):
                self.verify(http, '')


if __name__ == '__main__':
    unittest.main()
