package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"linux-mouse-tools/internal/action"
	"linux-mouse-tools/internal/config"
	"linux-mouse-tools/internal/evdev"
)

// recorder logs output as compact strings like "M:BTN_SIDE 1",
// "K:KEY_LEFTCTRL 1" and "spawn:kwin:Overview".
type recorder struct{ out []string }

func (r *recorder) frame(prefix string, evs []evdev.Event) {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.String()
	}
	r.out = append(r.out, prefix+strings.Join(parts, ","))
}
func (r *recorder) Mouse(evs []evdev.Event)    { r.frame("M:", evs) }
func (r *recorder) Keyboard(evs []evdev.Event) { r.frame("K:", evs) }
func (r *recorder) Spawn(a action.Action)      { r.out = append(r.out, "spawn:"+a.String()) }

func (r *recorder) take() []string {
	o := r.out
	r.out = nil
	return o
}

type harness struct {
	t   *testing.T
	e   *Engine
	r   *recorder
	now time.Time
}

func newHarness(t *testing.T, cfgText string) *harness {
	t.Helper()
	cfg, err := config.Parse(cfgText)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := New(cfg, r)
	e.SourceHiRes = [2]bool{true, true}
	return &harness{t: t, e: e, r: r, now: time.Unix(1000, 0)}
}

// frame feeds one device frame (events + SYN_REPORT), then ticks.
func (h *harness) frame(evs ...evdev.Event) {
	for _, ev := range evs {
		h.e.Handle(ev, true, h.now)
	}
	h.e.Handle(evdev.Event{Type: evdev.EV_SYN, Code: evdev.SYN_REPORT}, true, h.now)
	h.e.Tick(h.now)
}

func (h *harness) advance(d time.Duration) {
	end := h.now.Add(d)
	for {
		dl := h.e.NextDeadline()
		if dl.IsZero() || dl.After(end) {
			break
		}
		h.now = dl
		h.e.Tick(h.now)
	}
	h.now = end
	h.e.Tick(h.now)
}

func (h *harness) expect(want ...string) {
	h.t.Helper()
	got := h.r.take()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		h.t.Errorf("output mismatch\n got: %q\nwant: %q", got, want)
	}
}

func btn(code uint16, v int32) evdev.Event {
	return evdev.Event{Type: evdev.EV_KEY, Code: code, Value: v}
}
func rel(code uint16, v int32) evdev.Event {
	return evdev.Event{Type: evdev.EV_REL, Code: code, Value: v}
}

const noSmooth = "[scroll]\nsmooth = false\n"

func TestPassthrough(t *testing.T) {
	h := newHarness(t, noSmooth)
	h.frame(rel(evdev.REL_X, 3), rel(evdev.REL_Y, -2), btn(evdev.BTN_LEFT, 1))
	h.expect("M:REL_X 3,REL_Y -2,BTN_LEFT 1")
	h.frame(rel(evdev.REL_WHEEL, -1), rel(evdev.REL_WHEEL_HI_RES, -120))
	h.expect("M:REL_WHEEL_HI_RES -120,REL_WHEEL -1")
}

func TestSimpleKeyComboMirrorsPress(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.back]\nclick = 'key:ctrl+c'\n")
	h.frame(btn(evdev.BTN_SIDE, 1))
	h.expect("K:KEY_LEFTCTRL 1", "K:KEY_C 1")
	h.advance(time.Second) // stays down while held
	h.expect()
	h.frame(btn(evdev.BTN_SIDE, 0))
	h.expect("K:KEY_C 0", "K:KEY_LEFTCTRL 0")
}

func TestSimpleCommandFiresOnPress(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.forward]\nclick = 'overview'\n[buttons.back]\nclick = 'none'\n")
	h.frame(btn(evdev.BTN_EXTRA, 1))
	h.expect("spawn:kwin:Overview")
	h.frame(btn(evdev.BTN_EXTRA, 0), btn(evdev.BTN_SIDE, 1), btn(evdev.BTN_SIDE, 0))
	h.expect()
}

func TestHold(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.forward]\nhold = 'windows'\n")
	// Short press → original button passes through on release.
	h.frame(btn(evdev.BTN_EXTRA, 1))
	h.advance(100 * time.Millisecond)
	h.expect()
	h.frame(btn(evdev.BTN_EXTRA, 0))
	h.expect("M:BTN_EXTRA 1", "M:BTN_EXTRA 0")
	// Long press → hold action, no click.
	h.frame(btn(evdev.BTN_EXTRA, 1))
	h.advance(350 * time.Millisecond)
	h.expect("spawn:kwin:Expose")
	h.frame(btn(evdev.BTN_EXTRA, 0))
	h.expect()
}

func TestDoubleClick(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.middle]\nclick = 'key:a'\ndouble_click = 'key:b'\n")
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.advance(100 * time.Millisecond)
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.expect("K:KEY_B 1", "K:KEY_B 0")
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.advance(time.Second)
	h.expect()
	// Single click fires once the double-click window closes.
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.advance(200 * time.Millisecond)
	h.expect()
	h.advance(100 * time.Millisecond)
	h.expect("K:KEY_A 1", "K:KEY_A 0")
	// Pressing another button flushes the waiting click first.
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.frame(btn(evdev.BTN_LEFT, 1))
	h.expect("K:KEY_A 1", "K:KEY_A 0", "M:BTN_LEFT 1")
}

func TestDirectionalDrag(t *testing.T) {
	h := newHarness(t, noSmooth+"[timing]\ndrag_step = 50\n"+
		"[buttons.middle]\ndrag = { left = 'desktop-left', right = 'desktop-right', up = 'overview' }\n")
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.frame(rel(evdev.REL_X, 4)) // below threshold: swallowed, still a click
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.expect("M:BTN_MIDDLE 1", "M:BTN_MIDDLE 0")

	h.frame(btn(evdev.BTN_MIDDLE, 1))
	for range 12 {
		h.frame(rel(evdev.REL_X, 10), rel(evdev.REL_Y, 1))
	}
	h.expect("spawn:kwin:Switch One Desktop to the Right", "spawn:kwin:Switch One Desktop to the Right")
	h.frame(rel(evdev.REL_Y, -60))
	h.expect("spawn:kwin:Overview")
	h.frame(rel(evdev.REL_Y, 60)) // down has no action
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.expect()
	// Pointer moves normally again.
	h.frame(rel(evdev.REL_X, 5))
	h.expect("M:REL_X 5")
}

func TestDragScroll(t *testing.T) {
	h := newHarness(t, noSmooth+"[timing]\ndrag_scroll_speed = 4\ndrag_threshold = 2\n[buttons.back]\ndrag = 'scroll'\n")
	h.frame(btn(evdev.BTN_SIDE, 1))
	h.frame(rel(evdev.REL_Y, 5)) // crosses threshold, starts scrolling
	h.frame(rel(evdev.REL_Y, 25))
	h.frame(rel(evdev.REL_X, -10))
	h.frame(btn(evdev.BTN_SIDE, 0))
	h.expect("M:REL_WHEEL_HI_RES 20", "M:REL_WHEEL_HI_RES 100,REL_WHEEL 1", "M:REL_HWHEEL_HI_RES 40")
}

func TestScrollModifiers(t *testing.T) {
	h := newHarness(t, noSmooth+`
[buttons.back]
scroll = "horizontal"
[buttons.forward]
scroll = "zoom"
[buttons.middle]
scroll = { up = "volume-up", down = "volume-down" }
`)
	h.frame(btn(evdev.BTN_SIDE, 1))
	h.frame(rel(evdev.REL_WHEEL, -1), rel(evdev.REL_WHEEL_HI_RES, -120))
	h.frame(btn(evdev.BTN_SIDE, 0)) // used for scrolling, so no click
	h.expect("M:REL_HWHEEL_HI_RES 120,REL_HWHEEL 1")

	h.frame(btn(evdev.BTN_EXTRA, 1))
	h.frame(rel(evdev.REL_WHEEL_HI_RES, 120))
	h.frame(rel(evdev.REL_WHEEL_HI_RES, 120))
	h.frame(btn(evdev.BTN_EXTRA, 0))
	h.expect("K:KEY_LEFTCTRL 1", "M:REL_WHEEL_HI_RES 120,REL_WHEEL 1", "M:REL_WHEEL_HI_RES 120,REL_WHEEL 1", "K:KEY_LEFTCTRL 0")

	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.frame(rel(evdev.REL_WHEEL_HI_RES, 60))
	h.frame(rel(evdev.REL_WHEEL_HI_RES, 60))
	h.frame(rel(evdev.REL_WHEEL_HI_RES, -240))
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.expect("K:KEY_VOLUMEUP 1", "K:KEY_VOLUMEUP 0", "K:KEY_VOLUMEDOWN 1", "K:KEY_VOLUMEDOWN 0", "K:KEY_VOLUMEDOWN 1", "K:KEY_VOLUMEDOWN 0")
}

// wheelTotals sums emitted hi-res and legacy vertical wheel values.
func wheelTotals(out []string) (hi, lo int) {
	for _, f := range out {
		for _, ev := range strings.Split(strings.TrimPrefix(f, "M:"), ",") {
			var v int
			if n, _ := fmt.Sscanf(ev, "REL_WHEEL_HI_RES %d", &v); n == 1 {
				hi += v
			} else if n, _ := fmt.Sscanf(ev, "REL_WHEEL %d", &v); n == 1 {
				lo += v
			}
		}
	}
	return
}

func TestSmoothScroll(t *testing.T) {
	h := newHarness(t, "[scroll]\nsmooth = true\nacceleration = false\nsmoothness_ms = 40\n")
	h.frame(rel(evdev.REL_WHEEL, -1), rel(evdev.REL_WHEEL_HI_RES, -120))
	first := h.r.take()
	if len(first) != 1 {
		t.Fatalf("expected an immediate first step, got %q", first)
	}
	h.advance(500 * time.Millisecond)
	rest := h.r.take()
	if len(rest) < 5 {
		t.Errorf("expected the notch spread over many frames, got %d", len(rest))
	}
	hi, lo := wheelTotals(append(first, rest...))
	if hi != -120 || lo != -1 {
		t.Errorf("smooth scroll totals: hi=%d lo=%d, want -120/-1", hi, lo)
	}
	if !h.e.NextDeadline().IsZero() {
		t.Error("animation should have finished")
	}

	// Reversing direction cancels the remaining glide.
	h.frame(rel(evdev.REL_WHEEL_HI_RES, 1200))
	h.advance(FrameInterval)
	h.frame(rel(evdev.REL_WHEEL_HI_RES, -120))
	h.advance(time.Second)
	h.r.take()
	if h.e.axes[vertical].remaining != 0 {
		t.Error("glide not finished")
	}
}

func TestAccelerationScrollsFurther(t *testing.T) {
	total := func(accel bool) int {
		h := newHarness(t, fmt.Sprintf("[scroll]\nacceleration = %v\n", accel))
		for range 10 {
			h.frame(rel(evdev.REL_WHEEL_HI_RES, -120))
			h.advance(30 * time.Millisecond)
		}
		h.advance(time.Second)
		hi, _ := wheelTotals(h.r.take())
		return hi
	}
	plain, fast := total(false), total(true)
	if plain != -1200 || -fast < -plain*3/2 {
		t.Errorf("plain=%d fast=%d: want plain=-1200 and fast well beyond it", plain, fast)
	}
}

func TestResetReleasesHeldKeys(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.back]\nclick = 'key:shift'\n[buttons.forward]\nscroll = 'zoom'\n")
	h.frame(btn(evdev.BTN_SIDE, 1))
	h.frame(btn(evdev.BTN_EXTRA, 1), rel(evdev.REL_WHEEL_HI_RES, 120))
	h.r.take()
	h.e.Reset()
	got := h.r.take()
	if fmt.Sprint(got) != fmt.Sprint([]string{"K:KEY_LEFTSHIFT 0", "K:KEY_LEFTCTRL 0"}) {
		t.Errorf("reset output %q", got)
	}
}

func TestResetReleasesPassedThroughButtons(t *testing.T) {
	h := newHarness(t, noSmooth+"[buttons.back]\nhold = 'overview'\n")
	h.frame(btn(evdev.BTN_LEFT, 1), btn(evdev.BTN_RIGHT, 1))
	h.frame(btn(evdev.BTN_RIGHT, 0))
	h.r.take()
	// Letting go of the device must not leave BTN_LEFT down on the virtual
	// mouse, or the compositor sends every click to one window.
	h.e.Reset()
	h.expect("M:BTN_LEFT 0")
	h.e.Reset()
	h.expect()
}

func TestConfigChangeKeepsPassedThroughButtonsUntilReleased(t *testing.T) {
	h := newHarness(t, noSmooth)
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.expect("M:BTN_MIDDLE 1")
	// The middle button becomes mapped while it is held down.
	cfg, _ := config.Parse(noSmooth + "[buttons.middle]\nclick = 'overview'\n")
	h.e.SetConfig(cfg)
	h.expect()
	h.frame(btn(evdev.BTN_MIDDLE, 0))
	h.expect("M:BTN_MIDDLE 0")
	// Afterwards the new mapping applies.
	h.frame(btn(evdev.BTN_MIDDLE, 1))
	h.expect("spawn:kwin:Overview")
}
