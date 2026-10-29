#!/usr/bin/env bash
# Build and install mousefix for the current user.
set -euo pipefail
cd "$(dirname "$0")"

bin="$HOME/.local/bin/mousefix"
cfg="${XDG_CONFIG_HOME:-$HOME/.config}/mousefix/config.toml"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
rule=/etc/udev/rules.d/70-mousefix.rules

echo "==> building"
go build -o mousefix ./cmd/mousefix
install -Dm755 mousefix "$bin"

if [[ ! -e "$cfg" ]]; then
    echo "==> writing default config to $cfg"
    mkdir -p "$(dirname "$cfg")"
    "$bin" example-config > "$cfg"
fi

if ! cmp -s contrib/70-mousefix.rules "$rule"; then
    echo "==> installing udev rule $rule (needs sudo)"
    sudo install -Dm644 contrib/70-mousefix.rules "$rule"
    sudo udevadm control --reload-rules
    sudo udevadm trigger --subsystem-match=input --subsystem-match=misc --action=change
fi

echo "==> installing systemd user service"
install -Dm644 contrib/mousefix.service "$unit_dir/mousefix.service"
systemctl --user daemon-reload
systemctl --user enable mousefix.service
systemctl --user restart mousefix.service

echo "==> adding mousefix to the application menu"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps"
sed "s|@BIN@|$bin|" contrib/mousefix.desktop > "$apps/mousefix.desktop"

echo
echo "Done. Open \"mousefix\" from the application menu (or run: mousefix ui)."
echo "Logs: journalctl --user -u mousefix -f"
