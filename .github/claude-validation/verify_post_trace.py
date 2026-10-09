"""Audit native replay and logging transparency; retain header-order differences."""
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from verify_native_hello import EXPECTED, normalized


def wire_bodies(command, uri):
    packets = json.loads(subprocess.check_output(command + [
        '-Y', 'http.request', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
    bodies = []
    for packet in packets:
        http = packet['_source']['layers'].get('http', {})
        path = next((v['http.request.uri'] for v in http.values()
                     if isinstance(v, dict) and 'http.request.uri' in v), None)
        if path != uri:
            continue
        raw = http.get('http.file_data_raw')
        if raw is None:
            raw = next(v['data_raw'] for k, v in http.items() if k.startswith('Content-encoded entity body'))
        bodies.append(bytes.fromhex(raw[0]))
    assert bodies
    return bodies


def main():
    root, inputs, output = map(Path, sys.argv[1:])
    results = []
    for scenario in json.loads((root / 'summary.json').read_text()):
        folder = root / scenario['folder']
        expected = json.loads((inputs / (scenario['case'] + '.json')).read_text())
        original = base64.b64decode(expected['wire_body_base64'])
        record = json.loads((folder / 'requests.json').read_text())[0]
        command = ['tshark', '-r', str(folder / 'capture.pcap'), '-o',
                   'tls.keylog_file:' + str(folder / 'server-reference.keys'), '-o', 'http.decompress_body:FALSE']
        bodies = wire_bodies(command, expected['path'])
        packets = json.loads(subprocess.check_output(command + [
            '-Y', 'tls.handshake.type == 1', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
        hellos = [bytes.fromhex(p['_source']['layers']['tls']['tls.record']['tls.handshake_raw'][0]) for p in packets]
        origin = next(h for h in hellos if b'api.anthropic.com' in h)
        size, digest = normalized(origin.hex())
        row = dict(scenario, wire_bytes_equal=all(b == original for b in bodies),
                   logical_body_equal=record['raw_body_utf8'] == expected['raw_body_utf8'],
                   header_values_equal=record['headers'] == expected['headers'],
                   header_order_equal=list(record['headers']) == list(expected['headers']),
                   native_header_order=list(expected['headers']), actual_header_order=list(record['headers']),
                   normalized_hello_equal=size == 1499 and digest == EXPECTED,
                   zero_drops=bool(re.search(r'\b0 packets dropped by kernel\b', (folder / 'tcpdump.log').read_text())))
        if scenario['tracing']:
            raw = (folder / 'trace/requests.jsonl').read_text()
            events = [json.loads(line) for line in raw.splitlines()]
            chunks = [e['data'] for e in events if e['event'] == 'body.chunk' and e['data']['stream'] == 'upstream.request']
            end = next(e['data'] for e in events if e['event'] == 'body.end' and e['data']['stream'] == 'upstream.request')
            chunks.sort(key=lambda x: x['offset'])
            captured = bytearray()
            offsets_ok = True
            for chunk in chunks:
                offsets_ok &= chunk['offset'] == len(captured)
                captured.extend(base64.b64decode(chunk['base64']))
            row['trace_body_matches_wire'] = bytes(captured) == original and offsets_ok
            row['trace_digest_matches'] = end['sha256'] == hashlib.sha256(original).hexdigest()
            row['trace_body_complete'] = end['complete'] and not end['truncated']
            row['trace_credentials_redacted'] = 'local-container-key-not-a-real-credential' not in raw
            row['trace_write_failures'] = max(e.get('recorder_write_failures', 0) for e in events)
        results.append(row)
    output.write_text(json.dumps(results, indent=2) + '\n')
    print(json.dumps({'combinations': len(results), 'wire_matches': sum(r['wire_bytes_equal'] for r in results),
                      'header_order_differences': [r['folder'] for r in results if not r['header_order_equal']],
                      'trace_byte_matches': sum(r.get('trace_body_matches_wire', False) for r in results)}))
    for row in results:
        assert all(row[k] for k in ['wire_bytes_equal', 'logical_body_equal', 'header_values_equal',
                                    'normalized_hello_equal', 'zero_drops']), row
        if row['tracing']:
            assert all(row[k] for k in ['trace_body_matches_wire', 'trace_digest_matches',
                                        'trace_body_complete', 'trace_credentials_redacted']), row
            assert row['trace_write_failures'] == 0, row


if __name__ == '__main__':
    main()
