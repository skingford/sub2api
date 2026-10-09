"""Compare four isolated production transport paths with an unmodified CLI gzip capture.

Arguments: transport results directory, native request JSON, output JSON.
Run with tshark and the sibling verify_native_hello module in the offline image.
"""
import base64
import gzip
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from verify_native_hello import EXPECTED, normalized


def main():
    root, baseline, output = map(Path, sys.argv[1:])
    expected = json.loads(baseline.read_text())
    expected_wire = base64.b64decode(expected['wire_body_base64'])
    results = []
    for mode in ['auto', 'auto-http-proxy', 'auto-https-proxy', 'auto-socks5-proxy']:
        folder = root / mode
        record = json.loads((folder / 'requests.json').read_text())[0]
        command = ['tshark', '-r', str(folder / 'capture.pcap'), '-o',
                   'tls.keylog_file:' + str(folder / 'server-reference.keys'),
                   '-o', 'http.decompress_body:FALSE']
        packets = json.loads(subprocess.check_output(command + [
            '-Y', 'http.request', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
        entities = [value for packet in packets
                    for key, value in packet['_source']['layers'].get('http', {}).items()
                    if key.startswith('Content-encoded entity body')]
        assert len(entities) == 1, mode
        wire = bytes.fromhex(entities[0]['data_raw'][0])
        packets = json.loads(subprocess.check_output(command + [
            '-Y', 'tls.handshake.type == 1', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
        hellos = [bytes.fromhex(p['_source']['layers']['tls']['tls.record']['tls.handshake_raw'][0])
                  for p in packets]
        hello = next(h for h in hellos if b'api.anthropic.com' in h)
        length, digest = normalized(hello.hex())
        row = {'mode': mode, 'compressed_bytes_equal': wire == expected_wire,
               'logical_bytes_equal': gzip.decompress(wire).decode() == expected['raw_body_utf8'],
               'headers_equal': record['headers'] == expected['headers'],
               'header_order_equal': list(record['headers']) == list(expected['headers']),
               'client_hello_equal': length == 1499 and digest == EXPECTED,
               'zero_drops': bool(re.search(r'\b0 packets dropped by kernel\b', (folder / 'tcpdump.log').read_text())),
               'wire_sha256': hashlib.sha256(wire).hexdigest()}
        results.append(row)
        assert all(row[key] for key in ['compressed_bytes_equal', 'logical_bytes_equal',
                                       'headers_equal', 'header_order_equal', 'client_hello_equal', 'zero_drops']), row
    output.write_text(json.dumps(results, indent=2) + '\n')
    print(json.dumps(results))


if __name__ == '__main__':
    main()
