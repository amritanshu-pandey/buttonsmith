// Package evdev is a minimal, cgo-free wrapper around the Linux evdev and
// uinput interfaces: just enough to read, grab and re-emit mouse events.
package evdev

//go:generate go run ./gen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Event types and codes used here (from linux/input-event-codes.h).
const (
	EV_SYN = 0x00
	EV_KEY = 0x01
	EV_REL = 0x02
	EV_MSC = 0x04

	SYN_REPORT  = 0
	SYN_DROPPED = 3

	REL_X             = 0x00
	REL_Y             = 0x01
	REL_HWHEEL        = 0x06
	REL_WHEEL         = 0x08
	REL_WHEEL_HI_RES  = 0x0b
	REL_HWHEEL_HI_RES = 0x0c

	KEY_LEFTCTRL = 29
	BTN_LEFT     = 0x110
	BTN_RIGHT    = 0x111
	BTN_MIDDLE   = 0x112
	BTN_SIDE     = 0x113
	BTN_EXTRA    = 0x114
	BTN_TASK     = 0x117

	keyMax = 0x2ff
	relMax = 0x0f
)

// Event is one input event without its timestamp.
type Event struct {
	Type  uint16
	Code  uint16
	Value int32
}

func (e Event) String() string {
	switch e.Type {
	case EV_KEY:
		return fmt.Sprintf("%s %d", KeyName(e.Code), e.Value)
	case EV_REL:
		return fmt.Sprintf("%s %d", RelName(e.Code), e.Value)
	}
	return fmt.Sprintf("type=%d code=%d value=%d", e.Type, e.Code, e.Value)
}

// KeyCode looks up a KEY_*/BTN_* name (case-insensitive).
func KeyCode(name string) (uint16, bool) {
	c, ok := keyCodes[strings.ToUpper(name)]
	return c, ok
}

// KeyName returns the preferred name of a key/button code.
func KeyName(code uint16) string {
	if n, ok := keyNames[code]; ok {
		return n
	}
	return fmt.Sprintf("KEY_0x%x", code)
}

// RelName returns the REL_* name of a relative axis code.
func RelName(code uint16) string {
	if n, ok := relNames[code]; ok {
		return n
	}
	return fmt.Sprintf("REL_0x%x", code)
}

// IsMouseButton reports whether code belongs on the virtual mouse rather
// than the virtual keyboard.
func IsMouseButton(code uint16) bool { return code >= BTN_LEFT && code <= BTN_TASK }

// ioctl request encoding (asm-generic/ioctl.h).
func ioc(dir, typ, nr, size uintptr) uintptr { return dir<<30 | size<<16 | typ<<8 | nr }

const (
	iocWrite = 1
	iocRead  = 2
)

func ioctlPtr(fd int, req uintptr, p unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(p)); errno != 0 {
		return errno
	}
	return nil
}

// Device is an opened /dev/input/eventN node.
type Device struct {
	Path    string
	Name    string
	Vendor  uint16
	Product uint16
	fd      int
	keys    []byte
	rels    []byte
	dropped bool
}

// Open opens an event node in non-blocking mode and reads its capabilities.
func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	d := &Device{Path: path, fd: fd}

	name := make([]byte, 256)
	if err := ioctlPtr(fd, ioc(iocRead, 'E', 0x06, uintptr(len(name))), unsafe.Pointer(&name[0])); err == nil {
		d.Name = string(bytes.TrimRight(name, "\x00"))
	}
	var id [4]uint16 // bustype, vendor, product, version
	if err := ioctlPtr(fd, ioc(iocRead, 'E', 0x02, 8), unsafe.Pointer(&id[0])); err == nil {
		d.Vendor, d.Product = id[1], id[2]
	}
	d.keys = make([]byte, keyMax/8+1)
	d.rels = make([]byte, relMax/8+1)
	_ = ioctlPtr(fd, ioc(iocRead, 'E', 0x20+EV_KEY, uintptr(len(d.keys))), unsafe.Pointer(&d.keys[0]))
	_ = ioctlPtr(fd, ioc(iocRead, 'E', 0x20+EV_REL, uintptr(len(d.rels))), unsafe.Pointer(&d.rels[0]))
	return d, nil
}

func testBit(bits []byte, n uint16) bool {
	return int(n/8) < len(bits) && bits[n/8]&(1<<(n%8)) != 0
}

func (d *Device) HasKey(code uint16) bool { return testBit(d.keys, code) }
func (d *Device) HasRel(code uint16) bool { return testBit(d.rels, code) }

// IsPointer reports whether the node is a mouse (buttons + motion).
func (d *Device) IsPointer() bool { return d.HasKey(BTN_LEFT) && d.HasRel(REL_X) }

func (d *Device) Fd() int { return d.fd }

// KeysDown returns the keys/buttons currently held on the device.
func (d *Device) KeysDown() ([]uint16, error) {
	state := make([]byte, keyMax/8+1)
	if err := ioctlPtr(d.fd, ioc(iocRead, 'E', 0x18, uintptr(len(state))), unsafe.Pointer(&state[0])); err != nil {
		return nil, fmt.Errorf("read key state of %s: %w", d.Path, err)
	}
	var down []uint16
	for code := uint16(0); code <= keyMax; code++ {
		if testBit(state, code) {
			down = append(down, code)
		}
	}
	return down, nil
}

// Grab takes exclusive access: the compositor stops seeing this node's events.
func (d *Device) Grab() error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), ioc(iocWrite, 'E', 0x90, 4), 1)
	if errno != 0 {
		return fmt.Errorf("grab %s: %w", d.Path, errno)
	}
	return nil
}

func (d *Device) Close() error { return unix.Close(d.fd) }

const eventSize = 24 // struct input_event on 64-bit: timeval(16) + type + code + value

// Read returns all events currently queued. Returns (nil, nil) when nothing
// is pending. Events between SYN_DROPPED and the next SYN_REPORT are
// discarded, as the kernel documentation recommends.
func (d *Device) Read() ([]Event, error) {
	buf := make([]byte, eventSize*64)
	var out []Event
	for {
		n, err := unix.Read(d.fd, buf)
		if errors.Is(err, unix.EAGAIN) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if n == 0 {
			return out, unix.ENODEV
		}
		for i := 0; i+eventSize <= n; i += eventSize {
			ev := Event{
				Type:  binary.NativeEndian.Uint16(buf[i+16:]),
				Code:  binary.NativeEndian.Uint16(buf[i+18:]),
				Value: int32(binary.NativeEndian.Uint32(buf[i+20:])),
			}
			if ev.Type == EV_SYN && ev.Code == SYN_DROPPED {
				d.dropped = true
				continue
			}
			if d.dropped {
				if ev.Type == EV_SYN && ev.Code == SYN_REPORT {
					d.dropped = false
				}
				continue
			}
			out = append(out, ev)
		}
	}
}

// NodeInfo describes an event node using sysfs only, so it works even when
// the node itself isn't readable.
type NodeInfo struct {
	Path    string
	Name    string
	Vendor  uint16
	Product uint16
	Motion  bool     // reports REL_X/REL_Y, i.e. a mouse-like node
	Buttons []uint16 // mouse buttons (BTN_LEFT..BTN_TASK) it reports
}

// ListNodes enumerates /dev/input/event* via /sys/class/input.
func ListNodes() []NodeInfo {
	dirs, _ := filepath.Glob("/sys/class/input/event*")
	var nodes []NodeInfo
	for _, dir := range dirs {
		read := func(rel string) string {
			b, _ := os.ReadFile(filepath.Join(dir, "device", rel))
			return strings.TrimSpace(string(b))
		}
		hex := func(rel string) uint16 {
			v, _ := strconv.ParseUint(read(rel), 16, 16)
			return uint16(v)
		}
		nodes = append(nodes, NodeInfo{
			Path:    "/dev/input/" + filepath.Base(dir),
			Name:    read("name"),
			Vendor:  hex("id/vendor"),
			Product: hex("id/product"),
			Motion:  hasBits(read("capabilities/rel"), REL_X, REL_Y),
			Buttons: mouseButtons(read("capabilities/key")),
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return eventNum(nodes[i].Path) < eventNum(nodes[j].Path) })
	return nodes
}

// hasBits reports whether a sysfs capability bitmap has all the given bits
// set. The bitmap is space-separated 64-bit hex words, highest first.
func hasBits(caps string, bits ...uint16) bool {
	words := strings.Fields(caps)
	for _, b := range bits {
		i := len(words) - 1 - int(b/64)
		if i < 0 {
			return false
		}
		w, err := strconv.ParseUint(words[i], 16, 64)
		if err != nil || w&(1<<(b%64)) == 0 {
			return false
		}
	}
	return len(bits) > 0
}

func mouseButtons(caps string) []uint16 {
	var out []uint16
	for b := uint16(BTN_LEFT); b <= BTN_TASK; b++ {
		if hasBits(caps, b) {
			out = append(out, b)
		}
	}
	return out
}

func eventNum(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "event"))
	return n
}

// OpenNodes opens every node accepted by keep (without grabbing). It
// returns the opened devices and the paths it had no permission for.
func OpenNodes(keep func(NodeInfo) bool) (devs []*Device, denied []string) {
	for _, n := range ListNodes() {
		if strings.HasPrefix(n.Name, VirtualPrefix) || !keep(n) {
			continue
		}
		dev, err := Open(n.Path)
		if err != nil {
			denied = append(denied, n.Path)
			continue
		}
		devs = append(devs, dev)
	}
	return devs, denied
}
