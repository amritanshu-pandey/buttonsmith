package config

import (
	"testing"

	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
)

func TestExampleParses(t *testing.T) {
	c, err := Parse(Example)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Device.Matches(0x25a7, 0xfa61, "Compx 2.4G Receiver Mouse") {
		t.Error("example should match the ProtoArc receiver")
	}
	if c.Buttons[evdev.BTN_SIDE].Drag.Kind != DragScroll {
		t.Error("back button should drag-scroll")
	}
}

func TestButtons(t *testing.T) {
	c, err := Parse(`
[timing]
hold_ms = 500
[buttons.forward]
click = "key:ctrl+w"
drag = { left = "desktop-left", right = "desktop-right" }
scroll = "zoom"
[buttons.BTN_MIDDLE]
drag = "scroll"
scroll = { up = "volume-up", down = "volume-down" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Timing.HoldMs != 500 || c.Timing.DoubleClickMs != 250 {
		t.Errorf("timing defaults not merged: %+v", c.Timing)
	}
	f := c.Buttons[evdev.BTN_EXTRA]
	if f.Scroll.Kind != ScrollZoom || f.Drag.Kind != DragDirections || f.Drag.Up != nil || f.Drag.Left.Arg != "Switch One Desktop to the Left" {
		t.Errorf("forward parsed wrong: %+v %+v", f.Drag, f.Scroll)
	}
	m := c.Buttons[evdev.BTN_MIDDLE]
	if m.Drag.Kind != DragScroll || m.Scroll.Kind != ScrollActions || m.Scroll.Up == nil || m.Simple() {
		t.Errorf("middle parsed wrong: %+v", m)
	}
}

func TestErrors(t *testing.T) {
	for _, bad := range []string{
		"[buttons.back]\nclick = 'x'",
		"[buttons.back]\nclick = 'back'\n[buttons.side]\nclick = 'none'",
		"[buttons.back]\ndrag = 'fly'",
		"[buttons.back]\ndrag = { sideways = 'back' }",
		"[buttons.back]\nscroll = { up = 3 }",
		"[buttons.back]\nclik = 'back'",
		"[scroll]\nspeed = 0",
		"[device]\nmodel = 'x'",
		"[buttons.nosuch]\nclick = 'back'",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("expected error for:\n%s", bad)
		}
	}
}
