#!/usr/bin/env bash
# Build and install Buttonsmith for the current user.
set -euo pipefail
cd "$(dirname "$0")"

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

# Migrate from the old name ("mousefix").
if systemctl --user cat mousefix.service &>/dev/null; then
    echo "==> removing the old mousefix service"
    systemctl --user disable --now mousefix.service || true
fi
rm -f "$unit_dir/mousefix.service" "$HOME/.local/bin/mousefix" "$apps/mousefix.desktop"
if [[ -d "$config_home/mousefix" && ! -e "$config_home/$id" ]]; then
    echo "==> moving config $config_home/mousefix -> $config_home/$id"
    mv "$config_home/mousefix" "$config_home/$id"
fi

if [[ ! -e "$cfg" ]]; then
    echo "==> writing default config to $cfg"
    mkdir -p "$(dirname "$cfg")"
    "$bin" example-config > "$cfg"
fi

if ! cmp -s "contrib/70-$id.rules" "$rule" || [[ -e /etc/udev/rules.d/70-mousefix.rules ]]; then
    echo "==> installing udev rule $rule (needs sudo)"
    sudo install -Dm644 "contrib/70-$id.rules" "$rule"
    sudo rm -f /etc/udev/rules.d/70-mousefix.rules
    sudo udevadm control --reload-rules
    sudo udevadm trigger --subsystem-match=input --subsystem-match=misc --action=change
fi

echo "==> installing systemd user service"
install -Dm644 "contrib/$id.service" "$unit_dir/$id.service"
systemctl --user daemon-reload
systemctl --user enable "$id.service"
systemctl --user restart "$id.service"

echo "==> adding Buttonsmith to the application menu"
install -Dm644 internal/ui/icon.svg "$icon"
mkdir -p "$apps"
sed "s|@BIN@|$bin|" "contrib/$appid.desktop" > "$apps/$appid.desktop"
command -v kbuildsycoca6 >/dev/null && kbuildsycoca6 >/dev/null 2>&1 || true

echo
echo "Done. Open \"Buttonsmith\" from the application menu (or run: $id ui)."
echo "Logs: journalctl --user -u $id -f"
