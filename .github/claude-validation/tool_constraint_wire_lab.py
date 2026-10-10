"""Observe production tool-constraint exports over isolated real TLS.

The receiver verifies serialization, then records fields without treating a
synthetic 200 or a successful capture as proof of constraint preservation.
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
        assert self.path == current['path']
        assert wire == base64.b64decode(current['wire_body_base64'])
        decoded = gzip.decompress(wire) if self.headers.get('Content-Encoding') == 'gzip' else wire
        body = json.loads(decoded)
        if os.environ.get('CLAUDE_TOOL_WIRE_REQUIRE_CONTRACT') == '1':
            expected = current['contract']
            assert body['model'] == expected['model'] and body['max_tokens'] == expected['max_tokens']
            if 'disable_parallel_tool_use' in expected:
                assert body['tool_choice']['disable_parallel_tool_use'] is expected['disable_parallel_tool_use']
            if 'strict' in expected:
                assert body['tools'][0]['strict'] is expected['strict']
                if expected['strict']:
                    assert 'structured-outputs-2025-12-15' in self.headers.get('anthropic-beta', '').split(',')
                    assert body['tools'][0]['input_schema'] == expected['tool_schema']
            if 'output_schema' in expected:
                assert body['output_config']['format']['schema'] == expected['output_schema']
            if 'thinking_type' in expected:
                assert body['thinking']['type'] == expected['thinking_type']
                if 'thinking_budget' in expected:
                    assert body['thinking']['budget_tokens'] == expected['thinking_budget']
                else:
                    assert 'budget_tokens' not in body['thinking']
        records.append({'case': current['audit_case'], 'path': self.path,
                        'wire_body_base64': base64.b64encode(wire).decode(),
                        'wire_sha256': hashlib.sha256(wire).hexdigest(),
                        'fields': {k: body.get(k) for k in ['model', 'max_tokens', 'thinking', 'tools', 'tool_choice', 'output_config', 'messages', 'system', 'temperature', 'top_p']},
                        'beta': self.headers.get('anthropic-beta', '')})
        self.respond({'local_observation_only': True})

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
    assert inputs
    for path in inputs:
        current = json.loads(path.read_text())
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
print(json.dumps({'requests': len(records), 'exported_wire_matches': True, 'observations_not_acceptance': True}))
