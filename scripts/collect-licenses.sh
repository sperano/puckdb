#!/bin/sh

# Copies the license, notice and patent files of everything linked into the
# puckdb binary into DEST. The published image ships that binary, and the
# licenses of the Go toolchain and of most dependencies (BSD, MIT, Apache-2.0
# NOTICE files) require these texts to travel with a binary distribution.
#
#   DEST/go/                    Go standard library and runtime
#   DEST/modules/<path>@<ver>/  files found in each linked package directory
#                               and its parents up to the module root, at the
#                               same relative path
#   .../source/                 full module source, for MPL-2.0 modules only
#                               (MPL-2.0 section 3.2 asks for the source)
#
# Run it from the module root with the GOOS/GOARCH/CGO_ENABLED of the build.
# It fails when a linked module has no license file, so a new dependency
# cannot ship without one.

set -eu

dest=${1:?usage: collect-licenses.sh DEST}

is_license_file() {
	name=$(basename "$1" | tr '[:upper:]' '[:lower:]')
	case $name in
	*.go) return 1 ;;
	licen[cs]e* | copying* | notice* | patents* | unlicense*) return 0 ;;
	esac
	return 1
}

# copy_package_licenses MOD MODDIR PKGDIR copies the license files of PKGDIR
# and of each parent directory up to MODDIR.
copy_package_licenses() {
	mod=$1
	moddir=$2
	dir=$3
	while :; do
		rel=${dir#"$moddir"}
		for f in "$dir"/*; do
			out="$dest/modules/$mod$rel"
			# Sibling packages share parents: copy each file once.
			if [ -f "$f" ] && [ ! -e "$out/${f##*/}" ] && is_license_file "$f"; then
				mkdir -p "$out"
				cp "$f" "$out/"
			fi
		done
		if [ "$dir" = "$moddir" ] || [ "$dir" = / ]; then
			return
		fi
		dir=$(dirname "$dir")
	done
}

mkdir -p "$dest/go"
goroot=$(go env GOROOT)
cp "$goroot/LICENSE" "$goroot/PATENTS" "$dest/go/"

# Captured before filtering: sh has no pipefail to report a go list failure.
listing=$(go list -deps \
	-f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}} {{end}}{{end}}{{.Dir}}' .)
# Standard library and puckdb packages print a bare directory; drop them.
packages=$(echo "$listing" | grep ' ' | sort -u)

echo "$packages" | while read -r mod moddir pkgdir; do
	copy_package_licenses "$mod" "$moddir" "$pkgdir"
done

echo "$packages" | while read -r mod moddir _; do
	echo "$mod $moddir"
done | sort -u | while read -r mod moddir; do
	if [ ! -d "$dest/modules/$mod" ]; then
		echo "collect-licenses: no license file in $mod ($moddir)" >&2
		exit 1
	fi
	if grep -rqs 'Mozilla Public License' "$dest/modules/$mod" &&
		[ ! -d "$dest/modules/$mod/source" ]; then
		cp -R "$moddir" "$dest/modules/$mod/source"
	fi
done

# The module cache is read-only; let the caller delete or overwrite DEST.
chmod -R u+w "$dest"
