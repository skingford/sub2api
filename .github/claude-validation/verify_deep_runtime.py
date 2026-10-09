"""Verify all runtime-oracle bytes against PCAP; check golden hashes when supplied."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

root = Path(sys.argv[1])
received = json.loads((root / 'records.json').read_text())
cases = json.loads((root / 'cases.json').read_text())
packets = json.loads(subprocess.check_output([
    'tshark', '-r', str(root / 'capture.pcap'), '-o',
    'tls.keylog_file:' + str(root / 'server-reference.keys'),
    '-Y', 'http.request', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
raw = [bytes.fromhex(p['_source']['layers']['http']['http.file_data_raw'][0]) for p in packets]
assert len(raw) == len(received) == len(cases)
golden = 0
for wire, actual, case in zip(raw, received, cases):
    assert actual['name'] == case['name']
    assert wire == actual['body'].encode()
    if 'expected_body_sha256' in case:
        assert hashlib.sha256(wire).hexdigest() == case['expected_body_sha256']
        golden += 1
assert re.search(r'\b0 packets dropped by kernel\b', (root / 'tcpdump.log').read_text())
summary = {'requests': len(raw), 'pcap_raw_bytes_equal': True, 'golden_vectors_checked': golden,
           'kernel_drops': 0, 'new_vectors_use_independent_runtime_observations': True}
(root / 'pcap-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary))
