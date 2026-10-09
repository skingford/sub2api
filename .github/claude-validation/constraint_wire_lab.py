"""Receive and validate exported production constraints over real local TLS.

Requires network-none, a tmpfs /work, production exports at /inputs and the
compiled production transport probe. Credentials and replies are synthetic.
"""
import base64
import gzip
import hashlib
import json
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
                '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'), '-days', '2',
                '-subj', '/CN=api.anthropic.com', '-addext', 'subjectAltName=DNS:api.anthropic.com'],
               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
records = []
current = None


class Handler(lab.Handler):
    def do_POST(self):
        wire = self.rfile.read(int(self.headers['Content-Length']))
        expected = current['contract']
        assert self.path == current['path']
        assert wire == base64.b64decode(current['wire_body_base64'])
        if current['synthetic_auth'] == 'oauth':
            assert self.headers.get('Authorization') == 'Bearer local-only-oauth-token-not-a-real-credential'
            assert not self.headers.get('x-api-key')
        else:
            assert self.headers.get('x-api-key') == lab.DUMMY_KEY
            assert not self.headers.get('Authorization')
        decoded = gzip.decompress(wire) if self.headers.get('Content-Encoding') == 'gzip' else wire
        body = json.loads(decoded)
        assert body['model'] == expected['model'] and body['max_tokens'] == expected['max_tokens']
        if 'schema' in expected:
            assert body['output_config']['format']['schema'] == expected['schema']
            assert 'structured-outputs-2025-12-15' in self.headers.get('anthropic-beta', '').split(',')
        if 'stop_sequences' in expected:
            assert body['stop_sequences'] == expected['stop_sequences']
        records.append({'case': current['case'], 'path': self.path, 'contract': expected,
                        'wire_body_base64': base64.b64encode(wire).decode(),
                        'wire_sha256': hashlib.sha256(wire).hexdigest(), 'constraints_match': True})
        self.respond({'local_constraints_recorded': True})


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
    inputs = sorted(Path('/inputs').glob('*.json'))
    assert len(inputs) == 192
    for path in inputs:
        current = json.loads(path.read_text())
        current['case'] = path.stem
        mode = 'auto' if current['upstream_profile'] else 'default'
        before = len(records)
        p = subprocess.run(['/opt/trace-transport-probe', str(path), mode], capture_output=True, text=True,
                           timeout=25, env={'PATH': os.environ['PATH'], 'SSL_CERT_FILE': str(ROOT / 'server.pem')})
        assert p.returncode == 0 and len(records) == before + 1, (path.name, p.stderr)
        lab.write_json(ROOT / 'records.json', records)
finally:
    server.shutdown()
    server.server_close()
    capture.send_signal(signal.SIGINT)
    _, tail = capture.communicate(timeout=8)
    capture_log.write(tail)
    capture_log.close()
print(json.dumps({'requests': len(records), 'all_constraints_match': True}))
