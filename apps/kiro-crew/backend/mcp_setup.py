"""App-owned setup tools; the portable Outrider MCP server knows nothing about them."""
import json
from urllib.error import HTTPError, URLError
from urllib.request import Request, build_opener, ProxyHandler

from server import NoRedirect


def tool(name, description, properties, required=(), write=False):
    return {'name': name, 'description': description,
            'inputSchema': {'type': 'object', 'properties': properties, 'required': list(required), 'additionalProperties': False},
            'annotations': {'readOnlyHint': not write, 'destructiveHint': False, 'idempotentHint': True, 'openWorldHint': write}}


STRING = {'type': 'string', 'minLength': 1}
TOOLS = [
    tool('list_crew_folders', 'List Crew folders and project directories. Match the current project; ask when ambiguous.', {}),
    tool('inspect_crew_folder', 'Resolve a Crew folder’s inherited checkout and GitHub remotes before adding it.', {'folder_id': STRING}, ['folder_id']),
    tool('add_repository', 'Register a repository when the user asks. Uses an existing Crew folder and matching Git remote; prefers upstream then origin. Preserves existing registrations and settings. Inherits polling and automatic-session defaults, so reviews may start. Never changes trust or permissions.', {'repo': STRING, 'folder_id': STRING, 'remote': STRING}, ['repo', 'folder_id'], write=True),
]
ROUTES = {'list_crew_folders': '/mcp/folders', 'inspect_crew_folder': '/mcp/checkout', 'add_repository': '/mcp/repositories'}


def call(data_dir, name, arguments):
    definition = next(t for t in TOOLS if t['name'] == name)
    schema = definition['inputSchema']
    if not isinstance(arguments, dict) or set(arguments) - set(schema['properties']) or not set(schema['required']) <= set(arguments) or any(not isinstance(v, str) or not v or len(v) > 1000 for v in arguments.values()):
        raise ValueError('Invalid setup arguments; use the tool’s declared fields')
    try:
        control = json.loads((data_dir / 'mcp-control.json').read_text(encoding='utf-8'))
    except (OSError, ValueError) as error:
        raise RuntimeError('Enable Outrider in Crew before using setup tools') from error
    port, token = control.get('port'), control.get('token')
    if type(port) is not int or not 1 <= port <= 65535 or not isinstance(token, str) or len(token) < 32:
        raise ValueError('Invalid local setup connection')
    body = None if name == 'list_crew_folders' else json.dumps(arguments).encode()
    request = Request(f'http://127.0.0.1:{port}' + ROUTES[name], data=body,
                      headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    try:
        # Never use host proxies, follow redirects or automatically retry writes.
        with build_opener(ProxyHandler({}), NoRedirect()).open(request, timeout=30) as response:
            raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('Setup response too large')
            return json.loads(raw)
    except HTTPError as error:
        try:
            detail = json.loads(error.read(8192)).get('error', '')
        except ValueError:
            detail = ''
        finally:
            error.close()
        raise RuntimeError(f'Setup request rejected (HTTP {error.code}): {detail}') from error
    except (URLError, TimeoutError) as error:
        raise RuntimeError('Setup backend unavailable; check registered repositories before retrying an add') from error
