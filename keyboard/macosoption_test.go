package keyboard

import (
	"io"
	"testing"
	"time"
)

// optionProbe feeds raw input with the macOS Option decoding ON — its default
// on that platform — and returns the key names emitted.
func optionProbe(t *testing.T, raw string) []string {
	t.Helper()
	pr, pw := io.Pipe()
	manage, decode := false, true
	h := New(Options{InputReader: pr, ManageTerminal: &manage, DecodeMacOSOption: &decode})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	go func() {
		pw.Write([]byte(raw))
		pw.Close()
	}()

	var got []string
	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case k := <-h.Keys:
			got = append(got, k)
		case <-deadline:
			return got
		}
	}
}

// A backtick is a backtick.
//
// The Option table recognizes a CHARACTER, because the byte path carries no
// modifier field — so it can only hold characters that Option alone produces.
// '`' was in it, and a plain backtick came out "M-`": a key pressed all day
// reported as a chord almost nobody presses. It also split the press from its
// release, since the release replays the name recorded before the rewrite and
// the suffix hides it from the rewrite the second time: "M-`" down, "`:Release"
// up, for one press of one key.
func TestPlainBacktickIsNotAChord(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"`", "`", "a bare byte, no protocol"},
		{"\x1b[96u", "`", "the same key under the kitty protocol"},
	} {
		got := optionProbe(t, tc.raw)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: %q -> %v, want [%s]", tc.what, tc.raw, got, tc.want)
		}
	}

	// And its release names the press.
	got := optionProbe(t, "\x1b[96u\x1b[96;1:3u")
	want := []string{"`", "`:Release"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("press then release -> %v, want %v", got, want)
	}
}

// Option+backtick still reports as the chord it is wherever the terminal says
// so, which is the only place the two can be told apart.
func TestOptionBacktickUnderTheProtocol(t *testing.T) {
	got := optionProbe(t, "\x1b[96;3u") // mega bit set
	if len(got) != 1 || got[0] != "M-`" {
		t.Errorf("Option+backtick -> %v, want [M-`]", got)
	}
}

// The characters only Option can produce still decode, which is what the table
// is for.
func TestOptionCharactersStillDecode(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"å", "M-a"},
		{"≤", "M-,"},
		{"÷", "M-/"},
	} {
		got := optionProbe(t, tc.raw)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q -> %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}

// No entry may be keyed on a character an unmodified key produces. Every such
// entry silently takes that key away from the user.
func TestNoOptionEntryStealsAPlainKey(t *testing.T) {
	for r, name := range macOSOptionChars {
		if r < 128 {
			t.Errorf("%q maps to %q, but %q is what the plain key emits — "+
				"the table cannot tell them apart, so the key loses", r, name, r)
		}
	}
}
