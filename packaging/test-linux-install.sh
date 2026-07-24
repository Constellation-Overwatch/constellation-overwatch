#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp_base=${TMPDIR:-/tmp}
case "$tmp_base" in
	""|/|*"/../"*|*"/.."|../*|..) echo "unsafe temporary base: $tmp_base" >&2; exit 1 ;;
	/*) ;;
	*) echo "temporary base must be absolute: $tmp_base" >&2; exit 1 ;;
esac
test_root=$(mktemp -d "$tmp_base/constellation-overwatch-install.XXXXXX")
case "$test_root" in "$tmp_base"/constellation-overwatch-install.*) ;;
	*) echo "refusing unexpected temporary path: $test_root" >&2; exit 1 ;;
esac
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

bundle_root="$test_root/bundle"
bundle_packaging="$bundle_root/packaging"
fake_bin="$test_root/fake-bin"
test_home="$test_root/home"
install_root="$test_home/.local/opt/constellation-overwatch"
config_root="$test_home/.config/constellation-overwatch"
unit_root="$test_home/.config/systemd/user"
real_mv=$(command -v mv)
mkdir -p "$bundle_packaging" "$fake_bin" "$test_home"
cp "$script_dir/install-linux.sh" "$script_dir/constellation-overwatch.service" \
	"$script_dir/overwatch.env.example" "$bundle_packaging/"

cat >"$bundle_root/overwatch" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "version" ]; then
	echo "overwatch synthetic"
	exit 0
fi
exit 2
EOF
chmod 755 "$bundle_root/overwatch"

cat >"$fake_bin/systemctl" <<'EOF'
#!/bin/sh
test "${1:-}" = "--user"
test "${2:-}" = "daemon-reload"
EOF
cat >"$fake_bin/mv" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = "-Tf" ]; then
	shift
	source=$1
	destination=$2
	case "$destination" in "$FAKE_INSTALL_ROOT"/current) ;;
		*) echo "refusing unexpected synthetic mv target: $destination" >&2; exit 1 ;;
	esac
	rm -f "$destination"
	exec "$REAL_MV" "$source" "$destination"
fi
exec "$REAL_MV" "$@"
EOF
chmod 755 "$fake_bin/systemctl" "$fake_bin/mv"

run_installer() {
	release_id=$1
	PATH="$fake_bin:$PATH" \
		HOME="$test_home" \
		REAL_MV="$real_mv" \
		FAKE_INSTALL_ROOT="$install_root" \
		OVERWATCH_INSTALL_ROOT="$install_root" \
		OVERWATCH_CONFIG_ROOT="$config_root" \
		OVERWATCH_SYSTEMD_USER_ROOT="$unit_root" \
		"$bundle_packaging/install-linux.sh" "$release_id"
}

fresh_output=$(run_installer v-test-fresh)
test -x "$install_root/releases/v-test-fresh/overwatch"
cmp "$bundle_packaging/constellation-overwatch.service" \
	"$unit_root/constellation-overwatch.service"
printf '%s\n' "$fresh_output" | grep -q "Installed service unit"

cat >"$unit_root/constellation-overwatch.service" <<'EOF'
[Service]
EnvironmentFile=%h/.config/constellation-overwatch/production.env
EnvironmentFile=%h/.config/constellation-overwatch/secret.env
ExecStart=%h/.local/opt/constellation-overwatch/current/overwatch start
EOF
before_checksum=$(sha256sum "$unit_root/constellation-overwatch.service")
upgrade_output=$(run_installer v-test-upgrade)
after_checksum=$(sha256sum "$unit_root/constellation-overwatch.service")
test "$before_checksum" = "$after_checksum"
cmp "$bundle_packaging/constellation-overwatch.service" \
	"$unit_root/constellation-overwatch.service.packaged"
test "$(readlink "$install_root/current")" = "$install_root/releases/v-test-upgrade"
test "$(cat "$install_root/previous-release")" = "v-test-fresh"
printf '%s\n' "$upgrade_output" | grep -q "Preserved existing service unit"
