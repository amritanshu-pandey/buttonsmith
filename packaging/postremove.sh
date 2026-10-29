#!/bin/sh
# deb: $1 = "remove", "purge" or "upgrade"; rpm: $1 = 0 (remove) or 1 (upgrade).
# The per-mouse rule is written by `buttonsmith allow-device`, not packaged.
if [ "$1" = 0 ] || [ "$1" = purge ]; then
    rm -f /etc/udev/rules.d/71-buttonsmith-device.rules
fi
if command -v udevadm >/dev/null 2>&1; then
    udevadm control --reload-rules || true
fi
exit 0
