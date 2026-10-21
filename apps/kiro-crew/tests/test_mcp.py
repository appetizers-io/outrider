import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('outrider_mcp_launcher', Path(__file__).resolve().parents[1] / 'backend/mcp_launcher.py')
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


class MCPLauncherTest(unittest.TestCase):
    def test_launcher_selects_only_its_installed_instance(self):
        with patch.object(launcher, 'find_scanner', return_value='/bin/outrider'), patch.object(launcher, 'scanner_environment', return_value={'PATH': '/bin', 'OUTRIDER_CONFIG': '/host/config.yaml'}), patch.object(launcher.os, 'execve') as execute:
            launcher.launch()
        binary, args, env = execute.call_args.args
        data = Path(launcher.__file__).resolve().parents[1] / 'data'
        self.assertEqual(binary, '/bin/outrider')
        self.assertEqual(args, [binary, 'mcp', '--config-format', 'crew', '--config', str(data / 'settings.json'), '--state-dir', str(data)])
        self.assertEqual(env['OUTRIDER_CONFIG'], '/host/config.yaml')
