#!/bin/sh
# install.sh installs flow: the latest release, or the one FLOW_VERSION or
# --version names, from its GitHub release, checked against the SHA-256
# digest GitHub reports for it, or, when GitHub does not answer and
# FLOW_MIRROR_URL names a mirror, such as https://aquatic.dev/flow/releases,
# from it, checked against the mirror's SHA256SUMS. No mirror is tried
# unless it is set. It installs flow into ~/.flow/bin, or
# FLOW_INSTALL_DIR, beside the marker that makes flow update itself the same
# way, links it from ~/.local/bin, and adds that folder to PATH unless it is
# on it already, or FLOW_NO_MODIFY_PATH or --no-modify-path says not to. It
# runs on macOS and Linux, and on Windows in Git Bash, MSYS2 or Cygwin, where
# PATH takes ~/.flow/bin itself.
#
#   curl -fsSL https://aquatic.dev/flow/install | sh
#   curl -fsSL https://aquatic.dev/flow/install | sh -s -- --version v0.1.0
set -eu

# The whole script is main, which runs on its last line, so that the shell
# reads all of it first: a download cut off midway is a syntax error, and
# runs nothing, rather than part of the script.
main() {

repo=unaquatic/flow
marker=.flow-standalone
github_api=${FLOW_GITHUB_API:-https://api.github.com}
mirror=${FLOW_MIRROR_URL:-}
version=${FLOW_VERSION:-latest}
modify_path=true
case ${FLOW_NO_MODIFY_PATH:-} in 1 | true | yes) modify_path=false ;; esac

# stepped is set once a step shows, which an error then leaves a blank line
# under.
stepped=false

# error prints $1 on standard error after the label "error:", red where it
# shows colors, as flow prints its own errors.
error() {
	label=error:
	if [ -t 2 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != dumb ]; then
		label=$(printf '\033[31merror:\033[39m')
	fi
	[ "$stepped" = false ] || printf '\n' >&2
	stepped=false
	printf '%s flow: %s\n' "$label" "$1" >&2
}

fail() {
	error "$1"
	exit 1
}

while [ $# -gt 0 ]; do
	case $1 in
		--version)
			[ $# -ge 2 ] || fail "missing value: --version"
			version=$2
			shift
			;;
		--no-modify-path) modify_path=false ;;
		-h | --help)
			# Bold headings, as flow's help has them.
			b= r=
			if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != dumb ]; then
				b=$(printf '\033[1m') r=$(printf '\033[22m')
			fi
			cat <<EOF
${b}Usage:${r}
  install.sh [flags]

${b}Flags:${r}
      --version <version>  Version to install, such as v0.1.0 (default latest)
      --no-modify-path     Leave PATH as it is
  -h, --help               Show help

${b}Environment:${r}
  FLOW_VERSION         Version to install, such as v0.1.0 (default latest)
  FLOW_INSTALL_DIR     Folder to install flow into (default ~/.flow/bin)
  FLOW_NO_MODIFY_PATH  Set to 1 to leave PATH as it is
EOF
			exit 0
			;;
		*) fail "unknown option: $1" ;;
	esac
	shift
done

[ -n "${HOME:-}" ] || fail "missing variable: HOME"
install_dir=${FLOW_INSTALL_DIR:-$HOME/.flow/bin}
link_dir=$HOME/.local/bin

case $version in
	latest) ;;
	v*) ;;
	*) version=v$version ;;
esac

# Colors and the animation of the download, where standard output is a
# terminal that shows them.
tty=false color=false
if [ -t 1 ] && [ "${TERM:-}" != dumb ]; then
	tty=true
	[ -n "${NO_COLOR:-}" ] || color=true
fi
# icon draws flow's icon: its "f" near-black on a square of the orange.
bold= unbold= orange= icon= reset=
if [ "$color" = true ]; then
	bold=$(printf '\033[1m') unbold=$(printf '\033[22m') reset=$(printf '\033[0m')
	case ${COLORTERM:-} in
		truecolor | 24bit) orange=$(printf '\033[38;2;255;95;0m') icon=$(printf '\033[38;2;17;17;17m\033[48;2;255;95;0m') ;;
		*) orange=$(printf '\033[38;5;202m') icon=$(printf '\033[38;5;233m\033[48;5;202m') ;;
	esac
fi

# banner prints flow's icon, its "f" dark on an orange square, as flow draws
# it at startup (product.Icon), and the installer's title. Every line of the
# icon is 16 columns wide, as its spaces draw the square.
banner() {
	printf '\n'
	while IFS= read -r line; do
		printf '%s%s%s\n' "$icon" "$line" "$reset"
	done <<'EOF'
        ▄▄▄     
       ██▀▀     
     ▄▄██▄▄     
     ▀▀██▀▀     
       ██       
       ██       
    ██▄██       
     ▀▀▀        
EOF
	printf '\n%sFlow Installer%s\n\n' "$bold" "$unbold"
}

# step prints a step done, after the marker flow's notices have.
step() {
	printf '■ %s\n' "$1"
	stepped=true
}

banner

for tool in curl tar; do
	command -v "$tool" >/dev/null 2>&1 || fail "missing required tool: $tool"
done

# The platform as flow's releases name it: the system as Go names it and the
# CPU as npm does.
raw_os=$(uname -s)
case $raw_os in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN*) os=windows ;;
	*) fail "unsupported system: $raw_os" ;;
esac
case $(uname -m) in
	x86_64 | amd64) arch=x64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported arch: $(uname -m)" ;;
esac
# A shell that Rosetta, or Windows on ARM, runs as x64 runs on arm64.
if [ "$os" = darwin ] && [ "$arch" = x64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi
if [ "$os" = windows ] && [ "$arch" = x64 ]; then
	case "$raw_os ${PROCESSOR_IDENTIFIER:-}" in *-ARM64* | *" ARM"*) arch=arm64 ;; esac
fi
platform=$os-$arch

exe=flow
asset=flow-$platform.tar.gz
if [ "$os" = windows ]; then
	exe=flow.exe
	asset=flow-$platform.zip
	command -v unzip >/dev/null 2>&1 || fail "missing required tool: unzip"
fi
step "Detected $os/$arch"

tmp=$(mktemp -d)
# staging is the new binary beside the installed one until it is renamed over
# it, which an exit removes. A kill leaves it, for the next install, or flow's
# own update, which names its new binary alike, to remove. curl_pid is the
# download in progress, which an exit stops, and shows the cursor again.
staging= curl_pid=
trap 'rm -rf "$tmp"; [ -z "$staging" ] || rm -f "$staging"; [ -z "$curl_pid" ] || kill "$curl_pid" 2>/dev/null; [ "$tty" = false ] || printf "\033[?25h"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# The downloads below say why they failed in failure, and return 2 when the
# server did not answer, after which download tries the mirror, and 1 when
# it answered with something wrong, after which it gives up.
failure=

# fetch downloads $1 into $2.
fetch() {
	curl -fsL --retry 2 --connect-timeout 10 -o "$2" "$1" || {
		failure="download failed: $1"
		return 2
	}
}

# fields prints the "key value" of each string field of the JSON file $1, one
# a line, whatever its layout.
fields() {
	tr ',{}[]' '\n\n\n\n\n' <"$1" |
		sed -n 's/^[[:space:]]*"\([a-z_]*\)"[[:space:]]*:[[:space:]]*"\([^"]*\)"[[:space:]]*$/\1 \2/p'
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	else
		openssl dgst -sha256 "$1" | sed 's/^.*= //'
	fi
}

# progress downloads $1 into $2 under the step "Downloading package...", with
# the URL and how much of it has come under it, its marker blinking every
# 500 ms as flow's markers blink while they run, and clears the two lines
# once it is done. Where standard output is no terminal it prints them once.
progress() {
	if [ "$tty" = false ]; then
		step "Downloading package..."
		printf '  %s\n' "$1"
		fetch "$1" "$2"
		return
	fi
	# The URL fits the terminal on its line, shortened from the left, so that
	# each draw goes back over two lines.
	cols=$(stty size </dev/tty 2>/dev/null | awk '{ print $2 }') || cols=
	shown_url=$(printf '%s' "$1" | awk -v max=$((${cols:-80} - 10)) '{
		print (length($0) > max && max > 3) ? "..." substr($0, length($0) - max + 4) : $0
	}')
	curl -fsL --retry 2 --connect-timeout 10 -D "$2.headers" -o "$2" "$1" 2>/dev/null &
	curl_pid=$!
	printf '\033[?25l'
	tick=0
	while :; do
		running=true
		kill -0 "$curl_pid" 2>/dev/null || running=false
		size=$(grep -i '^content-length:' "$2.headers" 2>/dev/null | tail -n 1 | tr -dc '0-9') || size=
		got=0
		[ ! -f "$2" ] || got=$(wc -c <"$2" | tr -dc '0-9')
		percent=0
		[ -z "$size" ] || [ "$size" -eq 0 ] || percent=$((got * 100 / size))
		[ "$percent" -le 100 ] || percent=100
		mark='■'
		[ $((tick / 5 % 2)) -eq 0 ] || mark=' '
		[ "$tick" -eq 0 ] || printf '\033[2A'
		printf '\r\033[K%s Downloading package...\n\r\033[K  %s [%s%%]\n' "$mark" "$shown_url" "$percent"
		[ "$running" = true ] || break
		tick=$((tick + 1))
		sleep 0.1 2>/dev/null || sleep 1
	done
	status=0
	wait "$curl_pid" || status=$?
	curl_pid=
	printf '\033[2A\r\033[J\033[?25h'
	if [ "$status" -ne 0 ]; then
		failure="download failed: $1"
		return 2
	fi
}

# from_github downloads the release asset of the platform into $tmp/$asset,
# checked against the digest GitHub reports for it, and sets tag.
from_github() {
	if [ "$version" = latest ]; then
		release=$github_api/repos/$repo/releases/latest
	else
		release=$github_api/repos/$repo/releases/tags/$version
	fi
	fetch "$release" "$tmp/release.json" || return
	release=$(fields "$tmp/release.json")
	tag=$(printf '%s\n' "$release" | awk '$1 == "tag_name" { print $2; exit }')
	# Each asset has its digest before its download URL, which ends in its name.
	set -- $(printf '%s\n' "$release" | awk -v suffix="/$asset" '
		$1 == "digest" { digest = $2 }
		$1 == "browser_download_url" {
			if (digest != "" && substr($2, length($2) - length(suffix) + 1) == suffix) {
				print digest, $2
				exit
			}
			digest = ""
		}
	')
	if [ -z "$tag" ] || [ $# -ne 2 ]; then
		failure="release ${tag:-$version} has no build: $asset"
		return 1
	fi
	progress "$2" "$tmp/$asset" || return
	if [ "sha256:$(sha256 "$tmp/$asset")" != "$1" ]; then
		failure="digest mismatch: $asset"
		return 1
	fi
}

# from_mirror downloads the asset of the platform from the mirror into
# $tmp/$asset, checked against the mirror's SHA256SUMS, and sets tag.
from_mirror() {
	tag=$version
	if [ "$version" = latest ]; then
		# The mirror's latest is the latest release as JSON, its tag first.
		fetch "$mirror/latest" "$tmp/latest" || return
		tag=$(fields "$tmp/latest" | awk '$1 == "tag" { print $2; exit }')
		case $tag in
			v[0-9]*) ;;
			*)
				failure="download failed: $mirror/latest"
				return 2
				;;
		esac
	fi
	fetch "$mirror/$tag/SHA256SUMS" "$tmp/SHA256SUMS" || return
	sum=$(awk -v asset="$asset" '{ name = $2; sub(/^\*/, "", name) } name == asset { print $1; exit }' "$tmp/SHA256SUMS")
	if [ -z "$sum" ]; then
		failure="release $tag has no build: $asset"
		return 1
	fi
	progress "$mirror/$tag/$asset" "$tmp/$asset" || return
	if [ "$(sha256 "$tmp/$asset")" != "$sum" ]; then
		failure="digest mismatch: $asset"
		return 1
	fi
}

# unpack takes the flow binary out of $tmp/$asset into $tmp/flow, and checks
# that it is the version it was released as.
unpack() {
	mkdir -p "$tmp/unpacked"
	if [ "$os" = windows ]; then
		unzip -o -q "$tmp/$asset" "$exe" -d "$tmp/unpacked" || { failure="unpack failed: $asset"; return 1; }
	else
		tar -xzf "$tmp/$asset" -C "$tmp/unpacked" "$exe" || { failure="unpack failed: $asset"; return 1; }
	fi
	mv "$tmp/unpacked/$exe" "$tmp/flow"
	chmod 755 "$tmp/flow"
	built=$("$tmp/flow" --version 2>/dev/null | tail -n 1) || built=
	# flow --version prints "flow vX.Y.Z [os/arch]"; older releases leave out
	# the platform.
	built=${built%% \[*}
	if [ "$built" != "flow $tag" ]; then
		built=${built#flow }
		failure="version mismatch: $asset is ${built:-unknown}, not $tag"
		return 1
	fi
}

# download downloads and unpacks the release from GitHub, or, when GitHub
# does not answer and FLOW_MIRROR_URL names a mirror, from the mirror, and
# tells what failed.
download() {
	status=0
	from_github || status=$?
	if [ "$status" -eq 2 ] && [ -n "$mirror" ]; then
		github_failure=$failure
		status=0
		from_mirror || status=$?
		if [ "$status" -ne 0 ]; then
			error "$github_failure"
			error "$failure"
			return 1
		fi
	elif [ "$status" -ne 0 ]; then
		error "$failure"
		return 1
	fi
	unpack || {
		error "$failure"
		return 1
	}
}

download || exit 1
step "Package downloaded and verified"

mkdir -p "$install_dir"
rm -f "$install_dir"/.flow-new-*
staging=$install_dir/.flow-new-$$
cp "$tmp/flow" "$staging"
chmod 755 "$staging"
# Windows renames a running flow aside, as it cannot replace it.
if [ "$os" = windows ] && [ -e "$install_dir/$exe" ]; then
	mv -f "$install_dir/$exe" "$install_dir/$exe.old"
fi
mv -f "$staging" "$install_dir/$exe"
staging=
: >"$install_dir/$marker"
step "Package installed successfully"

# flow runs from the link in ~/.local/bin, where other tools are too, unless
# that holds another flow, which it leaves; Windows runs it from ~/.flow/bin.
path_dir=$install_dir
if [ "$os" != windows ]; then
	mkdir -p "$link_dir"
	if [ -L "$link_dir/flow" ] || [ ! -e "$link_dir/flow" ]; then
		ln -sf "$install_dir/$exe" "$link_dir/flow"
		path_dir=$link_dir
		step "Symlink created"
	fi
fi

if [ -n "${GITHUB_PATH:-}" ]; then
	printf '%s\n' "$path_dir" >>"$GITHUB_PATH"
fi

case ":$PATH:" in
	*":$path_dir:"*) on_path=true ;;
	*) on_path=false ;;
esac
if [ "$on_path" = false ] && [ "$modify_path" = true ]; then
	# The profile that the login shell reads, as Homebrew documents it.
	shell=${SHELL:-sh}
	case "$os:${shell##*/}" in
		*:fish) profile=$HOME/.config/fish/config.fish line="fish_add_path $path_dir" ;;
		darwin:zsh) profile=$HOME/.zprofile line="export PATH=\"$path_dir:\$PATH\"" ;;
		darwin:bash) profile=$HOME/.bash_profile line="export PATH=\"$path_dir:\$PATH\"" ;;
		*:zsh) profile=${ZDOTDIR:-$HOME}/.zshrc line="export PATH=\"$path_dir:\$PATH\"" ;;
		*:bash) profile=$HOME/.bashrc line="export PATH=\"$path_dir:\$PATH\"" ;;
		*) profile=$HOME/.profile line="export PATH=\"$path_dir:\$PATH\"" ;;
	esac
	if ! grep -qsF '# >>> flow >>>' "$profile"; then
		mkdir -p "$(dirname "$profile")"
		printf '\n# >>> flow >>>\n%s\n# <<< flow <<<\n' "$line" >>"$profile"
	fi
	# PowerShell and cmd read the user's Path, which Git Bash does not set.
	if [ "$os" = windows ] && command -v powershell.exe >/dev/null 2>&1 && command -v cygpath >/dev/null 2>&1; then
		win_dir=$(cygpath -w "$install_dir")
		powershell.exe -NoProfile -NonInteractive -Command "
			\$p = [Environment]::GetEnvironmentVariable('Path', 'User')
			if ((\"\$p\" -split ';') -notcontains '$win_dir') {
				[Environment]::SetEnvironmentVariable('Path', ((\"\$p\".TrimEnd(';'), '$win_dir') -join ';').TrimStart(';'), 'User')
			}" >/dev/null 2>&1 || true
	fi
fi

# How to start flow, as flow prints how to resume a session: the command bold
# and orange.
printf '\n%sInstallation complete%s\n\n%sStart flow agent with:%s\n  %s%sflow%s\n' \
	"$bold" "$unbold" "$bold" "$unbold" "$bold" "$orange" "$reset"

}

main "$@"
