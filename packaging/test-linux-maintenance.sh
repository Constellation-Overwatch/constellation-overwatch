#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp_base=${TMPDIR:-/tmp}
case "$tmp_base" in
	""|/|*"/../"*|*"/.."|../*|..) echo "unsafe temporary base: $tmp_base" >&2; exit 1 ;;
	/*) ;;
	*) echo "temporary base must be absolute: $tmp_base" >&2; exit 1 ;;
esac
test_root=$(mktemp -d "$tmp_base/constellation-overwatch-maintenance.XXXXXX")
case "$test_root" in "$tmp_base"/constellation-overwatch-maintenance.*) ;;
	*) echo "refusing unexpected temporary path: $test_root" >&2; exit 1 ;;
esac
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

data_dir="$test_root/state"
backup_dir="$test_root/backups"
fake_bin="$test_root/fake-bin"
service_state="$test_root/service-active"
mkdir -p "$data_dir/db" "$data_dir/overwatch/jetstream" "$fake_bin"
printf 'sqlite-state\n' >"$data_dir/db/constellation.db"
printf 'jetstream-state\n' >"$data_dir/overwatch/jetstream/stream.dat"

cat >"$fake_bin/systemctl" <<'EOF'
#!/bin/sh
set -eu
action=${2:-}
case "$action" in
	show) echo loaded ;;
	is-active) test -f "$FAKE_SYSTEMCTL_STATE" ;;
	stop) rm -f "$FAKE_SYSTEMCTL_STATE" ;;
	start) : >"$FAKE_SYSTEMCTL_STATE" ;;
	*) echo "unsupported fake systemctl action: $action" >&2; exit 2 ;;
esac
EOF
cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
exit "${FAKE_CURL_STATUS:-0}"
EOF
chmod 755 "$fake_bin/systemctl" "$fake_bin/curl"

: >"$service_state"
archive=$(PATH="$fake_bin:$PATH" \
	FAKE_SYSTEMCTL_STATE="$service_state" \
	"$script_dir/backup-linux.sh" "$data_dir" "$backup_dir" test.service)
test -f "$archive"
test -f "$archive.sha256"
test -f "$archive.meta"
test -f "$service_state"

printf 'pre-restore-state\n' >"$data_dir/db/constellation.db"
printf 'pre-restore-jetstream\n' >"$data_dir/overwatch/jetstream/stream.dat"
if PATH="$fake_bin:$PATH" \
	FAKE_SYSTEMCTL_STATE="$service_state" \
	FAKE_CURL_STATUS=1 \
	OVERWATCH_HEALTH_ATTEMPTS=1 \
	OVERWATCH_HEALTH_INTERVAL_SECONDS=0 \
	"$script_dir/restore-linux.sh" "$archive" "$data_dir" test.service --confirm-restore; then
	echo "restore unexpectedly succeeded despite failed health check" >&2
	exit 1
fi
test "$(cat "$data_dir/db/constellation.db")" = "pre-restore-state"
test "$(cat "$data_dir/overwatch/jetstream/stream.dat")" = "pre-restore-jetstream"
test -f "$service_state"
find "$test_root" -maxdepth 1 -type d -name 'state.restore-failed-*' | grep -q .

PATH="$fake_bin:$PATH" \
	FAKE_SYSTEMCTL_STATE="$service_state" \
	FAKE_CURL_STATUS=0 \
	"$script_dir/restore-linux.sh" "$archive" "$data_dir" test.service --confirm-restore

test "$(cat "$data_dir/db/constellation.db")" = "sqlite-state"
test "$(cat "$data_dir/overwatch/jetstream/stream.dat")" = "jetstream-state"
find "$test_root" -maxdepth 1 -type d -name 'state.restore-rollback-*' | grep -q .
