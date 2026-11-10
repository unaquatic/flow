#!/usr/bin/env bash
# release-assets.sh builds the release assets of a version tag into DIR: the
# flow binary of each platform, in flow-OS-ARCH.tar.gz, with npm's names of
# the platform, as npm-package.sh reads them.
#
#   release-assets.sh TAG DIR
#
# DIR must be outside the checkout, so that the binaries record TAG as their
# version, which flow updates itself from, rather than a dirty build's.
set -euo pipefail

tag=$1
out=$2
build=$(mktemp -d)

mkdir -p "$out"
for platform in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
	goos=${platform%/*}
	goarch=${platform#*/}
	name=flow-$goos-${goarch/amd64/x64}
	mkdir -p "$build/$name"
	# -s -w leave out the symbol table and debug info, which halves the download.
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags="-s -w" -o "$build/$name/flow" ./cmd/flow
	built=$(go version -m "$build/$name/flow" | awk '$1 == "mod" { print $3 }')
	if [ "$built" != "$tag" ]; then
		echo "$name binary is version $built, not $tag" >&2
		exit 1
	fi
	tar -czf "$out/$name.tar.gz" -C "$build/$name" flow
done
