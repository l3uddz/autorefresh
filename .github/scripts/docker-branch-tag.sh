#!/usr/bin/env bash
# Prints the Docker tag for a branch name. build.yml publishes the branch
# image with this tag, and cleanup.yml removes that tag when the branch is
# deleted. Both workflows use this script, so they always agree.
set -euo pipefail

branch="${1:?usage: docker-branch-tag.sh <branch>}"

# A Docker tag allows only letters, digits, "_", "." and "-".
tag="${branch//[^A-Za-z0-9_.-]/-}"

# Releases own "latest" and the version tags, which start with a digit.
# A branch must not publish or remove one of them.
if [[ "$tag" == "latest" || "$tag" =~ ^[0-9] ]]; then
  echo "branch '$branch' maps to the reserved Docker tag '$tag'" >&2
  exit 1
fi

# A Docker tag has at most 128 characters. Keep a hash of the full branch
# name, so that two long branches with the same start get different tags.
if (( ${#tag} > 128 )); then
  hash="$(printf '%s' "$branch" | sha256sum | cut -c1-8)"
  tag="${tag:0:119}-$hash"
fi

echo "$tag"
