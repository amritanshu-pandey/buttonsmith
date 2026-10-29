<p align="center"><img src="internal/ui/icon.svg" alt="Buttonsmith icon" width="128"></p>

<h1 align="center">Buttonsmith</h1>

Buttonsmith remaps mouse and trackball buttons on Linux. It adds
click-and-drag gestures and smooth scrolling. It works on Wayland and is
made for KDE Plasma.

The idea comes from [Mac Mouse Fix](https://github.com/noah-nuebling/mac-mouse-fix)
for macOS. Buttonsmith is an independent project. It is not affiliated
with Mac Mouse Fix or KDE.

## How it works

Wayland does not let applications intercept or inject input. Buttonsmith
works at the kernel level instead:

1. It grabs the evdev device of one mouse, so the compositor no longer
   receives its events.
2. It processes the events: remaps buttons, detects gestures and smooths
   the scroll wheel.
3. It sends the result through two virtual uinput devices, "Buttonsmith
   pointer" and "Buttonsmith keyboard".

All other input devices are left alone. Because this happens below the
compositor, Buttonsmith works with any compositor. Only the `kwin:` actions
and the desktop presets need KDE Plasma.

## Features

- Per button: actions for click, double click and hold.
- Hold a button and move the mouse: scroll in any direction, or run an
  action per direction (for example switch virtual desktop or open the
  overview).
- Hold a button and turn the wheel: horizontal scroll, zoom, precise
  scroll, fast scroll, or an action for up and down.
- Smooth scrolling with acceleration, adjustable speed and direction.
- Actions: key combinations, mouse buttons, shell commands, KWin
  shortcuts and presets for navigation, desktops and media keys.
- A settings window, and a TOML config file for manual editing.

## Requirements

- Linux with uinput and systemd.
- KDE Plasma for the `kwin:` actions and desktop presets. Other actions
  work on any desktop.
- PyQt6 with Qt WebEngine for the native settings window (optional, see
  [Settings window](#settings-window)).
- polkit (`pkexec`) for **Allow access** (optional, see
  [Mouse access](#mouse-access)).
- Go 1.26 or newer to build.

## Install

### Package (Fedora, Debian, Ubuntu)

Build the packages:

```sh
make packages                  # version 0.1.0
make packages VERSION=0.2.0    # or a version you choose
```

This writes `.rpm` and `.deb` packages for amd64 and arm64 to `dist/`. The
packaging tool, [nfpm](https://nfpm.goreleaser.com), is downloaded by `go run`.

Install the package for your system:

```sh
sudo dnf install ./dist/buttonsmith-0.1.0-1.x86_64.rpm    # Fedora
sudo apt install ./dist/buttonsmith_0.1.0_amd64.deb       # Debian, Ubuntu
```

The package installs:

| File | Purpose |
|------|---------|
| `/usr/bin/buttonsmith` | The program |
| `/usr/lib/systemd/user/buttonsmith.service` | User service that runs the remapper |
| `/usr/lib/udev/rules.d/70-buttonsmith.rules` | Access to `/dev/uinput` and the ProtoArc receiver |
| `/usr/share/applications/io.github.amritanshu_pandey.Buttonsmith.desktop` | Menu entry for the settings window |
| `/usr/share/polkit-1/actions/io.github.amritanshu_pandey.Buttonsmith.policy` | Password prompt for **Allow access** |

The package enables the service for all users. It starts at the next
login. To start it now, open **Buttonsmith** and press **Start**, or run
`systemctl --user start buttonsmith`.

After an upgrade, restart the service: `systemctl --user restart buttonsmith`.

To uninstall, run `sudo dnf remove buttonsmith` or `sudo apt remove buttonsmith`.
Your config in `~/.config/buttonsmith` is kept.

### From source, for the current user

Use this on distributions without `.deb` or `.rpm` packages (for example
Arch or Gentoo), or during development:

```sh
./scripts/install.sh
```

The script:

1. Builds `~/.local/bin/buttonsmith`.
2. Writes the default config to `~/.config/buttonsmith/config.toml` if it
   does not exist.
3. Installs `/etc/udev/rules.d/70-buttonsmith.rules`. This step uses sudo.
4. Installs, enables and starts the `buttonsmith` systemd user service.
5. Adds **Buttonsmith** to the application menu.

Run it again after changes to rebuild and restart the service.

Do not use both install methods at the same time. Files in your home
directory take priority over the package.

### After installing

Plasma sees "Buttonsmith pointer" as a new mouse. Set its pointer speed and
acceleration in System Settings > Mouse.

To read the service log, run `journalctl --user -u buttonsmith -f`.

## Mouse access

Buttonsmith needs read access to the mouse it takes over. It does not add
you to the `input` group, because that group can read every keyboard.
Instead, a udev rule gives the logged-in user access to one mouse.

The default rule covers the ProtoArc trackball receiver (`25a7:fa61`),
which is also the device in the default config. For another mouse:

1. Open the **Device** page in the settings window.
2. Press **Allow access** next to the mouse and enter the admin password.
3. Press **Use this**, then **Save**.

**Allow access** writes `/etc/udev/rules.d/71-buttonsmith-device.rules`.
This file holds one mouse; allowing another mouse replaces it. The rule
matches the vendor and product ID, so it works for USB and Bluetooth mice.
From a terminal, run `sudo buttonsmith allow-device VVVV:PPPP`. Use
`buttonsmith list` to find the ID.

## Settings window

Open **Buttonsmith** from the application menu, or run `buttonsmith ui`.

| Page | Settings |
|------|----------|
| Buttons | Add a button with **+ Add button (press it)** or **Add by name**. Choose actions for click, double click, hold, hold + move and hold + wheel. **Record** captures a key combination. |
| Scrolling | Smooth scrolling, speed, acceleration, smoothness, direction |
| Timing | Hold delay, double-click speed, drag threshold, drag step, drag-scroll speed |
| Device | The mouse to take over, and **Allow access** |

The header shows whether the service runs and whether the mouse is
connected and readable. It has buttons to start, stop and restart the
service.

**Save** writes `~/.config/buttonsmith/config.toml`. The service reloads
it within one second. The window writes the file without comments. If the
file was not written by the window, the first save keeps a copy as
`config.toml.bak`.

While the **Add button** dialog is open, the service is paused, so the
dialog can see the button presses.

The window uses PyQt6 with Qt WebEngine. If that is not installed,
Buttonsmith opens the page as a Chrome or Chromium app window, or in the
default browser. The page is served on 127.0.0.1 only and needs a random
token for each session. The server stops when the window closes.

## Config file

You can also edit `~/.config/buttonsmith/config.toml` directly.
`buttonsmith example-config` prints an example with comments for every
option. `buttonsmith check` validates the file.

```toml
[device]
vendor = "25a7"
product = "fa61"

[buttons.back]
click = "back"                  # without click, the button keeps its normal function
hold = "overview"
drag = "scroll"                 # hold + move: scroll in any direction
scroll = "horizontal"           # hold + wheel: horizontal scroll

[buttons.forward]
double_click = "key:ctrl+w"
drag = { left = "desktop-left", right = "desktop-right", up = "overview", down = "show-desktop" }
scroll = { up = "volume-up", down = "volume-down" }
```

Button names: `left`, `right`, `middle`, `back`, `forward`, `task`, or an
evdev name such as `BTN_TASK` or `KEY_F13`. Some mice send keyboard keys
from extra buttons. Those can be remapped too. The **Add button** dialog
and `buttonsmith detect` show what each button sends.

Actions:

| Action | Example |
|--------|---------|
| Do nothing | `none` |
| Key combination | `key:ctrl+shift+t` |
| Mouse button | `button:middle` |
| Shell command | `cmd:spectacle -r` |
| KWin shortcut | `kwin:Overview` |
| Preset | `back`, `overview`, `desktop-left`, `volume-up` |

Presets:

- Navigation: `back`, `forward`, `left-click`, `right-click`, `middle-click`
- Desktop: `overview`, `grid`, `windows`, `all-windows`, `app-windows`,
  `show-desktop`, `desktop-left`, `desktop-right`, `desktop-up`,
  `desktop-down`, `desktop-next`, `desktop-prev`
- Media: `volume-up`, `volume-down`, `mute`, `play-pause`, `next-track`,
  `prev-track`, `brightness-up`, `brightness-down`

To list all KWin shortcut names:

```sh
busctl --user call org.kde.kglobalaccel /component/kwin org.kde.kglobalaccel.Component shortcutNames
```

## Commands

```
buttonsmith ui [-no-open]             open the settings window
buttonsmith run [-config PATH] [-v]   run the remapper (-v logs every trigger)
buttonsmith detect [-all]             show what each button sends
buttonsmith list                      list input devices and whether they are readable
buttonsmith check [-config PATH]      validate the config
buttonsmith example-config            print the example config
buttonsmith allow-device VVVV:PPPP    give the logged-in user access to a mouse (needs root)
buttonsmith version                   print the version
```

## Behavior notes

- A button with only `click` acts on press, without delay. A key
  combination stays pressed while the button is held.
- If a button also has `hold`, `drag` or `scroll`, `click` runs on release.
- If a button has `double_click`, a single click waits `double_click_ms`
  before it runs.
- `drag` on the middle button replaces middle-drag in applications, for
  example orbit in Blender. Middle click still works.
- Drag gestures run one action per `drag_step` pixels. They are not
  animated like touchpad gestures.

## Development

```sh
make test                      # go test ./...
make                           # build ./buttonsmith
make packages                  # .deb and .rpm in dist/
go generate ./internal/evdev   # regenerate key names from linux/input-event-codes.h
```

| Path | Contents |
|------|----------|
| `cmd/buttonsmith` | Command line and the remapper loop |
| `internal/engine` | Button state machine and scroll smoothing (no I/O, unit tested) |
| `internal/evdev` | evdev and uinput access without cgo |
| `internal/action` | Action syntax and presets |
| `internal/config` | TOML config and the model used by the settings window |
| `internal/access` | Per-mouse udev rule |
| `internal/ui` | Settings server, `index.html` and the PyQt6 window `window.py` |
| `internal/app` | Names and version |
| `contrib` | udev rule, systemd unit, desktop file, polkit policy |
| `packaging` | nfpm config and package scripts |

## License

Copyright (C) 2026 Amritanshu Pandey

Buttonsmith is free software: you can redistribute it and/or modify it
under the terms of the GNU General Public License as published by the Free
Software Foundation, either version 3 of the License, or (at your option)
any later version. See [LICENSE](LICENSE).
