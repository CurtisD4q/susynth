#!/bin/sh
# Installs susynth on this machine: downloads the latest release for it,
# checks it against the release's SHA256SUMS, and runs `susynth install`,
# which puts susynth in ~/.local/bin. Nothing is left behind but the
# installed program; run `susynth uninstall` to remove it.
#
#   sh install.sh                 for you, in ~/.local/bin
#   sh install.sh --system        for everyone, in /usr/local (asks for sudo)
#   VERSION=0.2.0 sh install.sh   a given release instead of the latest
#
# It uses the GitHub CLI (gh) when you are logged in, which also works while
# the repository is private, and curl otherwise.
set -eu

name=susynth
repo=CurtisD4q/susynth

say() { printf '%s\n' "$*" >&2; }
die() { say "install: $*"; exit 1; }

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "no release for $(uname -s); build from source instead (see the README)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "no release for $(uname -m); build from source instead (see the README)" ;;
esac

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	via=gh
elif command -v curl >/dev/null 2>&1; then
	via=curl
else
	die "needs gh (logged in) or curl"
fi

tag=${VERSION:+v${VERSION#v}}
if [ -z "$tag" ]; then
	if [ $via = gh ]; then
		tag=$(gh api "repos/$repo/releases/latest" --jq .tag_name) || die "cannot read the latest release of $repo"
	else
		tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
		[ -n "$tag" ] || die "cannot read the latest release of $repo (if it is private, log in with gh auth login and run this again)"
	fi
fi
v=${tag#v}
archive="$name-$v-$os-$arch.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading $name $v for $os/$arch…"
if [ $via = gh ]; then
	gh release download "$tag" --repo "$repo" --dir "$tmp" --pattern "$archive" --pattern SHA256SUMS || die "download failed"
else
	base="https://github.com/$repo/releases/download/$tag"
	curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "download failed"
	curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "download failed"
fi

want=$(grep "  $archive\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
[ -n "$want" ] || die "$archive is not in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
[ "$got" = "$want" ] || die "$archive does not match its checksum; nothing was installed"

tar -C "$tmp" -xzf "$tmp/$archive"
bin="$tmp/$name-$v-$os-$arch/$name"
case " $* " in
*" --system "*)
	if [ "$(id -u)" != 0 ]; then
		say "Installing for everyone needs the administrator: running sudo $name install $*"
		sudo "$bin" install "$@"
		exit
	fi
	;;
esac
"$bin" install "$@"
