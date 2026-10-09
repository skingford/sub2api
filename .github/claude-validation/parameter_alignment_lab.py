"""Distinguish CLI controls from late extra-body overrides in network-none Docker."""
import comprehensive_lab as lab

lab.CASES = []
for model in lab.MODELS:
    disabled = {"type": "disabled", "display": "updates", "budget_tokens": 2048}
    variants = [
        ('thinking-off', {'MAX_THINKING_TOKENS': '0'}, {'thinking': {'type': 'disabled'}}),
        ('disabled-extra', {'CLAUDE_CODE_EXTRA_BODY': lab.json.dumps({'thinking': disabled})},
         {'thinking': disabled}),
        ('disabled-control-extra', {'MAX_THINKING_TOKENS': '0', 'CLAUDE_CODE_EXTRA_BODY': lab.json.dumps({'thinking': disabled})},
         {'thinking': disabled}),
        ('temperature', {'CLAUDE_CODE_EXTRA_BODY': '{"temperature":0.4}'}, {'temperature': 0.4}),
    ]
    for name, env, controls in variants:
        lab.CASES.append({'name': model + '-' + name, 'base': 'api-arg', 'model': model,
                          'env': env, 'generic_controls': controls})

if __name__ == '__main__':
    lab.main()
