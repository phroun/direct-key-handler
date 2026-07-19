package keyboard

import (
	"io"
	"testing"
	"time"
)

// newPipedHandler wires a handler to an in-memory pipe (no real terminal) and
// starts it, returning the write end and a cleanup.
func newPipedHandler(t *testing.T) (*Handler, *io.PipeWriter, func()) {
	t.Helper()
	noManage := false
	pr, pw := io.Pipe()
	h := New(Options{
		InputReader:    pr,
		ManageTerminal: &noManage,
	})
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return h, pw, func() { h.Stop(); pw.Close(); pr.Close() }
}

// TestOSC52ClipboardResponseBEL: an OSC 52 response terminated by BEL is
// decoded and delivered on OnClipboard, and is NOT echoed as key events.
func TestOSC52ClipboardResponseBEL(t *testing.T) {
	got := make(chan struct {
		sel  byte
		data string
	}, 1)

	h, pw, cleanup := newPipedHandler(t)
	defer cleanup()
	h.OnClipboard = func(selection byte, data []byte) {
		got <- struct {
			sel  byte
			data string
		}{selection, string(data)}
	}

	// "hello" base64 = aGVsbG8=
	if _, err := pw.Write([]byte("\x1b]52;c;aGVsbG8=\x07")); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-got:
		if r.sel != 'c' {
			t.Errorf("selection = %q, want 'c'", r.sel)
		}
		if r.data != "hello" {
			t.Errorf("data = %q, want %q", r.data, "hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnClipboard was not called")
	}

	// The response must not have leaked into the key stream.
	select {
	case k := <-h.Keys:
		t.Fatalf("clipboard response leaked as key %q", k)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestOSC52ClipboardResponseST: the ST form (ESC \) terminator works too.
func TestOSC52ClipboardResponseST(t *testing.T) {
	got := make(chan string, 1)

	h, pw, cleanup := newPipedHandler(t)
	defer cleanup()
	h.OnClipboard = func(_ byte, data []byte) { got <- string(data) }

	// "hi" base64 = aGk=, terminated by ST (ESC \)
	if _, err := pw.Write([]byte("\x1b]52;c;aGk=\x1b\\")); err != nil {
		t.Fatal(err)
	}

	select {
	case d := <-got:
		if d != "hi" {
			t.Errorf("data = %q, want %q", d, "hi")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnClipboard was not called")
	}
}

// TestOSC52DoesNotDisturbKeys: a normal keystroke after a clipboard response
// still parses (no lingering clipboard state).
func TestOSC52DoesNotDisturbKeys(t *testing.T) {
	h, pw, cleanup := newPipedHandler(t)
	defer cleanup()
	h.OnClipboard = func(byte, []byte) {}

	if _, err := pw.Write([]byte("\x1b]52;c;aGk=\x07a")); err != nil {
		t.Fatal(err)
	}

	select {
	case k := <-h.Keys:
		if k != "a" {
			t.Errorf("key after clipboard response = %q, want \"a\"", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plain key after clipboard response never arrived")
	}
}
