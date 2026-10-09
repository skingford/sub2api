"""New native branches after the parameter/gzip fixes; network-none Docker only."""
from pathlib import Path

import comprehensive_lab as lab

lab.CASES = [
    {'name': 'gzip-level1', 'base': 'api-arg', 'long_system': True,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1', 'CLAUDE_CODE_GZIP_REQUEST_BODY_LEVEL': '1'}},
    {'name': 'gzip-level9', 'base': 'api-arg', 'long_system': True,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1', 'CLAUDE_CODE_GZIP_REQUEST_BODY_LEVEL': '9'}},
    {'name': 'gzip-blocks1', 'base': 'api-arg', 'system_chars': 600000,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1', 'CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS': '1'}},
    {'name': 'gzip-blocks2', 'base': 'api-arg', 'system_chars': 600000,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1', 'CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS': '2'}},
    {'name': 'gzip-unicode', 'base': 'api-arg', 'system_chars': 40000, 'unicode_system': True,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1'}},
    {'name': 'gzip-count', 'base': 'count-context', 'system_chars': 30000,
     'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1'}},
    {'name': 'top-p-extra', 'base': 'api-arg', 'env': {'CLAUDE_CODE_EXTRA_BODY': '{"top_p":0.8}'},
     'generic_controls': {'top_p': 0.8}},
    {'name': 'top-k-extra', 'base': 'api-arg', 'env': {'CLAUDE_CODE_EXTRA_BODY': '{"top_k":20}'},
     'generic_controls': {'top_k': 20}},
]
original_launch = lab.launch


def launch(argv, *args, **kwargs):
    if argv and argv[0] == '/opt/claude' and lab.CURRENT.get('system_chars'):
        size = lab.CURRENT['system_chars']
        unit = '本地合成协议测试。😀🧪 ' if lab.CURRENT.get('unicode_system') else 'Local synthetic protocol fixture. '
        content = (unit * (size // len(unit) + 1))[:size]
        path = Path(kwargs['cwd']) / 'system-fixture.txt'
        path.write_text(content)
        at = argv.index('--system-prompt')
        argv[at:at + 2] = ['--system-prompt-file', str(path)]
    return original_launch(argv, *args, **kwargs)


if __name__ == '__main__':
    lab.launch = launch
    lab.main()
