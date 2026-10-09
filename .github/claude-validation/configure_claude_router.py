#!/usr/bin/env python3
"""Configure a Claude CLI user for the tested Bearer-authenticated router.

Interactive: python3 configure_claude_router.py
Automation:  python3 configure_claude_router.py --key-file /private/key
Never pass the secret as a command-line argument.
"""
import argparse
from datetime import datetime, timezone
import getpass
import json
import os
from pathlib import Path
import shutil
import tempfile
from urllib.parse import urlsplit


def read_object(path):
    value = json.loads(path.read_text()) if path.exists() else {}
    if not isinstance(value, dict):
        raise ValueError(f'{path} must contain a JSON object')
    return value


def save_private(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix=path.name + '.', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write('\n')
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def configure(home, base_url, key):
    url = urlsplit(base_url)
    if url.scheme != 'https' or not url.hostname or url.username or url.password or url.query or url.fragment:
        raise ValueError('Use an HTTPS base URL without credentials, query or fragment')
    if not key or any(char.isspace() for char in key):
        raise ValueError('API Key must be nonempty and contain no whitespace')
    folder = home / '.claude'
    settings_path, state_path = folder / 'settings.json', home / '.claude.json'
    settings, state = read_object(settings_path), read_object(state_path)
    environment = settings.setdefault('env', {})
    if not isinstance(environment, dict):
        raise ValueError('settings.json env must be a JSON object')
    folder.mkdir(mode=0o700, exist_ok=True)
    backup_root = folder / 'config-backups'
    backup_root.mkdir(mode=0o700, exist_ok=True)
    backup = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-'), dir=backup_root))
    for path in [settings_path, state_path]:
        if path.exists():
            destination = backup / path.name
            shutil.copy2(path, destination)
            destination.chmod(0o600)
    environment.pop('ANTHROPIC_API_KEY', None)
    environment['ANTHROPIC_BASE_URL'] = base_url.rstrip('/')
    environment['ANTHROPIC_AUTH_TOKEN'] = key
    state['hasCompletedOnboarding'] = True
    save_private(settings_path, settings)
    save_private(state_path, state)
    return backup


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='https://anyrouter.top')
    parser.add_argument('--key-file', type=Path, help='Read a private credential file instead of prompting')
    args = parser.parse_args()
    os.umask(0o077)
    if args.key_file:
        key = args.key_file.read_text().strip()
    else:
        key = getpass.getpass('API Key（隐藏输入）: ').strip()
    backup = configure(Path.home(), args.base_url, key)
    print('已配置请求地址和 Bearer 凭据，并跳过登录引导。')
    print(f'配置：{Path.home() / ".claude/settings.json"}（权限 600）')
    print(f'备份：{backup}')
    print('验证：claude auth status')


if __name__ == '__main__':
    main()
