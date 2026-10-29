// Package config loads and validates the TOML configuration.
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/amritanshu-pandey/buttonsmith/internal/action"
	"github.com/amritanshu-pandey/buttonsmith/internal/app"
)

//go:embed example.toml
var Example string

// DefaultPath is $XDG_CONFIG_HOME/buttonsmith/config.toml.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, app.ID, "config.toml")
}

// DeviceMatch selects the physical device to take over. All set fields
// must match.
type DeviceMatch struct {
	Vendor  string `toml:"vendor,omitempty" json:"vendor"`   // USB vendor id in hex, e.g. "25a7"
	Product string `toml:"product,omitempty" json:"product"` // USB product id in hex, e.g. "fa61"
	Name    string `toml:"name,omitempty" json:"name"`       // case-insensitive substring of the device name
}

func (m DeviceMatch) Empty() bool { return m.Vendor == "" && m.Product == "" && m.Name == "" }

func (m DeviceMatch) Matches(vendor, product uint16, name string) bool {
	hexEq := func(want string, got uint16) bool {
		if want == "" {
			return true
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(want, "0x"), 16, 16)
		return err == nil && uint16(v) == got
	}
	return hexEq(m.Vendor, vendor) && hexEq(m.Product, product) &&
		strings.Contains(strings.ToLower(name), strings.ToLower(m.Name))
}

type Scroll struct {
	Smooth       bool    `toml:"smooth" json:"smooth"`               // animate wheel ticks into many small hi-res steps
	Speed        float64 `toml:"speed" json:"speed"`                 // distance multiplier per tick
	Acceleration bool    `toml:"acceleration" json:"acceleration"`   // scroll further when the wheel is spun quickly
	SmoothnessMs float64 `toml:"smoothness_ms" json:"smoothness_ms"` // animation time constant; larger = longer glide
	Reverse      bool    `toml:"reverse" json:"reverse"`             // invert scroll direction
}

type Timing struct {
	HoldMs          int     `toml:"hold_ms" json:"hold_ms"`                     // press longer than this for `hold`
	DoubleClickMs   int     `toml:"double_click_ms" json:"double_click_ms"`     // max gap between clicks for `double_click`
	DragThreshold   float64 `toml:"drag_threshold" json:"drag_threshold"`       // px of movement before a press becomes a drag
	DragStep        float64 `toml:"drag_step" json:"drag_step"`                 // px per directional drag action
	DragScrollSpeed float64 `toml:"drag_scroll_speed" json:"drag_scroll_speed"` // hi-res units per px for drag = "scroll" (120 = one notch); negative inverts
}

// DragKind and ScrollKind describe what happens while a button is held.
type DragKind int

const (
	DragScroll     DragKind = iota + 1 // move the ball to scroll/pan
	DragDirections                     // one action per drag_step in a direction
)

type Drag struct {
	Kind                  DragKind
	Left, Right, Up, Down *action.Action
}

type ScrollKind int

const (
	ScrollHorizontal ScrollKind = iota + 1
	ScrollZoom
	ScrollPrecise
	ScrollFast
	ScrollActions
)

type ScrollMod struct {
	Kind     ScrollKind
	Up, Down *action.Action // for ScrollActions
}

// Button is the behaviour of one remapped button. Nil fields are unset; a
// nil Click means "pass the original button through".
type Button struct {
	Click, DoubleClick, Hold *action.Action
	Drag                     *Drag
	Scroll                   *ScrollMod
}

// Simple reports whether only Click is set, so it can run on press without
// waiting to see if the press becomes a hold/drag/double-click.
func (b *Button) Simple() bool {
	return b.DoubleClick == nil && b.Hold == nil && b.Drag == nil && b.Scroll == nil
}

type Config struct {
	Device  DeviceMatch
	Scroll  Scroll
	Timing  Timing
	Buttons map[uint16]*Button
}

type rawButton struct {
	Click       string `toml:"click"`
	DoubleClick string `toml:"double_click"`
	Hold        string `toml:"hold"`
	Drag        any    `toml:"drag"`   // "scroll" or {left, right, up, down}
	Scroll      any    `toml:"scroll"` // "horizontal" | "zoom" | "precise" | "fast" | {up, down}
}

type rawConfig struct {
	Device  DeviceMatch          `toml:"device"`
	Scroll  Scroll               `toml:"scroll"`
	Timing  Timing               `toml:"timing"`
	Buttons map[string]rawButton `toml:"buttons"`
}

func defaults() rawConfig {
	return rawConfig{
		Scroll: Scroll{Smooth: true, Speed: 1, Acceleration: true, SmoothnessMs: 45},
		Timing: Timing{HoldMs: 300, DoubleClickMs: 250, DragThreshold: 8, DragStep: 80, DragScrollSpeed: 4},
	}
}

func Parse(text string) (*Config, error) {
	raw := defaults()
	md, err := toml.Decode(text, &raw)
	if err != nil {
		return nil, err
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		// Keys inside drag/scroll tables are decoded by hand below.
		for _, k := range undec {
			if len(k) < 4 || k[0] != "buttons" || (k[2] != "drag" && k[2] != "scroll") {
				return nil, fmt.Errorf("unknown config key %q", k.String())
			}
		}
	}
	if raw.Scroll.Speed <= 0 || raw.Scroll.SmoothnessMs < 0 {
		return nil, fmt.Errorf("scroll.speed must be > 0 and scroll.smoothness_ms >= 0")
	}

	cfg := &Config{Device: raw.Device, Scroll: raw.Scroll, Timing: raw.Timing, Buttons: map[uint16]*Button{}}
	for name, rb := range raw.Buttons {
		code, err := action.ParseInput(name)
		if err != nil {
			return nil, err
		}
		if _, dup := cfg.Buttons[code]; dup {
			return nil, fmt.Errorf("button %q is configured twice (aliases refer to the same button)", name)
		}
		b, err := parseButton(rb)
		if err != nil {
			return nil, fmt.Errorf("buttons.%s: %w", name, err)
		}
		cfg.Buttons[code] = b
	}
	return cfg, nil
}

func Load(path string) (*Config, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(string(text))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func optAction(s, field string) (*action.Action, error) {
	if s == "" {
		return nil, nil
	}
	a, err := action.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &a, nil
}

// table extracts string values from an inline table, rejecting unknown keys.
func table(v any, field string, allowed ...string) (map[string]string, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a string or a table", field)
	}
	out := map[string]string{}
	for k, val := range m {
		known := false
		for _, a := range allowed {
			known = known || k == a
		}
		s, isStr := val.(string)
		if !known || !isStr {
			return nil, fmt.Errorf("%s.%s: expected one of %v with a string action", field, k, allowed)
		}
		out[k] = s
	}
	return out, nil
}

func parseButton(rb rawButton) (*Button, error) {
	var b Button
	var err error
	if b.Click, err = optAction(rb.Click, "click"); err != nil {
		return nil, err
	}
	if b.DoubleClick, err = optAction(rb.DoubleClick, "double_click"); err != nil {
		return nil, err
	}
	if b.Hold, err = optAction(rb.Hold, "hold"); err != nil {
		return nil, err
	}

	switch d := rb.Drag.(type) {
	case nil:
	case string:
		if d != "scroll" {
			return nil, fmt.Errorf("drag: unknown mode %q (use \"scroll\" or a table)", d)
		}
		b.Drag = &Drag{Kind: DragScroll}
	default:
		t, err := table(d, "drag", "left", "right", "up", "down")
		if err != nil {
			return nil, err
		}
		b.Drag = &Drag{Kind: DragDirections}
		for dir, dst := range map[string]**action.Action{"left": &b.Drag.Left, "right": &b.Drag.Right, "up": &b.Drag.Up, "down": &b.Drag.Down} {
			if *dst, err = optAction(t[dir], "drag."+dir); err != nil {
				return nil, err
			}
		}
	}

	switch s := rb.Scroll.(type) {
	case nil:
	case string:
		kinds := map[string]ScrollKind{"horizontal": ScrollHorizontal, "zoom": ScrollZoom, "precise": ScrollPrecise, "fast": ScrollFast}
		k, ok := kinds[s]
		if !ok {
			return nil, fmt.Errorf("scroll: unknown mode %q (horizontal, zoom, precise, fast or a table)", s)
		}
		b.Scroll = &ScrollMod{Kind: k}
	default:
		t, err := table(s, "scroll", "up", "down")
		if err != nil {
			return nil, err
		}
		b.Scroll = &ScrollMod{Kind: ScrollActions}
		if b.Scroll.Up, err = optAction(t["up"], "scroll.up"); err != nil {
			return nil, err
		}
		if b.Scroll.Down, err = optAction(t["down"], "scroll.down"); err != nil {
			return nil, err
		}
	}
	return &b, nil
}
