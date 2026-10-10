"""Check captured synthetic SSE response bytes independently against PCAP."""
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

root, output = map(Path, sys.argv[1:3])
rows = []
for path in sorted(root.glob('*/responses.json')):
    expected = [hashlib.sha256(x['body'].encode()).hexdigest() for x in json.loads(path.read_text())]
    packets = json.loads(subprocess.check_output([
        'tshark', '-r', str(path.parent / 'capture.pcap'), '-o',
        'tls.keylog_file:' + str(path.parent / 'server-reference.keys'),
        '-Y', 'http.response && http.content_type contains "text/event-stream"', '-T', 'json', '-x'],
        stderr=subprocess.DEVNULL))
    actual = []
    for packet in packets:
        http = packet['_source']['layers']['http']
        if 'http.file_data_raw' in http:
            body = bytes.fromhex(http['http.file_data_raw'][0])
        else:
            # Wireshark omits file_data for an explicitly empty HTTP body.
            # Missing data on a non-empty response must still fail validation.
            assert http.get('http.content_length_header') == '0', http
            body = b''
        actual.append(hashlib.sha256(body).hexdigest())
    drops = int(re.search(r'(\d+) packets dropped by kernel', (path.parent / 'tcpdump.log').read_text()).group(1))
    result = {'case': path.parent.name, 'responses': len(expected), 'response_bytes_match': Counter(expected) == Counter(actual), 'kernel_drops': drops}
    rows.append(result)
    assert result['response_bytes_match'] and drops == 0, result
assert rows
output.write_text(json.dumps(rows, indent=2) + '\n')
print(json.dumps({'cases': len(rows), 'responses': sum(x['responses'] for x in rows), 'all_response_bytes_match': True, 'kernel_drops': 0}))
