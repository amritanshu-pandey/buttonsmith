package evdev

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// VirtualPrefix starts the name of every device we create, so we never
// try to grab our own output.
const VirtualPrefix = "Buttonsmith "

// Virtual is a uinput device that events are written to.
type Virtual struct {
	fd int
}

// uinput_setup from linux/uinput.h.
type uinputSetup struct {
	bustype, vendor, product, version uint16
	name                              [80]byte
	ffEffectsMax                      uint32
}

// NewVirtual creates a uinput device with the given key and relative-axis
// capabilities.
func NewVirtual(name string, product uint16, keys, rels []uint16) (*Virtual, error) {
	fd, err := unix.Open("/dev/uinput", unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w", err)
	}
	set := func(nr uintptr, v uint16) error {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), ioc(iocWrite, 'U', nr, 4), uintptr(v))
		if errno != 0 {
			return errno
		}
		return nil
	}
	const uiSetEvBit, uiSetKeyBit, uiSetRelBit = 100, 101, 102
	fail := func(err error) (*Virtual, error) {
		unix.Close(fd)
		return nil, fmt.Errorf("uinput setup %q: %w", name, err)
	}
	if len(keys) > 0 {
		if err := set(uiSetEvBit, EV_KEY); err != nil {
			return fail(err)
		}
		for _, k := range keys {
			if err := set(uiSetKeyBit, k); err != nil {
				return fail(err)
			}
		}
	}
	if len(rels) > 0 {
		if err := set(uiSetEvBit, EV_REL); err != nil {
			return fail(err)
		}
		for _, r := range rels {
			if err := set(uiSetRelBit, r); err != nil {
				return fail(err)
			}
		}
	}

	setup := uinputSetup{bustype: 0x06 /* BUS_VIRTUAL */, vendor: 0x1209, product: product, version: 1}
	copy(setup.name[:len(setup.name)-1], VirtualPrefix+name)
	if err := ioctlPtr(fd, ioc(iocWrite, 'U', 3, unsafe.Sizeof(setup)), unsafe.Pointer(&setup)); err != nil {
		return fail(err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), ioc(0, 'U', 1, 0), 0); errno != 0 {
		return fail(errno)
	}
	return &Virtual{fd: fd}, nil
}

// Emit writes the events followed by SYN_REPORT.
func (v *Virtual) Emit(evs []Event) error {
	buf := make([]byte, 0, (len(evs)+1)*eventSize)
	for _, e := range append(evs, Event{Type: EV_SYN, Code: SYN_REPORT}) {
		var raw [eventSize]byte // timestamp left zero; the kernel fills it in
		binary.NativeEndian.PutUint16(raw[16:], e.Type)
		binary.NativeEndian.PutUint16(raw[18:], e.Code)
		binary.NativeEndian.PutUint32(raw[20:], uint32(e.Value))
		buf = append(buf, raw[:]...)
	}
	_, err := unix.Write(v.fd, buf)
	return err
}

func (v *Virtual) Close() error {
	unix.Syscall(unix.SYS_IOCTL, uintptr(v.fd), ioc(0, 'U', 2, 0), 0)
	return unix.Close(v.fd)
}

// PointerCaps are the capabilities of the virtual mouse.
func PointerCaps() (keys, rels []uint16) {
	for k := uint16(BTN_LEFT); k <= BTN_TASK; k++ {
		keys = append(keys, k)
	}
	return keys, []uint16{REL_X, REL_Y, REL_HWHEEL, REL_WHEEL, REL_WHEEL_HI_RES, REL_HWHEEL_HI_RES}
}

// KeyboardCaps are the capabilities of the virtual keyboard: every keyboard
// key, but no button ranges (which would make it look like a mouse/joystick).
func KeyboardCaps() []uint16 {
	var keys []uint16
	for k := uint16(1); k < 0x100; k++ {
		keys = append(keys, k)
	}
	for k := uint16(0x160); k < 0x2c0; k++ {
		if k < 0x220 || k > 0x223 { // BTN_DPAD_*
			keys = append(keys, k)
		}
	}
	return keys
}
