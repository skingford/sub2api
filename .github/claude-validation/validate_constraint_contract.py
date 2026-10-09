"""Fail a validation run when an explicit constraint or summary sequence drifts.

Run against the output directory containing entrypoint-controls.json,
summary-sequences-292.json and summary-sequences-295.json. Unlike the observation
probes, this gate fails on lost schema/stop, changed token caps or recovery errors.
It does not equate passing the recorded contract with all-provider equivalence.
"""
import json
from pathlib import Path
import sys


def validate(root):
    rows = json.loads((root / 'entrypoint-controls.json').read_text())
    if len(rows) != 102:
        raise ValueError(f'Expected 102 entrypoint observations, received {len(rows)}')
    keys = {(r['model'], r['route'], r['control']) for r in rows}
    models = ('claude-sonnet-4-6', 'claude-opus-4-6', 'claude-haiku-4-5-20251001',
              'claude-sonnet-5-5', 'claude-opus-5-5', 'claude-haiku-5-5')
    expected_keys = {(m, route, c) for m in models
                     for route in ('/v1/messages', '/v1/chat/completions', '/v1/responses')
                     for c in ('default', 'format', 'max4096', 'max64', 'effort-xhigh', 'stop')
                     if not (route == '/v1/responses' and c == 'stop')}
    if len(keys) != len(rows) or keys != expected_keys:
        raise ValueError('Missing, duplicate or unexpected entrypoint observations')
    for row in rows:
        label = (row['model'], row['route'], row['control'])
        if row.get('error') or row['status'] != 200 or row['calls'] != 1:
            raise ValueError(f'Request failed: {label}')
        control, fields = row['control'], row['fields']
        if control == 'format':
            expected = row['input'].get('output_config', {}).get('format', {}).get('schema')
            expected = expected or row['input'].get('text', {}).get('format', {}).get('schema')
            expected = expected or row['input'].get('response_format', {}).get('json_schema', {}).get('schema')
            actual = (fields.get('output_config') or {}).get('format', {}).get('schema')
            if expected is None or actual != expected:
                raise ValueError(f'Schema changed or disappeared: {label}')
        if control == 'stop' and fields.get('stop_sequences') != ['STOP_AUDIT']:
            raise ValueError(f'Stop sequences changed: {label}')
        if control in ('max64', 'max4096') and fields.get('max_tokens') != int(control[3:]):
            raise ValueError(f'Explicit output limit changed: {label}')
    for version in ('292', '295'):
        rows = json.loads((root / f'summary-sequences-{version}.json').read_text())
        if len(rows) != 18:
            raise ValueError(f'Expected 18 summary requests for {version}, received {len(rows)}')
        for case in ('summary-space', 'summary-bom', 'summary-nel'):
            selected = [r for r in rows if r['case'] == case]
            if [r['attempt'] for r in selected] != list(range(1, 7)):
                raise ValueError(f'Incomplete summary sequence: {version}/{case}')
            if not all(r.get('accepted') and not r.get('error') for r in selected):
                raise ValueError(f'Summary continuation rejected: {version}/{case}')
    return {'entrypoint_observations': 102, 'summary_requests': 36, 'contract_passed': True}


if __name__ == '__main__':
    print(json.dumps(validate(Path(sys.argv[1]))))
