package keyboard

import (
	"io"
	"sync"
	"testing"
	"time"
)

// numLockProbe feeds raw input and returns the key names emitted alongside
// every lock state announced, in order.
//
// OnNumLock runs on the handler's own goroutine, as OnKey and OnFocus do, so
// what it collects is shared and guarded here.
func numLockProbe(t *testing.T, raw string) (keys []string, locks []bool, h *Handler) {
	t.Helper()
	pr, pw := io.Pipe()
	manage := false
	h = New(Options{InputReader: pr, ManageTerminal: &manage})
	done := make(chan struct{})
	var mu sync.Mutex
	h.OnNumLock = func(on bool) {
		mu.Lock()
		defer mu.Unlock()
		locks = append(locks, on)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		pw.Write([]byte(raw))
		pw.Close()
	}()
	go func() {
		defer close(done)
		deadline := time.After(300 * time.Millisecond)
		for {
			select {
			case k := <-h.Keys:
				keys = append(keys, k)
			case <-deadline:
				return
			}
		}
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return keys, locks, h
}

// The lock cap pressed alone is eaten, whole.
//
// Its entire meaning is the state, which already decides what eleven other pad
// caps are called, and nobody writes a binding against a state. Press, repeat
// and release all have to go: emitting a release for a press that was never
// emitted is the mismatch the held-key registry exists to prevent, and it would
// arrive here as a bare ":Release".
func TestTheLockCapAloneIsNotAKey(t *testing.T) {
	keys, locks, _ := numLockProbe(t,
		"\x1b[57360;1u\x1b[57360;1:2u\x1b[57360;1:3u")
	if len(keys) != 0 {
		t.Errorf("the lock cap emitted %v, want nothing at all", keys)
	}
	if len(locks) != 1 || locks[0] != false {
		t.Errorf("announced %v, want exactly one state, off — the press moves the "+
			"lock once and a hold does not ratchet it", locks)
	}
}

// With a modifier held it is an ordinary key, and it is called Clear.
//
// Clear is an ACTION, which is the kind of thing a binding is for. The name is
// also what the cap says on every keyboard that has no lock behind it.
func TestTheLockCapWithAModifierIsClear(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[57360;2u", "S-Clear"},
		{"\x1b[57360;5u", "C-Clear"},
		{"\x1b[57360;3u", "M-Clear"},
		{"\x1b[57360;9u", "s-Clear"},
	} {
		keys, _, _ := numLockProbe(t, tc.raw)
		if len(keys) != 1 || keys[0] != tc.want {
			t.Errorf("%q -> %v, want [%s]", tc.raw, keys, tc.want)
		}
	}
}

// A LATCH is not a modifier, and reading it as one breaks every second press.
//
// Pressing the cap while the lock is already on arrives with numlock (128) set
// in the same field the held modifiers use. Test that field naively and the
// press looks like a chord: the first press would be eaten and the second would
// come out as "Clear", alternating forever. CapsLock (64) does the same to
// anyone typing in capitals, and ScrollLock is next — SDL already carries a
// third latch that kitty has no bit for.
//
// This is why the test is for what a held modifier IS rather than for what it
// is not: a latch nobody has thought of yet is simply not on the list.
func TestALatchIsNotAModifier(t *testing.T) {
	for _, tc := range []struct {
		raw, what string
	}{
		{"\x1b[57360;129u", "numlock on"},
		{"\x1b[57360;65u", "capslock on"},
		{"\x1b[57360;193u", "both latched"},
	} {
		keys, _, _ := numLockProbe(t, tc.raw)
		if len(keys) != 0 {
			t.Errorf("with %s the lock cap emitted %v; a latch was read as a "+
				"modifier, so the cap became a chord", tc.what, keys)
		}
	}

	// And a latch alongside a real modifier is still that modifier: the latch
	// contributes nothing to the name.
	keys, _, _ := numLockProbe(t, "\x1b[57360;130u")
	if len(keys) != 1 || keys[0] != "S-Clear" {
		t.Errorf("Shift with numlock latched -> %v, want [S-Clear]", keys)
	}
}

// The latch is detected from a pad key, not from the lock cap.
//
// A digit arriving with the numlock bit CLEAR can only mean this system has no
// latch: one that has a latch and has it off sends the navigation legend
// instead. That matters because on a Mac the lock cap is never pressed — it is
// not a lock there, so nobody presses it idly — and waiting for it would mean
// the pad was misnamed for the whole session.
func TestALatchlessSystemIsDetectedFromAPadKey(t *testing.T) {
	// P-7 (57406) with no latch bit: the pad produced its digit while nothing
	// claims to be locked, so there is no lock here and the pad is permanently
	// locked, which is what its caps say.
	keys, _, h := numLockProbe(t, "\x1b[57406u")
	if len(keys) != 1 || keys[0] != "P-7" {
		t.Fatalf("the pad key itself -> %v, want [P-7]", keys)
	}
	if !h.NumLock() {
		t.Error("a pad with no latch reports unlocked; it is permanently locked")
	}

	// On that system the lock cap is ours to keep, so it toggles.
	if changed, on := h.toggleNumLock(); !changed || on {
		t.Errorf("toggle on a latchless system -> changed=%v on=%v, want true/false",
			changed, on)
	}
}

// Where a real latch exists it is authoritative, and resyncs on every key.
//
// The user can toggle NumLock while this process is not focused and come back,
// so a count of our own would be stale. Every key carries the bit; every key is
// read for it.
func TestARealLatchOverrulesOurOwnCount(t *testing.T) {
	// P-Home (57423) is the unlocked legend: a latch exists and is off.
	_, _, h := numLockProbe(t, "\x1b[57423u")
	if h.NumLock() {
		t.Fatal("a navigation legend arrived and the pad still reports locked")
	}

	// Knowing there is a real latch, the cap no longer moves our copy — the OS
	// has already moved the real one and will say so on the next key.
	if changed, _ := h.toggleNumLock(); changed {
		t.Error("the cap toggled our copy on a system that keeps its own latch")
	}

	// And an ordinary letter carrying the bit resyncs us, with no pad key
	// involved at all.
	if changed, on := h.noteNumLock('a', 1+kittyNumLockBit); !changed || !on {
		t.Errorf("a letter with the latch bit set -> changed=%v on=%v, want true/true",
			changed, on)
	}
}

// The pad starts locked, before anything has been typed.
//
// That is what the printed legends promise, what a pad with no latch does
// permanently, and what a pad with one is in almost always. The first keystroke
// corrects it if not.
func TestThePadStartsLocked(t *testing.T) {
	manage := false
	pr, _ := io.Pipe()
	h := New(Options{InputReader: pr, ManageTerminal: &manage})
	if !h.NumLock() {
		t.Error("a fresh handler reports the pad unlocked")
	}
}
