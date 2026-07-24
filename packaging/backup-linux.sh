#!/bin/sh
set -eu
umask 077

data_dir=${1:-}
backup_dir=${2:-}
service_name=${3:-constellation-overwatch.service}
offline_maintenance=${OVERWATCH_OFFLINE_MAINTENANCE:-0}

validate_absolute_directory() {
	name=$1
	value=$2
	case "$value" in
		""|/|*/|*"/../"*|*"/.."|../*|..)
			echo "$name must be a specific absolute directory: $value" >&2
			exit 1
			;;
		/*) ;;
		*)
			echo "$name must be absolute: $value" >&2
			exit 1
			;;
	esac
}

validate_absolute_directory data_dir "$data_dir"
validate_absolute_directory backup_dir "$backup_dir"
case "$service_name" in ""|-*|*[!A-Za-z0-9_.@-]*) echo "invalid service name: $service_name" >&2; exit 1 ;; esac
case "$offline_maintenance" in 0|1) ;; *) echo "OVERWATCH_OFFLINE_MAINTENANCE must be 0 or 1" >&2; exit 1 ;; esac
case "$backup_dir/" in "$data_dir/"*) echo "backup_dir must be outside data_dir" >&2; exit 1 ;; esac
case "$data_dir/" in "$backup_dir/"*) echo "data_dir must be outside backup_dir" >&2; exit 1 ;; esac
if [ ! -d "$data_dir" ]; then
	echo "data directory does not exist: $data_dir" >&2
	exit 1
fi
if [ -L "$data_dir" ]; then
	echo "data directory must not be a symbolic link: $data_dir" >&2
	exit 1
fi

mkdir -p "$backup_dir"
chmod 700 "$backup_dir"
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$$
archive="$backup_dir/constellation-overwatch-$stamp.tar.gz"
archive_tmp="$archive.tmp"
was_active=0
backup_complete=0

restart_if_needed() {
	status=$?
	restart_status=0
	trap - EXIT HUP INT TERM
	if [ "$backup_complete" -ne 1 ]; then
		rm -f "$archive" "$archive.sha256" "$archive.sha256.tmp" \
			"$archive.meta" "$archive.meta.tmp"
	fi
	if [ "$was_active" -eq 1 ]; then
		if ! systemctl --user start "$service_name"; then
			echo "backup completed but $service_name failed to restart" >&2
			restart_status=1
		fi
	fi
	rm -f "$archive_tmp"
	if [ "$status" -eq 0 ] && [ "$restart_status" -ne 0 ]; then
		exit "$restart_status"
	fi
	exit "$status"
}
handle_signal() {
	trap - HUP INT TERM
	exit 130
}
trap restart_if_needed EXIT
trap handle_signal HUP INT TERM

if command -v systemctl >/dev/null 2>&1; then
	load_state=$(systemctl --user show "$service_name" --property=LoadState --value 2>/dev/null || true)
	if [ "$load_state" != "loaded" ] && [ "$offline_maintenance" -ne 1 ]; then
		echo "service manager cannot verify $service_name; set OVERWATCH_OFFLINE_MAINTENANCE=1 only after independently stopping the process" >&2
		exit 1
	fi
elif [ "$offline_maintenance" -ne 1 ]; then
	echo "systemctl is unavailable; set OVERWATCH_OFFLINE_MAINTENANCE=1 only after independently stopping the process" >&2
	exit 1
fi

if command -v systemctl >/dev/null 2>&1 &&
	systemctl --user is-active --quiet "$service_name"; then
	was_active=1
	systemctl --user stop "$service_name"
	if systemctl --user is-active --quiet "$service_name"; then
		echo "service did not stop; refusing inconsistent backup" >&2
		exit 1
	fi
fi

tar -C "$(dirname "$data_dir")" -czf "$archive_tmp" "$(basename "$data_dir")"
chmod 600 "$archive_tmp"
mv "$archive_tmp" "$archive"
(
	cd "$backup_dir"
	sha256sum "$(basename "$archive")" >"$(basename "$archive").sha256.tmp"
	mv "$(basename "$archive").sha256.tmp" "$(basename "$archive").sha256"
)
printf 'created_at=%s\ndata_dir=%s\nservice=%s\n' "$stamp" "$data_dir" "$service_name" >"$archive.meta.tmp"
chmod 600 "$archive.meta.tmp"
mv "$archive.meta.tmp" "$archive.meta"
sync
backup_complete=1

echo "$archive"
