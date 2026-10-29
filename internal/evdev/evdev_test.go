package evdev

import (
	"reflect"
	"testing"
)

func TestCapabilityBitmaps(t *testing.T) {
	// The ProtoArc receiver's mouse node: five buttons, wheels and motion.
	if got := mouseButtons("1f0000 0 0 0 0"); !reflect.DeepEqual(got, []uint16{BTN_LEFT, BTN_RIGHT, BTN_MIDDLE, BTN_SIDE, BTN_EXTRA}) {
		t.Errorf("mouseButtons = %v", got)
	}
	if !hasBits("1943", REL_X, REL_Y) || hasBits("1040", REL_X, REL_Y) || hasBits("", REL_X) {
		t.Error("hasBits rel")
	}
	if got := mouseButtons("0"); got != nil {
		t.Errorf("no buttons expected, got %v", got)
	}
}
