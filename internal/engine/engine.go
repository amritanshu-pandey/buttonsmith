// Package engine turns raw events from the grabbed mouse into remapped
// output: button triggers (click / double-click / hold / drag / scroll
// modifiers) and smooth scrolling. It does no I/O itself; time is passed in
// and output goes to a Sink, which keeps it deterministic and testable.
package engine

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/amritanshu-pandey/buttonsmith/internal/action"
	"github.com/amritanshu-pandey/buttonsmith/internal/app"
	"github.com/amritanshu-pandey/buttonsmith/internal/config"
	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
)

// FrameInterval is how often smooth-scroll animation emits events.
const FrameInterval = 6 * time.Millisecond

// accelWindow: wheel ticks closer together than this build up acceleration.
const accelWindow = 90 * time.Millisecond

// Sink receives the engine's output. Mouse and Keyboard each receive one
// complete frame (the implementation appends SYN_REPORT).
type Sink interface {
	Mouse(evs []evdev.Event)
	Keyboard(evs []evdev.Event)
	Spawn(a action.Action)
}

const (
	vertical   = 0
	horizontal = 1
)

type mode int

const (
	pending  mode = iota // pressed, outcome not decided yet
	dragging             // moving with a drag trigger
	consumed             // a hold/double-click/scroll trigger used this press
)

type held struct {
	code         uint16
	cfg          *config.Button
	holdAt       time.Time // zero when no hold timer is running
	mode         mode
	slopX, slopY float64 // movement before the drag threshold
	dragX, dragY float64 // movement towards the next directional action
	wheelAcc     float64 // hi-res units towards the next scroll action
}

type axisState struct {
	remaining float64 // hi-res units still to animate
	frac      float64 // sub-unit remainder not yet emitted
	legacy    int32   // hi-res units towards the next legacy wheel notch
	animating bool
	lastFrame time.Time
	lastTick  time.Time // last wheel input (for acceleration)
	lastSign  float64
	streak    int
}

type Engine struct {
	cfg     *config.Config
	sink    Sink
	Verbose bool

	// SourceHiRes[axis] is true when the physical device reports hi-res
	// wheel events; its legacy wheel events are then ignored.
	SourceHiRes [2]bool

	held     []*held             // complex buttons currently down, in press order
	mirrored map[uint16][]uint16 // simple buttons whose key combo is held down
	passed   map[uint16]bool     // passed-through keys/buttons currently down
	pending  map[uint16]time.Time
	zoomCtrl bool
	axes     [2]axisState

	mouseBuf, kbdBuf []evdev.Event
}

func New(cfg *config.Config, sink Sink) *Engine {
	return &Engine{cfg: cfg, sink: sink, mirrored: map[uint16][]uint16{}, passed: map[uint16]bool{}, pending: map[uint16]time.Time{}}
}

// SetConfig switches to a new configuration. Triggers in progress are
// cancelled; buttons passed through to the virtual mouse stay down until
// they are physically released.
func (e *Engine) SetConfig(cfg *config.Config) {
	e.cancelTriggers()
	e.cfg = cfg
}

// Reset releases everything the engine holds down on the virtual devices
// and clears all state. Call it when the physical device is let go
// (disconnect, exit): its releases would never reach us after that, and a
// button left down makes the compositor send all clicks to one window.
func (e *Engine) Reset() {
	e.cancelTriggers()
	for code := range e.passed {
		e.emitKey(code, 0)
	}
	clear(e.passed)
}

func (e *Engine) cancelTriggers() {
	e.flush()
	for code, keys := range e.mirrored {
		e.releaseKeys(keys)
		delete(e.mirrored, code)
	}
	if e.zoomCtrl {
		e.emitKey(evdev.KEY_LEFTCTRL, 0)
		e.zoomCtrl = false
	}
	e.held = nil
	clear(e.pending)
	e.axes = [2]axisState{}
}

func (e *Engine) logf(format string, args ...any) {
	if e.Verbose {
		fmt.Fprintf(os.Stderr, app.ID+": "+format+"\n", args...)
	}
}

// Handle processes one event from a grabbed device. fromPointer is false
// for auxiliary nodes (e.g. the receiver's keyboard interface), whose
// non-key events are dropped.
func (e *Engine) Handle(ev evdev.Event, fromPointer bool, now time.Time) {
	switch ev.Type {
	case evdev.EV_SYN:
		if ev.Code == evdev.SYN_REPORT {
			e.flush()
		}
	case evdev.EV_KEY:
		e.key(ev.Code, ev.Value, now)
	case evdev.EV_REL:
		if fromPointer {
			e.rel(ev.Code, ev.Value, now)
		}
	}
}

func (e *Engine) key(code uint16, value int32, now time.Time) {
	cfg, ok := e.cfg.Buttons[code]
	if value == 0 && e.passed[code] {
		// Pressed while unmapped (e.g. before a config change): release it
		// the same way.
		e.passthrough(code, 0)
		return
	}
	if !ok {
		if value == 1 {
			e.firePending(now, 0)
		}
		e.passthrough(code, value)
		return
	}
	if value == 2 { // autorepeat from keyboard-style buttons
		return
	}
	e.flush() // keep earlier motion ordered before what we emit
	if value == 1 {
		e.press(code, cfg, now)
	} else {
		e.release(code, cfg, now)
	}
}

func (e *Engine) passthrough(code uint16, value int32) {
	switch value {
	case 1:
		e.passed[code] = true
	case 0:
		delete(e.passed, code)
	}
	ev := evdev.Event{Type: evdev.EV_KEY, Code: code, Value: value}
	if evdev.IsMouseButton(code) {
		e.mouseBuf = append(e.mouseBuf, ev)
	} else {
		e.kbdBuf = append(e.kbdBuf, ev)
	}
}

func (e *Engine) press(code uint16, cfg *config.Button, now time.Time) {
	e.firePending(now, code)

	if cfg.Simple() {
		switch {
		case cfg.Click == nil:
			e.passthrough(code, 1)
		case cfg.Click.Kind == action.Keys:
			e.logf("%s → %v (held)", evdev.KeyName(code), *cfg.Click)
			e.pressKeys(cfg.Click.Keys)
			e.mirrored[code] = cfg.Click.Keys
		default:
			e.run(code, "click", *cfg.Click)
		}
		return
	}

	h := &held{code: code, cfg: cfg}
	if dl, ok := e.pending[code]; ok {
		delete(e.pending, code)
		if now.Before(dl) { // second press of a double click
			e.run(code, "double_click", *cfg.DoubleClick)
			h.mode = consumed
		} else {
			e.click(code, cfg)
		}
	}
	if cfg.Hold != nil && h.mode == pending {
		h.holdAt = now.Add(time.Duration(e.cfg.Timing.HoldMs) * time.Millisecond)
	}
	e.held = append(e.held, h)
}

func (e *Engine) release(code uint16, cfg *config.Button, now time.Time) {
	if keys, ok := e.mirrored[code]; ok {
		e.releaseKeys(keys)
		delete(e.mirrored, code)
		return
	}
	if cfg.Simple() {
		if cfg.Click == nil {
			e.passthrough(code, 0)
		}
		return
	}

	var h *held
	for i, x := range e.held {
		if x.code == code {
			h = x
			e.held = append(e.held[:i], e.held[i+1:]...)
			break
		}
	}
	if h == nil { // pressed before we grabbed the device
		return
	}
	if e.zoomCtrl && !e.anyHeldWith(config.ScrollZoom) {
		e.emitKey(evdev.KEY_LEFTCTRL, 0)
		e.zoomCtrl = false
	}
	if h.mode != pending {
		return
	}
	if cfg.DoubleClick != nil {
		e.pending[code] = now.Add(time.Duration(e.cfg.Timing.DoubleClickMs) * time.Millisecond)
		return
	}
	e.click(code, cfg)
}

func (e *Engine) anyHeldWith(kind config.ScrollKind) bool {
	for _, h := range e.held {
		if h.cfg.Scroll != nil && h.cfg.Scroll.Kind == kind {
			return true
		}
	}
	return false
}

// firePending fires waiting single clicks (except for `except`), because
// another button was pressed and their order matters.
func (e *Engine) firePending(now time.Time, except uint16) {
	for code := range e.pending {
		if code != except {
			delete(e.pending, code)
			e.click(code, e.cfg.Buttons[code])
		}
	}
}

func (e *Engine) click(code uint16, cfg *config.Button) {
	if cfg.Click == nil {
		e.logf("%s → click (passthrough)", evdev.KeyName(code))
		e.tapKeys([]uint16{code})
		return
	}
	e.run(code, "click", *cfg.Click)
}

func (e *Engine) run(code uint16, trigger string, a action.Action) {
	e.logf("%s %s → %v", evdev.KeyName(code), trigger, a)
	switch a.Kind {
	case action.None:
	case action.Keys:
		e.tapKeys(a.Keys)
	default:
		e.sink.Spawn(a)
	}
}

// --- key output -----------------------------------------------------------

func (e *Engine) emitKey(code uint16, value int32) {
	e.flush()
	ev := []evdev.Event{{Type: evdev.EV_KEY, Code: code, Value: value}}
	if evdev.IsMouseButton(code) {
		e.sink.Mouse(ev)
	} else {
		e.sink.Keyboard(ev)
	}
}

func (e *Engine) pressKeys(keys []uint16) {
	for _, k := range keys {
		e.emitKey(k, 1)
	}
}

func (e *Engine) releaseKeys(keys []uint16) {
	for i := len(keys) - 1; i >= 0; i-- {
		e.emitKey(keys[i], 0)
	}
}

func (e *Engine) tapKeys(keys []uint16) {
	e.pressKeys(keys)
	e.releaseKeys(keys)
}

func (e *Engine) flush() {
	if len(e.mouseBuf) > 0 {
		e.sink.Mouse(e.mouseBuf)
		e.mouseBuf = nil
	}
	if len(e.kbdBuf) > 0 {
		e.sink.Keyboard(e.kbdBuf)
		e.kbdBuf = nil
	}
}

// --- motion and wheel -----------------------------------------------------

func (e *Engine) rel(code uint16, value int32, now time.Time) {
	switch code {
	case evdev.REL_X, evdev.REL_Y:
		dx, dy := 0.0, 0.0
		if code == evdev.REL_X {
			dx = float64(value)
		} else {
			dy = float64(value)
		}
		if !e.motion(dx, dy) {
			e.mouseBuf = append(e.mouseBuf, evdev.Event{Type: evdev.EV_REL, Code: code, Value: value})
		}
	case evdev.REL_WHEEL_HI_RES:
		e.wheel(vertical, float64(value), now)
	case evdev.REL_HWHEEL_HI_RES:
		e.wheel(horizontal, float64(value), now)
	case evdev.REL_WHEEL:
		if !e.SourceHiRes[vertical] {
			e.wheel(vertical, float64(value)*120, now)
		}
	case evdev.REL_HWHEEL:
		if !e.SourceHiRes[horizontal] {
			e.wheel(horizontal, float64(value)*120, now)
		}
	}
}

// motion applies drag triggers; it returns true if the movement was used.
func (e *Engine) motion(dx, dy float64) bool {
	var h *held
	for i := len(e.held) - 1; i >= 0; i-- {
		if e.held[i].cfg.Drag != nil && e.held[i].mode != consumed {
			h = e.held[i]
			break
		}
	}
	if h == nil {
		return false
	}
	t := e.cfg.Timing
	if h.mode == pending {
		h.slopX += dx
		h.slopY += dy
		if math.Hypot(h.slopX, h.slopY) < t.DragThreshold {
			return true // hold the pointer still until we know it's a drag
		}
		h.mode = dragging
		h.holdAt = time.Time{}
		e.logf("%s drag start", evdev.KeyName(h.code))
	}
	d := h.cfg.Drag
	if d.Kind == config.DragScroll {
		// Like grabbing the page: moving the ball down pulls the content down.
		e.emitWheel(vertical, dy*t.DragScrollSpeed)
		e.emitWheel(horizontal, -dx*t.DragScrollSpeed)
		return true
	}
	h.dragX += dx
	h.dragY += dy
	if math.Max(math.Abs(h.dragX), math.Abs(h.dragY)) < t.DragStep {
		return true
	}
	var a *action.Action
	var dir string
	switch {
	case math.Abs(h.dragX) >= math.Abs(h.dragY) && h.dragX < 0:
		a, dir = d.Left, "drag left"
	case math.Abs(h.dragX) >= math.Abs(h.dragY):
		a, dir = d.Right, "drag right"
	case h.dragY < 0:
		a, dir = d.Up, "drag up"
	default:
		a, dir = d.Down, "drag down"
	}
	h.dragX, h.dragY = 0, 0
	if a != nil {
		e.run(h.code, dir, *a)
	}
	return true
}

func (e *Engine) wheel(axis int, delta float64, now time.Time) {
	var h *held
	if axis == vertical {
		for i := len(e.held) - 1; i >= 0; i-- {
			if e.held[i].cfg.Scroll != nil && e.held[i].mode != dragging {
				h = e.held[i]
				break
			}
		}
	}
	if h == nil {
		e.scroll(axis, delta, 1, true, now)
		return
	}

	h.mode = consumed
	h.holdAt = time.Time{}
	m := h.cfg.Scroll
	switch m.Kind {
	case config.ScrollHorizontal:
		// Wheel down scrolls right, as with shift+wheel.
		e.scroll(horizontal, -delta, 1, true, now)
	case config.ScrollPrecise:
		e.scroll(vertical, delta, 0.3, false, now)
	case config.ScrollFast:
		e.scroll(vertical, delta, 3, true, now)
	case config.ScrollZoom:
		if !e.zoomCtrl {
			e.emitKey(evdev.KEY_LEFTCTRL, 1)
			e.zoomCtrl = true
		}
		e.emitWheel(vertical, delta)
		e.flush()
	case config.ScrollActions:
		h.wheelAcc += delta
		for h.wheelAcc >= 120 {
			h.wheelAcc -= 120
			if m.Up != nil {
				e.run(h.code, "scroll up", *m.Up)
			}
		}
		for h.wheelAcc <= -120 {
			h.wheelAcc += 120
			if m.Down != nil {
				e.run(h.code, "scroll down", *m.Down)
			}
		}
	}
}

// scroll feeds wheel input (in hi-res units, 120 per notch) into the
// smooth scrolling animation, or emits it directly when smoothing is off.
func (e *Engine) scroll(axis int, delta, factor float64, accel bool, now time.Time) {
	sc := e.cfg.Scroll
	if sc.Reverse {
		delta = -delta
	}
	d := delta * sc.Speed * factor
	if !sc.Smooth || sc.SmoothnessMs == 0 {
		e.emitWheel(axis, d)
		return
	}

	a := &e.axes[axis]
	sign := math.Copysign(1, d)
	if sign == a.lastSign && now.Sub(a.lastTick) < accelWindow {
		a.streak++
	} else {
		a.streak = 0
	}
	a.lastTick, a.lastSign = now, sign
	if accel && sc.Acceleration && a.streak > 3 {
		d *= 1 + math.Min(float64(a.streak-3)*0.35, 7)
	}
	if a.remaining*sign < 0 { // direction change: stop the glide at once
		a.remaining, a.frac = 0, 0
	}
	a.remaining += d
	if !a.animating {
		a.animating = true
		a.lastFrame = now.Add(-FrameInterval) // emit the first step right away
		e.Tick(now)
	}
}

// emitWheel queues hi-res wheel output plus the legacy notch events that
// older clients rely on.
func (e *Engine) emitWheel(axis int, v float64) {
	a := &e.axes[axis]
	a.frac += v
	n := int32(a.frac) // truncates towards zero
	if n == 0 {
		return
	}
	a.frac -= float64(n)
	hiCode, loCode := uint16(evdev.REL_WHEEL_HI_RES), uint16(evdev.REL_WHEEL)
	if axis == horizontal {
		hiCode, loCode = evdev.REL_HWHEEL_HI_RES, evdev.REL_HWHEEL
	}
	e.mouseBuf = append(e.mouseBuf, evdev.Event{Type: evdev.EV_REL, Code: hiCode, Value: n})
	a.legacy += n
	if notches := a.legacy / 120; notches != 0 {
		a.legacy -= notches * 120
		e.mouseBuf = append(e.mouseBuf, evdev.Event{Type: evdev.EV_REL, Code: loCode, Value: notches})
	}
}

// --- timers ---------------------------------------------------------------

// Tick runs hold/double-click timers and scroll animation. Call it after
// handling events and whenever NextDeadline passes.
func (e *Engine) Tick(now time.Time) {
	for _, h := range e.held {
		if !h.holdAt.IsZero() && !now.Before(h.holdAt) {
			h.holdAt = time.Time{}
			if h.mode == pending {
				h.mode = consumed
				e.run(h.code, "hold", *h.cfg.Hold)
			}
		}
	}
	for code, dl := range e.pending {
		if !now.Before(dl) {
			delete(e.pending, code)
			e.click(code, e.cfg.Buttons[code])
		}
	}

	tau := e.cfg.Scroll.SmoothnessMs
	for axis := range e.axes {
		a := &e.axes[axis]
		if !a.animating || now.Sub(a.lastFrame) < FrameInterval {
			continue
		}
		dt := float64(now.Sub(a.lastFrame)) / float64(time.Millisecond)
		a.lastFrame = now
		// Exponential ease-out: each frame covers a fixed share of the
		// remaining distance, so it starts fast and glides to a stop.
		step := a.remaining * (1 - math.Exp(-dt/tau))
		if math.Abs(a.remaining-step) < 1 {
			step = a.remaining
		}
		a.remaining -= step
		e.emitWheel(axis, step)
		if a.remaining == 0 {
			// Emit the rounding leftover so every notch adds up exactly.
			a.frac = math.Round(a.frac)
			e.emitWheel(axis, 0)
			a.animating = false
		}
	}
	e.flush()
}

// NextDeadline returns when Tick next needs to run, or zero if nothing is
// scheduled.
func (e *Engine) NextDeadline() time.Time {
	var next time.Time
	consider := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, h := range e.held {
		consider(h.holdAt)
	}
	for _, dl := range e.pending {
		consider(dl)
	}
	for _, a := range e.axes {
		if a.animating {
			consider(a.lastFrame.Add(FrameInterval))
		}
	}
	return next
}
