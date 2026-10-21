"""Host-first runtime discovery and verified, app-private provisioning."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tempfile
import threading
from urllib.request import urlopen
import zipfile
import tarfile

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / 'release/runtime.json'
DATA = ROOT / 'data'
_install_lock = threading.Lock()


def environment():
    env = os.environ.copy()
    entries = env.get('PATH', os.defpath).split(os.pathsep)
    entries.append(str(Path.home() / '.local/bin'))
    if os.name != 'nt':
        entries += ['/opt/homebrew/bin', '/usr/local/bin', '/usr/bin', '/bin']
    env['PATH'] = os.pathsep.join(dict.fromkeys(filter(None, entries)))
    return env


def compatible(binary, runner=subprocess.run):
    try:
        for args, marker in [(['crew', 'scan', '--help'], '--state-dir'), (['crew', 'session-prompt', '--help'], ''), (['crew', 'capture', '--help'], '--pr'), (['mcp', '--help'], '--config-format')]:
            result = runner([str(binary), *args], capture_output=True, text=True, encoding='utf-8', timeout=10, check=False, env=environment())
            if result.returncode or marker not in result.stdout:
                return False
        return True
    except (OSError, subprocess.TimeoutExpired):
        return False


def platform_key():
    system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'windows'}.get(platform.system())
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'amd64': 'amd64', 'x86_64': 'amd64'}.get(platform.machine().lower())
    return f'{system}_{arch}' if system and arch else ''


def release():
    try:
        return json.loads(LOCK.read_text())
    except FileNotFoundError:
        return {'version': '', 'artifacts': {}}


def private_path(data=DATA):
    version = release().get('version', '')
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?', version):
        return None
    return Path(data) / 'runtime' / version / ('outrider.exe' if os.name == 'nt' else 'outrider')


def discover(runner=subprocess.run, data=DATA):
    override = os.environ.get('OUTRIDER_BIN')
    name = 'outrider.exe' if os.name == 'nt' else 'outrider'
    candidates = [override] if override else [shutil.which('outrider', path=environment()['PATH']), str(Path.home() / '.local/bin' / name)]
    incompatible = []
    for candidate in dict.fromkeys(filter(None, candidates)):
        if not Path(candidate).is_file():
            continue
        if compatible(candidate, runner):
            return {'status': 'ready', 'source': 'host', 'path': str(Path(candidate).resolve())}
        incompatible.append(candidate)
    private = private_path(data)
    if not override and private and private.is_file() and compatible(private, runner):
        return {'status': 'ready', 'source': 'private', 'path': str(private)}
    return {'status': 'incompatible' if incompatible or override else 'missing', 'source': 'host', 'path': override or '', 'incompatible': incompatible, 'override': bool(override)}


def find(runner=subprocess.run):
    result = discover(runner)
    if result['status'] == 'ready':
        return result['path']
    raise RuntimeError('No compatible Outrider runtime. Open Outrider → Doctor to check or install it.')


def doctor(data=DATA):
    runtime = discover(data=data)
    pin = release()
    runtime['download_available'] = bool(pin.get('artifacts', {}).get(platform_key())) and not runtime.get('override')
    runtime['version'] = pin.get('version') if runtime.get('source') == 'private' else None
    checks = []
    for name, args, help_text in [('git', ['--version'], 'Install Git for your operating system.'), ('gh', ['auth', 'status', '--hostname', 'github.com'], 'Install GitHub CLI and run gh auth login on the machine running Crew.')]:
        path = shutil.which(name, path=environment()['PATH'])
        ok = False
        if path:
            try:
                ok = subprocess.run([path, *args], env=environment(), capture_output=True, timeout=15, check=False).returncode == 0
            except (OSError, subprocess.TimeoutExpired):
                pass
        checks.append({'name': name, 'ready': ok, 'path': path, 'help': '' if ok else help_text})
    return {'runtime': runtime, 'checks': checks, 'ready': runtime['status'] == 'ready' and all(c['ready'] for c in checks), 'platform': platform_key(), 'installation': installation_status(), 'complete': (Path(data) / 'onboarding.json').exists()}


def install(data=DATA, allow_incompatible=False):
    if not _install_lock.acquire(blocking=False):
        raise ValueError('Runtime installation is already in progress')
    try:
        found = discover(data=data)
        if found['status'] == 'ready':
            return found
        if found.get('override'):
            raise ValueError('Fix or remove OUTRIDER_BIN before installing a private runtime')
        if found['status'] == 'incompatible' and not allow_incompatible:
            raise ValueError('Host Outrider is incompatible. Choose Use private runtime to keep the host installation unchanged.')
        pin = release()
        artifact = pin.get('artifacts', {}).get(platform_key())
        target = private_path(data)
        if not artifact or target is None:
            raise ValueError('This plugin build has no pinned runtime release for this platform. Install a compatible host Outrider or use a published plugin release.')
        name, digest = artifact.get('name', ''), artifact.get('sha256', '')
        if not re.fullmatch(r'[A-Za-z0-9_.-]+\.(?:tar\.gz|zip)', name) or not re.fullmatch(r'[a-f0-9]{64}', digest):
            raise ValueError('Invalid runtime release manifest')
        target.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='.install-', dir=target.parent) as directory:
            archive = Path(directory) / name
            url = f'https://github.com/appetizers-io/outrider/releases/download/{pin["version"]}/{name}'
            sha = hashlib.sha256()
            with urlopen(url, timeout=30) as response, archive.open('wb') as output:
                total = 0
                while chunk := response.read(1024 * 1024):
                    total += len(chunk)
                    if total > 128 * 1024 * 1024:
                        raise ValueError('Runtime download exceeds size limit')
                    sha.update(chunk)
                    output.write(chunk)
            if sha.hexdigest() != digest:
                raise ValueError('Runtime checksum verification failed; nothing was installed')
            staged = Path(directory) / target.name
            # Extract only the executable, never archive paths or links.
            if name.endswith('.zip'):
                with zipfile.ZipFile(archive) as bundle:
                    matches = [x for x in bundle.infolist() if x.filename == target.name and x.file_size <= 128 * 1024 * 1024]
                    if len(matches) != 1:
                        raise ValueError('Runtime archive must contain one executable')
                    with bundle.open(matches[0]) as source, staged.open('wb') as output:
                        shutil.copyfileobj(source, output)
            else:
                with tarfile.open(archive) as bundle:
                    matches = [x for x in bundle.getmembers() if x.name == target.name and x.isfile() and x.size <= 128 * 1024 * 1024]
                    if len(matches) != 1:
                        raise ValueError('Runtime archive must contain one executable')
                    with bundle.extractfile(matches[0]) as source, staged.open('wb') as output:
                        shutil.copyfileobj(source, output)
            staged.chmod(0o700)
            if not compatible(staged):
                raise ValueError('Downloaded runtime is incompatible; nothing was installed')
            os.replace(staged, target)
        return {'status': 'ready', 'source': 'private', 'path': str(target)}
    finally:
        _install_lock.release()

_job_lock = threading.Lock()
_job = {'status': 'idle'}


def installation_status():
    with _job_lock:
        return dict(_job)


def start_install(data=DATA, allow_incompatible=False):
    with _job_lock:
        if _job['status'] == 'running':
            return dict(_job)
        _job.clear()
        _job.update(status='running')
    def work():
        try:
            result = install(data, allow_incompatible)
            with _job_lock:
                _job.update(status='completed', runtime=result)
        except Exception as error:
            with _job_lock:
                _job.update(status='failed', error=str(error))
    threading.Thread(target=work, daemon=True).start()
    return {'status': 'running'}
