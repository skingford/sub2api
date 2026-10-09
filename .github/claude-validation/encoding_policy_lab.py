"""Observe actual HTTP framing from exported production header-policy requests."""
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
                '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'),
                '-days', '2', '-subj', '/CN=api.anthropic.com',
                '-addext', 'subjectAltName=DNS:api.anthropic.com'], check=True,
               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
records = []


class Handler(lab.Handler):
    def do_POST(self):
        assert self.headers.get('x-api-key') == lab.DUMMY_KEY
        wire = self.rfile.read(int(self.headers['Content-Length']))
        encoding = self.headers.get('Content-Encoding', '')
        error = ''
        try:
            decoded = gzip.decompress(wire) if encoding == 'gzip' else wire
            value = json.loads(decoded)
        except (OSError, EOFError, ValueError) as exc:
            error = type(exc).__name__
            value = None
        status = 400 if error else 200
        records.append({'headers': dict(self.headers), 'encoding': encoding,
                        'wire_body_base64': base64.b64encode(wire).decode(),
                        'wire_sha256': hashlib.sha256(wire).hexdigest(),
                        'decode_error': error, 'model': value.get('model') if value else None,
                        'status': status})
        self.respond({'local_protocol_result': 'invalid_encoding' if error else 'valid_json'}, status)


context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(str(ROOT / 'server.pem'), str(ROOT / 'server.key'))
context.set_alpn_protocols(['http/1.1'])
context.keylog_filename = str(ROOT / 'server-reference.keys')
server = lab.ThreadingHTTPServer(('127.0.0.1', 443), Handler)
server.daemon_threads = True
server.socket = context.wrap_socket(server.socket, server_side=True)
threading.Thread(target=server.serve_forever, daemon=True).start()
capture, capture_log = lab.start_capture(ROOT)
try:
    for case in ['unchanged-none', 'unchanged-gzip', 'mapped-none', 'mapped-identity', 'mapped-gzip', 'unchanged-identity']:
        for repeat in range(12 if case == 'unchanged-identity' else 2):
            before = len(records)
            process = subprocess.run(['/opt/trace-transport-probe', '/inputs/' + case + '.json', 'auto'],
                                     env={'PATH': os.environ['PATH'], 'SSL_CERT_FILE': str(ROOT / 'server.pem')},
                                     capture_output=True, text=True, timeout=20)
            assert len(records) == before + 1, (case, process.stderr)
            records[-1].update(case=case, repeat=repeat, exit_code=process.returncode,
                               stdout=process.stdout, stderr=process.stderr)
            lab.write_json(ROOT / 'records.json', records)
            print(json.dumps({k: v for k, v in records[-1].items() if k not in ['headers', 'wire_body_base64', 'stderr']}), flush=True)
finally:
    server.shutdown()
    server.server_close()
    capture.send_signal(signal.SIGINT)
    _, tail = capture.communicate(timeout=8)
    capture_log.write(tail)
    capture_log.close()
