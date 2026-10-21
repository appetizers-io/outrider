import hashlib
import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'backend'))
import runtime


class RuntimeTests(unittest.TestCase):
    def test_host_wins_without_download(self):
        with patch.dict(runtime.os.environ, {'USERPROFILE': str(Path.home())}, clear=True), patch.object(runtime.shutil, 'which', return_value='/host/outrider'), patch.object(Path, 'is_file', return_value=True), patch.object(runtime, 'compatible', return_value=True), patch.object(runtime, 'urlopen') as download:
            self.assertEqual(runtime.install()['source'], 'host')
            download.assert_not_called()

    def test_explicit_override_never_downloads(self):
        with patch.object(runtime, 'discover', return_value={'status':'missing', 'override':True}), patch.object(runtime, 'urlopen') as download:
            with self.assertRaisesRegex(ValueError, 'OUTRIDER_BIN'):
                runtime.install()
            download.assert_not_called()

    def test_incompatible_host_requires_choice(self):
        with patch.object(runtime, 'discover', return_value={'status':'incompatible'}), patch.object(runtime, 'urlopen') as download:
            with self.assertRaisesRegex(ValueError, 'Choose'):
                runtime.install()
            download.assert_not_called()

    def test_platforms(self):
        for system, machine, expected in [('Windows','AMD64','windows_amd64'),('Windows','ARM64','windows_arm64'),('Darwin','arm64','darwin_arm64'),('Linux','x86_64','linux_amd64')]:
            with patch.object(runtime.platform,'system',return_value=system),patch.object(runtime.platform,'machine',return_value=machine):
                self.assertEqual(runtime.platform_key(),expected)

    def archive(self, name, content, windows):
        buffer=io.BytesIO()
        if windows:
            with zipfile.ZipFile(buffer,'w') as archive:
                archive.writestr(name,content)
        else:
            with tarfile.open(fileobj=buffer,mode='w:gz') as archive:
                item=tarfile.TarInfo(name);item.size=len(content)
                archive.addfile(item,io.BytesIO(content))
        return buffer.getvalue()

    def test_verified_install_tar_and_zip(self):
        for windows in [False,True]:
            with self.subTest(windows=windows), tempfile.TemporaryDirectory() as tmp:
                filename='outrider.exe' if windows else 'outrider'
                payload=self.archive(filename,b'binary',windows)
                artifact={'name':'outrider.zip' if windows else 'outrider.tar.gz','sha256':hashlib.sha256(payload).hexdigest()}
                target=Path(tmp)/'runtime/v1.0.0'/filename
                with patch.object(runtime,'discover',return_value={'status':'missing'}),patch.object(runtime,'release',return_value={'version':'v1.0.0','artifacts':{'test':artifact}}),patch.object(runtime,'platform_key',return_value='test'),patch.object(runtime,'private_path',return_value=target),patch.object(runtime,'urlopen',return_value=io.BytesIO(payload)),patch.object(runtime,'compatible',return_value=True):
                    self.assertEqual(runtime.install(tmp)['source'],'private')
                    self.assertEqual(target.read_bytes(),b'binary')

    def test_checksum_failure_does_not_install_or_execute(self):
        with tempfile.TemporaryDirectory() as tmp:
            target=Path(tmp)/'runtime/v1.0.0/outrider'
            artifact={'name':'outrider.tar.gz','sha256':'0'*64}
            with patch.object(runtime,'discover',return_value={'status':'missing'}),patch.object(runtime,'release',return_value={'version':'v1.0.0','artifacts':{'test':artifact}}),patch.object(runtime,'platform_key',return_value='test'),patch.object(runtime,'private_path',return_value=target),patch.object(runtime,'urlopen',return_value=io.BytesIO(b'bad')),patch.object(runtime,'compatible') as execute:
                with self.assertRaisesRegex(ValueError,'checksum'):
                    runtime.install(tmp)
                self.assertFalse(target.exists())
                execute.assert_not_called()

    def test_missing_pin_is_actionable(self):
        with patch.object(runtime,'discover',return_value={'status':'missing'}),patch.object(runtime,'release',return_value={}):
            with self.assertRaisesRegex(ValueError,'no pinned runtime'):
                runtime.install()
