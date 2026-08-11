package keyboard

import (
	"io"
	"testing"
	"time"
)

// Line assembly must be unaffected by an application's key names. It runs on
// the canonical names (emitKey renames only what leaves the package), and it
// resolves the keys it acts on by identity rather than spelling — so neither
// a caller's vocabulary nor a future change to this package's own defaults can
// quietly turn Return into ordinary text and hang a prompt forever.
func TestLineAssemblySurvivesApplicationNames(t *testing.T) {
	pr, pw := io.Pipe()
	f := false
	h := New(Options{
		InputReader:    pr,
		ManageTerminal: &f,
		KeyNames: map[Key]string{
			KeyReturn:    "return",
			KeyBackspace: "back",
		},
	})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	h.SetLineMode(true)

	// "abX", DEL erases the X, CR completes the line.
	go func() {
		pw.Write([]byte("abX\x7f\r"))
		pw.Close()
	}()

	select {
	case line := <-h.Lines:
		if string(line) != "ab" {
			t.Errorf("line = %q, want %q (backspace should have erased the X)", line, "ab")
		}
	case <-time.After(time.Second):
		t.Fatal("line never completed: Return did not reach line assembly as itself")
	}
}
