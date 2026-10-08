"""Unmodified 2.1.292 CLI: extended local protocol audit (network-none only)."""
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
import uuid
import zlib
import struct
from pathlib import Path

import lab
import validation

ROOT = Path('/work')
STATE = validation.STATE
CASES = ['parallel-read', 'image-read', 'thinking-tool', 'compact', 'resume',
         'alias-sonnet', 'extra-metadata', 'structured-output', 'api-json', 'oauth-json', 'oauth-stream',
         'api-arg', 'oauth-arg', 'api-stdin', 'count-context', 'oauth-count-context', 'multi-turn']


class Handler(validation.Handler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        assert length < 8 * 1024 * 1024
        body = self.record(self.rfile.read(length))
        if self.path.startswith('/v1/messages/count_tokens'):
            self.respond({'input_tokens': 128})
            return
        if not self.path.startswith('/v1/messages'):
            self.respond({'type': 'error', 'error': {'type': 'not_found_error', 'message': 'Local mock only'}}, 404)
            return
        STATE['messages'] += 1
        n = STATE['messages']
        name = STATE['name']
        tool = n == 1 and name in ('parallel-read', 'image-read', 'thinking-tool')
        blocks = []
        if tool:
            if name == 'thinking-tool':
                blocks.append({'type': 'thinking', 'thinking': 'Synthetic local reasoning.',
                               'signature': 'local-fake-signature-not-provider-verified'})
            files = ['fixture.txt', 'second.txt'] if name == 'parallel-read' else [
                'fixture.png' if name == 'image-read' else 'fixture.txt']
            blocks += [{'type': 'tool_use', 'id': 'toolu_audit_' + str(i), 'name': 'Read',
                        'input': {'file_path': str(STATE['folder'] / file)}} for i, file in enumerate(files)]
        else:
            blocks = [{'type': 'text', 'text': 'OK ' + str(n)}]
            if self.headers.get('x-claude-code-request-class') == 'compaction':
                blocks = [{'type': 'text', 'text': '<analysis>Local synthetic analysis.</analysis>\n\n<summary>\nLOCAL_EXTENDED_AUDIT trusted summary ' + str(n) + '.\nKeep the original synthetic task.\n</summary>'}]
        message = {'id': 'msg_audit_' + str(n), 'type': 'message', 'role': 'assistant',
                   'model': body['model'], 'content': [], 'stop_reason': None, 'stop_sequence': None,
                   'usage': {'input_tokens': 128, 'output_tokens': 8,
                             'cache_creation_input_tokens': 0, 'cache_read_input_tokens': 0}}
        events = [('message_start', {'type': 'message_start', 'message': message})]
        for i, block in enumerate(blocks):
            kind = block['type']
            if kind == 'tool_use':
                initial = dict(block, input={})
                deltas = [{'type': 'input_json_delta', 'partial_json': json.dumps(block['input'])}]
            elif kind == 'thinking':
                initial = {'type': kind, 'thinking': ''}
                deltas = [{'type': 'thinking_delta', 'thinking': block['thinking']},
                          {'type': 'signature_delta', 'signature': block['signature']}]
            else:
                initial = {'type': 'text', 'text': ''}
                deltas = [{'type': 'text_delta', 'text': block['text']}]
            events.append(('content_block_start', {'type': 'content_block_start', 'index': i, 'content_block': initial}))
            events += [('content_block_delta', {'type': 'content_block_delta', 'index': i, 'delta': d}) for d in deltas]
            events.append(('content_block_stop', {'type': 'content_block_stop', 'index': i}))
        events += [('message_delta', {'type': 'message_delta',
                    'delta': {'stop_reason': 'tool_use' if tool else 'end_turn', 'stop_sequence': None},
                    'usage': {'output_tokens': 8}}), ('message_stop', {'type': 'message_stop'})]
        stream = ''.join('event: ' + k + '\ndata: ' + json.dumps(v) + '\n\n' for k, v in events)
        STATE['responses'].append({'request_index': len(STATE['records']) - 1, 'body': stream})
        payload = stream.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('request-id', 'req_audit_' + str(n))
        self.end_headers()
        self.wfile.write(payload)


def png():
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!IIBBBBB', 8, 8, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress((b'\0' + b'\x20\x80\xc0' * 8) * 8)) + chunk(b'IEND', b''))


def run_case(name):
    folder = ROOT / name
    folder.mkdir()
    for item in ('config', 'home'):
        (folder / item).mkdir()
    (folder / 'fixture.txt').write_text('LOCAL_EXTENDED_AUDIT first synthetic observation.\n')
    (folder / 'second.txt').write_text('LOCAL_EXTENDED_AUDIT second synthetic observation.\n')
    (folder / 'fixture.png').write_bytes(png())
    STATE.update(name=name, folder=folder, records=[], responses=[], messages=0)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(str(ROOT / 'server.pem'), str(ROOT / 'server.key'))
    ctx.set_alpn_protocols(['http/1.1'])
    ctx.keylog_filename = str(folder / 'server-reference.keys')
    server = lab.ThreadingHTTPServer(('127.0.0.1', 443), Handler)
    server.daemon_threads = True
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    capture, capture_log = lab.start_capture(folder)
    env = {'PATH': os.environ['PATH'], 'HOME': str(folder / 'home'), 'LANG': 'C.UTF-8',
           'ANTHROPIC_BASE_URL': 'https://api.anthropic.com', 'ANTHROPIC_API_KEY': lab.DUMMY_KEY,
           'CLAUDE_CONFIG_DIR': str(folder / 'config'), 'NODE_EXTRA_CA_CERTS': str(ROOT / 'server.pem'),
           'DISABLE_AUTOUPDATER': '1', 'DISABLE_TELEMETRY': '1', 'DISABLE_ERROR_REPORTING': '1',
           'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC': '1', 'CLAUDE_CODE_ENABLE_TELEMETRY': '0',
           'CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL': '1',
           'API_TIMEOUT_MS': '5000', 'TERM': 'dumb'}
    if name == 'extra-metadata':
        env['CLAUDE_CODE_EXTRA_METADATA'] = json.dumps({'audit_tag': 'local-synthetic-tag'})
    oauth = name.startswith('oauth-')
    if oauth:
        del env['ANTHROPIC_API_KEY']
        env['CLAUDE_CODE_OAUTH_TOKEN'] = validation.TOKEN
    tools = 'Read' if name in ('parallel-read', 'image-read', 'thinking-tool') else ''
    model = 'sonnet' if name == 'alias-sonnet' else 'claude-sonnet-4-6'
    model = os.environ.get('CLAUDE_AUDIT_MODEL', model)
    args = ['/opt/claude', '--bare', '--model', model, '--tools', tools,
            '--permission-mode', 'default', '--strict-mcp-config', '--mcp-config', '{"mcpServers":{}}',
            '--setting-sources', '', '--system-prompt', 'Local synthetic protocol test. Reply OK.',
            '--max-turns', '5', '--output-format', 'stream-json', '--input-format', 'stream-json', '--verbose', '-p']
    if oauth:
        args.remove('--bare')
    plain_input = name.endswith(('-json', '-arg', '-stdin'))
    if plain_input:
        args[args.index('--output-format') + 1] = 'json'
        at = args.index('--input-format')
        del args[at:at + 2]
    if name.endswith(('-arg', '-stdin')):
        args.remove('--verbose')
    if name != 'resume':
        args += ['--no-session-persistence']
    if name not in ('compact', 'count-context', 'oauth-count-context'):
        args += ['--disable-slash-commands']
    if tools:
        args += ['--allowedTools', 'Read']
    if name == 'structured-output':
        args += ['--json-schema', '{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}']
    session = str(uuid.uuid4())
    invocations, output, outcomes = [], [], []

    def execute(extra, prompts):
        invocation = args + extra
        if name.endswith('-arg'):
            invocation += ['--', prompts[0]]
        invocations.append(invocation)
        with (folder / ('stderr-' + str(len(invocations)) + '.txt')).open('w') as err:
            p = subprocess.Popen(invocation, cwd=folder, env=env, stdin=subprocess.PIPE,
                                 stdout=subprocess.PIPE, stderr=err, text=True, start_new_session=True)
            q = queue.Queue()
            def drain():
                for line in p.stdout:
                    q.put(line)
                q.put(None)
            threading.Thread(target=drain, daemon=True).start()
            def send(text):
                p.stdin.write((text if plain_input else json.dumps({'type': 'user', 'message': {'role': 'user', 'content': text}})) + '\n')
                p.stdin.flush()
            remaining = list(prompts)
            if name.endswith('-arg'):
                remaining.pop(0)
            else:
                send(remaining.pop(0))
            if not remaining:
                p.stdin.close()
            timed_out = False
            deadline = time.monotonic() + 45
            try:
                while time.monotonic() < deadline:
                    line = q.get(timeout=max(.1, deadline - time.monotonic()))
                    if line is None:
                        break
                    output.append(line)
                    event = json.loads(line)
                    # Slash commands can finish with a result or a local system message.
                    if isinstance(event, dict) and event.get('type') == 'result' and remaining:
                        send(remaining.pop(0))
                        if not remaining:
                            p.stdin.close()
                p.wait(timeout=2)
            except (subprocess.TimeoutExpired, queue.Empty):
                timed_out = True
                os.killpg(p.pid, signal.SIGKILL)
                p.wait()
            outcomes.append({'exit_code': p.returncode, 'timed_out': timed_out})
    try:
        if name == 'resume':
            execute(['--session-id', session], ['LOCAL_EXTENDED_AUDIT first turn.'])
            execute(['--resume', session], ['LOCAL_EXTENDED_AUDIT continued turn.'])
        elif name == 'compact':
            execute([], ['LOCAL_EXTENDED_AUDIT remember the synthetic task.', '/compact', 'Continue after compaction.', 'Another ordinary turn.', '/compact Keep the synthetic task.', 'Continue after the second compaction.'])
        elif name == 'multi-turn':
            execute([], ['LOCAL_EXTENDED_AUDIT first turn.', 'LOCAL_EXTENDED_AUDIT second turn.'])
        elif name.endswith('count-context'):
            execute([], ['/context'])
        else:
            execute([], ['LOCAL_EXTENDED_AUDIT inspect synthetic fixtures and reply OK.'])
    finally:
        server.shutdown()
        server.server_close()
        capture.send_signal(signal.SIGINT)
        _, rest = capture.communicate(timeout=8)
        capture_log.write(rest)
        capture_log.close()
    (folder / 'stdout.jsonl').write_text(''.join(output))
    lab.write_json(folder / 'invocation.json', {'argv': invocations, 'synthetic_auth': 'oauth-env' if oauth else 'api-key', 'external_network': 'disabled'})
    lab.write_json(folder / 'requests.json', STATE['records'])
    lab.write_json(folder / 'responses.json', STATE['responses'])
    decoded = lab.decode_capture(folder, folder / 'server-reference.keys', 'server-decrypted')
    result = {'case': name, 'outcomes': outcomes, 'model_requests': STATE['messages'], 'pcap': decoded}
    lab.write_json(folder / 'summary.json', result)
    print(json.dumps(result), flush=True)
    return result


if __name__ == '__main__':
    os.umask(0o077)
    lab.ROOT = ROOT
    lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
    digest = hashlib.sha256(Path('/opt/claude').read_bytes()).hexdigest()
    assert digest == 'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
                    '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'), '-days', '2',
                    '-subj', '/CN=api.anthropic.com', '-addext', 'subjectAltName=DNS:api.anthropic.com,IP:127.0.0.1'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    results = []
    for case in sys.argv[1:] or CASES:
        assert case in CASES, case
        results.append(run_case(case))
        lab.write_json(ROOT / 'summary.json', {'binary_sha256': digest, 'cases': results})
