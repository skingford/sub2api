"""Compare ordinary pinned CLI prompt/tool modes without modifying its binary.

Run only through ram_capture.py in a network-none container. OAuth credentials,
MCP responses and model replies are synthetic; no subscription claim is tested.
Unlike EXTRA_BODY probes, these cases use public CLI options and a local MCP.
"""
import json
import sys


INSTRUCTION = "LOCAL_ALIGNMENT_INSTRUCTION: preserve this instruction as system text."
TOOL_NAMES = ["sessions_list", "session_open", "read", "write", "search", "close"]


def mcp_server():
    for line in sys.stdin:
        request = json.loads(line)
        if "id" not in request:
            continue
        method = request.get("method")
        if method == "initialize":
            result = {"protocolVersion": request["params"]["protocolVersion"],
                      "capabilities": {"tools": {}},
                      "serverInfo": {"name": "local-alignment", "version": "1"}}
        elif method == "tools/list":
            result = {"tools": [{"name": name, "description": "Local inert test tool " + name,
                                 "inputSchema": {"type": "object", "properties": {}}}
                                for name in TOOL_NAMES]}
        elif method == "tools/call":
            result = {"content": [{"type": "text", "text": "LOCAL_MCP_RESULT"}]}
        else:
            result = {}
        print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)


def main():
    import comprehensive_lab as lab
    import validation
    lab.CASES = [
        {"name": "oauth-default-no-tools", "base": "oauth-arg", "system_mode": "default"},
        {"name": "oauth-default-tools", "base": "oauth-arg", "system_mode": "default", "default_tools": True},
        {"name": "oauth-custom-system", "base": "oauth-arg", "system_mode": "replace"},
        {"name": "oauth-append-system", "base": "oauth-arg", "system_mode": "append"},
        {"name": "oauth-custom-mcp", "base": "oauth-arg", "system_mode": "replace", "mcp": True},
        {"name": "oauth-default-count", "base": "oauth-count-context", "system_mode": "default"},
        {"name": "oauth-custom-count", "base": "oauth-count-context", "system_mode": "replace"},
        {"name": "oauth-default-multi-turn", "base": "multi-turn", "system_mode": "default"},
        {"name": "oauth-default-resume", "base": "resume", "system_mode": "default"},
        {"name": "oauth-default-retry503", "base": "oauth-arg", "system_mode": "default", "reject": 503,
         "reply_headers": {"retry-after-ms": "1"}, "env": {"CLAUDE_CODE_MAX_RETRIES": "1"}},
    ]
    original_launch = lab.launch

    def launch(argv, *args, **kwargs):
        if argv and argv[0] == "/opt/claude":
            current = lab.CURRENT
            env = kwargs["env"]
            env.pop("ANTHROPIC_API_KEY", None)
            env["CLAUDE_CODE_OAUTH_TOKEN"] = validation.TOKEN
            if "--bare" in argv:
                argv.remove("--bare")
            at = argv.index("--system-prompt")
            del argv[at:at + 2]
            mode = current["system_mode"]
            if mode != "default":
                at = argv.index("--") if "--" in argv else len(argv)
                argv[at:at] = ["--system-prompt" if mode == "replace" else "--append-system-prompt", INSTRUCTION]
            if current.get("default_tools"):
                at = argv.index("--tools")
                del argv[at:at + 2]
            if current.get("mcp"):
                argv[argv.index("--mcp-config") + 1] = json.dumps({"mcpServers": {"alignment": {
                    "command": "python3", "args": [__file__, "--mcp-server"]}}})
        return original_launch(argv, *args, **kwargs)

    lab.launch = launch
    lab.main()
    # The base harness calls the multi-turn/resume fixture an API-key case.
    # Record the actual environment choice made above without changing captures.
    for folder in lab.ROOT.iterdir():
        invocation = folder / "invocation.json"
        if invocation.exists():
            data = json.loads(invocation.read_text())
            data["synthetic_auth"] = "oauth-env"
            data["binary_modified"] = False
            data["cli_arguments_wrapped"] = True
            invocation.write_text(json.dumps(data, indent=2) + "\n")


if __name__ == "__main__":
    if sys.argv[1:] == ["--mcp-server"]:
        mcp_server()
    else:
        main()
