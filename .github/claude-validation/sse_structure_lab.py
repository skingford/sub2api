"""Extend the pinned terminal lab with structural and lifecycle observations."""
import json

import sse_terminal_lab as terminal

lab = terminal.lab
original_frames = terminal.response_frames
CONTROLS = terminal.CONTROLS + [
    'null-known', 'array-known', 'scalar-known', 'empty-known', 'no-message-start',
    'null-message', 'null-block', 'null-delta', 'empty-stream', 'start-only',
    'empty-complete', 'closed-content', 'duplicate-message-start',
    'duplicate-block-start', 'closed-then-delta', 'wrong-delta-type', 'gap-index',
    'delta-no-index', 'stop-no-index', 'valid-tool-json', 'truncated-tool-json',
]
lab.CASES = [{'name': control, 'base': 'api-arg', 'control': control,
              'env': {'CLAUDE_CODE_MAX_RETRIES': '0',
                      'CLAUDE_CODE_OVERLOADED_RETRY_BASE_DELAY_MS': '1'}}
             for control in CONTROLS]


def response_frames(control, model):
    if control in terminal.CONTROLS:
        return original_frames(control, model)
    source = original_frames('complete', model)
    events = [json.loads(part.split('\ndata: ', 1)[1]) for part in source.strip().split('\n\n')]
    frame = lambda event: 'event: ' + event['type'] + '\ndata: ' + json.dumps(event, ensure_ascii=False) + '\n\n'
    frames = [frame(event) for event in events]
    if control in ('null-known', 'array-known', 'scalar-known', 'empty-known'):
        value = {'null-known': 'null', 'array-known': '[]', 'scalar-known': '42', 'empty-known': '{}'}[control]
        frames.insert(2, 'event: content_block_delta\ndata: ' + value + '\n\n')
    elif control == 'no-message-start': frames = frames[1:]
    elif control == 'null-message': frames[0] = frame(dict(events[0], message=None))
    elif control == 'null-block': frames[1] = frame(dict(events[1], content_block=None))
    elif control == 'null-delta': frames.insert(2, frame(dict(events[2], delta=None)))
    elif control == 'empty-stream': frames = []
    elif control == 'start-only': frames = frames[:1]
    elif control == 'empty-complete': frames = frames[:1] + frames[4:]
    elif control == 'closed-content': frames = frames[:4]
    elif control == 'duplicate-message-start': frames.insert(1, frames[0])
    elif control == 'duplicate-block-start': frames.insert(2, frames[1])
    elif control == 'closed-then-delta': frames.insert(4, frames[2])
    elif control == 'wrong-delta-type': frames.insert(2, frame(dict(events[2], delta={'type': 'thinking_delta', 'thinking': 'UNEXPECTED_THOUGHT'})))
    elif control == 'gap-index': frames[1] = frame(dict(events[1], index=2))
    elif control == 'delta-no-index': frames[2] = frame({k: v for k, v in events[2].items() if k != 'index'})
    elif control == 'stop-no-index': frames[3] = frame({k: v for k, v in events[3].items() if k != 'index'})
    elif control in ('valid-tool-json', 'truncated-tool-json'):
        if lab.audit.STATE['messages'] > 1:
            return source
        tool = {'type': 'tool_use', 'id': 'toolu_structure', 'name': 'Read', 'input': {}}
        frames[1] = frame(dict(events[1], content_block=tool))
        args = '{"file_path":"/work/synthetic-unavailable"}' if control == 'valid-tool-json' else '{"file_path":'
        frames[2] = frame(dict(events[2], delta={'type': 'input_json_delta', 'partial_json': args}))
        frames[4] = frame(dict(events[4], delta={'stop_reason': 'tool_use', 'stop_sequence': None}))
    return ''.join(frames)


if __name__ == '__main__':
    terminal.response_frames = response_frames
    lab.Handler = terminal.Handler
    lab.main()
    summary = json.loads((lab.ROOT / 'summary.json').read_text())
    rows = []
    for case in summary['cases']:
        raw = (lab.ROOT / case['case'] / 'stdout.jsonl').read_text()
        rows.append(dict(case, cli_output=[json.loads(line) for line in raw.splitlines() if line]))
    lab.lab.write_json(lab.ROOT / 'observations.json', rows)
