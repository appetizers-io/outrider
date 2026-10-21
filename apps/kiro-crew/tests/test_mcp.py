import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('outrider_mcp', Path(__file__).resolve().parents[1] / 'backend/mcp_server.py')
mcp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mcp)


class MCPTest(unittest.TestCase):
    def request(self, method, params=None):
        return mcp.handle({'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params or {}})

    def test_handshake_and_only_readonly_tools(self):
        result = self.request('initialize', {'protocolVersion': '2025-03-26'})['result']
        self.assertEqual(result['protocolVersion'], '2025-03-26')
        tools = self.request('tools/list')['result']['tools']
        self.assertTrue(all(tool['annotations']['readOnlyHint'] for tool in tools))
        self.assertIsNone(mcp.handle({'jsonrpc': '2.0', 'method': 'notifications/initialized'}))
        self.assertEqual(self.request('unknown')['error']['code'], -32601)

    def test_proposal_does_not_write_and_invalid_config_is_error(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(mcp, 'DATA', Path(directory)):
            with patch.object(mcp, 'convert_settings', return_value={'yaml': '{}', 'settings': {}}):
                result = self.request('tools/call', {'name': 'propose_repository_config', 'arguments': {'repo': 'o/r', 'yaml': '{}'}})
                self.assertFalse(json.loads(result['result']['content'][0]['text'])['applied'])
            with patch.object(mcp, 'convert_settings', side_effect=ValueError('Invalid scope')):
                result = self.request('tools/call', {'name': 'propose_repository_config', 'arguments': {'repo': 'o/r', 'yaml': 'bad'}})
                self.assertTrue(result['result']['isError'])
            self.assertEqual(list(Path(directory).iterdir()), [])
