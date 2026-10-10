"""Feed synthetic SSE order/stop boundaries to both unmodified pinned CLIs."""
import json
import comprehensive_lab as lab
from content_edges_lab import MODELS

lab.CASES = [{"name": model + "-" + control, "base": "thinking-tool", "model": model,
              "response_control": control}
             for model in MODELS for control in ["text-thinking-tool", "tool-text", "text-redacted-tool",
                 "text-thinking-text", "max-tokens", "context-limit", "pause-turn", "refusal"]]
OriginalHandler = lab.Handler


class Handler(OriginalHandler):
    def do_POST(self):
        if not self.path.startswith('/v1/messages?'):
            return super().do_POST()
        body = self.record(self.rfile.read(int(self.headers['Content-Length'])))
        state = lab.audit.STATE
        state['messages'] += 1
        n = state['messages']
        control = lab.CURRENT['response_control']
        text = lambda value: {'type': 'text', 'text': value}
        thinking = {'type': 'thinking', 'thinking': 'LOCAL_THOUGHT', 'signature': 'local-fake-signature-not-provider-verified'}
        redacted = {'type': 'redacted_thinking', 'data': 'LOCAL_OPAQUE_REDACTED'}
        tool = {'type': 'tool_use', 'id': 'toolu_response_order', 'name': 'Read',
                'input': {'file_path': str(state['folder'] / 'fixture.txt')}}
        blocks, stop = [text('LOCAL_FINAL')], 'end_turn'
        if n == 1:
            if control == 'text-tool': blocks, stop = [text('LOCAL_BEFORE'), tool], 'tool_use'
            elif control == 'text-thinking-tool': blocks, stop = [text('LOCAL_BEFORE'), thinking, tool], 'tool_use'
            elif control == 'tool-text': blocks, stop = [tool, text('LOCAL_AFTER')], 'tool_use'
            elif control == 'text-redacted-tool': blocks, stop = [text('LOCAL_BEFORE'), redacted, tool], 'tool_use'
            elif control == 'text-thinking-text': blocks = [text('LOCAL_BEFORE'), thinking, text('LOCAL_AFTER')]
            else:
                stop = {'max-tokens': 'max_tokens', 'context-limit': 'model_context_window_exceeded',
                        'pause-turn': 'pause_turn', 'refusal': 'refusal'}[control]
        message = {'id': 'msg_response_' + str(n), 'type': 'message', 'role': 'assistant', 'model': body['model'],
                   'content': [], 'stop_reason': None, 'stop_sequence': None,
                   'usage': {'input_tokens': 128, 'output_tokens': 0, 'cache_read_input_tokens': 64, 'cache_creation_input_tokens': 16}}
        events = [{'type': 'message_start', 'message': message}]
        for index, block in enumerate(blocks):
            kind = block['type']
            if kind == 'text':
                initial, deltas = {'type': kind, 'text': ''}, [{'type': 'text_delta', 'text': block['text']}]
            elif kind == 'thinking':
                initial = {'type': kind, 'thinking': ''}
                deltas = [{'type': 'thinking_delta', 'thinking': block['thinking']}, {'type': 'signature_delta', 'signature': block['signature']}]
            elif kind == 'tool_use':
                initial = dict(block, input={})
                args = json.dumps(block['input']); split = len(args) // 2
                deltas = [{'type': 'input_json_delta', 'partial_json': args[:split]}, {'type': 'input_json_delta', 'partial_json': args[split:]}]
            else:
                initial, deltas = block, []
            events.append({'type': 'content_block_start', 'index': index, 'content_block': initial})
            events.extend({'type': 'content_block_delta', 'index': index, 'delta': delta} for delta in deltas)
            events.append({'type': 'content_block_stop', 'index': index})
        events += [{'type': 'message_delta', 'delta': {'stop_reason': stop, 'stop_sequence': None}, 'usage': {'output_tokens': 8}}, {'type': 'message_stop'}]
        stream = ''.join('event: ' + event['type'] + '\ndata: ' + json.dumps(event) + '\n\n' for event in events)
        state['responses'].append({'request_index': len(state['records']) - 1, 'body': stream})
        payload = stream.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('request-id', 'req_local_response_order')
        self.end_headers()
        self.wfile.write(payload)


if __name__ == '__main__':
    lab.Handler = Handler
    lab.main()
