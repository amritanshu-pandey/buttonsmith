#!/bin/sh
# deb: $1 = "configure" [old version]; rpm: $1 = 1 (install) or 2 (upgrade).

# Apply the udev rule to devices that are already plugged in.
if command -v udevadm >/dev/null 2>&1; then
    udevadm control --reload-rules || true
    udevadm trigger --subsystem-match=input --subsystem-match=misc --action=change || true
fi

if [ "$1" = 1 ] || { [ "$1" = configure ] && [ -z "$2" ]; }; then
    # Start the remapper in every user's graphical session from now on.
    systemctl --global enable buttonsmith.service >/dev/null 2>&1 || true
    echo "Buttonsmith: log out and back in, or open Buttonsmith and press Start."
else
    echo "Buttonsmith: restart the service to use the new version: systemctl --user restart buttonsmith"
fi
exit 0
