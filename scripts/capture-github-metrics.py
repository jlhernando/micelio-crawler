#!/usr/bin/env python3
"""Capture repository adoption metrics privately; no app telemetry or scheduling."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess
import sys

BINARY = re.compile(r"^(?:micelio-(?:linux|darwin)-(?:amd64|arm64)|micelio-windows-(?:amd64|arm64)\.exe)$")


def github(route, paginate=False):
    args = ["gh", "api", "--hostname", "github.com"]
    if paginate:
        args += ["--paginate", "--slurp"]
    result = subprocess.run(args + [route], capture_output=True, text=True, timeout=60)
    if result.returncode:
        status = re.search(r"HTTP (\d{3})", result.stderr)
        raise RuntimeError("HTTP " + status[1] if status else "GitHub request failed; check gh authentication and repository access")
    return json.loads(result.stdout)


def capture(repo):
    route = "repos/" + repo
    metadata = github(route)
    snapshot = {
        "schema_version": 1,
        "captured_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "repository": repo,
        "repository_totals": {"stars": metadata["stargazers_count"], "forks": metadata["forks_count"]},
        "traffic": {"window_days": 14, "errors": {}},
    }
    for key, endpoint in [("views", "views?per=day"), ("clones", "clones?per=day"),
                          ("referrers", "popular/referrers"), ("paths", "popular/paths")]:
        try:
            snapshot["traffic"][key] = github(route + "/traffic/" + endpoint)
        except RuntimeError as error:
            snapshot["traffic"][key] = None
            snapshot["traffic"]["errors"][key] = str(error)
    pages = github(route + "/releases?per_page=100", paginate=True)
    releases = [release for page in pages for release in page if not release["draft"]]
    assets = []
    binary_events = checksum_events = other_events = 0
    for release in releases:
        for asset in release["assets"]:
            kind = "binary" if BINARY.fullmatch(asset["name"]) else "checksum" if asset["name"] == "checksums.txt" else "other"
            count = asset["download_count"]
            if kind == "binary": binary_events += count
            elif kind == "checksum": checksum_events += count
            else: other_events += count
            assets.append({"release_id": release["id"], "tag": release["tag_name"],
                           "prerelease": release["prerelease"], "kind": kind,
                           **{key: asset.get(key) for key in ["id", "name", "download_count", "digest", "created_at"]}})
    snapshot["downloads"] = {"published_releases": len(releases), "binary_events": binary_events,
                             "checksum_events": checksum_events, "other_events": other_events, "assets": assets}
    return snapshot


def compare(previous, current):
    old = {a["id"]: a for a in previous["downloads"]["assets"] if a["kind"] == "binary"}
    new = {a["id"]: a for a in current["downloads"]["assets"] if a["kind"] == "binary"}
    shared = old.keys() & new.keys()
    decreases = [key for key in shared if new[key]["download_count"] < old[key]["download_count"]]
    return {
        "previous_captured_at": previous["captured_at"],
        "same_asset_download_events": sum(new[k]["download_count"] - old[k]["download_count"] for k in shared if k not in decreases),
        "new_asset_ids": sorted(new.keys() - old.keys()),
        "removed_asset_ids": sorted(old.keys() - new.keys()),
        "decreasing_asset_ids": sorted(decreases),
    }


def store(snapshot):
    # No output-dir option: traffic and referrers must not land in a checkout.
    directory = Path.home() / ".micelio" / "metrics" / "github" / snapshot["repository"]
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    if os.name == "posix":
        for parent in [directory, directory.parent, directory.parent.parent]:
            parent.chmod(0o700)
    for path in sorted(directory.glob("*.json"), reverse=True):
        try:
            previous = json.loads(path.read_text())
        except (OSError, ValueError):
            continue
        if previous.get("repository") == snapshot["repository"] and previous.get("schema_version") == 1:
            snapshot["comparison"] = compare(previous, snapshot)
            break
    timestamp = dt.datetime.now(dt.timezone.utc)
    while True:
        target = directory / timestamp.strftime("%Y%m%dT%H%M%S.%fZ.json")
        try:
            fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            break
        except FileExistsError:
            # Coarse clocks can return the same instant for successive writes.
            # Keep exclusive creation and chronological filename ordering.
            timestamp += dt.timedelta(microseconds=1)
    with os.fdopen(fd, "w") as stream:
        json.dump(snapshot, stream, indent=2)
        stream.write("\n")
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default="jlhernando/micelio-crawler", help="OWNER/REPO to read using gh authentication")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repo) or any(p in [".", ".."] for p in args.repo.split("/")):
        parser.error("Repository must be OWNER/REPO")
    try:
        snapshot = capture(args.repo)
        target = store(snapshot)
    except (OSError, RuntimeError, ValueError, KeyError, subprocess.TimeoutExpired) as error:
        print("Metrics capture failed: " + str(error), file=sys.stderr)
        return 1
    totals = snapshot["repository_totals"]
    print(f"{args.repo}: {totals['stars']} stars; {totals['forks']} forks")
    views = snapshot["traffic"]["views"]
    print(f"Rolling 14 days: {views['count']} views; {views['uniques']} unique visitors" if views is not None else "Traffic unavailable; public metrics still saved")
    downloads = snapshot["downloads"]
    print(f"{downloads['binary_events']} binary download events across {downloads['published_releases']} releases; not installations or unique users")
    if "comparison" in snapshot:
        delta = snapshot["comparison"]
        print(f"Since previous snapshot: {delta['same_asset_download_events']} download events on unchanged asset IDs")
        if any(delta[k] for k in ["new_asset_ids", "removed_asset_ids", "decreasing_asset_ids"]):
            print("Asset discontinuities recorded; do not subtract aggregate download totals")
    print(f"Private snapshot saved to {target}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
