#!/bin/sh
# deb: $1 = "remove" or "upgrade"; rpm: $1 = 0 (remove) or 1 (upgrade).
if [ "$1" = 0 ] || [ "$1" = remove ]; then
    systemctl --global disable buttonsmith.service >/dev/null 2>&1 || true
fi
exit 0
