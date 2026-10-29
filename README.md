# mousefix

A Linux take on [Mac Mouse Fix](https://github.com/noah-nuebling/mac-mouse-fix):
remap a mouse's extra buttons and add smooth, accelerated scrolling. It works
on Wayland, and on X11.

Wayland doesn't let apps intercept or inject input, so mousefix works one
layer lower. It grabs your mouse's evdev device so the compositor no longer
sees it. It then processes the events and sends the result back through
virtual `uinput` devices ("mousefix pointer" and "mousefix keyboard"). It
doesn't depend on any particular compositor. Only the `kwin:` actions and
presets are KDE-specific.

## Features

| Mac Mouse Fix                     | mousefix                                                        |
|-----------------------------------|-----------------------------------------------------------------|
| Click / double click / hold       | `click`, `double_click`, `hold` per button                      |
| Click and drag (Spaces, Mission Control) | `drag = { left = "desktop-left", up = "overview", … }`   |
| Click and drag to scroll          | `drag = "scroll"` (handy on a trackball)                        |
| Button + scroll (zoom, horizontal, …) | `scroll = "zoom" / "horizontal" / "precise" / "fast" / {up, down}` |
| Smooth scrolling with acceleration | `[scroll]`: animated hi-res wheel events, speed, reverse      |
| Actions                           | key combos, mouse buttons, shell commands, KWin shortcuts, presets |

## Install

You need Go 1.26 or newer.

```sh
./install.sh
```

This script:

1. Builds `~/.local/bin/mousefix`.
2. Writes a default config to `~/.config/mousefix/config.toml`.
3. Installs `/etc/udev/rules.d/70-mousefix.rules` (asks for sudo). The rule
   gives the logged-in user access to this mouse and `/dev/uinput`. It does
   not add you to the `input` group, which would expose every keyboard.
4. Enables the `mousefix` systemd user service.
5. Adds **mousefix** to the application menu. It opens the settings window.

Logs: `journalctl --user -u mousefix -f`

> **Pointer settings:** Plasma sees the virtual "mousefix pointer" as a new
> mouse. Set its pointer speed and acceleration in System Settings → Mouse.

## Settings window

Open **mousefix** from the application menu, or run `mousefix ui`.

- **Buttons:** click **+ Add button (press it)** and press the button on
  your mouse. Then choose what click, double click, hold, hold + move and
  hold + wheel do. Actions can be presets (back/forward, overview, switch
  desktop, media keys…), keyboard shortcuts (with a **Record** button),
  mouse buttons, any KDE shortcut, or a shell command.
- **Scrolling:** smooth scrolling, speed, acceleration, glide length,
  direction.
- **Timing:** hold delay, double-click speed, drag sensitivity.
- **Device:** pick which mouse mousefix takes over.
- The header shows whether the service is running and whether the mouse is
  connected and readable, with Start/Stop and Restart buttons.

**Save** writes `~/.config/mousefix/config.toml`, and the running service
picks it up within a second. If the file was hand-written, the first save
keeps the original as `config.toml.bak` (the UI rewrites the file without
comments). While the "Add button" dialog is open, the service is paused so
the dialog can see your button presses.

The window is a local web page. It opens as an app window in
Chrome/Chromium, otherwise in your default browser. The server listens
only on 127.0.0.1, requires a random per-session token, and exits about 10
seconds after the window closes.

## Config file

Everything in the window can also be set in the TOML file directly.
`mousefix example-config` prints a fully commented example and
`mousefix check` validates your file.

```toml
[buttons.back]
click = "back"                  # unset = keep the button's normal function
hold = "overview"
drag = "scroll"                 # hold + move the ball = scroll/pan
scroll = "horizontal"           # hold + wheel = sideways scroll

[buttons.forward]
double_click = "key:ctrl+w"
drag = { left = "desktop-left", right = "desktop-right", up = "overview", down = "show-desktop" }
scroll = { up = "volume-up", down = "volume-down" }
```

Actions: `none`, `key:ctrl+shift+t`, `button:middle`, `cmd:spectacle -r`,
`kwin:<shortcut name>`, or a preset (`back`, `forward`, `middle-click`,
`overview`, `grid`, `windows`, `show-desktop`, `desktop-left/right/up/down`,
`volume-up/down`, `mute`, `play-pause`, `next-track`, `prev-track`, …).

Some trackballs send keyboard keys from their extra buttons, for example
`KEY_F13`. Those can be remapped too, e.g. `[buttons.KEY_F13]`. Both the
"Add button" dialog and `mousefix detect` show what each button sends.

## Commands

```
mousefix ui [-no-open]             open the settings window
mousefix run [-config PATH] [-v]   run the remapper (-v logs every trigger)
mousefix detect [-all]             show what each button sends
mousefix list                      list input devices and whether they're readable
mousefix check [-config PATH]      validate the config
mousefix example-config            print the commented example config
```

## Notes and limitations

- A button with only `click` acts on press with no delay, and its key combo
  stays held while the button is down. Adding `hold`, `drag` or `scroll`
  makes the click fire on release. Adding `double_click` delays a single
  click by `double_click_ms`.
- Configuring `drag` on the middle button replaces the app's own
  middle-drag (for example Blender orbit). Middle *click* still works.
- Drag gestures trigger discrete actions such as "switch desktop". They
  are not 1:1 animated swipes the way trackpad gestures are.

## Development

```sh
go test ./...
go generate ./internal/evdev   # regenerate key names from linux/input-event-codes.h
```

Layout: `internal/evdev` (cgo-free evdev/uinput), `internal/action`
(action syntax), `internal/config` (TOML and the UI's editable model),
`internal/engine` (button state machine and scroll smoothing; pure logic,
unit-tested), `internal/ui` (settings server plus a single-file
`index.html`), `cmd/mousefix` (daemon loop and CLI).
