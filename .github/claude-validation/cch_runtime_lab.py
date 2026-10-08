"""Run the instrumented native runtime only in the existing network-none lab."""
import json
import os
from pathlib import Path
import signal
import ssl
import subprocess
import sys
import threading

sys.path.insert(0, '/opt')
import lab


def main():
    os.umask(0o077)
    root = Path('/work')
    lab.ROOT = root
    lab.write_json(root / 'isolation.json', lab.verify_isolation())
    subprocess.run([
        'openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
        '-keyout', str(root / 'server.key'), '-out', str(root / 'server.pem'),
        '-days', '2', '-subj', '/CN=api.anthropic.com',
        '-addext', 'subjectAltName=DNS:api.anthropic.com',
    ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    records = []

    class Handler(lab.Handler):
        def do_POST(self):
            assert self.headers.get('x-api-key') == lab.DUMMY_KEY
            assert not self.headers.get('Authorization')
            assert not self.headers.get('Cookie')
            size = int(self.headers['Content-Length'])
            assert 0 <= size < 8 * 1024 * 1024
            body = self.rfile.read(size)
            records.append({'name': self.headers['x-lab-case'], 'path': self.path,
                            'body': body.decode(), 'headers': dict(self.headers)})
            self.respond({'ok': True})

    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(root / 'server.pem'), str(root / 'server.key'))
    context.keylog_filename = str(root / 'server-reference.keys')
    context.set_alpn_protocols(['http/1.1'])
    server = lab.ThreadingHTTPServer(('127.0.0.1', 443), Handler)
    server.daemon_threads = True
    server.socket = context.wrap_socket(server.socket, server_side=True)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    capture, capture_log = lab.start_capture(root)
    try:
        result = subprocess.run(['/opt/claude-runtime-probe'], env={
            'PATH': os.environ['PATH'], 'HOME': '/tmp/lab-home',
            'NODE_EXTRA_CA_CERTS': str(root / 'server.pem'),
        }, capture_output=True, text=True, timeout=45)
        (root / 'stdout.txt').write_text(result.stdout)
        (root / 'stderr.txt').write_text(result.stderr)
        lab.write_json(root / 'records.json', records)
        print(result.returncode, result.stdout, result.stderr[-2000:])
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)
        capture.send_signal(signal.SIGINT)
        _, tail = capture.communicate(timeout=8)
        capture_log.write(tail)
        capture_log.close()
    assert result.returncode == 0
    assert len(records) == len(json.loads((root / 'cases.json').read_text()))


if __name__ == '__main__':
    main()
