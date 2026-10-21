"""Build the Crew app archive from an explicit runtime-file allowlist."""
import gzip
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / 'apps/kiro-crew'
OUT = ROOT / 'build/crew'
manifest = json.loads((APP / 'app.json').read_text())
files = [APP / name for name in ('app.json', 'README.md', 'LICENSE')]
for directory, extensions in {'agents': {'.json'}, 'backend': {'.py'}, 'ui': {'.mjs', '.svg'}, 'assets': {'.png', '.svg'}, 'release': {'.md', '.json'}}.items():
    files.extend(path for path in (APP / directory).rglob('*') if path.is_file() and path.suffix in extensions and '__pycache__' not in path.parts)
for path in files:
    if path.is_symlink() or not path.resolve().is_relative_to(APP):
        raise SystemExit(f'Refusing external or symlinked runtime file: {path}')
    if not path.is_file():
        raise SystemExit(f'Missing runtime file: {path}')
for resource in [manifest['backend']['entryPoint'], 'ui/' + manifest['ui']['entry'], manifest['iconPath'], *manifest['agents']]:
    if APP / resource not in files:
        raise SystemExit(f'Manifest resource is absent from package: {resource}')
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
dirty = bool(subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT, text=True).strip())
provenance = json.dumps({'app_version': manifest['version'], 'source_commit': commit, 'dirty': dirty, 'external_commands': manifest['dependencies']['commands']}, indent=2).encode() + b'\n'
OUT.mkdir(parents=True, exist_ok=True)
archive = OUT / f'outrider-crew-{manifest["version"]}.tar.gz'
with archive.open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', filename='', mtime=0) as compressed, tarfile.open(fileobj=compressed, mode='w') as tar:
    entries = [(str(path.relative_to(APP)), path.read_bytes()) for path in files] + [('release-info.json', provenance)]
    for name, data in sorted(entries):
        info = tarfile.TarInfo('outrider/' + name)
        info.size = len(data)
        info.mode = 0o644
        tar.addfile(info, io.BytesIO(data))
checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
(OUT / (archive.name + '.sha256')).write_text(f'{checksum}  {archive.name}\n')
print(f'{archive} ({len(files)} runtime files; source {commit}; dirty={dirty})')
