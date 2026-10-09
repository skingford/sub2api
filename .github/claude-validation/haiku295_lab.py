"""Capture the 2.1.295 Haiku 5.5 API/OAuth and explicit-control branches."""
import os
import comprehensive_lab as lab

assert os.environ.get('CLAUDE_LAB_CLI_VERSION') == '2.1.295'
lab.CASES = [
    {'name': 'haiku55-' + mode, 'base': mode, 'model': 'claude-haiku-5-5', 'generic_controls': {}}
    for mode in ['api-arg', 'oauth-arg', 'oauth-stream']
]
for name, controls, env, args in [
    ('temperature', {'temperature': 0.4}, {'CLAUDE_CODE_EXTRA_BODY': '{"temperature":0.4}'}, []),
    ('effort-low', {'output_config': {'effort': 'low'}}, {}, ['--effort', 'low']),
    ('effort-xhigh', {'output_config': {'effort': 'xhigh'}}, {}, ['--effort', 'xhigh']),
    ('max4096', {'max_tokens': 4096}, {'CLAUDE_CODE_MAX_OUTPUT_TOKENS': '4096'}, []),
]:
    lab.CASES.append({'name': 'haiku55-' + name, 'base': 'oauth-arg', 'model': 'claude-haiku-5-5',
                      'generic_controls': controls, 'env': env, 'args': args})
for name in ['compact', 'multi-turn', 'count-context', 'oauth-count-context']:
    lab.CASES.append({'name': 'haiku55-' + name, 'base': name, 'model': 'claude-haiku-5-5'})

if __name__ == '__main__':
    lab.main()
