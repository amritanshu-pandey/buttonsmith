// Command buttonsmith remaps mouse buttons and smooths scrolling on Linux
// (Wayland or X11) by grabbing the mouse via evdev and re-emitting events
// through uinput.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/amritanshu-pandey/buttonsmith/internal/action"
	"github.com/amritanshu-pandey/buttonsmith/internal/app"
	"github.com/amritanshu-pandey/buttonsmith/internal/config"
	"github.com/amritanshu-pandey/buttonsmith/internal/engine"
	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
	"github.com/amritanshu-pandey/buttonsmith/internal/ui"
)

var usage = strings.ReplaceAll(`CMD - mouse button remapping, gestures and smooth scrolling for Linux

Usage:
  CMD ui [-no-open]             open the settings window
  CMD run [-config PATH] [-v]   run the remapper (usually via systemd)
  CMD detect [-all]             show which codes your mouse's buttons send
  CMD list                      list input devices
  CMD check [-config PATH]      validate the config file
  CMD example-config            print a commented example config

Default config: $XDG_CONFIG_HOME/CMD/config.toml
`, "CMD", app.ID)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "config file")
	verbose := fs.Bool("v", false, "log every trigger")
	all := fs.Bool("all", false, "detect: watch all readable mice and keyboards, not just the configured device")
	noOpen := fs.Bool("no-open", false, "ui: only print the URL, don't open a window")
	addr := fs.String("addr", "127.0.0.1:0", "ui: listen address")
	fs.Parse(os.Args[2:])

	var err error
	switch os.Args[1] {
	case "run":
		err = run(*cfgPath, *verbose)
	case "ui":
		err = ui.Run(*cfgPath, *addr, *noOpen)
	case "detect":
		err = detect(*cfgPath, *all)
	case "list":
		list()
	case "check":
		var cfg *config.Config
		if cfg, err = config.Load(*cfgPath); err == nil {
			fmt.Printf("%s: OK (%d buttons configured)\n", *cfgPath, len(cfg.Buttons))
		}
	case "example-config":
		fmt.Print(config.Example)
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, app.ID+":", err)
		os.Exit(1)
	}
}

// loadOrDefault loads the config, falling back to the built-in example when
// the file doesn't exist yet.
func loadOrDefault(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, app.ID+": %s not found, using built-in defaults (see `%s example-config`)\n", path, app.ID)
		return config.Parse(config.Example)
	}
	return cfg, err
}

// sink writes engine output to the virtual devices.
type sink struct{ mouse, kbd *evdev.Virtual }

func (s sink) Mouse(evs []evdev.Event)    { logErr(s.mouse.Emit(evs)) }
func (s sink) Keyboard(evs []evdev.Event) { logErr(s.kbd.Emit(evs)) }
func (s sink) Spawn(a action.Action)      { action.Spawn(a) }

func logErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, app.ID+": write:", err)
	}
}

type source struct {
	dev     *evdev.Device
	pointer bool
}

// errButtonsDown means a button on the device is held, so grabbing now
// would hide its release from the compositor.
var errButtonsDown = errors.New("buttons held")

// openSources finds and grabs the configured device's event nodes: the
// pointer node always, other nodes (keyboard/consumer interfaces of the same
// receiver) only if a configured button lives there. Nothing is grabbed
// while a button is held: the compositor saw the press, and if we took the
// release it would think the button is stuck and keep sending every click
// and scroll to the window under it.
func openSources(cfg *config.Config) ([]source, error) {
	var srcs []source
	var permErr error
	for _, n := range evdev.ListNodes() {
		if strings.HasPrefix(n.Name, evdev.VirtualPrefix) || !cfg.Device.Matches(n.Vendor, n.Product, n.Name) {
			continue
		}
		dev, err := evdev.Open(n.Path)
		if err != nil {
			if errors.Is(err, unix.EACCES) {
				permErr = err
			}
			continue
		}
		wanted := dev.IsPointer()
		if !wanted {
			for code := range cfg.Buttons {
				wanted = wanted || (!evdev.IsMouseButton(code) && dev.HasKey(code))
			}
		}
		if !wanted {
			dev.Close()
			continue
		}
		srcs = append(srcs, source{dev: dev, pointer: dev.IsPointer()})
	}
	hasPointer := false
	for _, s := range srcs {
		hasPointer = hasPointer || s.pointer
	}
	if !hasPointer {
		closeSources(srcs)
		if permErr != nil {
			return nil, fmt.Errorf("%w (install the udev rule: see README)", permErr)
		}
		return nil, nil
	}
	for _, s := range srcs {
		down, err := s.dev.KeysDown()
		if err == nil && len(down) > 0 {
			err = errButtonsDown
		}
		if err != nil {
			closeSources(srcs)
			return nil, err
		}
	}
	for _, s := range srcs {
		if err := s.dev.Grab(); err != nil {
			closeSources(srcs)
			return nil, err
		}
	}
	return srcs, nil
}

// grabKey identifies which nodes a config needs grabbed; a reload only
// re-grabs when it changes.
func grabKey(cfg *config.Config) string {
	var codes []int
	for code := range cfg.Buttons {
		if !evdev.IsMouseButton(code) {
			codes = append(codes, int(code))
		}
	}
	sort.Ints(codes)
	return fmt.Sprintf("%+v %v", cfg.Device, codes)
}

func closeSources(srcs []source) {
	for _, s := range srcs {
		s.dev.Close()
	}
}

func run(cfgPath string, verbose bool) error {
	cfg, err := loadOrDefault(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Device.Empty() {
		return fmt.Errorf("no [device] configured; refusing to grab every mouse")
	}

	mkeys, mrels := evdev.PointerCaps()
	vmouse, err := evdev.NewVirtual("pointer", 1, mkeys, mrels)
	if err != nil {
		return err
	}
	defer vmouse.Close()
	vkbd, err := evdev.NewVirtual("keyboard", 2, evdev.KeyboardCaps(), nil)
	if err != nil {
		return err
	}
	defer vkbd.Close()

	eng := engine.New(cfg, sink{vmouse, vkbd})
	eng.Verbose = verbose

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var srcs []source
	defer func() { eng.Reset(); closeSources(srcs) }()
	var nextScan, lastCfgCheck time.Time
	cfgMtime := mtime(cfgPath)
	waiting, waitingRelease := false, false

	// release lets go of the device; eng.Reset first releases anything we
	// hold down on the virtual devices, since the device's own releases
	// won't reach us any more.
	release := func() {
		eng.Reset()
		closeSources(srcs)
		srcs = nil
	}

	reload := func() {
		newCfg, err := loadOrDefault(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, app.ID+": keeping old config:", err)
			return
		}
		fmt.Fprintln(os.Stderr, app.ID+": config reloaded")
		regrab := grabKey(newCfg) != grabKey(cfg)
		eng.SetConfig(newCfg)
		cfg = newCfg
		if regrab {
			release()
			nextScan = time.Time{}
		}
	}

	for {
		select {
		case s := <-sigs:
			if s == syscall.SIGHUP {
				reload()
				continue
			}
			return nil
		default:
		}
		now := time.Now()

		if now.Sub(lastCfgCheck) > time.Second {
			lastCfgCheck = now
			if m := mtime(cfgPath); !m.Equal(cfgMtime) {
				cfgMtime = m
				reload()
			}
		}

		if srcs == nil && !now.Before(nextScan) {
			nextScan = now.Add(2 * time.Second)
			srcs, err = openSources(cfg)
			switch {
			case errors.Is(err, errButtonsDown):
				nextScan = now.Add(20 * time.Millisecond)
				if !waitingRelease {
					fmt.Fprintln(os.Stderr, app.ID+": waiting for buttons to be released before grabbing")
					waitingRelease = true
				}
			case err != nil:
				fmt.Fprintln(os.Stderr, app.ID+":", err)
			case srcs == nil && !waiting:
				fmt.Fprintln(os.Stderr, app.ID+": waiting for device to appear…")
				waiting = true
			case srcs != nil:
				waiting, waitingRelease = false, false
				eng.SourceHiRes = [2]bool{}
				for _, s := range srcs {
					fmt.Fprintf(os.Stderr, app.ID+": grabbed %s (%s)\n", s.dev.Path, s.dev.Name)
					if s.pointer {
						eng.SourceHiRes = [2]bool{s.dev.HasRel(evdev.REL_WHEEL_HI_RES), s.dev.HasRel(evdev.REL_HWHEEL_HI_RES)}
					}
				}
			}
		}

		timeout := 250 * time.Millisecond
		if srcs == nil {
			timeout = min(timeout, max(time.Until(nextScan), 0))
		}
		if dl := eng.NextDeadline(); !dl.IsZero() {
			timeout = min(timeout, max(time.Until(dl), 0))
		}
		pfds := make([]unix.PollFd, len(srcs))
		for i, s := range srcs {
			pfds[i] = unix.PollFd{Fd: int32(s.dev.Fd()), Events: unix.POLLIN}
		}
		ms := int((timeout + time.Millisecond - 1) / time.Millisecond)
		if _, err := unix.Poll(pfds, ms); err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("poll: %w", err)
		}

		lost := false
		for i, p := range pfds {
			if p.Revents == 0 {
				continue
			}
			evs, err := srcs[i].dev.Read()
			now := time.Now()
			for _, ev := range evs {
				eng.Handle(ev, srcs[i].pointer, now)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, app.ID+": lost %s: %v\n", srcs[i].dev.Path, err)
				lost = true
			}
		}
		if lost {
			release()
		}
		eng.Tick(time.Now())
	}
}

func mtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func detect(cfgPath string, all bool) error {
	var match config.DeviceMatch
	if !all {
		cfg, err := loadOrDefault(cfgPath)
		if err != nil {
			return err
		}
		match = cfg.Device
	}
	devs, denied := evdev.OpenNodes(func(n evdev.NodeInfo) bool {
		return match.Matches(n.Vendor, n.Product, n.Name) && (!all || n.Motion || strings.Contains(strings.ToLower(n.Name), "keyboard"))
	})
	for _, dev := range devs {
		fmt.Printf("watching %s  %04x:%04x  %s\n", dev.Path, dev.Vendor, dev.Product, dev.Name)
	}
	if len(denied) > 0 {
		fmt.Printf("no permission for %s\n  → install the udev rule (see README) or run with sudo\n", strings.Join(denied, " "))
	}
	if len(devs) == 0 {
		return fmt.Errorf("no matching devices to watch")
	}
	fmt.Println("\nPress each button (and turn/tilt the wheel). Ctrl+C to quit.")

	pfds := make([]unix.PollFd, len(devs))
	for i, d := range devs {
		pfds[i] = unix.PollFd{Fd: int32(d.Fd()), Events: unix.POLLIN}
	}
	for {
		if _, err := unix.Poll(pfds, -1); err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		for i, p := range pfds {
			if p.Revents == 0 {
				continue
			}
			evs, err := devs[i].Read()
			for _, ev := range evs {
				switch {
				case ev.Type == evdev.EV_KEY && ev.Value == 1:
					fmt.Printf("%-18s pressed    → config: [buttons.%s]   (%s)\n",
						evdev.KeyName(ev.Code), action.InputName(ev.Code), devs[i].Path)
				case ev.Type == evdev.EV_REL && ev.Code != evdev.REL_X && ev.Code != evdev.REL_Y:
					fmt.Printf("%-18s %+d   (%s)\n", evdev.RelName(ev.Code), ev.Value, devs[i].Path)
				}
			}
			if err != nil {
				return fmt.Errorf("%s: %w", devs[i].Path, err)
			}
		}
	}
}

func list() {
	fmt.Printf("%-20s %-10s %-8s %s\n", "NODE", "ID", "ACCESS", "NAME")
	for _, n := range evdev.ListNodes() {
		access := "yes"
		if unix.Access(n.Path, unix.R_OK) != nil {
			access = "no"
		}
		fmt.Printf("%-20s %04x:%04x  %-8s %s\n", n.Path, n.Vendor, n.Product, access, n.Name)
	}
}
