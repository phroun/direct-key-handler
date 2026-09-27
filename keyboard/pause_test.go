package keyboard

import (
	"io"
	"testing"
	"time"
)

// startPiped starts a handler on a pipe, with no terminal to manage.
func startPiped(t *testing.T) (*Handler, *io.PipeWriter) {
	t.Helper()
	pr, pw := io.Pipe()
	f := false
	h := New(Options{InputReader: pr, ManageTerminal: &f})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pw.Close()
		h.Stop()
	})
	return h, pw
}

// collectKeys reads keys until none arrive for the settle window.
func collectKeys(h *Handler, settle time.Duration) []string {
	var keys []string
	for {
		select {
		case k := <-h.Keys:
			keys = append(keys, k)
		case <-time.After(settle):
			return keys
		}
	}
}

// A key held across a suspend comes up when the handler pauses. Its real key-up
// is typed into the shell while the program is stopped, so it never arrives
// here, and without the release the press would stand for good downstream.
func TestAKeyHeldAcrossAPauseComesUp(t *testing.T) {
	h, pw := startPiped(t)
	go pw.Write([]byte("\x1b[97;5u"))
	if got := collectKeys(h, 100*time.Millisecond); len(got) != 1 || got[0] != "^A" {
		t.Fatalf("press -> %v, want [^A]", got)
	}
	if err := h.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := collectKeys(h, 100*time.Millisecond); len(got) != 1 || got[0] != "^A:Release" {
		t.Fatalf("pause -> %v, want [^A:Release]", got)
	}
}

// Keys still arrive after a resume: pausing leaves the reader and the
// processor running, which is the difference from Stop, whose channel cannot
// be reopened.
func TestKeysArriveAfterAResume(t *testing.T) {
	h, pw := startPiped(t)
	if err := h.Pause(); err != nil {
		t.Fatal(err)
	}
	if !h.IsPaused() {
		t.Fatal("IsPaused false after Pause")
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if h.IsPaused() {
		t.Fatal("IsPaused true after Resume")
	}
	go pw.Write([]byte("x"))
	if got := collectKeys(h, 100*time.Millisecond); len(got) != 1 || got[0] != "x" {
		t.Fatalf("key after resume -> %v, want [x]", got)
	}
}

// Pause and Resume are no-ops where they have nothing to do: on a handler that
// is not running, and when repeated.
func TestPauseAndResumeAreNoOpsWhereTheyHaveNothingToDo(t *testing.T) {
	f := false
	idle := New(Options{InputReader: &io.PipeReader{}, ManageTerminal: &f})
	if err := idle.Pause(); err != nil || idle.IsPaused() {
		t.Fatalf("Pause on a handler never started: err=%v paused=%v", err, idle.IsPaused())
	}
	if err := idle.Resume(); err != nil {
		t.Fatalf("Resume on a handler never started: %v", err)
	}

	h, _ := startPiped(t)
	if err := h.Resume(); err != nil || h.IsPaused() {
		t.Fatalf("Resume without a Pause: err=%v paused=%v", err, h.IsPaused())
	}
	for i := 0; i < 2; i++ {
		if err := h.Pause(); err != nil || !h.IsPaused() {
			t.Fatalf("Pause #%d: err=%v paused=%v", i+1, err, h.IsPaused())
		}
	}
	for i := 0; i < 2; i++ {
		if err := h.Resume(); err != nil || h.IsPaused() {
			t.Fatalf("Resume #%d: err=%v paused=%v", i+1, err, h.IsPaused())
		}
	}
}

// Stopping a paused handler ends the pause too, so IsPaused does not report a
// handler that is no longer running.
func TestStoppingAPausedHandlerEndsThePause(t *testing.T) {
	h, _ := startPiped(t)
	if err := h.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if h.IsPaused() {
		t.Fatal("IsPaused true after Stop")
	}
}
