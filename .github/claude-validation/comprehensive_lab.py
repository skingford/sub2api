"""Source-guided 2.1.292 request branches; unmodified CLI in network-none Docker.

Reuses the synthetic TLS responder and process isolation from extended_audit.
The wrapper changes only documented CLI arguments/environment and mock replies.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

import extended_audit as audit
import lab

ROOT = Path('/work')
MODELS = ['claude-sonnet-4-6', 'claude-opus-4-6', 'claude-haiku-4-5-20251001',
          'claude-sonnet-5-5', 'claude-opus-5-5']
CASES = []
for model in MODELS:
    for mode in ['api-arg', 'oauth-arg', 'oauth-stream']:
        CASES.append({'name': model + '-' + mode, 'base': mode, 'model': model,
                      'generic_controls': {}})
CASES += [
    {'name': 'max4096', 'base': 'api-arg', 'env': {'CLAUDE_CODE_MAX_OUTPUT_TOKENS': '4096'},
     'generic_controls': {'max_tokens': 4096}},
    {'name': 'haiku-budget2048', 'base': 'api-arg', 'model': MODELS[2],
     'env': {'MAX_THINKING_TOKENS': '2048'},
     'generic_controls': {'thinking': {'type': 'enabled', 'budget_tokens': 2048, 'display': 'omitted'}}},
    {'name': 'thinking-off', 'base': 'api-arg', 'env': {'MAX_THINKING_TOKENS': '0'},
     'generic_controls': {'thinking': {'type': 'disabled'}}},
    {'name': 'effort-low', 'base': 'api-arg', 'args': ['--effort', 'low'],
     'generic_controls': {'output_config': {'effort': 'low'}}},
    {'name': 'effort-unset', 'base': 'api-arg', 'env': {'CLAUDE_CODE_EFFORT_LEVEL': 'unset'}},
    {'name': 'cache-off', 'base': 'api-arg', 'env': {'DISABLE_PROMPT_CACHING': '1'}},
    {'name': 'cache-1h', 'base': 'oauth-arg', 'env': {'CLAUDE_CODE_PROMPT_CACHE_TTL': '1h'}},
    {'name': 'permission-plan', 'base': 'parallel-read', 'permission': 'plan'},
    {'name': 'gzip-enabled', 'base': 'api-arg', 'long_system': True,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1'}},
    {'name': 'gzip-rejected', 'base': 'api-arg', 'long_system': True, 'reject': 415,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1'}},
    {'name': 'disabled-extra-fields', 'base': 'api-arg',
     'env': {'CLAUDE_CODE_EXTRA_BODY': '{"thinking":{"type":"disabled","display":"updates","budget_tokens":2048}}'},
     'generic_controls': {'thinking': {'type': 'disabled', 'display': 'updates', 'budget_tokens': 2048}}},
    {'name': 'explicit-temperature', 'base': 'api-arg',
     'env': {'CLAUDE_CODE_EXTRA_BODY': '{"temperature":0.4}'},
     'generic_controls': {'temperature': 0.4}},
]
for status in [400, 401, 403, 408, 409, 429, 503, 529]:
    CASES.append({'name': 'status-' + str(status), 'base': 'api-arg', 'reject': status,
                  'reply_headers': {'retry-after-ms': '1'},
                  'env': {'CLAUDE_CODE_MAX_RETRIES': '1', 'CLAUDE_CODE_OVERLOADED_RETRY_BASE_DELAY_MS': '1'}})
CASES += [
    {'name': '503-no-retry', 'base': 'api-arg', 'reject': 503,
     'reply_headers': {'x-should-retry': 'false'}},
    {'name': '400-force-retry', 'base': 'api-arg', 'reject': 400,
     'reply_headers': {'x-should-retry': 'true', 'retry-after-ms': '1'}},
]
for name in ['parallel-read', 'image-read', 'thinking-tool', 'resume', 'extra-metadata',
             'structured-output', 'compact', 'count-context', 'oauth-count-context', 'multi-turn']:
    CASES.append({'name': name, 'base': name})
for model in MODELS[-2:]:
    for name in ['compact', 'count-context']:
        CASES.append({'name': model + '-' + name, 'base': name, 'model': model})

CURRENT = {}
OriginalHandler = audit.Handler
original_popen = subprocess.Popen


class Handler(OriginalHandler):
    def record(self, raw=b''):
        body = super().record(raw)
        audit.STATE['records'][-1].update(
            wire_body_base64=base64.b64encode(raw).decode(),
            wire_body_sha256=hashlib.sha256(raw).hexdigest())
        return body

    def do_POST(self):
        if (CURRENT.get('reject') and self.path.startswith('/v1/messages') and
                not self.path.startswith('/v1/messages/count_tokens') and audit.STATE['messages'] == 0):
            self.record(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
            audit.STATE['messages'] += 1
            status = CURRENT['reject']
            kind = 'overloaded_error' if status in [503, 529] else 'rate_limit_error' if status == 429 else 'invalid_request_error'
            payload = json.dumps({'type': 'error', 'error': {'type': kind,
                                  'message': 'Local synthetic protocol branch'}}).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(payload)))
            self.send_header('request-id', 'req_local_rejection')
            for key, value in CURRENT.get('reply_headers', {}).items():
                self.send_header(key, value)
            self.end_headers()
            self.wfile.write(payload)
            return
        super().do_POST()


def launch(argv, *args, **kwargs):
    if argv and argv[0] == '/opt/claude':
        at = argv.index('--') if '--' in argv else len(argv)
        argv[at:at] = CURRENT.get('args', [])
        if 'permission' in CURRENT:
            argv[argv.index('--permission-mode') + 1] = CURRENT['permission']
        if CURRENT.get('long_system'):
            argv[argv.index('--system-prompt') + 1] += ' Synthetic compression fixture.' * 400
        kwargs['env'].update(CURRENT.get('env', {}))
    return original_popen(argv, *args, **kwargs)


def main():
    global CURRENT
    os.umask(0o077)
    audit.ROOT = ROOT
    lab.ROOT = ROOT
    lab.write_json(ROOT / 'isolation.json', lab.verify_isolation())
    digest = hashlib.sha256(Path('/opt/claude').read_bytes()).hexdigest()
    assert digest == 'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
                    '-keyout', str(ROOT / 'server.key'), '-out', str(ROOT / 'server.pem'), '-days', '2',
                    '-subj', '/CN=api.anthropic.com', '-addext', 'subjectAltName=DNS:api.anthropic.com,IP:127.0.0.1'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    audit.Handler = Handler
    subprocess.Popen = launch
    selected = set(sys.argv[1:])
    assert not selected.difference(c['name'] for c in CASES), selected
    results = []
    # Finish renamed variants before a base case retains the same directory.
    for case in sorted(CASES, key=lambda item: item['name'] == item['base']):
        if selected and case['name'] not in selected:
            continue
        CURRENT = case
        audit.TURN_PAUSE_SECONDS = case.get('turn_pause_seconds', 0)
        os.environ['CLAUDE_AUDIT_MODEL'] = case.get('model', MODELS[0])
        result = audit.run_case(case['base'])
        folder = ROOT / case['base']
        if case['name'] != case['base']:
            folder.rename(ROOT / case['name'])
        lab.write_json(ROOT / case['name'] / 'case.json', case)
        result['case'] = case['name']
        results.append(result)
        lab.write_json(ROOT / 'summary.json', {'binary_sha256': digest, 'cases': results})


if __name__ == '__main__':
    main()
