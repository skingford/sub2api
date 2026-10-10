"""Verify all pinned structure cases against the independent CLI fixture."""
import json
from pathlib import Path
import sys

root = Path(sys.argv[1])
repo = Path(__file__).resolve().parents[2]
fixture = json.loads((repo / 'backend/internal/service/testdata/claude_sse_structure_contract.json').read_text())
summary = json.loads((root / 'summary.json').read_text())
expected = {row['name']: row for row in fixture['cases']}
assert len(expected) == len(summary['cases']) == 32
assert {row['case'] for row in summary['cases']} == set(expected)
for row in summary['cases']:
    case = row['case']
    contract = expected[case]
    folder = root / case
    output = json.loads((folder / 'stdout.jsonl').read_text())
    assert row['outcomes'] == [{'exit_code': int(not contract['success']), 'timed_out': False}], row
    assert output['is_error'] is not contract['success'], (case, output)
    assert output['result'] == contract['cli_result'], (case, output)
    assert output['usage']['input_tokens'] == contract['cli_input_tokens'], (case, output)
    assert output['usage']['output_tokens'] == contract['cli_output_tokens'], (case, output)
    requests = [r for r in json.loads((folder / 'requests.json').read_text())
                if r['path'].startswith('/v1/messages?')]
    assert [r['body']['stream'] for r in requests] == contract['request_stream_sequence'], case
    assert row['model_requests'] == len(requests)
    responses = json.loads((folder / 'responses.json').read_text())
    assert len(responses) == contract['response_count'], case
    assert responses[0]['body'] == contract['body'], case
    if case.endswith('tool-json'):
        tool = next(block for message in requests[1]['body']['messages'] if message['role'] == 'assistant'
                    for block in message['content'] if block['type'] == 'tool_use')
        want = ({'file_path': '/work/synthetic-unavailable'} if case == 'valid-tool-json'
                else {'__unparsedToolInput': {'raw': '{"file_path":', 'len': 13}})
        assert tool['input'] == want, (case, tool)
print(json.dumps({'cli_version': summary['cli_version'], 'cases': 32,
                  'accepted': 13, 'rejected': 19, 'model_requests': 58, 'sse_responses': 41, 'passed': True}))
