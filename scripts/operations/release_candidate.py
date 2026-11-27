#!/usr/bin/env python3
"""Recognize the single open, administrator-owned release candidate for one channel."""
from __future__ import annotations

import argparse
import json

import release_pr


def active_candidate(pulls, repository, channel):
    """Return the one eligible PR; do not silently choose among competing sources."""
    candidates = []
    for pull in pulls:
        if not isinstance(pull, dict):
            raise release_pr.ReleaseError("open release PR entry must be an object")
        if pull.get("state") not in ("open", "OPEN") or pull.get("draft") is not False:
            continue
        base = pull.get("base")
        if not isinstance(base, dict) or base.get("ref") != "main":
            continue
        head = pull.get("head")
        head_repo = head.get("repo") if isinstance(head, dict) else None
        if not isinstance(head_repo, dict) or head_repo.get("full_name") != repository:
            continue
        user = pull.get("user")
        if not isinstance(user, dict) or user.get("login") != "chyroc":
            continue
        title = pull.get("title")
        if not isinstance(title, str):
            continue
        try:
            release_pr.release_title(title, channel == "beta")
        except release_pr.ReleaseError:
            continue
        number = pull.get("number")
        if type(number) is not int or number <= 0:
            raise release_pr.ReleaseError("an open release candidate has no valid PR number")
        candidates.append(pull)
    if len(candidates) > 1:
        numbers = ", ".join("#" + str(pull["number"]) for pull in candidates)
        raise release_pr.ReleaseError(
            "multiple open " + channel + " release candidates exist (" + numbers
            + "); explicitly supersede all but one without moving their immutable sources"
        )
    return candidates[0] if candidates else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--channel", choices=("release", "beta"), required=True)
    parser.add_argument("--pulls", required=True, help="JSON list from the repository's open-PR API")
    args = parser.parse_args()
    if not release_pr.REPOSITORY.fullmatch(args.repository):
        raise SystemExit("a public owner/repository is required")
    with open(args.pulls, encoding="utf-8") as stream:
        pulls = json.load(stream)
    if not isinstance(pulls, list):
        raise SystemExit("open release PR response is not a list")
    candidate = active_candidate(pulls, args.repository, args.channel)
    if candidate:
        html_url = candidate.get("html_url")
        if not isinstance(html_url, str) or not html_url.strip():
            raise SystemExit("an open release candidate has no valid PR url")
        print(candidate["number"])
        print(html_url)


if __name__ == "__main__":
    main()
