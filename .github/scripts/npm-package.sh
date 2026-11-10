#!/usr/bin/env bash
# npm-package.sh builds the npm package of a version tag into DIR from its
# release assets in ASSETS, as release-assets.sh builds them: @unaquatic/flow,
# with the flow binary of every platform, bin/flow-OS-ARCH, beside the
# launcher that runs the one of its platform.
#
#   npm-package.sh TAG ASSETS DIR
set -euo pipefail

tag=$1
assets=$2
out=$3
platforms=()

mkdir -p "$out/bin"
cp .github/npm/bin/flow.js "$out/bin/"
cp README.md LICENSE "$out/"
for archive in "$assets"/flow-*.tar.gz; do
	name=$(basename "$archive" .tar.gz)
	tar -xzf "$archive" -C "$out/bin" flow
	mv "$out/bin/flow" "$out/bin/$name"
	platforms+=("${name#flow-}")
done

# npm installs the package only on the systems and CPUs of the binaries.
jq --arg version "${tag#v}" --args '
	.version = $version
	| .os = ($ARGS.positional | map(split("-")[0]) | unique)
	| .cpu = ($ARGS.positional | map(split("-")[1]) | unique)
' "${platforms[@]}" < .github/npm/package.json > "$out/package.json"
