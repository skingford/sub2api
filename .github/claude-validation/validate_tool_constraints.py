"""Fail on lost explicit tool/reasoning constraints in the 504-case audit.

This checks wire intent, not live provider acceptance. Messages controls and
documented rejections stay in the denominator. The pre-fix audited revision
fails; a test-process PASS alone is not a conformance result.
"""
import argparse
import itertools
import json
from pathlib import Path

MODELS = ['claude-sonnet-4-6', 'claude-opus-4-6', 'claude-haiku-4-5-20251001',
          'claude-sonnet-5-5', 'claude-opus-5-5', 'claude-haiku-5-5']
CONTROLS = ['parallel-false-auto', 'parallel-false-default', 'parallel-true-auto',
            'strict-true', 'strict-false', 'strict-parallel-schema', 'max64-high',
            'max1024-high', 'max1025-high', 'max4096-high', 'max64-none',
            'max64-low', 'named-tool', 'required-tool']
ROUTES = ['/v1/messages', '/v1/chat/completions', '/v1/responses']

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('observations', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    rows = json.loads(args.observations.read_text())
    expected = set(itertools.product(MODELS, ['apikey', 'oauth'], ROUTES, CONTROLS))
    keys = [(r['model'], r['account'], r['route'], r['control']) for r in rows]
    assert len(keys) == len(set(keys)) == 504 and set(keys) == expected, 'incomplete matrix'
    failures, rejections = [], []
    for row, key in zip(rows, keys):
        model, account, route, control = key
        if row.get('error'):
            allowed = (control in ['named-tool', 'required-tool'] and model in MODELS[3:]) or (
                control == 'max64-none' and model in ['claude-opus-5-5', 'claude-haiku-5-5'] and route != '/v1/messages') or (
                model == 'claude-haiku-4-5-20251001' and control in ['max64-high', 'max1024-high'] and route != '/v1/messages')
            assert allowed and row['status'] == 400 and row['calls'] == 0, row
            rejections.append(key)
            continue
        assert row['calls'] == 1 and row['status'] == 200, row
        fields = row['fields']
        cap_key = 'max_output_tokens' if route == '/v1/responses' else 'max_tokens'
        assert fields['max_tokens'] == row['input'][cap_key], row
        if control == 'strict-parallel-schema':
            if route == '/v1/messages':
                schema = row['input']['output_config']['format']['schema']
            elif route == '/v1/responses':
                schema = row['input']['text']['format']['schema']
            else:
                schema = row['input']['response_format']['json_schema']['schema']
            assert fields['output_config']['format']['schema'] == schema, row
        if route == '/v1/messages':
            if control in ['parallel-false-auto', 'parallel-false-default', 'strict-parallel-schema']:
                assert fields['tool_choice']['disable_parallel_tool_use'] is True, row
            if control in ['strict-true', 'strict-parallel-schema']:
                assert fields['tools'][0]['strict'] is True, row
        reasons = []
        if route != '/v1/messages':
            choice = fields.get('tool_choice') or {}
            if control in ['parallel-false-auto', 'parallel-false-default', 'strict-parallel-schema'] and choice.get('disable_parallel_tool_use') is not True:
                reasons.append('parallel_false_lost')
            if control == 'parallel-true-auto' and choice.get('disable_parallel_tool_use') is not False:
                reasons.append('parallel_true_lost')
            if control in ['strict-true', 'strict-parallel-schema'] and fields['tools'][0].get('strict') is not True:
                reasons.append('strict_true_lost')
            thinking = fields.get('thinking') or {}
            # 1,025/4,096 avoid the original CLI's independent 1,024 budget floor
            # at smaller caps; do not attribute that native boundary to the gateway.
            if control in ['max64-high', 'max1024-high', 'max1025-high', 'max4096-high'] and model in MODELS[:3] and (
                (model in MODELS[:2] and thinking.get('type') != 'adaptive') or
                (model == MODELS[2] and (thinking.get('type') != 'enabled' or not 1024 <= thinking.get('budget_tokens', 0) < fields['max_tokens']))):
                reasons.append('generated_budget_exceeds_cap')
            if control == 'max64-none' and model in MODELS[:3] and (thinking.get('type') != 'disabled' or thinking.get('budget_tokens') or fields['output_config'] and fields['output_config'].get('effort') == 'none'):
                reasons.append('none_enables_thinking')
            if control in ['strict-true', 'strict-parallel-schema'] and 'structured-outputs-2025-12-15' not in row.get('beta', '').split(','):
                reasons.append('strict_capability_missing')
        if reasons:
            failures.append({'key': key, 'reasons': reasons, 'fields': fields})
    counts = {reason: sum(reason in row['reasons'] for row in failures) for reason in
              ['parallel_false_lost', 'parallel_true_lost', 'strict_true_lost', 'strict_capability_missing', 'generated_budget_exceeds_cap', 'none_enables_thinking']}
    result = {'cases': len(rows), 'explicit_rejections': len(rejections),
              'failing_cases': len(failures), 'failure_counts': counts,
              'contract_passed': not failures, 'failures': failures}
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({k: v for k, v in result.items() if k != 'failures'}))
    return 1 if failures else 0

if __name__ == '__main__':
    raise SystemExit(main())
