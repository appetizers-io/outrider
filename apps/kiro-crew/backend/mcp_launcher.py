"""Crew adapter: launch the portable Outrider MCP server with explicit instance paths."""
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from server import find_scanner, scanner_environment  # noqa: E402


def launch():
    data = Path(__file__).resolve().parents[1] / 'data'
    binary = find_scanner()
    os.execve(binary, [binary, 'mcp', '--config-format', 'crew', '--config', str(data / 'settings.json'), '--state-dir', str(data)], scanner_environment())


if __name__ == '__main__':
    launch()
