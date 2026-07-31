#!/bin/sh
set -eu
umask 077

archive=${1:-}
data_dir=${2:-}
service_name=${3:-constellation-overwatch.service}
confirmation=${4:-}
offline_maintenance=${OVERWATCH_OFFLINE_MAINTENANCE:-0}

if [ "$confirmation" != "--confirm-restore" ]; then
	echo "usage: $0 ARCHIVE ABSOLUTE_DATA_DIR [SERVICE] --confirm-restore" >&2
	exit 1
fi
case "$archive" in
	""|/|*/|*"/../"*|*"/.."|../*|..) echo "unsafe archive path: $archive" >&2; exit 1 ;;
	/*) ;;
	*) echo "archive path must be absolute: $archive" >&2; exit 1 ;;
esac
case "$data_dir" in
	""|/|*/|*"/../"*|*"/.."|../*|..) echo "unsafe data directory: $data_dir" >&2; exit 1 ;;
	/*) ;;
	*) echo "data directory must be absolute: $data_dir" >&2; exit 1 ;;
esac
case "$service_name" in ""|-*|*[!A-Za-z0-9_.@-]*) echo "invalid service name: $service_name" >&2; exit 1 ;; esac
case "$offline_maintenance" in 0|1) ;; *) echo "OVERWATCH_OFFLINE_MAINTENANCE must be 0 or 1" >&2; exit 1 ;; esac
if [ ! -f "$archive" ] || [ ! -f "$archive.sha256" ]; then
	echo "archive or checksum is missing: $archive" >&2
	exit 1
fi
if [ -L "$archive" ] || [ -L "$archive.sha256" ]; then
	echo "archive and checksum must not be symbolic links" >&2
	exit 1
fi
if [ -L "$data_dir" ]; then
	echo "data directory must not be a symbolic link: $data_dir" >&2
	exit 1
fi
if [ ! -d "$(dirname "$data_dir")" ]; then
	echo "data parent directory does not exist: $(dirname "$data_dir")" >&2
	exit 1
fi
(
	cd "$(dirname "$archive")"
	sha256sum -c "$(basename "$archive").sha256"
)

archive_root=$(basename "$data_dir")
if ! tar -tzf "$archive" | awk -v root="$archive_root" '
	BEGIN { ok = 1 }
	$0 == root || index($0, root "/") == 1 {
		if ($0 ~ /(^|\/)\.\.(\/|$)/) ok = 0
		next
	}
	{ ok = 0 }
	END { exit ok ? 0 : 1 }
'; then
	echo "archive contains a path outside $archive_root" >&2
	exit 1
fi

was_active=0
restore_started=0
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$$
rollback_dir="$data_dir.restore-rollback-$stamp"
failed_dir="$data_dir.restore-failed-$stamp"
if [ -e "$rollback_dir" ]; then
	echo "rollback directory already exists: $rollback_dir" >&2
	exit 1
fi
if [ -e "$failed_dir" ]; then
	echo "failed-restore directory already exists: $failed_dir" >&2
	exit 1
fi

restore_failed() {
	trap - HUP INT TERM
	if [ "$restore_started" -eq 1 ] || [ "$was_active" -eq 1 ]; then
		if command -v systemctl >/dev/null 2>&1; then
			systemctl --user stop "$service_name" >/dev/null 2>&1 || true
		fi
	fi
	if [ -e "$rollback_dir" ]; then
		if [ -e "$data_dir" ]; then
			mv "$data_dir" "$failed_dir"
		fi
		mv "$rollback_dir" "$data_dir"
	elif [ "$restore_started" -eq 1 ] && [ -e "$data_dir" ]; then
		mv "$data_dir" "$failed_dir"
	fi
	if [ "$was_active" -eq 1 ]; then
		systemctl --user start "$service_name" || true
	fi
}
restore_interrupted() {
	echo "restore interrupted; reactivating prior data" >&2
	restore_failed
	exit 130
}
trap restore_interrupted HUP INT TERM

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
		echo "service did not stop; refusing restore" >&2
		exit 1
	fi
fi

restore_started=1
if [ -e "$data_dir" ]; then
	mv "$data_dir" "$rollback_dir"
fi

if ! tar -C "$(dirname "$data_dir")" -xzf "$archive"; then
	restore_failed
	echo "restore extraction failed; prior data was reactivated" >&2
	exit 1
fi

if [ "$was_active" -eq 1 ]; then
	if ! systemctl --user start "$service_name"; then
		restore_failed
		echo "restored service did not start; prior data was reactivated" >&2
		exit 1
	fi
	health_url=${OVERWATCH_HEALTH_URL:-http://127.0.0.1:8090/health}
	health_attempts=${OVERWATCH_HEALTH_ATTEMPTS:-30}
	health_interval=${OVERWATCH_HEALTH_INTERVAL_SECONDS:-1}
	case "$health_attempts" in ""|0|*[!0-9]*) echo "invalid health attempt count: $health_attempts" >&2; restore_failed; exit 1 ;; esac
	case "$health_interval" in ""|*[!0-9]*) echo "invalid health interval: $health_interval" >&2; restore_failed; exit 1 ;; esac
	healthy=0
	attempt=1
	while [ "$attempt" -le "$health_attempts" ]; do
		if curl --fail --silent "$health_url" >/dev/null; then
			healthy=1
			break
		fi
		sleep "$health_interval"
		attempt=$((attempt + 1))
	done
	if [ "$healthy" -ne 1 ]; then
		restore_failed
		echo "restored service failed health; prior data was reactivated" >&2
		exit 1
	fi
fi

trap - HUP INT TERM
if [ -e "$rollback_dir" ]; then
	echo "Restore succeeded. Prior data retained at: $rollback_dir"
else
	echo "Restore succeeded. No prior data directory existed."
fi
