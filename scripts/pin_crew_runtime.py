"""Pin a published CLI release for marketplace distribution (no system install).

Usage: python scripts/pin_crew_runtime.py v0.2.0 /path/to/checksums.txt
Manual recovery helper. Normal releases use prepare_crew_release.py and open
a source-update PR automatically.
"""
import argparse
import json
from pathlib import Path
import re


def make_lock(version, checksums):
    if not re.fullmatch(r'v\d+\.\d+\.\d+', version):
        raise ValueError('Use an exact stable version, for example v0.2.0')
    artifacts = {}
    for line in checksums.splitlines():
        match = re.fullmatch(r'([a-f0-9]{64})\s+\*?(outrider_[A-Za-z0-9_.-]+)', line)
        if not match:
            continue
        digest, name = match.groups()
        for system in ('darwin', 'linux', 'windows'):
            for arch in ('amd64', 'arm64'):
                extension = 'zip' if system == 'windows' else 'tar.gz'
                if name == f'outrider_{version[1:]}_{system}_{arch}.{extension}':
                    artifacts[f'{system}_{arch}'] = {'name': name, 'sha256': digest}
    if len(artifacts) != 6:
        raise ValueError('Checksums must include all six macOS/Linux/Windows AMD64/ARM64 runtime archives')
    return {'version': version, 'artifacts': artifacts}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('checksums', type=Path)
    args = parser.parse_args()
    result = make_lock(args.version, args.checksums.read_text())
    path = Path(__file__).resolve().parents[1] / 'apps/kiro-crew/release/runtime.json'
    path.write_text(json.dumps(result, indent=2) + '\n')
    print(f'Pinned {result["version"]} for {len(result["artifacts"])} platforms in {path}')
