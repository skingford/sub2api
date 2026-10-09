"""Verify captured encrypted wire bytes separately from decoded JSON bodies."""
import base64
from collections import Counter
import gzip
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


def main():
    root, output = map(Path, sys.argv[1:3])
    results = []
    for folder in sorted(p.parent for p in root.glob('*/requests.json')):
        captured = json.loads((folder / 'requests.json').read_text())
        expected = []
        compressed = 0
        for row in captured:
            if row['method'] != 'POST' or not row['path'].startswith('/v1/messages'):
                continue
            wire = base64.b64decode(row['wire_body_base64'])
            encoding = row['headers'].get('Content-Encoding', row['headers'].get('content-encoding', ''))
            logical = gzip.decompress(wire) if encoding == 'gzip' else wire
            assert logical.decode() == row['raw_body_utf8'], folder
            assert hashlib.sha256(wire).hexdigest() == row['wire_body_sha256'], folder
            expected.append((row['path'], row['wire_body_sha256']))
            compressed += encoding == 'gzip'
        command = ['tshark', '-r', str(folder / 'capture.pcap'), '-o',
                   'tls.keylog_file:' + str(folder / 'server-reference.keys'),
                   '-o', 'http.decompress_body:FALSE', '-Y', 'http.request', '-T', 'json', '-x']
        packets = json.loads(subprocess.check_output(command, stderr=subprocess.DEVNULL))
        actual = []
        for packet in packets:
            http = packet['_source']['layers'].get('http', {})
            uri = next((v['http.request.uri'] for v in http.values()
                        if isinstance(v, dict) and 'http.request.uri' in v), None)
            if uri and uri.startswith('/v1/messages'):
                raw = http.get('http.file_data_raw')
                if raw is None:
                    encoded = next(value for key, value in http.items()
                                   if key.startswith('Content-encoded entity body'))
                    raw = encoded['data_raw']
                body = bytes.fromhex(raw[0])
                actual.append((uri, hashlib.sha256(body).hexdigest()))
        log = (folder / 'tcpdump.log').read_text()
        drops = int(re.search(r'(\d+) packets dropped by kernel', log).group(1))
        row = {'case': folder.name, 'requests': len(expected), 'compressed_requests': compressed,
               'wire_bytes_match': Counter(expected) == Counter(actual),
               'logical_bytes_match': True, 'kernel_drops': drops}
        results.append(row)
        assert row['wire_bytes_match'] and drops == 0, row
    output.write_text(json.dumps(results, indent=2) + '\n')
    print(json.dumps({'cases': len(results), 'requests': sum(r['requests'] for r in results),
                      'compressed_requests': sum(r['compressed_requests'] for r in results),
                      'all_match': True}))


if __name__ == '__main__':
    main()
