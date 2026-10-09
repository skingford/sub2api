"""Verify actual malformed/valid policy-case bytes without assuming acceptance."""
import base64
import json
from pathlib import Path
import re
import sys

from verify_post_trace import wire_bodies


def main():
    root = Path(sys.argv[1])
    records = json.loads((root / 'records.json').read_text())
    command = ['tshark', '-r', str(root / 'capture.pcap'), '-o',
               'tls.keylog_file:' + str(root / 'server-reference.keys'),
               '-o', 'http.decompress_body:FALSE']
    actual = wire_bodies(command, '/v1/messages?beta=true')
    assert len(actual) == len(records)
    assert all(body == base64.b64decode(row['wire_body_base64']) for body, row in zip(actual, records))
    assert re.search(r'\b0 packets dropped by kernel\b', (root / 'tcpdump.log').read_text())
    result = {'requests': len(records), 'pcap_bytes_match': True, 'kernel_drops': 0}
    (root / 'pcap-verification.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
