"""Stage a versioned Crew app after verifying published CLI downloads."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import re
import subprocess
import tempfile

from pin_crew_runtime import make_lock

ROOT = Path(__file__).resolve().parents[1]


def verify_archives(lock, directory):
    for artifact in lock['artifacts'].values():
        path = directory / artifact['name']
        if hashlib.sha256(path.read_bytes()).hexdigest() != artifact['sha256']:
            raise ValueError(f'Published archive checksum mismatch: {path.name}')


def stage_app(version, downloads, destination):
    lock = make_lock(version, (downloads / 'checksums.txt').read_text())
    verify_archives(lock, downloads)
    # Only create the staging tree after every published archive is verified.
    shutil.copytree(ROOT / 'apps/kiro-crew', destination)
    manifest_path = destination / 'app.json'
    manifest = json.loads(manifest_path.read_text())
    manifest['version'] = version.removeprefix('v')
    manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
    (destination / 'release/runtime.json').write_text(json.dumps(lock, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    # Validate the tag before passing it to gh. make_lock also validates it.
    if not re.fullmatch(r'v\d+\.\d+\.\d+', args.version):
        parser.error('Use an exact stable version, such as v0.2.0')
    with tempfile.TemporaryDirectory() as temporary:
        downloads = Path(temporary)
        command = ['gh', 'release', 'download', args.version, '--repo',
                   'appetizers-io/outrider', '--dir', temporary]
        subprocess.run(command + ['--pattern', 'checksums.txt'], check=True)
        lock = make_lock(args.version, (downloads / 'checksums.txt').read_text())
        subprocess.run(command + [arg for artifact in lock['artifacts'].values()
                                  for arg in ('--pattern', artifact['name'])], check=True)
        stage_app(args.version, downloads, args.output)


if __name__ == '__main__':
    main()
