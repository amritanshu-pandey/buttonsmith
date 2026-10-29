#!/usr/bin/env bash
# Build and install Buttonsmith for the current user, into ~/.local.
# For distros without .deb/.rpm packages, and for development.
set -euo pipefail
cd "$(dirname "$0")/.."

id=buttonsmith
appid=io.github.amritanshu_pandey.Buttonsmith
config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
bin="$HOME/.local/bin/$id"
cfg="$config_home/$id/config.toml"
unit_dir="$config_home/systemd/user"
apps="$data_home/applications"
icon="$data_home/icons/hicolor/scalable/apps/$appid.svg"
rule="/etc/udev/rules.d/70-$id.rules"

echo "==> building"
go build -o "$id" "./cmd/$id"
install -Dm755 "$id" "$bin"

if [[ ! -e "$cfg" ]]; then
    echo "==> writing default config to $cfg"
    mkdir -p "$(dirname "$cfg")"
    "$bin" example-config > "$cfg"
fi

if ! cmp -s "contrib/70-$id.rules" "$rule"; then
    echo "==> installing udev rule $rule (needs sudo)"
    sudo install -Dm644 "contrib/70-$id.rules" "$rule"
    sudo udevadm control --reload-rules
    sudo udevadm trigger --subsystem-match=input --subsystem-match=misc --action=change
fi

echo "==> installing systemd user service"
mkdir -p "$unit_dir"
sed "s|@BIN@|$bin|" "contrib/$id.service" > "$unit_dir/$id.service"
systemctl --user daemon-reload
systemctl --user enable "$id.service"
systemctl --user restart "$id.service"

echo "==> adding Buttonsmith to the application menu"
install -Dm644 internal/ui/icon.svg "$icon"
mkdir -p "$apps"
sed "s|@BIN@|$bin|" "contrib/$appid.desktop" > "$apps/$appid.desktop"
command -v kbuildsycoca6 >/dev/null && kbuildsycoca6 >/dev/null 2>&1 || true
# Running KDE apps (panel, menu, task switcher) only scan icon folders that
# existed when they started; ask them to rescan so the new icon shows up.
command -v dbus-send >/dev/null &&
	dbus-send --session --type=signal /KIconLoader org.kde.KIconLoader.iconChanged int32:0 || true

echo
echo "Done. Open \"Buttonsmith\" from the application menu (or run: $id ui)."
echo "Logs: journalctl --user -u $id -f"
