"""Read-only stdio MCP interface to the installed Outrider app."""
import json
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from server import SETUP_SCHEMA, convert_settings, normalize_repo, read_json  # noqa: E402

DATA = Path(os.environ.get("KIROCREW_HOME", Path.home() / ".kiro/crew")) / "apps/outrider/data"
TOOLS = [
    {"name": "list_repositories", "description": "Read watched repositories and their Outrider settings.", "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False}},
    {"name": "configuration_schema", "description": "Read supported per-repository configuration fields and workflow constraints.", "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False}},
    {"name": "list_reviews", "description": "List up to 100 latest snapshots and review status, without raw PR content.", "inputSchema": {"type": "object", "properties": {"repo": {"type": "string"}}, "additionalProperties": False}},
    {"name": "propose_repository_config", "description": "Validate repository YAML and return a draft. Does NOT apply settings. Show the YAML to the user to save in Outrider.", "inputSchema": {"type": "object", "properties": {"repo": {"type": "string"}, "yaml": {"type": "string"}}, "required": ["repo", "yaml"], "additionalProperties": False}},
]
for tool in TOOLS:
    tool["annotations"] = {"readOnlyHint": True, "destructiveHint": False, "idempotentHint": True, "openWorldHint": False}


def call_tool(name, arguments):
    if not isinstance(arguments, dict):
        raise ValueError("Tool arguments must be an object")
    if name == "configuration_schema":
        return SETUP_SCHEMA
    if name == "list_repositories":
        return read_json(DATA / "settings.json", {"repos": [], "repo_settings": {}})
    if name == "propose_repository_config":
        repo = normalize_repo(arguments.get("repo"))
        content = arguments.get("yaml")
        if not isinstance(content, str) or len(content) > 65536:
            raise ValueError("Provide at most 64 KiB of YAML")
        return {"repo": repo, "applied": False, **convert_settings(content)}
    if name == "list_reviews":
        repo = normalize_repo(arguments["repo"]) if arguments.get("repo") else None
        runs = read_json(DATA / "runs.json", {})
        items = [read_json(path, {}) for path in (DATA / "inbox/queue").glob("*.json")]
        items = sorted((item for item in items if not repo or item.get("repo") == repo), key=lambda item: item.get("created", ""), reverse=True)[:100]
        return [{k: item.get(k) for k in ("id", "repo", "pr", "title", "url", "head", "created", "workflow")} | {"status": runs.get(item["id"], {}).get("status", "pending")} for item in items]
    raise ValueError("Unknown tool")


def handle(request):
    if not isinstance(request, dict) or request.get("jsonrpc") != "2.0":
        return {"jsonrpc": "2.0", "id": None, "error": {"code": -32600, "message": "Invalid request"}}
    if "id" not in request:
        return None
    response = {"jsonrpc": "2.0", "id": request["id"]}
    method = request.get("method")
    params = request.get("params") or {}
    if not isinstance(params, dict):
        return response | {"error": {"code": -32602, "message": "Invalid params"}}
    if method == "initialize":
        version = params.get("protocolVersion")
        result = {"protocolVersion": version if version in {"2024-11-05", "2025-03-26", "2025-06-18"} else "2024-11-05", "capabilities": {"tools": {}}, "serverInfo": {"name": "outrider", "version": "0.1.0"}, "instructions": "Outrider tools read state or validate drafts. Apply changes only through the Outrider settings UI."}
    elif method == "ping":
        result = {}
    elif method == "tools/list":
        result = {"tools": TOOLS}
    elif method == "tools/call":
        try:
            value = call_tool(params.get("name"), params.get("arguments", {}))
            result = {"content": [{"type": "text", "text": value if isinstance(value, str) else json.dumps(value)}]}
        except Exception as error:
            result = {"isError": True, "content": [{"type": "text", "text": str(error)}]}
    else:
        return response | {"error": {"code": -32601, "message": "Method not found"}}
    return response | {"result": result}


def main():
    while True:
        line = sys.stdin.buffer.readline(131073)
        if not line:
            break
        if len(line) > 131072:
            break
        try:
            response = handle(json.loads(line))
        except (ValueError, TypeError):
            response = {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "Parse error"}}
        if response is not None:
            print(json.dumps(response), flush=True)


if __name__ == "__main__":
    main()
