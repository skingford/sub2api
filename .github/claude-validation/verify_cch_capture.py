"""Verify receiver bytes, independent PCAP bytes and committed native vectors."""
import hashlib
import json
from pathlib import Path
import re
import subprocess


def main():
    root = Path('/work')
    expected = json.loads((root / 'records.json').read_text())
    result = subprocess.run([
        'tshark', '-r', str(root / 'capture.pcap'),
        '-o', 'tls.keylog_file:' + str(root / 'server-reference.keys'),
        '-Y', 'http.request', '-T', 'json', '-x',
    ], capture_output=True, text=True, check=True, timeout=30)
    (root / 'pcap-decoded.json').write_text(result.stdout)
    records = []
    for packet in json.loads(result.stdout):
        http = packet['_source']['layers'].get('http', {})
        raw = http.get('http.file_data_raw')
        assert raw is not None, 'Missing raw body'
        records.append(bytes.fromhex(raw[0]))
    cases = json.loads((root / 'cases.json').read_text())
    assert len(records) == len(expected) == len(cases)
    for actual, received, case in zip(records, expected, cases):
        assert actual == received['body'].encode(), received['name']
        assert received['name'] == case['name']
        assert hashlib.sha256(actual).hexdigest() == case['expected_body_sha256'], case['name']
    log = (root / 'tcpdump.log').read_text()
    assert re.search(r'\b0 packets dropped by kernel\b', log), log
    report = {'requests': len(records), 'pcap_raw_bytes_all_match': True,
              'committed_vectors_all_match': True, 'kernel_drops': 0,
              'body_sha256': [{'name': row['name'], 'sha256': hashlib.sha256(body).hexdigest()}
                              for row, body in zip(expected, records)]}
    (root / 'pcap-verification.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({key: value for key, value in report.items() if key != 'body_sha256'}))


if __name__ == '__main__':
    main()
