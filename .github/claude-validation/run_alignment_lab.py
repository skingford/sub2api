"""Run the production-service/native-CLI lab only inside network-none Docker."""
import hashlib
import os
from pathlib import Path
import subprocess
import sys

import lab

root = Path('/work')
os.umask(0o077)
lab.write_json(root / 'isolation.json', lab.verify_isolation())
digest = hashlib.sha256(Path('/opt/claude').read_bytes()).hexdigest()
assert digest == 'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3'
env = {'PATH': os.environ['PATH'], 'CLAUDE_RECOVERY_NATIVE_CLI': '/opt/claude',
       'CLAUDE_RECOVERY_LAB_OUTPUT': str(root), 'GOTOOLCHAIN': 'local'}
with (root / 'native-service.log').open('w') as log:
    result = subprocess.run(['/opt/service-alignment.test', '-test.v',
                             '-test.run=^TestClaudeAlignmentNativeCLILab$', '-test.timeout=5m'],
                            cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT)
print((root / 'native-service.log').read_text())
sys.exit(result.returncode)
