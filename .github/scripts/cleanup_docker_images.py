#!/usr/bin/env python3
# Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by the Apache v2 license that can be found in the
# LICENSE file.

"""Cleans up old Docker images from GitHub Container Registry.

Deletes container package versions older than 7 days, keeping 'latest' and
semantic version tags (v1, v1.2, v1.2.3, etc).
"""

import os
import re
import sys
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import json


def get_env(name: str) -> str:
    val = os.environ.get(name, "")
    if not val:
        print(f"Error: {name} is not set.", file=sys.stderr)
        sys.exit(1)
    return val


def api_request(url: str, token: str, method: str = "GET") -> tuple[int, bytes]:
    req = Request(url, method=method)
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("Authorization", f"token {token}")
    try:
        with urlopen(req) as resp:
            return resp.status, resp.read()
    except HTTPError as e:
        return e.code, e.read()


def main():
    owner = get_env("GITHUB_REPOSITORY_OWNER")
    repo = get_env("GITHUB_REPOSITORY").split("/", 1)[-1]
    token = get_env("GITHUB_TOKEN")
    print(f"Starting cleanup for image package: {owner}/{repo}")
    cutoff = datetime.now(timezone.utc) - timedelta(days=7)
    print(f"Cutoff date: {cutoff}")
    deleted_count = 0
    failed_count = 0
    kept_count = 0
    processed: list[tuple[str, str, str]] = []  # (tag, created_at, status)

    page = 1
    while True:
        print(f"Processing page {page}...")
        url = f"https://api.github.com/users/{owner}/packages/container/{repo}/versions?per_page=100&page={page}"
        status, body = api_request(url, token)
        if status != 200:
            print(f"Error fetching versions: HTTP {status}", file=sys.stderr)
            break
        versions = json.loads(body)
        if not versions:
            break
        for v in versions:
            version_id = v["id"]
            tag = v["name"]
            created_at = v["created_at"]
            # Skip special tags.
            if tag == "latest":
                kept_count += 1
                continue
            # Skip semantic version tags (v1, v1.2, v1.2.3, etc).
            if re.match(r"^v[0-9]+", tag):
                kept_count += 1
                continue
            created = datetime.fromisoformat(created_at)
            if created < cutoff:
                created_fmt = str(created)
                print(f"Deleting image: {tag} (created: {created_fmt})")
                del_url = f"https://api.github.com/users/{owner}/packages/container/{repo}/versions/{version_id}"
                del_status, _ = api_request(del_url, token, method="DELETE")
                if del_status == 204:
                    deleted_count += 1
                    processed.append((tag, created_fmt, "Deleted"))
                else:
                    print(f"Failed to delete {tag} (HTTP Status: {del_status})")
                    failed_count += 1
                    processed.append((tag, created_fmt, f"Failed ({del_status})"))
            else:
                kept_count += 1
        page += 1

    print(f"Cleanup complete. Deleted: {deleted_count}, Failed: {failed_count}, Kept/Skipped: {kept_count}")

    summary_path = os.environ.get("GITHUB_STEP_SUMMARY", "")
    if summary_path:
        with open(summary_path, "a") as f:
            f.write("# Docker Image Cleanup Report\n\n")
            f.write(f"**Repository:** {owner}/{repo}\n")
            f.write(f"**Date:** {datetime.now(timezone.utc)}\n")
            f.write(f"**Cutoff Date:** {cutoff}\n\n")
            f.write("## Summary\n")
            f.write("| Status | Count |\n")
            f.write("| :--- | :--- |\n")
            f.write(f"| Deleted | {deleted_count} |\n")
            f.write(f"| Failed | {failed_count} |\n")
            f.write(f"| Kept/Skipped | {kept_count} |\n\n")
            if processed:
                f.write("## Processed Images\n")
                f.write("| Tag | Created At | Status |\n")
                f.write("| :--- | :--- | :--- |\n")
                for tag, created_fmt, status in processed:
                    f.write(f"| `{tag}` | {created_fmt} | {status} |\n")
            else:
                f.write("_No images were old enough to be deleted._\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
