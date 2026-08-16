package keyboard

import (
	"io"
	"strings"
	"testing"
	"time"
)

// A terminal answers "what are you" with a Primary DA reply listing what it
// supports, where attribute 4 means Sixel graphics. An application that wants
// to draw a picture has no other portable way to ask, so the reply has to
// reach it rather than being swallowed as unparseable escape noise.
func TestPrimaryDeviceAttributesReply(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"\x1b[?62;4;22c", "DA1:62;4;22"}, // VT220 + Sixel
		{"\x1b[?1;2c", "DA1:1;2"},         // VT100 with AVO, no Sixel
		{"\x1b[?62c", "DA1:62"},           // single attribute
		{"\x1b[>0;276;0c", "DA2:0;276;0"}, // Secondary DA (terminal id)
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}

// The kitty graphics protocol answers on the APC channel: ESC _ G i=<id>;OK
// ESC \. It is the query that tells an application the terminal can show it a
// picture at all, and nothing surfaced APC before -- CSI u was being read as
// "restore cursor" and APC went nowhere.
func TestAPCResponseSurfaces(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"kitty graphics OK, ST terminated", "\x1b_Gi=31;OK\x1b\\", "APC:Gi=31;OK"},
		{"BEL terminated", "\x1b_Gi=31;OK\x07", "APC:Gi=31;OK"},
		{"an error reply reaches the app too", "\x1b_Gi=31;ENOENT:bad\x1b\\", "APC:Gi=31;ENOENT:bad"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q -> %v, want [%s]", c.name, c.raw, got, c.want)
		}
	}
}

// An APC body carries whatever the two ends agreed on, so it must pass
// through verbatim -- including the semicolons and equals signs a graphics
// reply is made of -- and must not be mistaken for keys.
func TestAPCBodyIsNotEmittedAsKeys(t *testing.T) {
	got := feedKeys(t, "\x1b_Ga=t,i=7,s=100,v=100;OK\x1b\\")
	if len(got) != 1 {
		t.Fatalf("APC body leaked as keys: %v", got)
	}
	if got[0] != "APC:Ga=t,i=7,s=100,v=100;OK" {
		t.Errorf("body mangled: %q", got[0])
	}
}

// An empty APC string has no reply in it to dispatch on, so it emits nothing
// rather than an "APC:" with no payload.
func TestEmptyAPCEmitsNothing(t *testing.T) {
	if got := feedKeys(t, "\x1b_\x1b\\"); len(got) != 0 {
		t.Errorf("empty APC emitted %v, want nothing", got)
	}
}

// Ordinary typing that FOLLOWS a reply still arrives: the accumulator has to
// hand the stream back when the terminator lands, not swallow the rest.
func TestInputResumesAfterReplies(t *testing.T) {
	got := feedKeys(t, "\x1b_Gi=1;OK\x1b\\ab\x1b[?62;4;22cZ")
	want := []string{"APC:Gi=1;OK", "a", "b", "DA1:62;4;22", "Z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A terminal that never terminates an APC string must not grow the buffer
// without bound -- this is a stream we do not control.
func TestAPCBodyIsCapped(t *testing.T) {
	got := feedKeys(t, "\x1b_"+strings.Repeat("x", maxAPCBody*2)+"\x1b\\")
	if len(got) != 1 {
		t.Fatalf("want one APC key, got %d", len(got))
	}
	if body := strings.TrimPrefix(got[0], "APC:"); len(body) != maxAPCBody {
		t.Errorf("body length %d, want it capped at %d", len(body), maxAPCBody)
	}
}

// ESC _ is equally how a terminal reports Mega+_ typed by hand. An APC that
// never terminates is therefore not one, and must be given back as that key
// with everything after it intact -- swallowing a keystroke, and then the
// typing that follows it, would be a far worse bug than missing a reply.
func TestUnterminatedAPCFallsBackToMegaUnderscore(t *testing.T) {
	got := feedKeys(t, "\x1b_")
	if len(got) != 1 || got[0] != "M-_" {
		t.Errorf("bare ESC _ -> %v, want [M-_]", got)
	}
}

// ...and the typing after it still arrives, in order.
func TestTypingAfterMegaUnderscoreSurvives(t *testing.T) {
	pr, pw := io.Pipe()
	f := false
	h := New(Options{InputReader: pr, ManageTerminal: &f})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	go func() {
		pw.Write([]byte("\x1b_"))
		// Long enough that the APC accumulator gives up, as a human pause is.
		time.Sleep(4 * escapeTimeout)
		pw.Write([]byte("hi"))
		pw.Close()
	}()
	var keys []string
	deadline := time.After(time.Second)
	for len(keys) < 3 {
		select {
		case k := <-h.Keys:
			keys = append(keys, k)
		case <-deadline:
			t.Fatalf("timed out; got %v", keys)
		}
	}
	if strings.Join(keys, ",") != "M-_,h,i" {
		t.Errorf("got %v, want [M-_ h i]", keys)
	}
}
