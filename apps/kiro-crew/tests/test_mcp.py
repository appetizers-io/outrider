import importlib.util
import io
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location('outrider_mcp_launcher', Path(__file__).resolve().parents[1] / 'backend/mcp_launcher.py')
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


class MCPAdapterTest(unittest.TestCase):
    def test_tools_are_owned_by_app_and_other_traffic_is_forwarded(self):
        upstream, output = io.StringIO(), io.StringIO()
        adapter = launcher.Adapter(Path('/instance/data'), upstream, output)
        request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/list'}
        adapter.receive(request)
        self.assertEqual(json.loads(upstream.getvalue()), request)
        adapter.relay({'jsonrpc': '2.0', 'id': 1, 'result': {'tools': [{'name': 'list_repositories'}]}})
        tools = json.loads(output.getvalue())['result']['tools']
        self.assertEqual([t['name'] for t in tools], ['list_repositories', 'list_crew_folders', 'inspect_crew_folder', 'add_repository'])
        self.assertFalse(tools[-1]['annotations']['readOnlyHint'])
        previous = upstream.getvalue()
        with patch.object(launcher.mcp_setup, 'call', return_value={'added': True}) as call:
            adapter.receive({'jsonrpc': '2.0', 'id': 2, 'method': 'tools/call', 'params': {'name': 'add_repository', 'arguments': {'repo': 'o/r', 'folder_id': 'f'}}})
            call.assert_called_once_with(Path('/instance/data'), 'add_repository', {'repo': 'o/r', 'folder_id': 'f'})
        self.assertEqual(upstream.getvalue(), previous)
        self.assertIn('added', json.loads(output.getvalue().splitlines()[-1])['result']['content'][0]['text'])
        with patch.object(launcher.mcp_setup, 'call', side_effect=RuntimeError('backend unavailable')):
            adapter.receive({'id': 3, 'method': 'tools/call', 'params': {'name': 'list_crew_folders'}})
        self.assertTrue(json.loads(output.getvalue().splitlines()[-1])['result']['isError'])

    def test_malformed_stdio_does_not_stop_later_messages(self):
        upstream, output = io.StringIO(), io.StringIO()
        adapter = launcher.Adapter(Path('/instance/data'), upstream, output)
        adapter.receive_line('not json')
        self.assertEqual(json.loads(output.getvalue())['error']['code'], -32700)
        adapter.receive_line('[]')
        self.assertEqual(json.loads(output.getvalue().splitlines()[-1])['error']['code'], -32600)
        request = {'jsonrpc': '2.0', 'id': 9, 'method': 'tools/list'}
        adapter.receive_line(json.dumps(request))
        self.assertEqual(json.loads(upstream.getvalue()), request)
        with patch.object(launcher.sys, 'stderr', io.StringIO()) as errors:
            adapter.relay_line('secret malformed text')
            adapter.relay_line('[]')
        self.assertNotIn('secret', errors.getvalue())
        adapter.relay_line(json.dumps({'jsonrpc': '2.0', 'id': 9, 'result': {'tools': []}}))
        reply = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual(reply['id'], 9)
        self.assertEqual(len(reply['result']['tools']), 3)

    def test_app_client_authenticates_and_never_follows_redirects(self):
        calls = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass
            def do_GET(self):
                calls.append((self.path, self.headers.get('Authorization')))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'[{"id":"folder"}]')
            def do_POST(self):
                calls.append((self.path, self.headers.get('Authorization')))
                self.send_response(307)
                self.send_header('Location', '/must-not-follow')
                self.end_headers()
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with tempfile.TemporaryDirectory() as temporary:
            data = Path(temporary)
            token = 'x' * 32
            (data / 'mcp-control.json').write_text(json.dumps({'port': server.server_port, 'token': token}))
            self.assertEqual(launcher.mcp_setup.call(data, 'list_crew_folders', {}), [{'id': 'folder'}])
            with self.assertRaises(RuntimeError):
                launcher.mcp_setup.call(data, 'add_repository', {'repo': 'o/r', 'folder_id': 'f'})
            self.assertEqual(calls, [('/mcp/folders', 'Bearer ' + token), ('/mcp/repositories', 'Bearer ' + token)])
            with self.assertRaises(ValueError):
                launcher.mcp_setup.call(data, 'add_repository', {'repo': 'o/r', 'folder_id': 'f', 'trust': 'trust'})
            self.assertEqual(len(calls), 2)
