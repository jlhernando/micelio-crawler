import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / "capture-github-metrics.py"
spec = importlib.util.spec_from_file_location("capture_metrics", SCRIPT)
metrics = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metrics)


def asset(identifier, name, count):
    return {"id": identifier, "name": name, "download_count": count, "digest": None, "created_at": "2026-11-22T00:00:00Z"}


class MetricsTests(unittest.TestCase):
    def fixture(self, route, paginate=False):
        if route.endswith("/releases?per_page=100"):
            self.assertTrue(paginate)
            return [[{"id": 1, "tag_name": "v1", "draft": False, "prerelease": False,
                      "assets": [asset(1, "micelio-linux-amd64", 10), asset(2, "checksums.txt", 3), asset(3, "notes.txt", 99)]}],
                    [{"id": 2, "tag_name": "v2", "draft": False, "prerelease": True,
                      "assets": [asset(4, "micelio-windows-arm64.exe", 4)]},
                     {"id": 3, "tag_name": "v3", "draft": True, "prerelease": False,
                      "assets": [asset(5, "micelio-linux-arm64", 1000)]}]]
        if "/traffic/" in route:
            if "referrers" in route or "paths" in route: return []
            return {"count": 42, "uniques": 23, "views": []}
        return {"stargazers_count": 4, "forks_count": 1, "private_field": "must not persist"}

    def snapshot(self):
        with patch.object(metrics, "github", side_effect=self.fixture):
            return metrics.capture("owner/repo")

    def test_paginated_downloads_exclude_drafts_and_separate_checksums(self):
        snapshot = self.snapshot()
        self.assertEqual(snapshot["downloads"]["published_releases"], 2)
        self.assertEqual(snapshot["downloads"]["binary_events"], 14)
        self.assertEqual(snapshot["downloads"]["checksum_events"], 3)
        self.assertEqual(snapshot["downloads"]["other_events"], 99)
        self.assertNotIn("private_field", json.dumps(snapshot))
        self.assertTrue(next(a for a in snapshot["downloads"]["assets"] if a["id"] == 4)["prerelease"])

    def test_traffic_access_denied_is_unknown_not_zero(self):
        def request(route, paginate=False):
            if "/traffic/" in route: raise RuntimeError("HTTP 403")
            return self.fixture(route, paginate)
        with patch.object(metrics, "github", side_effect=request): snapshot = metrics.capture("owner/repo")
        self.assertIsNone(snapshot["traffic"]["views"])
        self.assertEqual(snapshot["traffic"]["errors"]["views"], "HTTP 403")
        self.assertEqual(snapshot["downloads"]["binary_events"], 14)

    def test_replaced_assets_and_decreasing_counters_are_discontinuities(self):
        previous = self.snapshot()
        current = copy.deepcopy(previous)
        current["downloads"]["assets"][0]["id"] = 91
        current["downloads"]["assets"][0]["download_count"] = 2
        current["downloads"]["assets"][-1]["download_count"] = 1
        result = metrics.compare(previous, current)
        self.assertEqual(result["same_asset_download_events"], 0)
        self.assertEqual(result["new_asset_ids"], [91])
        self.assertEqual(result["removed_asset_ids"], [1])
        self.assertEqual(result["decreasing_asset_ids"], [4])

    def test_only_stable_asset_ids_have_a_delta(self):
        previous = self.snapshot()
        current = copy.deepcopy(previous)
        current["downloads"]["assets"][0]["download_count"] += 7
        self.assertEqual(metrics.compare(previous, current)["same_asset_download_events"], 7)

    def test_storage_is_private_and_preserves_overlapping_windows(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(metrics.Path, "home", return_value=Path(temporary)):
                first = self.snapshot()
                path = metrics.store(first)
                second = self.snapshot()
                second_path = metrics.store(second)
            saved = json.loads(second_path.read_text())
            self.assertNotEqual(path, second_path)
            self.assertEqual(saved["traffic"]["views"]["uniques"], 23)
            self.assertNotIn("visitors", saved["comparison"])
            self.assertEqual(saved["comparison"]["same_asset_download_events"], 0)
            if os.name == "posix":
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)


if __name__ == "__main__":
    unittest.main()
