package keyboard

import (
	"io"
	"testing"
	"time"
)

// feedKeysNamed is feedKeys with an application name table installed.
func feedKeysNamed(t *testing.T, raw string, names map[Key]string) []string {
	t.Helper()
	pr, pw := io.Pipe()
	f := false
	h := New(Options{InputReader: pr, ManageTerminal: &f, KeyNames: names})
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

// An application's names replace the defaults on the way out, so it never has
// to translate them itself.
func TestKeyNamesRenameOnEmit(t *testing.T) {
	names := map[Key]string{
		KeyEscape:    "esc",
		KeyPageUp:    "pgup",
		KeyDelete:    "fdel",
		KeyReturn:    "return",
		KeyBackspace: "back",
	}
	cases := []struct{ raw, want string }{
		{"\x1b\x1b", "esc"},   // ESC ESC resolves a bare Escape
		{"\x1b[5~", "pgup"},   // legacy CSI path
		{"\x1b[3~", "fdel"},   // legacy tilde path
		{"\r", "return"},      // control byte path
		{"\x7f", "back"},      // DEL
		{"\x1b[57364u", "F1"}, // unlisted: keeps the default name
	}
	for _, c := range cases {
		got := feedKeysNamed(t, c.raw, names)
		if len(got) == 0 || got[len(got)-1] != c.want {
			t.Errorf("%q -> %v, want %s", c.raw, got, c.want)
		}
	}
}

// Renaming applies to the base name, leaving the modifier prefix this package
// put in front of it and any event suffix in place.
func TestKeyNamesKeepPrefixAndSuffix(t *testing.T) {
	names := map[Key]string{KeyPageUp: "pgup", KeyLeft: "left"}
	cases := []struct{ raw, want string }{
		{"\x1b[5;3~", "M-pgup"},             // legacy modified tilde
		{"\x1b[1;5D", "C-left"},             // legacy modified cursor key
		{"\x1b[57421;1:3u", "pgup:Release"}, // kitty, with an event suffix
	}
	for _, c := range cases {
		got := feedKeysNamed(t, c.raw, names)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}

// With no table, every name is the default - the behavior every existing
// consumer already depends on.
func TestKeyNamesDefaultUnchanged(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"\x1b[5~", "PageUp"},
		{"\r", "Return"},
		{"\x1b[3~", "Delete"},
	}
	for _, c := range cases {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}

// The home-row key and the keypad's are two physical keys and must stay
// distinguishable: an application folds them together by naming both, rather
// than having the choice made for it.
func TestReturnAndKeypadEnterAreDistinct(t *testing.T) {
	if got := feedKeys(t, "\r"); len(got) != 1 || got[0] != "Return" {
		t.Errorf("CR -> %v, want [Return]", got)
	}
	if got := feedKeys(t, "\x1b[57414u"); len(got) != 1 || got[0] != "Enter" {
		t.Errorf("KP_Enter -> %v, want [Enter]", got)
	}

	folded := map[Key]string{KeyReturn: "return", KeyKeypadEnter: "return"}
	if got := feedKeysNamed(t, "\r", folded); len(got) != 1 || got[0] != "return" {
		t.Errorf("folded CR -> %v, want [return]", got)
	}
	if got := feedKeysNamed(t, "\x1b[57414u", folded); len(got) != 1 || got[0] != "return" {
		t.Errorf("folded KP_Enter -> %v, want [return]", got)
	}
}

// AllKeys enumerates every nameable key, so an application can assert its own
// table is complete instead of discovering a gap when a key reaches it under
// this package's spelling.
func TestAllKeysAreNameableAndUnique(t *testing.T) {
	all := AllKeys()
	if len(all) < 30 {
		t.Fatalf("AllKeys returned %d keys, expected the full set", len(all))
	}
	seen := make(map[string]Key, len(all))
	for _, k := range all {
		name := k.DefaultName()
		if name == "" {
			t.Errorf("%v has no default name", k)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("default name %q used by both %v and %v", name, prev, k)
		}
		seen[name] = k
		if keyByDefaultName[name] != k {
			t.Errorf("%q does not resolve back to %v", name, k)
		}
	}
}
