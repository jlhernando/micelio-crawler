"""Offline installation checks: no user directories, servers or real downloads."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
REPO = "https://github.com/jlhernando/micelio-crawler"
PAYLOAD = b"fixture binary, never executed\n"


@unittest.skipIf(os.name == "nt", "Unix installer; Windows has its own fixture runner")
class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="micelio-install-test-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.bin = self.base / "commands"
        self.bin.mkdir()
        self.destination = self.base / "path with spaces"
        self.target = self.destination / "micelio"
        self.fixture = self.base / "fixture.json"
        self.log = self.base / "requests.jsonl"
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        TEST_INSTALL_FIXTURE=str(self.fixture), TEST_INSTALL_LOG=str(self.log))
        self.settings = {"platform": "Linux", "arch": "x86_64", "payload": PAYLOAD.decode(),
                         "checksum": hashlib.sha256(PAYLOAD).hexdigest(), "checksum_mode": "valid"}
        self.command("uname", '''import json,os,sys
d=json.load(open(os.environ['TEST_INSTALL_FIXTURE']))
print(d['platform'] if sys.argv[1]=='-s' else d['arch'])
''')
        self.command("curl", '''import json,os,sys,pathlib
d=json.load(open(os.environ['TEST_INSTALL_FIXTURE']));args=sys.argv[1:];url=args[-1]
with open(os.environ['TEST_INSTALL_LOG'],'a') as f:f.write(json.dumps(args)+'\\n')
if url.endswith('/releases/latest'):
 print(d.get('effective','https://github.com/jlhernando/micelio-crawler/releases/tag/v1.16.0'),end='');sys.exit(0)
if d.get('fail_download'):sys.exit(22)
if d.get('collision'):
 p=pathlib.Path(d['collision']);p.parent.mkdir(parents=True,exist_ok=True)
 if not p.exists():
  p.mkdir() if d.get('collision_directory') else p.write_text('keep this file')
out=pathlib.Path(args[args.index('--output')+1])
if url.endswith('/checksums.txt'):
 mode=d['checksum_mode']
 if mode=='missing':sys.exit(22)
 asset='micelio-'+('darwin' if d['platform']=='Darwin' else 'linux')+'-'+('arm64' if d['arch'] in ['arm64','aarch64'] else 'amd64')
 line=d['checksum']+'  '+asset+'\\n'
 if mode=='absent_entry':line=d['checksum']+'  other-file\\n'
 if mode=='duplicate':line+=line
 if mode=='extra_field':line=line.rstrip()+' unexpected\\n'
 out.write_text(line)
else:out.write_text(d['payload'])
''')

    def command(self, name, code):
        path = self.bin / name
        path.write_text("#!/usr/bin/env python3\n" + code)
        path.chmod(0o755)

    def run_install(self, *args, explicit=True):
        self.fixture.write_text(json.dumps(self.settings))
        command = ["sh", str(ROOT / "scripts/install.sh")]
        if explicit: command += ["--install-dir", str(self.destination)]
        return subprocess.run(command + list(args), env=self.env, capture_output=True, text=True, timeout=15)

    def assert_not_installed(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.target.exists())
        self.assertFalse(list(self.destination.glob(".micelio-install.*")))

    def test_success_pins_both_files_and_preserves_permissions(self):
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.target.read_bytes(), PAYLOAD)
        self.assertTrue(os.access(self.target, os.X_OK))
        requests = [json.loads(line) for line in self.log.read_text().splitlines()]
        urls = [r[-1] for r in requests]
        self.assertEqual(urls, [REPO + "/releases/latest", REPO + "/releases/download/v1.16.0/micelio-linux-amd64", REPO + "/releases/download/v1.16.0/checksums.txt"])
        self.assertTrue(all("--proto-redir" in r and "=https" in r for r in requests))
        self.assertFalse(list(self.destination.glob(".micelio-install.*")))

    def test_explicit_version_skips_latest_and_supports_macos_arm(self):
        self.settings.update(platform="Darwin", arch="arm64")
        result = self.run_install("--version", "v1.15.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        urls = [json.loads(line)[-1] for line in self.log.read_text().splitlines()]
        self.assertEqual(urls[0], REPO + "/releases/download/v1.15.2/micelio-darwin-arm64")
        self.assertNotIn(REPO + "/releases/latest", urls)

    def test_invalid_and_unavailable_checksums_never_install(self):
        for mode in ["missing", "absent_entry", "duplicate", "extra_field"]:
            with self.subTest(mode=mode):
                self.settings["checksum_mode"] = mode
                self.assert_not_installed(self.run_install())
        for checksum in ["0" * 64, "invalid", "z" * 64]:
            with self.subTest(checksum=checksum):
                self.settings.update(checksum_mode="valid", checksum=checksum)
                self.assert_not_installed(self.run_install())

    def test_existing_file_directory_and_symlinks_are_untouched(self):
        self.destination.mkdir()
        original = self.base / "private-build"
        original.write_bytes(b"private binary")
        for kind in ["file", "directory", "symlink", "dangling"]:
            with self.subTest(kind=kind):
                if kind == "file": self.target.write_bytes(b"existing")
                elif kind == "directory": self.target.mkdir()
                else: self.target.symlink_to(original if kind == "symlink" else self.base / "absent")
                result = self.run_install()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.log.exists(), "Must reject before any network request")
                self.assertEqual(original.read_bytes(), b"private binary")
                if kind == "directory": self.target.rmdir()
                else: self.target.unlink()

    def test_late_collisions_never_overwrite_or_write_inside_directory(self):
        self.settings["collision"] = str(self.target)
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertEqual(self.target.read_text(), "keep this file")
        self.target.unlink()
        self.settings["collision_directory"] = True
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertEqual(list(self.target.iterdir()), [])

    def test_other_micelio_on_path_requires_explicit_directory(self):
        self.command("micelio", "raise SystemExit('must not run existing Micelio')\n")
        result = self.run_install(explicit=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("separate directory", result.stderr)
        self.assertFalse(self.log.exists())
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_download_failure_architecture_redirect_and_arguments(self):
        self.settings["fail_download"] = True
        self.assert_not_installed(self.run_install())
        self.settings.pop("fail_download")
        self.settings["arch"] = "unsupported"
        self.assert_not_installed(self.run_install())
        self.settings["arch"] = "x86_64"
        self.settings["effective"] = "http://untrusted.example/v1.16.0"
        self.assert_not_installed(self.run_install())
        self.assert_not_installed(self.run_install("--version", "v1.16.0/other"))
        self.assert_not_installed(self.run_install("--unknown"))


if __name__ == "__main__":
    unittest.main()
