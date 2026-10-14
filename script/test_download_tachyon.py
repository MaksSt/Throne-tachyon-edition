import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import download_tachyon


class DownloadTests(unittest.TestCase):
    def test_windows_archive_reads_only_expected_binary(self):
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, "w") as package:
            package.writestr("../../unexpected", b"do not extract")
            package.writestr("tachyon-core.exe", b"binary")
        self.assertEqual(download_tachyon.extract_binary(archive.getvalue(), True), b"binary")

    def test_unix_archive_does_not_follow_symlinks(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w:gz") as package:
            link = tarfile.TarInfo("tachyon-core-link")
            link.type = tarfile.SYMTYPE
            link.linkname = "/etc/passwd"
            package.addfile(link)
            binary = tarfile.TarInfo("./tachyon-core-linux-amd64-v0.1.1")
            binary.size = 6
            package.addfile(binary, io.BytesIO(b"binary"))
        self.assertEqual(download_tachyon.extract_binary(archive.getvalue(), False), b"binary")

    def test_hash_failure_does_not_write_binary(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(download_tachyon.urllib.request, "urlopen", return_value=io.BytesIO(b"archive")):
                with patch.object(download_tachyon, "extract_binary", return_value=b"invalid"):
                    with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
                        download_tachyon.download("windows", "amd64", Path(temporary))
            self.assertEqual(list(Path(temporary).iterdir()), [])

    def test_unsupported_platform_does_not_download(self):
        with patch.object(download_tachyon.urllib.request, "urlopen") as request:
            self.assertIsNone(download_tachyon.download("windows", "arm64", Path("unused")))
            self.assertIsNone(download_tachyon.download("windowslegacy", "amd64", Path("unused")))
            request.assert_not_called()

    def test_all_pinned_hashes_have_sha256_length(self):
        for platform, (_, digest) in download_tachyon.TARGETS.items():
            self.assertEqual(len(bytes.fromhex(digest)), hashlib.sha256().digest_size, platform)


if __name__ == "__main__":
    unittest.main()
