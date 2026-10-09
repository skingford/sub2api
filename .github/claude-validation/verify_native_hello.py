"""Compare isolated native model-request ClientHellos with the Go golden digest.

Run inside the network-none capture image. Arguments are capture root, output
JSON, then case names. Only connection randomness is zeroed, as in the Go test.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

EXPECTED = '8845ac2401a951ffc4acef2824c3422124c7883e0c9bc4b5f90d3c6be05da2f9'


def normalized(data):
    hello = bytearray.fromhex(data)
    assert hello[0] == 1
    hello[6:38] = bytes(32)
    session = hello[38]
    hello[39:39 + session] = bytes(session)
    pos = 39 + session
    pos += 2 + int.from_bytes(hello[pos:pos + 2], 'big')
    pos += 1 + hello[pos]
    end = pos + 2 + int.from_bytes(hello[pos:pos + 2], 'big')
    pos += 2
    assert end == len(hello)
    while pos < end:
        kind = int.from_bytes(hello[pos:pos + 2], 'big')
        size = int.from_bytes(hello[pos + 2:pos + 4], 'big')
        pos += 4
        if kind == 51:
            key = pos + 2
            while key < pos + size:
                key_size = int.from_bytes(hello[key + 2:key + 4], 'big')
                key += 4
                assert key + key_size <= pos + size
                hello[key:key + key_size] = bytes(key_size)
                key += key_size
        pos += size
    assert pos == end
    return len(hello), hashlib.sha256(hello).hexdigest()


def main():
    root, output = map(Path, sys.argv[1:3])
    results = []
    for name in sys.argv[3:]:
        folder = root / name
        command = ['tshark', '-r', str(folder / 'capture.pcap'), '-o',
                   'tls.keylog_file:' + str(folder / 'server-reference.keys')]
        requests = subprocess.check_output(command + ['-Y', 'http.request', '-T', 'fields',
                                                     '-e', 'tcp.stream', '-e', 'http.request.uri'],
                                           text=True, stderr=subprocess.DEVNULL)
        streams = {line.split('\t')[0] for line in requests.splitlines()
                   if '\t/v1/messages' in line}
        packets = json.loads(subprocess.check_output(command + [
            '-Y', 'tls.handshake.type == 1', '-T', 'json', '-x'], stderr=subprocess.DEVNULL))
        matched = set()
        for packet in packets:
            layers = packet['_source']['layers']
            stream = layers['tcp']['tcp.stream']
            if stream not in streams:
                continue
            raw = layers['tls']['tls.record']['tls.handshake_raw'][0]
            size, digest = normalized(raw)
            results.append({'case': name, 'tcp_stream': stream, 'length': size,
                            'normalized_sha256': digest, 'matches_go_golden': digest == EXPECTED})
            matched.add(stream)
        assert streams and matched == streams, (name, streams, matched)
    assert results
    output.write_text(json.dumps(results, indent=2) + '\n')
    assert all(r['matches_go_golden'] for r in results), 'ClientHello differs from pinned golden'
    print(json.dumps({'client_hellos': len(results), 'all_match': True, 'sha256': EXPECTED}))


if __name__ == '__main__':
    main()
