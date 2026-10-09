"""Deeper native boundaries: size/UTF-16 gates, block fallback, and late headers.

Uses only the pinned unmodified CLI and network-none synthetic responses.
"""
import json
import post_alignment_lab as edge

lab = edge.lab
GZIP = {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1'}
BLOCKS = dict(GZIP, CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS='2')
lab.CASES = []
for size in [100, 3000, 4096]:
    lab.CASES.append({'name': f'runtime-size-{size}', 'base': 'api-arg', 'system_chars': size, 'env': GZIP})
for size in [500000, 524000]:
    lab.CASES.append({'name': f'blocks-size-{size}', 'base': 'api-arg', 'system_chars': size, 'env': BLOCKS})
for size in [400000, 500000]:
    lab.CASES.append({'name': f'blocks-unicode-{size}', 'base': 'api-arg', 'system_chars': size,
                      'unicode_system': True, 'env': BLOCKS})
for level in [0, 2, 5, 8]:
    lab.CASES.append({'name': f'blocks-level-{level}', 'base': 'api-arg', 'system_chars': 600000,
                      'env': dict(BLOCKS, CLAUDE_CODE_GZIP_REQUEST_BODY_LEVEL=str(level))})
for name, status, count, origin in [('origin-once', 400, 1, True), ('origin-twice', 400, 2, True),
                                     ('proxy-415', 415, 1, False)]:
    lab.CASES.append({'name': 'blocks-reject-' + name, 'base': 'multi-turn', 'system_chars': 600000,
                      'env': BLOCKS, 'turn_pause_seconds': 1,
                      'compression_rejection': {'status': status, 'count': count, 'origin': origin}})
lab.CASES.append({'name': 'blocks-retry-503', 'base': 'api-arg', 'system_chars': 600000,
                  'reject': 503, 'reply_headers': {'retry-after-ms': '1'},
                  'env': dict(BLOCKS, CLAUDE_CODE_MAX_RETRIES='1')})
for name, extra in [('speed-fast', {'speed': 'fast'}), ('thread-create', {'thread': {'type': 'create'}}),
                     ('thread-continue', {'thread': {'type': 'continue', 'id': 'local-synthetic-thread'}})]:
    lab.CASES.append({'name': 'blocks-exclusion-' + name, 'base': 'api-arg', 'system_chars': 600000,
                      'env': dict(BLOCKS, CLAUDE_CODE_EXTRA_BODY=json.dumps(extra, separators=(',', ':')))})
for name, header in [('ua-override', 'User-Agent: claude-cli/2.1.293 (external, cli)'),
                      ('version-empty', 'anthropic-version:')]:
    lab.CASES.append({'name': 'gzip-' + name, 'base': 'api-arg', 'system_chars': 30000,
                      'env': dict(GZIP, ANTHROPIC_CUSTOM_HEADERS=header)})
for name, extra in [('top-p', {'top_p': 0.8}), ('top-k', {'top_k': 20})]:
    lab.CASES.append({'name': name + '-extra', 'base': 'api-arg',
                      'env': {'CLAUDE_CODE_EXTRA_BODY': json.dumps(extra)}, 'generic_controls': extra})

OriginalHandler = lab.Handler


class Handler(OriginalHandler):
    def do_POST(self):
        rule = lab.CURRENT.get('compression_rejection')
        if (rule and self.path.startswith('/v1/messages?') and
                lab.audit.STATE['messages'] < rule['count']):
            self.record(self.rfile.read(int(self.headers['Content-Length'])))
            lab.audit.STATE['messages'] += 1
            payload = json.dumps({'type': 'error', 'error': {'type': 'invalid_request_error',
                'message': 'The request body is not valid JSON (synthetic compression rejection)'}}).encode()
            self.send_response(rule['status'])
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(payload)))
            if rule['origin']:
                self.send_header('request-id', 'req_local_compression_rejection')
            self.end_headers()
            self.wfile.write(payload)
            return
        super().do_POST()


if __name__ == '__main__':
    lab.launch = edge.launch
    lab.Handler = Handler
    lab.main()
