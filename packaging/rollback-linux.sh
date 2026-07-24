#!/bin/sh
set -eu

install_root=${OVERWATCH_INSTALL_ROOT:-"$HOME/.local/opt/constellation-overwatch"}
target=${1:-}
if [ -z "$target" ] && [ -f "$install_root/previous-release" ]; then
	target=$(cat "$install_root/previous-release")
fi
case "$target" in
	""|*/*|*".."*|*[!A-Za-z0-9._+-]*)
		echo "usage: $0 RELEASE_ID" >&2
		exit 1
		;;
esac

target_dir="$install_root/releases/$target"
if [ ! -x "$target_dir/overwatch" ]; then
	echo "rollback target is not an installed release: $target_dir" >&2
	exit 1
fi

old_target=
if [ -L "$install_root/current" ]; then
	old_target=$(basename "$(readlink "$install_root/current")")
fi
ln -s "$target_dir" "$install_root/current.next"
mv -Tf "$install_root/current.next" "$install_root/current"
if [ -n "$old_target" ]; then
	printf '%s\n' "$old_target" >"$install_root/previous-release"
fi

if command -v systemctl >/dev/null 2>&1 &&
	systemctl --user is-active --quiet constellation-overwatch.service; then
	systemctl --user restart constellation-overwatch.service
fi

echo "Activated rollback release $target."
