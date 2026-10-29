package action

import (
	"reflect"
	"testing"

	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
)

func TestParse(t *testing.T) {
	k := func(names ...string) Action {
		a := Action{Kind: Keys}
		for _, n := range names {
			c, _ := evdev.KeyCode(n)
			a.Keys = append(a.Keys, c)
		}
		return a
	}
	good := map[string]Action{
		"key:ctrl+shift+t":  k("KEY_LEFTCTRL", "KEY_LEFTSHIFT", "KEY_T"),
		"key:meta+F5":       k("KEY_LEFTMETA", "KEY_F5"),
		"key:alt+left":      k("KEY_LEFTALT", "KEY_LEFT"),
		"key:KEY_VOLUMEUP":  k("KEY_VOLUMEUP"),
		"button:back":       k("BTN_SIDE"),
		"cmd:echo hi:there": {Kind: Command, Arg: "echo hi:there"},
		"kwin:Grid View":    {Kind: Kwin, Arg: "Grid View"},
		"overview":          {Kind: Kwin, Arg: "Overview"},
		"none":              {Kind: None},
		"  middle-click   ": k("BTN_MIDDLE"),
		"key:ctrl+BTN_LEFT": k("KEY_LEFTCTRL", "BTN_LEFT"),
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"bogus", "key:ctrl+nosuchkey", "button:KEY_A", "cmd:", "foo:bar"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestParseInput(t *testing.T) {
	for in, want := range map[string]uint16{"forward": evdev.BTN_EXTRA, "BTN_SIDE": evdev.BTN_SIDE, "KEY_F13": 0xb7} {
		if got, err := ParseInput(in); err != nil || got != want {
			t.Errorf("ParseInput(%q) = %#x, %v; want %#x", in, got, err, want)
		}
	}
}
