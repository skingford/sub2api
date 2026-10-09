"""Verify fixed first-party policy requests through the real Go transport.

The local responder always returns 200; CCH comparison uses a separate native
runtime oracle and is not a claim about real Anthropic acceptance.
"""
import base64
import json
import gzip
import os
from pathlib import Path
import signal
import ssl
import subprocess
import threading

import lab

ROOT = Path('/work')
os.umask(0o077)
lab.ROOT = ROOT
lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
                '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'),
                '-days', '2', '-subj', '/CN=api.anthropic.com',
                '-addext', 'subjectAltName=DNS:api.anthropic.com'], check=True,
               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
oracle = {r['name']: r['body'] for r in json.loads(Path('/oracle/records.json').read_text())}
records = []
current = None


class Handler(lab.Handler):
    def do_POST(self):
        wire = self.rfile.read(int(self.headers['Content-Length']))
        assert self.headers.get('x-api-key') == lab.DUMMY_KEY
        expected = oracle[current]
        encoded = self.headers.get('Content-Encoding', '')
        if encoded == 'gzip':
            fixture = 'parameter_alignment/native-gzip.json' if current.startswith('runtime-') else 'gzip_alignment/gzip-blocks2-2.json'
            source = json.loads((Path('/fixtures') / fixture).read_text())
            original = base64.b64decode(source['wire_body_base64'])
            parity = wire == original
            json.loads(gzip.decompress(wire))
        else:
            assert not encoded
            parity = wire == expected.encode()
        records.append({'name': current, 'path': self.path, 'headers': dict(self.headers),
                        'wire_body_base64': base64.b64encode(wire).decode(),
                        'encoding': encoded, 'native_wire_or_cch_equal': parity})
        self.respond({'local_audit_recorded': True})


ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(str(ROOT / 'server.pem'), str(ROOT / 'server.key'))
ctx.set_alpn_protocols(['http/1.1'])
ctx.keylog_filename = str(ROOT / 'server-reference.keys')
server = lab.ThreadingHTTPServer(('127.0.0.1', 443), Handler)
server.daemon_threads = True
server.socket = ctx.wrap_socket(server.socket, server_side=True)
threading.Thread(target=server.serve_forever, daemon=True).start()
capture, capture_log = lab.start_capture(ROOT)
try:
    for current in sorted(oracle):
        path = Path('/inputs') / (current + '.json')
        value = json.loads(path.read_text())
        if value['target'] != 'https://api.anthropic.com/v1/messages?beta=true':
            continue
        mode = 'auto' if value['upstream_profile'] == 'claude_2_1_292_x64' else 'default'
        before = len(records)
        result = subprocess.run(['/opt/trace-transport-probe', str(path), mode],
            env={'PATH': os.environ['PATH'], 'SSL_CERT_FILE': str(ROOT / 'server.pem')},
            text=True, capture_output=True, timeout=25)
        assert result.returncode == 0 and len(records) == before + 1, (current, result.stderr)
        assert records[-1]['wire_body_base64'] == value['wire_body_base64']
        lab.write_json(ROOT / 'records.json', records)
finally:
    server.shutdown()
    server.server_close()
    capture.send_signal(signal.SIGINT)
    _, tail = capture.communicate(timeout=8)
    capture_log.write(tail)
    capture_log.close()
print(json.dumps({'requests': len(records), 'cch_mismatches': [r['name'] for r in records if not r['native_wire_or_cch_equal']]}))

assert len(records) == 16 and all(r['native_wire_or_cch_equal'] for r in records)
