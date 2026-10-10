"""Observe terminal/error SSE semantics using pinned, unmodified offline CLIs.

Observation only: a successful harness exit is not a parity verdict. All replies
are synthetic; errors are repeated on retries so recovery cannot hide a failure.
"""
import json

import comprehensive_lab as lab


CONTROLS = ['complete', 'error-before', 'error-after', 'invalid-json',
            'missing-stop', 'missing-delta', 'partial-content', 'unknown-event',
            'unknown-json', 'negative-index', 'delta-before-start']
MARKER = 'LOCAL_TERMINAL_中文_🙂'
lab.CASES = [{'name': control, 'base': 'api-arg', 'control': control,
              'env': {'CLAUDE_CODE_MAX_RETRIES': '0',
                      'CLAUDE_CODE_OVERLOADED_RETRY_BASE_DELAY_MS': '1'}}
             for control in CONTROLS]


def response_frames(control, model):
    events = [
        {'type': 'message_start', 'message': {'id': 'msg_terminal', 'type': 'message',
         'role': 'assistant', 'model': model, 'content': [], 'stop_reason': None,
         'stop_sequence': None, 'usage': {'input_tokens': 17, 'output_tokens': 0}}},
        {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
        {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': MARKER}},
        {'type': 'content_block_stop', 'index': 0},
        {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn', 'stop_sequence': None},
         'usage': {'output_tokens': 9}},
        {'type': 'message_stop'},
    ]
    frame = lambda event: 'event: ' + event['type'] + '\ndata: ' + json.dumps(event, ensure_ascii=False) + '\n\n'
    frames = [frame(event) for event in events]
    error = frame({'type': 'error', 'error': {'type': 'invalid_request_error', 'message': 'LOCAL_SYNTHETIC_STREAM_ERROR'}})
    if control == 'error-before':
        frames = [error]
    elif control == 'error-after':
        frames = frames[:3] + [error]
    elif control == 'invalid-json':
        frames.insert(2, 'event: content_block_delta\ndata: {invalid\n\n')
    elif control == 'missing-stop':
        frames = frames[:-1]
    elif control == 'missing-delta':
        frames = frames[:4] + frames[5:]
    elif control == 'partial-content':
        frames = frames[:3]
    elif control == 'unknown-event':
        frames.insert(2, 'event: local_future_event\ndata: ' + json.dumps(dict(events[2], delta={'type': 'text_delta', 'text': 'SHOULD_BE_IGNORED'})) + '\n\n')
    elif control == 'unknown-json':
        frames.insert(2, 'event: local_future_event\ndata: {invalid\n\n')
    elif control == 'negative-index':
        frames.insert(2, frame(dict(events[2], index=-1)))
    elif control == 'delta-before-start':
        frames.insert(1, frames[2])
    return ''.join(frames)


class Handler(lab.Handler):
    def do_POST(self):
        if not self.path.startswith('/v1/messages?'):
            return super().do_POST()
        body = self.record(self.rfile.read(int(self.headers['Content-Length'])))
        state = lab.audit.STATE
        state['messages'] += 1
        if not body.get('stream'):
            # Explicitly fail fallback attempts. A synthetic fallback success
            # must not make a broken initial SSE stream appear accepted.
            return self.respond({'type': 'error', 'error': {'type': 'invalid_request_error',
                                'message': 'LOCAL_NONSTREAM_FALLBACK_NOT_PROVIDED'}}, 400)
        stream = response_frames(lab.CURRENT['control'], body['model'])
        state['responses'].append({'request_index': len(state['records']) - 1, 'body': stream})
        payload = stream.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


if __name__ == '__main__':
    lab.Handler = Handler
    lab.main()
    summary = json.loads((lab.ROOT / 'summary.json').read_text())
    rows = []
    for case in summary['cases']:
        raw = (lab.ROOT / case['case'] / 'stdout.jsonl').read_text()
        rows.append(dict(case, cli_output=[json.loads(line) for line in raw.splitlines() if line]))
    lab.lab.write_json(lab.ROOT / 'observations.json', rows)
