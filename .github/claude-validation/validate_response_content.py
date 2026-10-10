"""Assemble captured synthetic SSE independently, then compare conversions."""
import base64
import collections
import copy
import json
from pathlib import Path
import sys


def assemble(root, output):
    fixtures = []
    for folder in sorted(root.glob('capture-*/')):
        if not (folder / 'pcap-verification.json').exists():
            continue
        for path in sorted(folder.glob('*/responses.json')):
            for number, row in enumerate(json.loads(path.read_text())):
                events = [json.loads(line[6:]) for line in row['body'].splitlines() if line.startswith('data: ')]
                if not events or events[0]['type'] != 'message_start':
                    continue
                response = copy.deepcopy(events[0]['message'])
                blocks, arguments = {}, {}
                for event in events[1:]:
                    kind = event['type']
                    index = event.get('index')
                    if kind == 'content_block_start':
                        blocks[index] = copy.deepcopy(event['content_block'])
                    elif kind == 'content_block_delta':
                        delta = event['delta']; block = blocks[index]
                        if delta['type'] == 'input_json_delta':
                            arguments[index] = arguments.get(index, '') + delta['partial_json']
                        else:
                            key = {'text_delta': 'text', 'thinking_delta': 'thinking', 'signature_delta': 'signature'}.get(delta['type'])
                            if key:
                                block[key] = block.get(key, '') + delta[key]
                    elif kind == 'message_delta':
                        response.update(event.get('delta', {}))
                        response['usage'].update(event.get('usage', {}))
                for index, raw in arguments.items():
                    blocks[index]['input'] = json.loads(raw)
                response['content'] = [blocks[index] for index in sorted(blocks)]
                fixtures.append({'name': str(path.parent.relative_to(root)) + '/' + str(number),
                                 'response': response, 'events': events, 'sse': row['body']})
    assert fixtures
    output.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'captured_responses': len(fixtures)}))


def canonical(output):
    blocks = []
    for item in output:
        if item['type'] == 'message':
            blocks.extend({'type': 'text', 'text': p['text']} for p in item.get('content', []) if p['type'] == 'output_text')
        elif item['type'] == 'function_call':
            blocks.append({'type': 'tool_use', 'name': item['name'], 'input': json.loads(item['arguments'])})
        elif item['type'] == 'reasoning':
            envelope = item.get('encrypted_content', '')
            if envelope.startswith('anthropic-thinking-v1:'):
                value = envelope.split(':', 1)[1]
                blocks.append(json.loads(base64.b64decode(value + '=' * (-len(value) % 4))))
            else:
                blocks.append({'type': 'thinking', 'thinking': ''.join(x.get('text', '') for x in item.get('summary', []))})
    return blocks


def validate(path, output):
    rows = json.loads(path.read_text())
    failures, mappings = [], collections.Counter()
    for row in rows:
        source = row['source']
        original = source['content']
        ordered = lambda xs: [{k: b[k] for k in ('type', 'text', 'name', 'input') if k in b}
                              for b in xs if b['type'] in ('text', 'tool_use')]
        opaque = lambda xs: [{k: b[k] for k in ('type', 'thinking', 'signature', 'data') if b.get(k)}
                             for b in xs if b['type'] in ('thinking', 'redacted_thinking')]
        for mode in ('nonstream', 'stream', 'gateway_buffered', 'gateway_stream'):
            converted = row.get(mode)
            reasons = []
            if converted is None:
                reasons.append('missing terminal response')
            else:
                actual = canonical(converted['output'])
                if ordered(actual) != ordered(original): reasons.append('text/tool order or content changed')
                if [block['type'] for block in actual] != [block['type'] for block in original]: reasons.append('block order/type changed')
                if opaque(actual) != opaque(original): reasons.append('opaque thinking/signature changed')
                usage = source['usage']
                expected_in = sum(usage.get(k, 0) for k in ('input_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens'))
                if converted['usage']['input_tokens'] != expected_in or converted['usage']['output_tokens'] != usage['output_tokens']:
                    reasons.append('usage changed')
                if source.get('stop_reason') == 'max_tokens' and converted['status'] != 'incomplete': reasons.append('token limit not incomplete')
                mappings[(source.get('stop_reason'), converted['status'])] += 1
            if reasons: failures.append({'name': row['name'], 'model': source['model'], 'mode': mode, 'reasons': reasons})
        for mode in ('events', 'gateway_events'):
            events = row[mode]
            sequence = [e['sequence_number'] for e in events]
            if sequence != list(range(len(sequence))): failures.append({'name': row['name'], 'mode': mode, 'reasons': ['invalid event sequence']})
            terminal = [e for e in events if e['type'] in ('response.completed', 'response.incomplete')]
            if len(terminal) != 1: failures.append({'name': row['name'], 'mode': mode, 'reasons': ['terminal event count']})
    result = {'source_responses': len(rows), 'conversion_comparisons': 4 * len(rows),
              'failed_comparisons': len(failures), 'contract_passed': not failures,
              'failures_by_reason': dict(collections.Counter(reason for f in failures for reason in f['reasons'])),
              'stop_mappings': [{'source': k[0], 'target': k[1], 'count': v} for k, v in sorted(mappings.items())],
              'failures': failures}
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({k: v for k, v in result.items() if k != 'failures'}))
    return bool(failures)


if __name__ == '__main__':
    if sys.argv[1] == '--assemble': assemble(Path(sys.argv[2]), Path(sys.argv[3]))
    elif sys.argv[1] == '--validate': sys.exit(validate(Path(sys.argv[2]), Path(sys.argv[3])))
    else: raise ValueError('Expected --assemble or --validate')
