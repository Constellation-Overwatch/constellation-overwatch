#!/bin/sh
set -eu

install_root=${OVERWATCH_INSTALL_ROOT:-"$HOME/.local/opt/constellation-overwatch"}
unit="$HOME/.config/systemd/user/constellation-overwatch.service"

if command -v systemctl >/dev/null 2>&1; then
	systemctl --user disable --now constellation-overwatch.service >/dev/null 2>&1 || true
fi
rm -f "$unit" "$install_root/current" "$install_root/current.next"
if command -v systemctl >/dev/null 2>&1; then
	systemctl --user daemon-reload
fi

printf '%s\n' \
	"Constellation Overwatch service activation was removed." \
	"Installed releases, configuration, secrets, database, JetStream data, and backups were preserved." \
	"Delete those paths manually only after verifying backups."
