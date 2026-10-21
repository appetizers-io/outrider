"""Release pinning must describe the bytes actually published for every platform."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'scripts'))
from prepare_crew_release import stage_app


class ReleaseStagingTests(unittest.TestCase):
    def fixture(self, directory):
        lines = []
        for system in ('darwin', 'linux', 'windows'):
            for arch in ('amd64', 'arm64'):
                ext = 'zip' if system == 'windows' else 'tar.gz'
                name = f'outrider_1.2.3_{system}_{arch}.{ext}'
                data = name.encode()
                (directory / name).write_bytes(data)
                lines.append(f'{hashlib.sha256(data).hexdigest()}  {name}')
        (directory / 'checksums.txt').write_text('\n'.join(lines))

    def test_stage_preserves_source_and_packages_matching_version_and_lock(self):
        source = ROOT / 'apps/kiro-crew/app.json'
        before = source.read_bytes()
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            self.fixture(directory)
            app = directory / 'app'
            stage_app('v1.2.3', directory, app)
            self.assertEqual(json.loads((app / 'app.json').read_text())['version'], '1.2.3')
            subprocess.run([sys.executable, str(ROOT / 'scripts/package_crew.py'),
                            '--marketplace', '--app-dir', str(app)], check=True, capture_output=True)
            archive = ROOT / 'build/crew/outrider-crew-1.2.3.tar.gz'
            try:
                with tarfile.open(archive) as package:
                    lock = json.load(package.extractfile('outrider/release/runtime.json'))
                    provenance = json.load(package.extractfile('outrider/release-info.json'))
                self.assertEqual(lock['version'], 'v1.2.3')
                self.assertEqual(len(lock['artifacts']), 6)
                self.assertEqual(provenance['runtime_version'], 'v1.2.3')
            finally:
                archive.unlink()
                archive.with_name(archive.name + '.sha256').unlink()
        self.assertEqual(source.read_bytes(), before)

    def test_corrupt_or_missing_archive_never_stages_app(self):
        for missing in (False, True):
            with self.subTest(missing=missing), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                self.fixture(directory)
                archive = directory / 'outrider_1.2.3_windows_arm64.zip'
                if missing:
                    archive.unlink()
                else:
                    archive.write_bytes(b'corrupted')
                with self.assertRaises((ValueError, FileNotFoundError)):
                    stage_app('v1.2.3', directory, directory / 'app')
                self.assertFalse((directory / 'app').exists())

    def test_incomplete_checksums_never_stages_app(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / 'checksums.txt').write_text('')
            with self.assertRaises(ValueError):
                stage_app('v1.2.3', directory, directory / 'app')
            self.assertFalse((directory / 'app').exists())
