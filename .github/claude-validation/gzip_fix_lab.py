"""Independent native compression vectors, including warmed block reuse."""
import post_alignment_lab as edge

lab = edge.lab
lab.CASES = [c for c in lab.CASES if c['name'].startswith('gzip-')]
for mode in [1, 2]:
    for level in [1, 6, 9]:
        lab.CASES.append({
            'name': f'gzip-blocks{mode}-multi-level{level}', 'base': 'multi-turn',
            'system_chars': 600000, 'turn_pause_seconds': 2,
            'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1',
                    'CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS': str(mode),
                    'CLAUDE_CODE_GZIP_REQUEST_BODY_LEVEL': str(level)},
        })
if __name__ == '__main__':
    lab.launch = edge.launch
    lab.main()
