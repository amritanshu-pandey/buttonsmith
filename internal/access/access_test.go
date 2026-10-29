package access

import (
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	v, p, err := ParseID("25a7:fa61")
	if err != nil || v != 0x25a7 || p != 0xfa61 {
		t.Fatalf("got %04x:%04x %v", v, p, err)
	}
	for _, bad := range []string{"", "25a7", "25a7fa61", "25a7:fa6", "+5a7:fa61", "25a7:fa61\n", "25a7:fa61 x", "zzzz:fa61"} {
		if _, _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) should fail", bad)
		}
	}
}

func TestRule(t *testing.T) {
	r := Rule(0x25a7, 0xfa61)
	if !strings.Contains(r, `ATTRS{id/vendor}=="25a7", ATTRS{id/product}=="fa61", TAG+="uaccess"`) {
		t.Errorf("unexpected rule:\n%s", r)
	}
}
