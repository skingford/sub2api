"""Replay native inputs through production transport, logging on/off, offline."""
import json
import os
from pathlib import Path
import signal
import ssl
import subprocess
import threading

import lab
import validation
from proxy_audit_lab import start_proxy

ROOT = Path('/work')
os.umask(0o077)
lab.ROOT = ROOT
lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
                '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'),
                '-days', '2', '-subj', '/CN=api.anthropic.com',
                '-addext', 'subjectAltName=DNS:api.anthropic.com,IP:127.0.0.1'],
               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
results = []
for case in ['plain', 'gzip-runtime', 'gzip-blocks', 'gzip-unicode']:
    for tracing in [False, True]:
        for mode in ['auto', 'auto-http-proxy', 'auto-https-proxy', 'auto-socks5-proxy']:
            folder = ROOT / (case + '-' + ('trace' if tracing else 'plain') + '-' + mode)
            folder.mkdir()
            validation.STATE.update(name=folder.name, folder=folder, records=[], messages=0)
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            ctx.load_cert_chain(str(ROOT / 'server.pem'), str(ROOT / 'server.key'))
            ctx.set_alpn_protocols(['http/1.1'])
            ctx.keylog_filename = str(folder / 'server-reference.keys')
            server = lab.ThreadingHTTPServer(('127.0.0.1', 443), validation.Handler)
            server.daemon_threads = True
            server.socket = ctx.wrap_socket(server.socket, server_side=True)
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            capture, capture_log = lab.start_capture(folder)
            proxy = None
            command = ['/opt/trace-transport-probe', '/inputs/' + case + '.json', mode]
            if mode.endswith('-proxy'):
                proxy, url = start_proxy(ROOT / 'server.pem', ROOT / 'server.key', mode.split('-')[1])
                command.append(url)
            env = {'PATH': os.environ['PATH'], 'SSL_CERT_FILE': str(ROOT / 'server.pem')}
            if tracing:
                env['CLAUDE_CAPTURE_TRACE_DIR'] = str(folder / 'trace')
            try:
                process = subprocess.run(command, capture_output=True, text=True, timeout=25, env=env)
                (folder / 'stdout.txt').write_text(process.stdout)
                (folder / 'stderr.txt').write_text(process.stderr)
            finally:
                if proxy is not None:
                    proxy.shutdown()
                    proxy.server_close()
                server.shutdown()
                server.server_close()
                worker.join(timeout=2)
                capture.send_signal(signal.SIGINT)
                _, tail = capture.communicate(timeout=8)
                capture_log.write(tail)
                capture_log.close()
            lab.write_json(folder / 'requests.json', validation.STATE['records'])
            row = {'case': case, 'tracing': tracing, 'mode': mode, 'folder': folder.name,
                   'exit_code': process.returncode, 'requests': len(validation.STATE['records'])}
            results.append(row)
            lab.write_json(ROOT / 'summary.json', results)
            print(json.dumps(row), flush=True)
            assert process.returncode == 0 and len(validation.STATE['records']) == 1, row
