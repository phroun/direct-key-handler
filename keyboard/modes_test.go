package keyboard

import (
	"io"
	"reflect"
	"testing"
)

// newModeHandler makes a handler with nothing fed to it yet.
func newModeHandler(t *testing.T) *Handler {
	t.Helper()
	manage := false
	pr, _ := io.Pipe()
	return New(Options{InputReader: pr, ManageTerminal: &manage})
}

// A state nobody can see is ABSENT, not off.
//
// The difference is the one a display cares about: a fresh handler has no idea
// whether CapsLock is down or whether the window has the keyboard, and drawing
// either as "off" claims knowledge it does not have.
func TestUnknownModesAreAbsent(t *testing.T) {
	h := newModeHandler(t)

	// The pad's lock is the exception: it is answerable from the start, since
	// a pad with no latch is permanently locked and one with a latch almost
	// always is.
	if got := h.Modes(); !reflect.DeepEqual(got, []Mode{{ModeNumLock, ModeOn}}) {
		t.Errorf("a fresh handler knows %v, want just the pad's lock", got)
	}
	for _, name := range []string{ModeCapsLock, ModeFocus, "kana"} {
		if v, ok := h.Mode(name); ok {
			t.Errorf("%s reported %q before anything said so", name, v)
		}
	}
}

// CapsLock rides in on a key, and the modes list gains it once one arrives.
func TestCapsLockBecomesKnownFromAKey(t *testing.T) {
	_, _, h := numLockProbe(t, "\x1b[97;65u") // 'a' with capslock (64) latched
	if v, ok := h.Mode(ModeCapsLock); !ok || v != ModeOn {
		t.Errorf("caps = %q ok=%v after a key carrying the latch, want on", v, ok)
	}

	_, _, h = numLockProbe(t, "\x1b[97;1u") // 'a' with nothing latched
	if v, ok := h.Mode(ModeCapsLock); !ok || v != ModeOff {
		t.Errorf("caps = %q ok=%v after a key with no latch, want off", v, ok)
	}
}

// Focus is a state as much as an event: something drawing a status bar reads it
// from the same list as the latches, and a game that pauses when the window
// goes away does not have to wire up a second callback to find out.
func TestFocusBecomesAMode(t *testing.T) {
	_, _, h := numLockProbe(t, "\x1b[O") // focus out
	if v, ok := h.Mode(ModeFocus); !ok || v != ModeOff {
		t.Errorf("focus = %q ok=%v after a focus-out report, want off", v, ok)
	}

	_, _, h = numLockProbe(t, "\x1b[O\x1b[I") // out, then back in
	if v, ok := h.Mode(ModeFocus); !ok || v != ModeOn {
		t.Errorf("focus = %q ok=%v after focus came back, want on", v, ok)
	}
}

// A host can publish a state this package has never heard of, and it is
// reported beside the ones this package keeps.
func TestAHostCanAddItsOwnMode(t *testing.T) {
	h := newModeHandler(t)

	if !h.SetMode("overtype", ModeOn) {
		t.Fatal("setting a new mode reported no change")
	}
	if v, ok := h.Mode("overtype"); !ok || v != ModeOn {
		t.Errorf("overtype = %q ok=%v, want on", v, ok)
	}
	if h.SetMode("overtype", ModeOn) {
		t.Error("setting a mode to what it already is reported a change")
	}

	// The value is a token, not a boolean: a mode that is not a latch says
	// whatever it needs to.
	h.SetMode("kana", "hiragana")
	if v, _ := h.Mode("kana"); v != "hiragana" {
		t.Errorf("kana = %q, want hiragana", v)
	}

	// Sorted, so a status bar drawn straight from the list does not reshuffle
	// itself between calls.
	want := []Mode{{"kana", "hiragana"}, {ModeNumLock, ModeOn}, {"overtype", ModeOn}}
	if got := h.Modes(); !reflect.DeepEqual(got, want) {
		t.Errorf("modes = %v, want %v", got, want)
	}

	// And the empty value takes it away again.
	if !h.SetMode("overtype", "") {
		t.Error("removing a mode reported no change")
	}
	if _, ok := h.Mode("overtype"); ok {
		t.Error("overtype survived being set to the empty value")
	}
}

// Writing one of the states this package keeps sets a BELIEF.
//
// It is how a host moves the simulated lock where the system has no latch
// behind the cap. The two latches take only their two tokens; anything else is
// ignored rather than guessed at.
func TestWritingTheModesThisPackageKeeps(t *testing.T) {
	h := newModeHandler(t)

	if !h.SetMode(ModeNumLock, ModeOff) {
		t.Fatal("turning the pad's lock off reported no change")
	}
	if v, _ := h.Mode(ModeNumLock); v != ModeOff {
		t.Errorf("num = %q after being set off, want off", v)
	}

	if h.SetMode(ModeNumLock, "maybe") {
		t.Error("a latch accepted a value that is not one of its two")
	}
	if v, _ := h.Mode(ModeNumLock); v != ModeOff {
		t.Errorf("num = %q after a rejected write, want the value it had", v)
	}
}

// Every change is announced, whoever made it.
func TestOnModeFires(t *testing.T) {
	h := newModeHandler(t)
	var got []Mode
	h.OnMode = func(m Mode) { got = append(got, m) }

	h.SetMode(ModeNumLock, ModeOff)
	h.SetMode("overtype", ModeOn)
	h.SetMode("overtype", ModeOn) // no change, no announcement
	h.SetMode("overtype", "")

	want := []Mode{{ModeNumLock, ModeOff}, {"overtype", ModeOn}, {"overtype", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("announced %v, want %v", got, want)
	}
}
