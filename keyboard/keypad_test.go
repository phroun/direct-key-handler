package keyboard

import (
	"strconv"
	"strings"
	"testing"
)

// The keypad arrows pointed the wrong way.
//
// 57417 is KP_LEFT and this table called it "Up"; 57419 is KP_UP and it called
// that "Left". The whole block was rotated, so a keypad arrow moved the cursor
// ninety degrees from the direction printed on the cap — and silently, because
// every name it produced was a real name that a keymap would happily bind.
func TestKeypadArrowsPointWhereTheCapSays(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[57417u", "P-Left"},
		{"\x1b[57418u", "P-Right"},
		{"\x1b[57419u", "P-Up"},
		{"\x1b[57420u", "P-Down"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}

// A keypad key is told apart from the main-cluster key it duplicates.
//
// The pad is a modal double of keys that already exist: it has its own Home,
// its own Enter, its own arrows, its own erase. Naming them "Home" and "Enter"
// made the two physically distinct keys arrive under one name, so an
// application could not bind them apart even though the terminal had told it
// exactly which was pressed. The prefix says which one, and costs no new names.
func TestKeypadKeysAreDistinctFromTheMainCluster(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x1b[57423u", "P-Home", "the pad's Home"},
		{"\x1b[1~", "Home", "and the main cluster's, which keeps the bare name"},
		{"\x1b[57414u", "P-Enter", "the pad's Enter"},
		{"\r", "Return", "and the home row's Return, a different key"},
		{"\x1b[57426u", "P-Delete", "the pad's erase, a pad action"},
		{"\x1b[3~", "FDel", "and forward delete, which is not it"},
		{"\x7f", "Delete", "and the erase-left key, which is also not it"},
		{"\x1b[57425u", "P-Insert", ""},
		{"\x1b[57421u", "P-PageUp", ""},
		{"\x1b[57422u", "P-PageDown", ""},
		{"\x1b[57424u", "P-End", ""},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q (%s) parsed as %v, want [%s]", tc.raw, tc.what, got, tc.want)
		}
	}
}

// NumLock is already decided by the time the keycode arrives, and the two
// answers are two different keycodes for the one physical key: 57406 is the
// pad's 7 with the lock on, 57423 is the same key's Home with it off. Reading
// both is what lets an application bind either without tracking lock state.
func TestNumLockPicksTheKeycodeAndBothAreRead(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[57399u", "P-0"},
		{"\x1b[57400u", "P-1"},
		{"\x1b[57401u", "P-2"},
		{"\x1b[57402u", "P-3"},
		// 57403 and 57405 are not filler. Their low bytes are ';' and '=',
		// and a truncating symbol test claimed them before the keypad table
		// was consulted, so keypad 4 typed a semicolon and keypad 6 typed an
		// equals sign. Sampling this range is what hid that; every code in it
		// is named here now.
		{"\x1b[57403u", "P-4"},
		{"\x1b[57405u", "P-6"},
		{"\x1b[57406u", "P-7"}, // KP_0 is 57399, so the 7 is 57406
		{"\x1b[57407u", "P-8"},
		{"\x1b[57408u", "P-9"},
		{"\x1b[57409u", "P-."},
		{"\x1b[57410u", "P-/"},
		{"\x1b[57411u", "P-*"},
		{"\x1b[57412u", "P--"},
		{"\x1b[57413u", "P-+"},
		{"\x1b[57415u", "P-="},
		// KP_SEPARATOR is the LOWERCASE comma. kitty resolves it from the xkb
		// keysym, and X11's keypad keysyms are the DEC LK201's pad — which
		// wears its comma in the right-hand column above Enter, the archaic
		// one. "P-," is the PC-98's, in the bottom row beside the period.
		{"\x1b[57416u", "p-,"},
		// The 5 with the lock off is the one pad key that duplicates nothing
		// elsewhere, so it is the one base name the pad needed of its own.
		{"\x1b[57427u", "P-Begin"},
		{"\x1b[57404u", "P-5"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}

// No keycode in the pad's range escapes the pad.
//
// The table above says what each key IS; this says that none of them is
// something else. A parse path that claims a keycode before the keypad table
// is reached produces a real key — a symbol, a letter — so nothing looks
// broken downstream, and the only way to catch it is to assert over the whole
// contiguous range rather than the codes someone thought to list.
func TestNothingInTheKeypadRangeEscapesThePad(t *testing.T) {
	for code := 57399; code <= 57427; code++ {
		raw := "\x1b[" + strconv.Itoa(code) + "u"
		got := feedKeys(t, raw)
		if len(got) != 1 {
			t.Errorf("%q produced %v, want exactly one key", raw, got)
			continue
		}
		if !strings.HasPrefix(got[0], "P-") && !strings.HasPrefix(got[0], "p-") {
			t.Errorf("keycode %d (low byte %q) parsed as %q — something claimed "+
				"it before the keypad table", code, byte(code), got[0])
		}
	}
}

// Control on a SHOWN pad key takes the caret, the same as anywhere else.
//
// A shown key IS its character, and Control against a character is written
// "^7" throughout this vocabulary. Emitting "C-P-7" would have invented a
// spelling nothing reads: the main number row already produces "^7", so the
// pad's 7 under Control would have been unbindable alongside it. A NAMED pad
// key keeps "C-", because a name has no character for the caret to sit against.
func TestControlOnAShownKeypadKeyTakesTheCaret(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x1b[57406;5u", "P-^7", "control on a shown pad key"},
		{"\x1b[57406;6u", "S-P-^7", "shift keeps its prefix, ahead of the pad's"},
		{"\x1b[57412;5u", "P-^-", "and punctuation is shown too"},
		{"\x1b[57416;5u", "p-^,", "the lowercase pad prefix behaves the same"},
		{"\x1b[57423;5u", "C-P-Home", "but a named pad key keeps C-"},
		{"\x1b[57414;5u", "C-P-Enter", ""},
		{"\x1b[57427;5u", "C-P-Begin", ""},
		// Without Control the shown key is just itself under the prefix.
		{"\x1b[57406;2u", "S-P-7", "shift alone leaves the character alone"},
		{"\x1b[57406;3u", "M-P-7", "and so does Mega"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q (%s) parsed as %v, want [%s]", tc.raw, tc.what, got, tc.want)
		}
	}
}

// The event type rides in the modifier field, and the caret path must hand it
// back on rather than dropping it — a release that arrives spelled as a press
// is a key that never comes up.
func TestKeypadCaretKeepsTheEventSuffix(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[57406;5:3u", "P-^7:Release"},
		{"\x1b[57406;5:2u", "P-^7:Repeat"},
		{"\x1b[57423;5:3u", "C-P-Home:Release"},
		{"\x1b[57406;1:3u", "P-7:Release"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}

// Both pad prefixes split the same way, including the members no keycode
// reaches.
//
// Where a pad character exists twice only ONE of the pair has a keycode: the
// separator arrives as "p-," and the PC-98's "P-," does not arrive at all,
// while equals goes the other way round — "P-=" arrives and "p-=" does not. A
// byte feed can therefore only ever exercise half of this, so the split is
// pinned where it lives. The host that eventually reads HID usage IDs should
// find the rule already working rather than be the thing that tests it.
func TestTheLowercasePadPrefixSplitsTheSameWay(t *testing.T) {
	for _, tc := range []struct {
		name, pad, shown string
		ok               bool
	}{
		{"p-,", "p-", ",", true},
		{"p-=", "p-", "=", true},
		{"P-,", "P-", ",", true},
		{"p-Enter", "", "", false},  // a name has no character for the caret
		{"P-Begin", "", "", false},  // nor does this one
		{"Home", "", "", false},     // and a non-pad name falls straight through
		{"C-P-Home", "", "", false}, // a stacked prefix is peeled before this
	} {
		pad, shown, ok := splitPadShownKey(tc.name)
		if ok != tc.ok || pad != tc.pad || shown != tc.shown {
			t.Errorf("splitPadShownKey(%q) = %q, %q, %v; want %q, %q, %v",
				tc.name, pad, shown, ok, tc.pad, tc.shown, tc.ok)
		}
	}
}

// An application's names reach through the lowercase pad prefix too.
//
// Renaming is driven off namePrefixes, and a prefix missing from that list is
// never peeled — the base never reaches the name table and the application gets
// this package's spelling instead. The only "p-" a decoder emits today is the
// separator, whose base is a character with no name of its own, so a byte feed
// cannot reach this at all. Pinned directly instead, for the same reason: the
// emitter that arrives later should find the path already working.
func TestNamesReachThroughTheLowercasePadPrefix(t *testing.T) {
	f := false
	h := New(Options{ManageTerminal: &f, KeyNames: map[Key]string{
		KeyHome: "hm", KeyBegin: "mid", KeyKeypadEnter: "kpenter",
	}})
	for _, tc := range []struct{ in, want string }{
		{"p-Home", "p-hm"},
		{"p-Begin", "p-mid"},
		{"C-p-Home", "C-p-hm"},
		{"p-Enter:Release", "p-kpenter:Release"},
		{"p-,", "p-,"},     // no name of its own; passes through untouched
		{"P-Home", "P-hm"}, // and the uppercase form still works
	} {
		if got := h.displayKey(tc.in); got != tc.want {
			t.Errorf("displayKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Renaming reaches through the pad prefix, as it does through every other one.
// An application that calls Home "hm" gets "P-hm" for the pad's, without having
// to know that a pad prefix exists.
func TestApplicationNamesApplyUnderThePadPrefix(t *testing.T) {
	names := map[Key]string{
		KeyHome:        "hm",
		KeyBegin:       "mid",
		KeyKeypadEnter: "kpenter",
	}
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[57423u", "P-hm"},
		{"\x1b[57423;5u", "C-P-hm"},
		{"\x1b[57427u", "P-mid"},
		{"\x1b[57414u", "P-kpenter"},
		{"\x1b[1~", "hm"}, // the main cluster's, renamed by the same entry
	} {
		if got := feedKeysNamed(t, tc.raw, names); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}

// The keys an American keyboard does not have still need names, because their
// characters are already spoken for.
//
// A shown key normally IS its character, and that works only because the
// character names a position on the US grid. HID 100 prints "<" and ">" on a
// German board — characters that belong to Shift+comma and Shift+period — and
// 135 and 137 print "\" and "|", which belong to the key at HID 49. Left as
// characters they would collide with keys that already exist, so a keymap
// could not tell the positions apart. These names are what it binds instead.
func TestKeysWithNoAmericanCharacterAreNameable(t *testing.T) {
	for _, tc := range []struct {
		key  Key
		want string
	}{
		{KeyZig, "Zig"},           // ISO, beside Return (HID 50)
		{KeyZag, "Zag"},           // ISO, between LeftShift and Z (HID 100)
		{KeyRo, "Ro"},             // JIS, beside RightShift (HID 135)
		{KeyYen, "Yen"},           // JIS, beside Delete (HID 137)
		{KeyKanaLock, "KanaLock"}, // HID 136
		{KeyHangulLock, "HangulLock"},
		{KeyHenkan, "Henkan"},     // converts the pending kana
		{KeyMuhenkan, "Muhenkan"}, // commits it unconverted
		{KeyHanja, "Hanja"},       // converts the preceding Hangul
		{KeyBegin, "Begin"},
	} {
		if got := tc.key.DefaultName(); got != tc.want {
			t.Errorf("DefaultName() = %q, want %q", got, tc.want)
		}
		if _, ok := keyByDefaultName[tc.want]; !ok {
			t.Errorf("%q does not resolve back to a Key, so an application "+
				"could not override it", tc.want)
		}
	}

	// AllKeys is what an application iterates to prove its own table is
	// complete, so a key missing from it is a key that reaches the application
	// under this package's spelling with nobody noticing.
	inAll := make(map[Key]bool, len(defaultKeyNames))
	for _, k := range AllKeys() {
		inAll[k] = true
	}
	for _, k := range []Key{KeyZig, KeyZag, KeyRo, KeyYen, KeyKanaLock,
		KeyHangulLock, KeyHenkan, KeyMuhenkan, KeyHanja, KeyBegin} {
		if !inAll[k] {
			t.Errorf("%v is missing from AllKeys()", k)
		}
	}
}
