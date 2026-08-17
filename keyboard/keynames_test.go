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
		KeyDEL:       "del",
	}
	cases := []struct{ raw, want string }{
		{"\x1b\x1b", "esc"},   // ESC ESC resolves a bare Escape
		{"\x1b[5~", "pgup"},   // legacy CSI path
		{"\x1b[3~", "fdel"},   // legacy tilde path
		{"\r", "return"},      // control byte path
		{"\x08", "back"},      // BS, the vt100 lineage's erase
		{"\x7f", "del"},       // DEL, what most terminals send for the same key
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
		{"\x1b[3~", "FDel"},
	}
	for _, c := range cases {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}

// The two Enter keys, told apart the only way a terminal offers.
//
// In numeric keypad mode both send CR, and a reader handed CR cannot know which
// was struck — that is the wire's limit, not this package's. In application
// keypad mode (DECKPAM) the keypad's sends SS3 M instead, and that sequence is
// the whole distinction. It decoded as three keystrokes — Escape, O, M — which
// left the keypad's Enter unreadable from any terminal that sent it, and made
// the KeyKeypadEnter constant something nothing could ever produce on the
// legacy path.
func TestBothEnterKeysAreReadable(t *testing.T) {
	for _, c := range []struct{ raw, want, what string }{
		{"\r", "Return", "CR is the home-row key"},
		{"\x1bOM", "Enter", "SS3 M is the keypad's, in application keypad mode"},
		{"\x1b[57414u", "Enter", "and the kitty protocol reports it by keycode"},
		{"\x1b[13u", "Return", "as it does the home-row key"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q -> %v, want [%s]", c.what, c.raw, got, c.want)
		}
	}

	// The two must not collapse onto one name, which is the point of all four.
	if feedKeys(t, "\r")[0] == feedKeys(t, "\x1bOM")[0] {
		t.Error("the home-row and keypad Enter keys decode to one name; an " +
			"application can no longer tell which key the user struck")
	}
}

// The three erase inputs are three names, and which name goes with which is
// the whole point: an application that maps terminal input to key events has
// to be able to tell them apart, and folding any two together destroys that
// before the application can see it.
//
// The pairing is easy to get backwards, and did go backwards more than once
// while it was being settled, so it is pinned here rather than left to the
// tables. BS and DEL both erase BEHIND the cursor — they are the same key on
// two lineages of terminal, and terminfo still answers both ways (kbs=^H for
// vt100/vt220/ansi, kbs=^? for xterm, linux, screen, tmux, rxvt). Only CSI 3 ~
// erases AHEAD, and it is the one named for the key rather than for a byte.
func TestEraseInputsAreThreeDistinctNames(t *testing.T) {
	for _, c := range []struct{ raw, want, what string }{
		{"\x08", "Backspace", "BS (8), what a vt100-lineage terminal sends"},
		{"\x7f", "Delete", "DEL (127), what most terminals in use send"},
		{"\x1b[3~", "FDel", "CSI 3 ~, the only one that erases forward"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q -> %v, want [%s]", c.what, c.raw, got, c.want)
		}
	}

	// And no two of them collide, which is the property a consumer relies on.
	seen := map[string]string{}
	for _, raw := range []string{"\x08", "\x7f", "\x1b[3~"} {
		got := feedKeys(t, raw)
		if len(got) != 1 {
			continue
		}
		if prev, dup := seen[got[0]]; dup {
			t.Errorf("%q and %q both emit %q; a consumer cannot tell them apart",
				prev, raw, got[0])
		}
		seen[got[0]] = raw
	}
}

// A person pressing backspace in line-read mode erases a character, whichever
// byte their terminal chose to send for it. Naming the two bytes apart is
// deliberate, but line assembly is where a human is typing, so it accepts
// both — matching only KeyBackspace left the key dead on every terminal whose
// kbs is ^?, which is most of them.
func TestLineModeErasesOnEitherEraseByte(t *testing.T) {
	for _, c := range []struct{ raw, what string }{
		{"\x08", "BS"},
		{"\x7f", "DEL"},
	} {
		h := New(Options{})
		h.inLineReadMode = true
		h.currentLine = []byte("ab")
		h.charByteLengths = []int{1, 1}
		h.handleLineAssembly(keyForRaw(t, c.raw))
		if got := string(h.currentLine); got != "a" {
			t.Errorf("%s: line = %q, want %q — the erase key did nothing", c.what, got, "a")
		}
	}
}

// keyForRaw feeds one raw sequence and returns the single key it produced.
func keyForRaw(t *testing.T, raw string) string {
	t.Helper()
	got := feedKeys(t, raw)
	if len(got) != 1 {
		t.Fatalf("%q produced %v, want exactly one key", raw, got)
	}
	return got[0]
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

// Power is in the vocabulary but no decoder here can produce it.
//
// That is the point of it. There is no escape sequence for a power key and no
// kitty keycode, so neither path in this package will ever emit one — but a
// graphical host reading HID usage 102 has a key to name, and needs the
// canonical spelling to come from here rather than be invented locally where
// the renaming could not see it.
//
// The test pins both halves: the name resolves and travels through renaming
// like any other, and nothing in the byte or protocol decoders answers to it.
func TestPowerIsNameableButNotDecodable(t *testing.T) {
	if got := KeyPower.DefaultName(); got != "Power" {
		t.Errorf("KeyPower.DefaultName() = %q, want %q", got, "Power")
	}

	// It takes an application's rename, which is how a host reaches it.
	var found bool
	for _, k := range AllKeys() {
		if k == KeyPower {
			found = true
		}
	}
	if !found {
		t.Error("KeyPower is absent from AllKeys, so a consumer building a " +
			"name table from this package would never see it")
	}
}
