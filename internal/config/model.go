package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/amritanshu-pandey/buttonsmith/internal/action"
	"github.com/amritanshu-pandey/buttonsmith/internal/app"
	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
)

// Model is an editable, JSON-friendly form of the config file, used by the
// settings UI. Actions stay as their config strings.
type Model struct {
	Device  DeviceMatch   `json:"device"`
	Scroll  Scroll        `json:"scroll"`
	Timing  Timing        `json:"timing"`
	Buttons []ModelButton `json:"buttons"`
}

type ModelButton struct {
	Name        string `json:"name"`
	Click       string `json:"click"`
	DoubleClick string `json:"double_click"`
	Hold        string `json:"hold"`
	// Drag.Mode is "scroll" or "directions"; Scroll.Mode is "horizontal",
	// "zoom", "precise", "fast" or "actions". Nil means unset.
	Drag   *ModelGesture `json:"drag"`
	Scroll *ModelGesture `json:"scroll"`
}

type ModelGesture struct {
	Mode  string `json:"mode"`
	Left  string `json:"left,omitempty"`
	Right string `json:"right,omitempty"`
	Up    string `json:"up,omitempty"`
	Down  string `json:"down,omitempty"`
}

// ModelFromText parses config text into a Model. Button names are
// normalised (e.g. BTN_SIDE → back) and sorted.
func ModelFromText(text string) (*Model, error) {
	if _, err := Parse(text); err != nil {
		return nil, err
	}
	raw := defaults()
	if _, err := toml.Decode(text, &raw); err != nil {
		return nil, err
	}
	m := &Model{Device: raw.Device, Scroll: raw.Scroll, Timing: raw.Timing, Buttons: []ModelButton{}}
	codes := map[string]uint16{}
	for name, rb := range raw.Buttons {
		code, _ := action.ParseInput(name) // validated by Parse above
		norm := action.InputName(code)
		codes[norm] = code
		m.Buttons = append(m.Buttons, ModelButton{
			Name:        norm,
			Click:       rb.Click,
			DoubleClick: rb.DoubleClick,
			Hold:        rb.Hold,
			Drag:        gestureFromRaw(rb.Drag, "directions"),
			Scroll:      gestureFromRaw(rb.Scroll, "actions"),
		})
	}
	// Mouse buttons first, then keyboard-style buttons, each by code.
	rank := func(b ModelButton) int {
		c := int(codes[b.Name])
		if !evdev.IsMouseButton(codes[b.Name]) {
			c += 1 << 16
		}
		return c
	}
	sort.Slice(m.Buttons, func(i, j int) bool { return rank(m.Buttons[i]) < rank(m.Buttons[j]) })
	return m, nil
}

// gestureFromRaw converts a decoded `drag`/`scroll` value: a string mode, or
// a table of directions (reported as tableMode).
func gestureFromRaw(v any, tableMode string) *ModelGesture {
	switch t := v.(type) {
	case string:
		return &ModelGesture{Mode: t}
	case map[string]any:
		g := &ModelGesture{Mode: tableMode}
		str := func(k string) string { s, _ := t[k].(string); return s }
		g.Left, g.Right, g.Up, g.Down = str("left"), str("right"), str("up"), str("down")
		return g
	}
	return nil
}

// ModelHeader starts every config file written from a Model.
const ModelHeader = "# " + app.Name + " configuration, written by the settings window.\n" +
	"# See \"" + app.ID + " example-config\" for documentation of every option.\n\n"

// Encode renders the model as TOML and checks that it parses.
func (m *Model) Encode() (string, error) {
	type file struct {
		Device  DeviceMatch               `toml:"device"`
		Scroll  Scroll                    `toml:"scroll"`
		Timing  Timing                    `toml:"timing"`
		Buttons map[string]map[string]any `toml:"buttons"`
	}
	f := file{Device: m.Device, Scroll: m.Scroll, Timing: m.Timing, Buttons: map[string]map[string]any{}}
	for _, b := range m.Buttons {
		if _, dup := f.Buttons[b.Name]; dup {
			return "", fmt.Errorf("button %q is listed twice", b.Name)
		}
		out := map[string]any{}
		for k, v := range map[string]string{"click": b.Click, "double_click": b.DoubleClick, "hold": b.Hold} {
			if v != "" {
				out[k] = v
			}
		}
		if g := b.Drag; g != nil {
			switch g.Mode {
			case "scroll":
				out["drag"] = "scroll"
			case "directions":
				out["drag"] = g.directions("left", "right", "up", "down")
			default:
				return "", fmt.Errorf("buttons.%s: unknown drag mode %q", b.Name, g.Mode)
			}
		}
		if g := b.Scroll; g != nil {
			if g.Mode == "actions" {
				out["scroll"] = g.directions("up", "down")
			} else {
				out["scroll"] = g.Mode // validated by Parse below
			}
		}
		f.Buttons[b.Name] = out
	}

	var buf bytes.Buffer
	buf.WriteString(ModelHeader)
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return "", err
	}
	if _, err := Parse(buf.String()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (g *ModelGesture) directions(keys ...string) map[string]string {
	all := map[string]string{"left": g.Left, "right": g.Right, "up": g.Up, "down": g.Down}
	out := map[string]string{}
	for _, k := range keys {
		if all[k] != "" {
			out[k] = all[k]
		}
	}
	return out
}
