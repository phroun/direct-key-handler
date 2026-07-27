package keyboard

import (
	"io"
	"testing"
	"time"
)

// feedKeys runs raw bytes through a handler (no terminal management) and
// collects the parsed keys emitted within a short window.
func feedKeys(t *testing.T, raw string) []string {
	t.Helper()
	pr, pw := io.Pipe()
	f := false
	h := New(Options{InputReader: pr, ManageTerminal: &f})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	go func() {
		pw.Write([]byte(raw))
		pw.Close()
	}()
	var keys []string
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case k := <-h.Keys:
			keys = append(keys, k)
		case <-deadline:
			return keys
		}
	}
}

// F1-F4 arrive as CSI-number sequences from terminals with the Kitty keyboard
// protocol enabled (and from legacy xterm-R6/rxvt terminals); they must parse
// as function keys, not leak "11~" into the application as text.
func TestLegacyCSIFunctionKeys(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"\x1b[11~", "F1"},
		{"\x1b[12~", "F2"},
		{"\x1b[13~", "F3"},
		{"\x1b[14~", "F4"},
		{"\x1bOP", "F1"}, // SS3 forms still work
		{"\x1b[15~", "F5"},
		{"\x1b[[A", "F1"}, // Linux console
		{"\x1b[[E", "F5"},
	}
	for _, c := range cases {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}

// Modified forms (ESC [ 11 ; mod ~) resolve through the tilde parser.
func TestModifiedLegacyF1(t *testing.T) {
	got := feedKeys(t, "\x1b[11;2~")
	if len(got) != 1 || got[0] != "S-F1" {
		t.Errorf("shift-F1 -> %v, want [S-F1]", got)
	}
}

// Home (ESC [ 1 ~) still resolves: the added 11~ entry must not disturb the
// shared-prefix disambiguation.
func TestHomeStillWorks(t *testing.T) {
	got := feedKeys(t, "\x1b[1~")
	if len(got) != 1 || got[0] != "Home" {
		t.Errorf("home -> %v, want [Home]", got)
	}
}
