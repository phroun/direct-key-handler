package keyboard

import (
	"strings"
	"testing"
)

// Hyper is synthesized from a doubled side modifier, the same way the graphical
// host has always done it.
//
// The four cases are the graphical host's own, quoted from its comment, and
// they are pinned together here so the two cannot drift: a keymap is one file
// for both, and a chord that means Hyper on one host and Control on the other
// is worse than a chord that means nothing anywhere.
func TestHyperFromADoubledSideModifier(t *testing.T) {
	for _, tc := range []struct {
		down      []string
		raw, want string
		what      string
	}{
		{
			down: []string{"\x1b[57442;1u", "\x1b[57448;1u"}, // both Ctrl
			raw:  "\x1b[120;5u", want: "H-x",
			what: "LCtrl+RCtrl+X",
		},
		{
			down: []string{"\x1b[57443;1u", "\x1b[57449;1u"}, // both Mega
			raw:  "\x1b[120;3u", want: "H-x",
			what: "LMega+RMega+X",
		},
		{
			// Both Mega beside a SINGLE Ctrl: the doubled one is spent on
			// Hyper, the single one keeps its normal role and its caret.
			down: []string{"\x1b[57443;1u", "\x1b[57449;1u"},
			raw:  "\x1b[120;7u", want: "H-^X",
			what: "LMega+RMega+Ctrl+X",
		},
		{
			// Both Ctrl beside a single Mega. Hyper sits in canonical rank
			// (C- G- M- m- S- s- H-), which is why it follows the M- here.
			down: []string{"\x1b[57442;1u", "\x1b[57448;1u"},
			raw:  "\x1b[120;3u", want: "M-H-x",
			what: "LCtrl+RCtrl+Mega+X",
		},
		{
			// Super doubles too: both Command caps, or both Windows caps.
			// Like Ctrl and Mega it sits on both sides of the space bar, which
			// is what makes holding the pair a gesture rather than an accident.
			down: []string{"\x1b[57444;1u", "\x1b[57450;1u"}, // both Super
			raw:  "\x1b[120;9u", want: "H-x",
			what: "LSuper+RSuper+X",
		},
		{
			// And a doubled Super beside a single Ctrl keeps the caret.
			down: []string{"\x1b[57444;1u", "\x1b[57450;1u"},
			raw:  "\x1b[120;13u", want: "H-^X",
			what: "LSuper+RSuper+Ctrl+X",
		},
		{
			// A doubled Ctrl beside a single Super: the Super survives and
			// sorts before Hyper.
			down: []string{"\x1b[57442;1u", "\x1b[57448;1u"},
			raw:  "\x1b[120;9u", want: "s-H-x",
			what: "LCtrl+RCtrl+Super+X",
		},
	} {
		got := feedHyper(t, append(append([]string{}, tc.down...), tc.raw)...)
		if len(got) == 0 || got[len(got)-1] != tc.want {
			t.Errorf("%s -> %v, want the last to be %q", tc.what, got, tc.want)
		}
	}
}

// ONE side is not two. A single Ctrl is a Control chord and always was.
func TestOneSideIsNotHyper(t *testing.T) {
	for _, tc := range []struct {
		down      []string
		raw, want string
	}{
		{down: []string{"\x1b[57442;1u"}, raw: "\x1b[120;5u", want: "^X"},
		{down: []string{"\x1b[57448;1u"}, raw: "\x1b[120;5u", want: "^X"},
		{down: nil, raw: "\x1b[120;5u", want: "^X"},
	} {
		got := feedHyper(t, append(append([]string{}, tc.down...), tc.raw)...)
		if len(got) == 0 || got[len(got)-1] != tc.want {
			t.Errorf("%v then %q -> %v, want the last to be %q",
				tc.down, tc.raw, got, tc.want)
		}
	}
}

// Letting one side go ends the promotion.
//
// A user who lifts the right Ctrl and keeps typing is holding an ordinary
// Control chord again, and the very next key has to say so.
func TestReleasingASideEndsTheHyper(t *testing.T) {
	got := feedHyper(t,
		"\x1b[57442;1u",   // LCtrl down
		"\x1b[57448;1u",   // RCtrl down
		"\x1b[120;5u",     // x  -> H-x
		"\x1b[57448;1:3u", // RCtrl up
		"\x1b[120;5u",     // x  -> ^X again
	)
	// The modifier keys report themselves too under this flag, and one of them
	// falls between the two x presses, so pick the presses out rather than
	// counting back from the end.
	var xs []string
	for _, k := range got {
		if !strings.HasPrefix(k, "LMod:") && !strings.HasPrefix(k, "RMod:") &&
			!strings.HasPrefix(k, "Mod:") {
			xs = append(xs, k)
		}
	}
	if len(xs) != 2 || xs[0] != "H-x" || xs[1] != "^X" {
		t.Errorf("got %v; the x presses were %v, want [H-x ^X]", got, xs)
	}
}

// Shift never doubles into Hyper: it produces text, and a doubled Shift would
// swallow the capital letters most people type with both hands.
func TestShiftDoesNotDoubleIntoHyper(t *testing.T) {
	got := feedHyper(t,
		"\x1b[57441;1u", // LShift down
		"\x1b[57447;1u", // RShift down
		"\x1b[97;2u",    // a with shift -> "A", not "H-a"
	)
	if len(got) == 0 || got[len(got)-1] != "A" {
		t.Errorf("both Shifts then a -> %v, want the last to be \"A\"", got)
	}
}

// Micro does not double either. A keyboard that has it at all rarely has two,
// so the gesture would exist on almost no hardware.
func TestMicroDoesNotDoubleIntoHyper(t *testing.T) {
	got := feedHyper(t,
		"\x1b[57446;1u", // LMicro down
		"\x1b[57452;1u", // RMicro down
		"\x1b[120;33u",  // x with micro -> "m-x", not "H-x"
	)
	if len(got) == 0 || got[len(got)-1] != "m-x" {
		t.Errorf("both Micros then x -> %v, want the last to be \"m-x\"", got)
	}
}

// feedHyper runs a sequence of raw inputs through one handler and returns every
// key name it produced, modifier events included.
func feedHyper(t *testing.T, raws ...string) []string {
	t.Helper()
	all := ""
	for _, r := range raws {
		all += r
	}
	keys, _, _ := numLockProbe(t, all)
	return keys
}
