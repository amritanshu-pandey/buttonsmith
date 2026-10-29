// Package access grants the logged-in user read access to one mouse by
// writing a udev rule that tags its event nodes with "uaccess".
//
// Writing the rule needs root. The settings window runs
// `buttonsmith allow-device VVVV:PPPP` through pkexec, which asks for the
// admin password with the desktop's polkit dialog.
package access

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/amritanshu-pandey/buttonsmith/internal/app"
)

// RulePath holds the rule for the chosen mouse. It must sort before
// 73-seat-late.rules, which turns "uaccess" into an ACL.
const RulePath = "/etc/udev/rules.d/71-" + app.ID + "-device.rules"

// Rule returns the udev rule granting access to the device. Matching on the
// input device's id/vendor and id/product covers USB and Bluetooth mice.
func Rule(vendor, product uint16) string {
	return fmt.Sprintf("# Written by `%s allow-device`: lets the logged-in user read this mouse.\n"+
		"SUBSYSTEM==\"input\", KERNEL==\"event*\", ATTRS{id/vendor}==\"%04x\", ATTRS{id/product}==\"%04x\", TAG+=\"uaccess\"\n",
		app.ID, vendor, product)
}

// ParseID parses "vvvv:pppp" (hex).
func ParseID(s string) (vendor, product uint16, err error) {
	bad := fmt.Errorf("invalid device id %q, want VVVV:PPPP (hex)", s)
	if len(s) != 9 || s[4] != ':' {
		return 0, 0, bad
	}
	v, err1 := strconv.ParseUint(s[:4], 16, 16)
	p, err2 := strconv.ParseUint(s[5:], 16, 16)
	if err1 != nil || err2 != nil {
		return 0, 0, bad
	}
	return uint16(v), uint16(p), nil
}

// Install writes the rule (replacing any previously allowed mouse) and
// re-applies udev rules to input devices so the ACL takes effect without
// replugging. Must run as root.
func Install(vendor, product uint16) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("allow-device must run as root (try: pkexec %s allow-device %04x:%04x)", app.ID, vendor, product)
	}
	rule := []byte(Rule(vendor, product))
	if old, err := os.ReadFile(RulePath); err != nil || !bytes.Equal(old, rule) {
		if err := os.WriteFile(RulePath, rule, 0o644); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{"control", "--reload-rules"},
		{"trigger", "--subsystem-match=input", "--action=change"},
		{"settle", "--timeout=5"},
	} {
		if out, err := exec.Command("udevadm", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("udevadm %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Request asks for admin rights with pkexec and runs Install as root.
func Request(vendor, product uint16) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	out, err := exec.Command("pkexec", exe, "allow-device", fmt.Sprintf("%04x:%04x", vendor, product)).CombinedOutput()
	if err == nil {
		return nil
	}
	if e, ok := err.(*exec.ExitError); ok && (e.ExitCode() == 126 || e.ExitCode() == 127) {
		return fmt.Errorf("permission was not granted")
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return err
}
