"""One real TTY invocation of a pinned, unmodified CLI in network-none Docker.

Uses an isolated pre-onboarded test home and a synthetic OAuth environment token.
This observes interactive request construction, not a real subscription login.
"""
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import ssl
import struct
import subprocess
import termios
import threading
import time

import comprehensive_lab as suite
import extended_audit as audit
import lab
import validation


def main():
    root = Path("/work")
    os.umask(0o077)
    version = os.environ["CLAUDE_LAB_CLI_VERSION"]
    digest = hashlib.sha256(Path("/opt/claude").read_bytes()).hexdigest()
    assert digest == suite.CLI_PINS[version]
    lab.write_json(root / "isolation.json", lab.verify_isolation())
    mode = os.environ.get("CLAUDE_ALIGNMENT_SYSTEM_MODE", "default")
    assert mode in ("default", "replace", "empty", "append")
    folder = root / ("interactive-" + mode)
    folder.mkdir()
    for name in ("home", "config"):
        (folder / name).mkdir()
    state = {"hasCompletedOnboarding": True, "lastOnboardingVersion": version, "theme": "dark",
             "projects": {str(folder): {"hasTrustDialogAccepted": True}}}
    for path in [folder / "home/.claude.json", folder / "config/.claude.json"]:
        lab.write_json(path, state)
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-sha256", "-nodes",
                    "-keyout", str(root / "server.key"), "-out", str(root / "server.pem"), "-days", "2",
                    "-subj", "/CN=api.anthropic.com", "-addext", "subjectAltName=DNS:api.anthropic.com"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    audit.STATE.update(name="interactive-default", folder=folder, records=[], responses=[], messages=0)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(str(root / "server.pem"), str(root / "server.key"))
    ctx.set_alpn_protocols(["http/1.1"])
    ctx.keylog_filename = str(folder / "server-reference.keys")
    server = lab.ThreadingHTTPServer(("127.0.0.1", 443), suite.Handler)
    server.daemon_threads = True
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    capture, capture_log = lab.start_capture(folder)
    env = {"PATH": os.environ["PATH"], "HOME": str(folder / "home"), "LANG": "C.UTF-8", "TERM": "xterm-256color",
           "ANTHROPIC_BASE_URL": "https://api.anthropic.com", "CLAUDE_CODE_OAUTH_TOKEN": validation.TOKEN,
           "CLAUDE_CONFIG_DIR": str(folder / "config"), "NODE_EXTRA_CA_CERTS": str(root / "server.pem"),
           "DISABLE_AUTOUPDATER": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1",
           "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
           "CLAUDE_CODE_ENABLE_TELEMETRY": "0", "API_TIMEOUT_MS": "5000"}
    argv = ["/opt/claude", "--model", "claude-sonnet-4-6", "--tools", "", "--setting-sources", "",
            "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--permission-mode", "default",
            "--disable-slash-commands", "--", "LOCAL_INTERACTIVE_ALIGNMENT: Reply OK."]
    if mode != "default":
        from caller_cli_alignment_lab import INSTRUCTION
        at = argv.index("--")
        argv[at:at] = ["--append-system-prompt" if mode == "append" else "--system-prompt", "" if mode == "empty" else INSTRUCTION]
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
    process = subprocess.Popen(argv, cwd=folder, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    transcript = bytearray()
    first_response = None
    try:
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and process.poll() is None:
            if select.select([master], [], [], .2)[0]:
                try:
                    transcript.extend(os.read(master, 65536))
                except OSError:
                    break
            if audit.STATE["messages"] and first_response is None:
                first_response = time.monotonic()
            if first_response and time.monotonic() - first_response > 2:
                break
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
        os.close(master)
        server.shutdown()
        server.server_close()
        capture.send_signal(signal.SIGINT)
        _, tail = capture.communicate(timeout=8)
        capture_log.write(tail)
        capture_log.close()
    (folder / "terminal.bin").write_bytes(transcript)
    lab.write_json(folder / "requests.json", audit.STATE["records"])
    lab.write_json(folder / "responses.json", audit.STATE["responses"])
    lab.write_json(folder / "invocation.json", {"argv": argv, "synthetic_auth": "oauth-env", "tty": True,
                                              "binary_modified": False, "preconfigured_onboarding_and_workspace_trust": True})
    result = {"cli_version": version, "binary_sha256": digest, "model_requests": audit.STATE["messages"],
              "exit_code": process.returncode, "controlled_stop_after_response": first_response is not None}
    lab.write_json(root / "summary.json", result)
    print(json.dumps(result), flush=True)


if __name__ == "__main__":
    main()
