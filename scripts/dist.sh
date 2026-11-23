#!/bin/sh
# dist.sh builds a pmin program for each target and packs one archive each,
# with the licences of everything compiled in, and a SHA256SUMS file. It is
# the same in every program: pmin/templates/program/dist.sh is the original,
# and pmin/scripts/check-program.py fails when a copy differs.
#
#   NAME=jobwatch VERSION=0.2.0 scripts/dist.sh
#   TARGETS="linux/amd64 darwin/arm64" NAME=cairn scripts/dist.sh
#
# NAME      the command (required); built from ./cmd/$NAME unless CMD is set
# VERSION   goes into main.Version; default from git describe
# TARGETS   default linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
# FILES     extra files to ship at the archive's top (README.md and LICENSE
#           always go in)
#
# Each archive NAME-VERSION-OS-ARCH.tar.gz holds one folder with the
# program, README.md, LICENSE, FILES, and THIRD_PARTY/<module>/ with each
# compiled-in module's LICENSE and NOTICE, since Apache-2.0 asks for those
# with a binary. Unpack it and run `./NAME install`.
set -eu

: "${NAME:?set NAME to the command's name}"
cmd=${CMD:-./cmd/$NAME}
version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')}
targets=${TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"}
files=${FILES:-}
out=${OUT:-dist}

rm -rf "$out"
mkdir -p "$out"

# the modules compiled into the program, and where their files are (a
# vendored module has no folder of its own, so look in vendor/)
third=$(mktemp -d)
trap 'rm -rf "$third"' EXIT
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' "$cmd" | sort -u |
while read -r path dir; do
	[ -n "$dir" ] || dir="vendor/$path"
	[ -d "$dir" ] || continue
	found=
	for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE*; do
		[ -f "$f" ] || continue
		mkdir -p "$third/$path"
		cp "$f" "$third/$path/"
		found=1
	done
	if [ -z "$found" ]; then
		echo "dist: $path has no licence file" >&2
		exit 1
	fi
done

for t in $targets; do
	os=${t%/*}
	arch=${t#*/}
	exe=$NAME
	[ "$os" = windows ] && exe=$NAME.exe
	name="$NAME-$version-$os-$arch"
	dir="$out/$name"
	mkdir -p "$dir"
	GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.Version=$version" -o "$dir/$exe" "$cmd"
	cp README.md LICENSE "$dir/"
	for f in $files; do cp "$f" "$dir/"; done
	cp -R "$third" "$dir/THIRD_PARTY"
	tar -C "$out" -czf "$out/$name.tar.gz" "$name"
	rm -rf "$dir"
done

(cd "$out" && sha256sum ./*.tar.gz | sed 's| \./| |' > SHA256SUMS)
printf "dist:"; for f in "$out"/*; do printf " %s" "${f##*/}"; done; echo
