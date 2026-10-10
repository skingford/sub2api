"""Exercise SSE framing with unmodified pinned CLI binaries in network-none Docker.

Uses only synthetic responses and credentials; validates CLI output separately
from the existing PCAP response-byte verifier.
"""
import json

import comprehensive_lab as lab


CONTROLS = ['standard', 'comments', 'data-first', 'multiline', 'crlf', 'cr', 'fragmented']
MARKER = 'LOCAL_SSE_中文_🙂'
lab.CASES = [{'name': control, 'base': 'api-arg', 'control': control} for control in CONTROLS]


class Handler(lab.Handler):
    def do_POST(self):
        if not self.path.startswith('/v1/messages?'):
            return super().do_POST()
        body = self.record(self.rfile.read(int(self.headers['Content-Length'])))
        state = lab.audit.STATE
        state['messages'] += 1
        events = [
            {'type': 'message_start', 'message': {'id': 'msg_framing', 'type': 'message',
             'role': 'assistant', 'model': body['model'], 'content': [], 'stop_reason': None,
             'stop_sequence': None, 'usage': {'input_tokens': 17, 'output_tokens': 0}}},
            {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
            {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': MARKER}},
            {'type': 'content_block_stop', 'index': 0},
            {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn', 'stop_sequence': None},
             'usage': {'output_tokens': 9}},
            {'type': 'message_stop'},
        ]
        control = lab.CURRENT['control']
        frames = []
        for event in events:
            payload = json.dumps(event, ensure_ascii=False, indent=2 if control == 'multiline' else None)
            event_line = 'event: ' + event['type']
            data = ['data: ' + line for line in payload.splitlines()]
            lines = [event_line] + data
            if control == 'comments':
                lines = [event_line, ': synthetic keepalive', 'id: local', 'retry: 1'] + data
            elif control == 'data-first':
                lines = data + [event_line]
            frames.append('\n'.join(lines) + '\n\n')
        stream = ''.join(frames)
        if control in ('crlf', 'cr'):
            stream = stream.replace('\n', '\r\n' if control == 'crlf' else '\r')
        state['responses'].append({'request_index': len(state['records']) - 1, 'body': stream})
        payload = stream.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        if control == 'fragmented':
            for byte in payload:
                self.wfile.write(bytes([byte]))
                self.wfile.flush()
        else:
            self.wfile.write(payload)


if __name__ == '__main__':
    lab.Handler = Handler
    lab.main()
    summary = json.loads((lab.ROOT / 'summary.json').read_text())
    for case in summary['cases']:
        assert case['model_requests'] == 1, case
        assert case['outcomes'] == [{'exit_code': 0, 'timed_out': False}], case
        output = json.loads((lab.ROOT / case['case'] / 'stdout.jsonl').read_text())
        assert output['result'] == MARKER, output
        assert output['usage']['input_tokens'] == 17, output
        assert output['usage']['output_tokens'] == 9, output
    print(json.dumps({'validated_cli_cases': len(summary['cases']), 'content_and_usage_match': True}))
