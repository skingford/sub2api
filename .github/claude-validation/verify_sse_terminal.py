"""Fail closed on a missing/changed pinned terminal-stream CLI observation."""
import json
from pathlib import Path
import sys


SUCCESS = {'complete': 9, 'missing-stop': 9, 'missing-delta': 0,
           'unknown-event': 9, 'unknown-json': 9}
FAILURE = {'error-before', 'error-after', 'invalid-json', 'partial-content',
           'negative-index', 'delta-before-start'}
root = Path(sys.argv[1])
summary = json.loads((root / 'summary.json').read_text())
assert {row['case'] for row in summary['cases']} == set(SUCCESS) | FAILURE
assert len(summary['cases']) == 11
for row in summary['cases']:
    case = row['case']
    failure = case in FAILURE
    assert row['outcomes'] == [{'exit_code': int(failure), 'timed_out': False}], row
    folder = root / case
    output = json.loads((folder / 'stdout.jsonl').read_text())
    assert output['is_error'] is failure, (case, output)
    requests = [r for r in json.loads((folder / 'requests.json').read_text())
                if r['path'].startswith('/v1/messages?')]
    assert [r['body']['stream'] for r in requests] == ([True, False] if failure else [True]), case
    assert row['model_requests'] == len(requests)
    assert len(json.loads((folder / 'responses.json').read_text())) == 1
    if failure:
        assert output['result'] == 'API Error: 400 LOCAL_NONSTREAM_FALLBACK_NOT_PROVIDED', output
    else:
        assert output['result'] == 'LOCAL_TERMINAL_中文_🙂', output
        assert output['usage']['input_tokens'] == 17, output
        assert output['usage']['output_tokens'] == SUCCESS[case], output
print(json.dumps({'cli_version': summary['cli_version'], 'cases': 11,
                  'accepted_streams': 5, 'failed_streams_with_explicit_fallback': 6,
                  'model_requests': 17, 'passed': True}))
