"""Native CLI scenarios against a local TLS mock in Docker network=none."""
import hashlib
import json
import os
import queue
import signal
import ssl
import subprocess
import sys
import threading
import time
from pathlib import Path
import lab

ROOT = Path('/work')
TOKEN = 'local-only-oauth-token-not-a-real-credential'
MARKER = 'LOCAL_CLI_VALIDATION_20261008'
STATE = {}


class Handler(lab.Handler):
    def record(self, raw=b''):
        key = self.headers.get('x-api-key')
        auth = self.headers.get('Authorization')
        assert key in (None, lab.DUMMY_KEY), 'Unexpected API key'
        assert auth in (None, 'Bearer ' + TOKEN), 'Unexpected bearer token'
        assert not self.headers.get('Cookie'), 'Unexpected cookie'
        decoded = lab.gzip.decompress(raw) if self.headers.get('Content-Encoding') == 'gzip' else raw
        body = json.loads(decoded) if decoded else None
        with lab.LOCK:
            item = {'case': STATE['name'], 'method': self.command, 'path': self.path,
                    'headers': dict(self.headers), 'body': body,
                    'raw_body_utf8': decoded.decode(),
                    'raw_body_sha256': hashlib.sha256(decoded).hexdigest(),
                    'tls_version': self.connection.version(),
                    'cipher': list(self.connection.cipher()),
                    'alpn': self.connection.selected_alpn_protocol()}
            STATE['records'].append(item)
        return body

    def do_GET(self):
        self.record()
        self.respond({'type':'error','error':{'type':'not_found_error','message':'Local mock only'}}, 404)

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        assert length < 8 * 1024 * 1024
        body = self.record(self.rfile.read(length))
        if self.path.startswith('/v1/messages/count_tokens'):
            self.respond({'input_tokens': 128})
            return
        if not self.path.startswith('/v1/messages'):
            self.respond({'type':'error','error':{'type':'not_found_error','message':'Local mock only'}}, 404)
            return
        STATE['messages'] += 1
        attempt = STATE['messages']
        if STATE['name'] == 'retry-503' and attempt == 1:
            self.respond({'type':'error','error':{'type':'overloaded_error','message':'Synthetic retry probe'}}, 503)
            return
        tool = STATE['name'] == 'read-tool' and attempt == 1
        block = ({'type':'tool_use','id':'toolu_local_read','name':'Read',
                  'input':{'file_path':str(STATE['folder'] / 'fixture.txt')}} if tool
                 else {'type':'text','text':'OK ' + str(attempt)})
        message = {'id':'msg_local_' + STATE['name'] + '_' + str(attempt),
                   'type':'message','role':'assistant','model':body['model'],
                   'content':[],'stop_reason':None,'stop_sequence':None,
                   'usage':{'input_tokens':128,'output_tokens':8,'cache_creation_input_tokens':0,
                            'cache_read_input_tokens':0}}
        if not body.get('stream'):
            message.update(content=[block], stop_reason='tool_use' if tool else 'end_turn')
            self.respond(message)
            return
        events = [('message_start', {'type':'message_start','message':message})]
        if tool:
            initial = dict(block, input={})
            delta = {'type':'input_json_delta','partial_json':json.dumps(block['input'])}
        else:
            initial = {'type':'text','text':''}
            delta = {'type':'text_delta','text':block['text']}
        events += [('content_block_start', {'type':'content_block_start','index':0,'content_block':initial}),
                   ('content_block_delta', {'type':'content_block_delta','index':0,'delta':delta}),
                   ('content_block_stop', {'type':'content_block_stop','index':0}),
                   ('message_delta', {'type':'message_delta','delta':{'stop_reason':'tool_use' if tool else 'end_turn',
                    'stop_sequence':None},'usage':{'output_tokens':8}}),
                   ('message_stop', {'type':'message_stop'})]
        payload = ''.join('event: '+n+'\ndata: '+json.dumps(v)+'\n\n' for n,v in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('request-id', 'req_local_' + str(attempt))
        self.end_headers()
        self.wfile.write(payload)


def run_case(name):
    folder = ROOT / name
    folder.mkdir()
    (folder / 'config').mkdir()
    (folder / 'home').mkdir()
    (folder / 'fixture.txt').write_text(MARKER + ' synthetic Read tool file.\n')
    STATE.update(name=name, folder=folder, records=[], messages=0)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(str(ROOT / 'server.pem'), str(ROOT / 'server.key'))
    ctx.set_alpn_protocols(['http/1.1'])
    ctx.keylog_filename = str(folder / 'server-reference.keys')
    server = lab.ThreadingHTTPServer(('127.0.0.1',443), Handler)
    server.daemon_threads = True
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    capture, capture_log = lab.start_capture(folder)
    env = {'PATH':os.environ['PATH'], 'HOME':str(folder / 'home'), 'LANG':'C.UTF-8',
           'ANTHROPIC_BASE_URL':'https://api.anthropic.com',
           'CLAUDE_CONFIG_DIR':str(folder / 'config'), 'NODE_EXTRA_CA_CERTS':str(ROOT / 'server.pem'),
           'DISABLE_AUTOUPDATER':'1', 'DISABLE_TELEMETRY':'1', 'DISABLE_ERROR_REPORTING':'1',
           'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC':'1',
           'CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL':'1',
           'CLAUDE_CODE_ENABLE_TELEMETRY':'0', 'API_TIMEOUT_MS':'5000', 'TERM':'dumb'}
    oauth = name == 'oauth-synthetic'
    env['CLAUDE_CODE_OAUTH_TOKEN' if oauth else 'ANTHROPIC_API_KEY'] = TOKEN if oauth else lab.DUMMY_KEY
    args = ['/opt/claude', *([] if oauth else ['--bare']), '--model','claude-sonnet-4-6',
            '--tools','Read' if name == 'read-tool' else '', '--permission-mode','default',
            '--strict-mcp-config','--mcp-config','{"mcpServers":{}}','--setting-sources','',
            '--disable-slash-commands', '--system-prompt','Local synthetic protocol test. Reply with OK.',
            '--no-session-persistence','--max-turns','4', '--output-format','json', '-p']
    if name == 'read-tool':
        args += ['--allowedTools','Read']
    multi = name == 'multi-turn'
    if multi:
        args[args.index('json')] = 'stream-json'
        args += ['--input-format','stream-json','--verbose']
    else:
        prompt = MARKER + (' Read the fixture.txt file.' if name == 'read-tool' else ' Reply OK.')
        if name == 'unicode':
            prompt = '你好世界，这是一次本地协议测试。😀🧪 '+ MARKER
        if name == 'count-context':
            args.remove('--disable-slash-commands')
            prompt = '/context'
        args += ['--', prompt]
    lab.write_json(folder / 'invocation.json', {'argv':args,'synthetic_auth':'oauth-env' if oauth else 'api-key',
                                              'external_network':'disabled'})
    started = time.monotonic()
    timed_out = False
    lines = []
    with (folder / 'stderr.txt').open('w') as err:
        proc = subprocess.Popen(args, cwd=folder, env=env, stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=err, text=True, start_new_session=True)
        try:
            if multi:
                received = queue.Queue()
                def drain():
                    for line in proc.stdout:
                        received.put(line)
                    received.put(None)
                threading.Thread(target=drain,daemon=True).start()
                def send(prompt):
                    proc.stdin.write(json.dumps({'type':'user','message':{'role':'user','content':prompt}})+'\n')
                    proc.stdin.flush()
                send(MARKER + ' first turn. Reply OK.')
                sent_second = False
                deadline = time.monotonic() + 50
                while time.monotonic() < deadline:
                    line = received.get(timeout=max(0.1, deadline-time.monotonic()))
                    if line is None:
                        break
                    lines.append(line)
                    event = json.loads(line)
                    if event.get('type') == 'result' and not sent_second:
                        send(MARKER + ' second turn. Reply OK again.')
                        proc.stdin.close()
                        sent_second = True
                proc.wait(timeout=5)
            else:
                out, _ = proc.communicate(timeout=50)
                lines = [out]
        except (subprocess.TimeoutExpired, queue.Empty):
            timed_out = True
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join(timeout=2)
            capture.send_signal(signal.SIGINT)
            _, rest = capture.communicate(timeout=8)
            capture_log.write(rest)
            capture_log.close()
    (folder / 'stdout.json').write_text(''.join(lines))
    lab.write_json(folder / 'requests.json', STATE['records'])
    decoded = lab.decode_capture(folder, folder / 'server-reference.keys', 'server-decrypted')
    summary = {'name':name,'exit_code':proc.returncode,'timed_out':timed_out,
               'seconds':round(time.monotonic()-started,2), 'model_requests':STATE['messages'],
               'paths':[r['path'] for r in STATE['records']], 'pcap':decoded}
    lab.write_json(folder / 'summary.json', summary)
    print(json.dumps(summary), flush=True)
    return summary


def main():
    os.umask(0o077)
    lab.ROOT = ROOT
    lab.MARKER = MARKER
    lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
    subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-sha256','-nodes',
                    '-keyout',str(ROOT / 'server.key'),'-out',str(ROOT / 'server.pem'),
                    '-days','2','-subj','/CN=api.anthropic.com',
                    '-addext','subjectAltName=DNS:api.anthropic.com,IP:127.0.0.1'],
                   check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    version = subprocess.check_output(['/opt/claude','--version'],text=True,
                                     env={'PATH':os.environ['PATH'],'DISABLE_AUTOUPDATER':'1'}).strip()
    assert version.startswith('2.1.292'), version
    result = {'version':version,'binary_sha256':hashlib.sha256(Path('/opt/claude').read_bytes()).hexdigest(),
              'official_service_requests_possible':False,'cases':[]}
    for name in (sys.argv[1:] or ['baseline','read-tool','retry-503','multi-turn','oauth-synthetic','unicode','count-context']):
        result['cases'].append(run_case(name))
        lab.write_json(ROOT / 'summary.json',result)
    assert all(c['exit_code'] == 0 and not c['timed_out'] for c in result['cases']), result
    counts = {'baseline':1,'read-tool':2,'retry-503':2,'multi-turn':2,'oauth-synthetic':1,'unicode':1,'count-context':0}
    assert all(c['model_requests'] == counts[c['name']] for c in result['cases']), result


if __name__ == '__main__':
    main()
