package config

import (
	"reflect"
	"testing"
)

func TestModelRoundTrip(t *testing.T) {
	m, err := ModelFromText(Example)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Buttons) != 3 || m.Buttons[0].Name != "middle" || m.Buttons[0].Drag.Mode != "directions" ||
		m.Buttons[0].Drag.Up != "overview" || m.Buttons[1].Drag.Mode != "scroll" || m.Buttons[2].Scroll.Mode != "zoom" {
		t.Fatalf("unexpected model: %+v", m.Buttons)
	}

	m.Buttons = append(m.Buttons, ModelButton{
		Name:   "KEY_F13",
		Click:  `cmd:notify-send "hi \"there\""`,
		Scroll: &ModelGesture{Mode: "actions", Up: "volume-up"},
	})
	m.Scroll.Speed = 1.5
	text, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ModelFromText(text)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if !reflect.DeepEqual(m, back) {
		t.Errorf("round trip mismatch\n got: %+v\nwant: %+v\n%s", back, m, text)
	}
}

func TestModelNormalisesNames(t *testing.T) {
	m, err := ModelFromText("[buttons.BTN_SIDE]\nclick = 'none'\n[buttons.BTN_EXTRA]\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Buttons[0].Name != "back" || m.Buttons[1].Name != "forward" {
		t.Errorf("names not normalised: %+v", m.Buttons)
	}
}

func TestModelEncodeValidates(t *testing.T) {
	for _, m := range []Model{
		{Scroll: Scroll{Speed: 1}, Buttons: []ModelButton{{Name: "back", Click: "nonsense"}}},
		{Scroll: Scroll{Speed: 1}, Buttons: []ModelButton{{Name: "back"}, {Name: "back"}}},
		{Scroll: Scroll{Speed: 1}, Buttons: []ModelButton{{Name: "back", Scroll: &ModelGesture{Mode: "sideways"}}}},
		{Scroll: Scroll{Speed: 0}},
	} {
		if _, err := m.Encode(); err == nil {
			t.Errorf("expected error for %+v", m)
		}
	}
}
