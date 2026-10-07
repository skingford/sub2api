import json
import os
from pathlib import Path
import signal
import ssl
import subprocess
import threading
import lab
import validation

ROOT = Path('/work')
os.umask(0o077)
lab.ROOT = ROOT
lab.MARKER = validation.MARKER
lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-sha256','-nodes',
                '-keyout',str(ROOT/'server.key'),'-out',str(ROOT/'server.pem'),'-days','2',
                '-subj','/CN=api.anthropic.com','-addext','subjectAltName=DNS:api.anthropic.com,IP:127.0.0.1'],
               check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
summary = []
for mode in ['default','fingerprint']:
    folder = ROOT / mode
    folder.mkdir()
    validation.STATE.update(name='transport-'+mode,folder=folder,records=[],messages=0)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(str(ROOT/'server.pem'),str(ROOT/'server.key'))
    ctx.set_alpn_protocols(['http/1.1'])
    ctx.keylog_filename = str(folder/'server-reference.keys')
    server = lab.ThreadingHTTPServer(('127.0.0.1',443), validation.Handler)
    server.daemon_threads = True
    server.socket = ctx.wrap_socket(server.socket,server_side=True)
    worker = threading.Thread(target=server.serve_forever,daemon=True)
    worker.start()
    capture, capture_log = lab.start_capture(folder)
    try:
        result = subprocess.run(['/opt/transport-probe','/opt/input.json',mode],capture_output=True,text=True,timeout=25,
                                env={'PATH':os.environ['PATH'],'SSL_CERT_FILE':str(ROOT/'server.pem')})
        (folder/'stdout.txt').write_text(result.stdout)
        (folder/'stderr.txt').write_text(result.stderr)
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)
        capture.send_signal(signal.SIGINT)
        _,rest=capture.communicate(timeout=8)
        capture_log.write(rest)
        capture_log.close()
    lab.write_json(folder/'requests.json',validation.STATE['records'])
    decoded=lab.decode_capture(folder,folder/'server-reference.keys','server-decrypted')
    item={'mode':mode,'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr,
          'requests':len(validation.STATE['records']),'pcap':decoded}
    summary.append(item)
    print(json.dumps(item),flush=True)
lab.write_json(ROOT/'summary.json',summary)
assert all(x['exit_code']==0 and x['requests']==1 for x in summary)
