#!/usr/bin/env bash
# release-assets.sh builds the release assets of a version tag into DIR: the
# flow binary of each platform, in flow-OS-ARCH.tar.gz, and flow.exe in
# flow-windows-ARCH.zip, with Go's name of the system and npm's of the CPU,
# as the install script, flow update and npm-package.sh read them.
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
out=$(cd "$out" && pwd) # zip writes from the build folder.
for platform in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64; do
	goos=${platform%/*}
	goarch=${platform#*/}
	name=flow-$goos-${goarch/amd64/x64}
	exe=flow
	[ "$goos" = windows ] && exe=flow.exe
	mkdir -p "$build/$name"
	# -s -w leave out the symbol table and debug info, which halves the download.
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags="-s -w" -o "$build/$name/$exe" ./cmd/flow
	built=$(go version -m "$build/$name/$exe" | awk '$1 == "mod" { print $3 }')
	if [ "$built" != "$tag" ]; then
		echo "$name binary is version $built, not $tag" >&2
		exit 1
	fi
	if [ "$goos" = windows ]; then
		# -X leaves out Unix owners and times, which Windows has no use for.
		(cd "$build/$name" && zip -q -X "$out/$name.zip" "$exe")
	else
		tar -czf "$out/$name.tar.gz" -C "$build/$name" "$exe"
	fi
done
