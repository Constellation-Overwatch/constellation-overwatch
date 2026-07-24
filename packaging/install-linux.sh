#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bundle_dir=$(dirname "$script_dir")
binary="$bundle_dir/overwatch"
install_root=${OVERWATCH_INSTALL_ROOT:-"$HOME/.local/opt/constellation-overwatch"}
config_root=${OVERWATCH_CONFIG_ROOT:-"$HOME/.config/constellation-overwatch"}
unit_root=${OVERWATCH_SYSTEMD_USER_ROOT:-"$HOME/.config/systemd/user"}

if [ ! -x "$binary" ]; then
	echo "overwatch binary not found next to packaging directory: $binary" >&2
	exit 1
fi

release_id=${1:-$("$binary" version | awk 'NR == 1 { print $2 }')}
case "$release_id" in
	""|*/*|*".."*|*[!A-Za-z0-9._+-]*)
		echo "invalid release id: $release_id" >&2
		exit 1
		;;
esac

release_dir="$install_root/releases/$release_id"
if [ -e "$release_dir" ]; then
	echo "release already exists; refusing to overwrite: $release_dir" >&2
	exit 1
fi

mkdir -p "$release_dir" "$config_root" "$unit_root" "$HOME/.local/share/constellation-overwatch"
chmod 700 "$config_root" "$HOME/.local/share/constellation-overwatch"
install -m 0755 "$binary" "$release_dir/overwatch"
install -m 0644 "$script_dir/overwatch.env.example" "$config_root/overwatch.env.example"
install -m 0644 "$script_dir/constellation-overwatch.service" "$unit_root/constellation-overwatch.service"

if [ -L "$install_root/current" ]; then
	basename "$(readlink "$install_root/current")" >"$install_root/previous-release"
fi
ln -s "$release_dir" "$install_root/current.next"
mv -Tf "$install_root/current.next" "$install_root/current"

if command -v systemctl >/dev/null 2>&1; then
	systemctl --user daemon-reload
fi

printf '%s\n' \
	"Installed $release_id at $release_dir." \
	"Production was not started and no secret file was created or overwritten." \
	"Review $config_root/overwatch.env.example, write $config_root/overwatch.env with mode 0600, then run:" \
	"  systemctl --user enable --now constellation-overwatch.service"
