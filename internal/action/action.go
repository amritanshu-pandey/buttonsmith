// Package action defines what a mouse trigger can do and the string syntax
// used for actions in the config file.
package action

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"linux-mouse-tools/internal/evdev"
)

type Kind int

const (
	None    Kind = iota // swallow the input
	Keys                // press a key/button combination
	Command             // run a shell command
	Kwin                // invoke a KWin global shortcut over D-Bus
)

type Action struct {
	Kind Kind
	Keys []uint16 // for Keys
	Arg  string   // for Command and Kwin
}

func (a Action) String() string {
	switch a.Kind {
	case Keys:
		names := make([]string, len(a.Keys))
		for i, k := range a.Keys {
			names[i] = evdev.KeyName(k)
		}
		return "key:" + strings.Join(names, "+")
	case Command:
		return "cmd:" + a.Arg
	case Kwin:
		return "kwin:" + a.Arg
	}
	return "none"
}

// Parse parses an action string:
//
//	none
//	key:ctrl+c          key combo
//	button:middle       mouse button (left/right/middle/back/forward)
//	cmd:spectacle -r    shell command
//	kwin:Overview       KWin shortcut by name
//	<preset>            see presets
func Parse(s string) (Action, error) {
	s = strings.TrimSpace(s)
	if kind, arg, ok := strings.Cut(s, ":"); ok {
		arg = strings.TrimSpace(arg)
		switch strings.TrimSpace(kind) {
		case "key", "keys":
			keys, err := ParseCombo(arg)
			return Action{Kind: Keys, Keys: keys}, err
		case "button":
			b, err := ParseMouseButton(arg)
			return Action{Kind: Keys, Keys: []uint16{b}}, err
		case "cmd", "command", "exec":
			if arg == "" {
				return Action{}, fmt.Errorf("empty command in %q", s)
			}
			return Action{Kind: Command, Arg: arg}, nil
		case "kwin":
			return Action{Kind: Kwin, Arg: arg}, nil
		default:
			return Action{}, fmt.Errorf("unknown action type %q in %q", kind, s)
		}
	}
	if a, ok := presets[s]; ok {
		return a, nil
	}
	return Action{}, fmt.Errorf("unknown action %q (use key:…, button:…, cmd:…, kwin:… or a preset name)", s)
}

func key(k uint16) Action  { return Action{Kind: Keys, Keys: []uint16{k}} }
func kwin(n string) Action { return Action{Kind: Kwin, Arg: n} }
func mustKey(n string) uint16 {
	k, ok := evdev.KeyCode(n)
	if !ok {
		panic("unknown key " + n)
	}
	return k
}

// Preset is a named shortcut for a common action.
type Preset struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Group  string `json:"group"`
	Action Action `json:"-"`
}

// Presets in display order.
var Presets = []Preset{
	{"back", "Back", "Navigation", key(evdev.BTN_SIDE)},
	{"forward", "Forward", "Navigation", key(evdev.BTN_EXTRA)},
	{"left-click", "Left click", "Navigation", key(evdev.BTN_LEFT)},
	{"right-click", "Right click", "Navigation", key(evdev.BTN_RIGHT)},
	{"middle-click", "Middle click", "Navigation", key(evdev.BTN_MIDDLE)},

	{"overview", "Overview", "Desktop", kwin("Overview")},
	{"grid", "Grid view", "Desktop", kwin("Grid View")},
	{"windows", "Windows on this desktop", "Desktop", kwin("Expose")},
	{"all-windows", "Windows on all desktops", "Desktop", kwin("ExposeAll")},
	{"app-windows", "Windows of current app", "Desktop", kwin("ExposeClass")},
	{"show-desktop", "Show desktop", "Desktop", kwin("Show Desktop")},
	{"desktop-left", "Desktop to the left", "Desktop", kwin("Switch One Desktop to the Left")},
	{"desktop-right", "Desktop to the right", "Desktop", kwin("Switch One Desktop to the Right")},
	{"desktop-up", "Desktop above", "Desktop", kwin("Switch One Desktop Up")},
	{"desktop-down", "Desktop below", "Desktop", kwin("Switch One Desktop Down")},
	{"desktop-next", "Next desktop", "Desktop", kwin("Switch to Next Desktop")},
	{"desktop-prev", "Previous desktop", "Desktop", kwin("Switch to Previous Desktop")},

	{"volume-up", "Volume up", "Media", key(mustKey("KEY_VOLUMEUP"))},
	{"volume-down", "Volume down", "Media", key(mustKey("KEY_VOLUMEDOWN"))},
	{"mute", "Mute", "Media", key(mustKey("KEY_MUTE"))},
	{"play-pause", "Play / pause", "Media", key(mustKey("KEY_PLAYPAUSE"))},
	{"next-track", "Next track", "Media", key(mustKey("KEY_NEXTSONG"))},
	{"prev-track", "Previous track", "Media", key(mustKey("KEY_PREVIOUSSONG"))},
	{"brightness-up", "Brightness up", "Media", key(mustKey("KEY_BRIGHTNESSUP"))},
	{"brightness-down", "Brightness down", "Media", key(mustKey("KEY_BRIGHTNESSDOWN"))},
}

var presets = func() map[string]Action {
	m := map[string]Action{"none": {Kind: None}}
	for _, p := range Presets {
		m[p.Name] = p.Action
	}
	return m
}()

var keyAliases = map[string]string{
	"ctrl": "KEY_LEFTCTRL", "control": "KEY_LEFTCTRL",
	"shift": "KEY_LEFTSHIFT",
	"alt":   "KEY_LEFTALT", "altgr": "KEY_RIGHTALT",
	"meta": "KEY_LEFTMETA", "super": "KEY_LEFTMETA", "win": "KEY_LEFTMETA", "logo": "KEY_LEFTMETA",
	"esc": "KEY_ESC", "return": "KEY_ENTER", "del": "KEY_DELETE", "ins": "KEY_INSERT",
	"pgup": "KEY_PAGEUP", "pgdn": "KEY_PAGEDOWN", "print": "KEY_SYSRQ", "printscreen": "KEY_SYSRQ",
}

// ParseCombo parses "ctrl+shift+t" style combos.
func ParseCombo(s string) ([]uint16, error) {
	var keys []uint16
	for _, tok := range strings.Split(s, "+") {
		k, err := ParseKey(tok)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// ParseKey parses one key: an alias (ctrl, meta…), a bare name (a, f5,
// volumeup, left) or a raw evdev name (KEY_A, BTN_SIDE).
func ParseKey(tok string) (uint16, error) {
	t := strings.ToLower(strings.TrimSpace(tok))
	if alias, ok := keyAliases[t]; ok {
		t = alias
	}
	if k, ok := evdev.KeyCode(t); ok {
		return k, nil
	}
	if k, ok := evdev.KeyCode("KEY_" + t); ok {
		return k, nil
	}
	return 0, fmt.Errorf("unknown key %q", strings.TrimSpace(tok))
}

var buttonAliases = map[string]uint16{
	"left": evdev.BTN_LEFT, "right": evdev.BTN_RIGHT, "middle": evdev.BTN_MIDDLE,
	"side": evdev.BTN_SIDE, "back": evdev.BTN_SIDE,
	"extra": evdev.BTN_EXTRA, "forward": evdev.BTN_EXTRA,
	"task": evdev.BTN_TASK,
}

// ParseMouseButton parses a mouse button by alias or BTN_* name.
func ParseMouseButton(s string) (uint16, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if b, ok := buttonAliases[t]; ok {
		return b, nil
	}
	if k, ok := evdev.KeyCode(t); ok && evdev.IsMouseButton(k) {
		return k, nil
	}
	return 0, fmt.Errorf("unknown mouse button %q", strings.TrimSpace(s))
}

// InputName is the preferred [buttons] table name for a code: the alias
// for standard mouse buttons, otherwise the evdev name.
func InputName(code uint16) string {
	switch code {
	case evdev.BTN_LEFT:
		return "left"
	case evdev.BTN_RIGHT:
		return "right"
	case evdev.BTN_MIDDLE:
		return "middle"
	case evdev.BTN_SIDE:
		return "back"
	case evdev.BTN_EXTRA:
		return "forward"
	case evdev.BTN_TASK:
		return "task"
	}
	return evdev.KeyName(code)
}

// ParseInput parses a key of the [buttons] table: a mouse button alias or
// any KEY_*/BTN_* name (extra buttons on some mice send keyboard keys).
func ParseInput(s string) (uint16, error) {
	if b, err := ParseMouseButton(s); err == nil {
		return b, nil
	}
	if k, ok := evdev.KeyCode(strings.TrimSpace(s)); ok {
		return k, nil
	}
	return 0, fmt.Errorf("unknown button %q", strings.TrimSpace(s))
}

// Spawn runs a Command or Kwin action in the background.
func Spawn(a Action) {
	var cmd *exec.Cmd
	switch a.Kind {
	case Command:
		cmd = exec.Command("sh", "-c", a.Arg)
	case Kwin:
		cmd = exec.Command("busctl", "--user", "call", "org.kde.kglobalaccel", "/component/kwin",
			"org.kde.kglobalaccel.Component", "invokeShortcut", "s", a.Arg)
	default:
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "mousefix: failed to run %v: %v\n", a, err)
		return
	}
	go cmd.Wait() // reap
}
