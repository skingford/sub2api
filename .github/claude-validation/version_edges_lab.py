"""Observe aliases and Haiku 5.5 parameters using the pinned, unmodified CLI.

Select 2.1.295 with CLAUDE_LAB_CLI_VERSION and mount its verified binary at
/opt/claude. The default remains 2.1.292. All responses are local synthetic data.
"""
import comprehensive_lab as lab

lab.CASES = []
for model in ['haiku', 'sonnet', 'opus', 'claude-haiku-5-5',
              'claude-haiku-4-5-20251001', 'sonnet[1m]']:
    lab.CASES.append({'name': 'model-' + model.replace('[', '-').replace(']', ''),
                      'base': 'api-arg', 'model': model, 'generic_controls': {}})
lab.CASES += [
    {'name': 'haiku55-thinking-off', 'base': 'api-arg', 'model': 'claude-haiku-5-5',
     'env': {'MAX_THINKING_TOKENS': '0'},
     'generic_controls': {'thinking': {'type': 'disabled'}}},
    {'name': 'haiku55-effort-xhigh', 'base': 'api-arg', 'model': 'claude-haiku-5-5',
     'args': ['--effort', 'xhigh'], 'generic_controls': {'output_config': {'effort': 'xhigh'}}},
    {'name': 'haiku55-temperature', 'base': 'api-arg', 'model': 'claude-haiku-5-5',
     'env': {'CLAUDE_CODE_EXTRA_BODY': '{"temperature":0.4}'},
     'generic_controls': {'temperature': 0.4}},
    {'name': 'native-version-custom-ua', 'base': 'api-arg',
     'env': {'ANTHROPIC_CUSTOM_HEADERS': 'User-Agent: claude-cli/2.1.292 (external, cli)'}},
]

if __name__ == '__main__':
    lab.main()
