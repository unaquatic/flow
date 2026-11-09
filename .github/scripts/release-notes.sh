#!/usr/bin/env bash
# release-notes.sh prints the release notes of a version tag: every commit
# since the previous version, oldest first, each a squash-merged pull
# request's title and summary, then how to install the version.
#
#   release-notes.sh TAG
#
# The first version lists every commit after the initial one.
set -euo pipefail

tag=$1
prev=$(git tag --merged "$tag" --list 'v[0-9]*' --sort=-v:refname | grep -vxF "$tag" | head -1 || true)
from=${prev:-$(git rev-list --max-parents=0 "$tag")}
package=$(jq -r .name .github/npm/package.json)

echo "## Changes"
echo
for c in $(git rev-list --reverse "$from..$tag"); do
	git log -1 --format=%s "$c"
	body=$(git log -1 --format=%b "$c")
	if [ -n "$body" ]; then
		echo
		echo "$body"
	fi
	echo
done
echo "## Use"
echo
echo '```sh'
echo "npm install -g $package@${tag#v}"
echo '```'
