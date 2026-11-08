package preview

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFramesRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, Text, []byte(`{"t":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&b, Binary, []byte{0xff, 0xd8, 0}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&b, Text, nil); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&b)
	for _, want := range []struct {
		kind byte
		p    string
	}{{Text, `{"t":"ready"}`}, {Binary, "\xff\xd8\x00"}, {Text, ""}} {
		k, p, err := ReadFrame(r)
		if err != nil || k != want.kind || string(p) != want.p {
			t.Fatalf("got %q %q %v, want %q %q", k, p, err, want.kind, want.p)
		}
	}
	if _, _, err := ReadFrame(r); err != io.EOF {
		t.Fatalf("after the last frame: %v, want EOF", err)
	}
}

func TestFramesRefuseWhatIsNotOne(t *testing.T) {
	if err := WriteFrame(io.Discard, Binary, make([]byte, MaxFrame)); !errors.Is(err, ErrTooBig) {
		t.Fatalf("a frame over the cap was written: %v", err)
	}
	for name, raw := range map[string][]byte{
		"too big":      {0x7f, 0xff, 0xff, 0xff, 't'},
		"empty":        {0, 0, 0, 0},
		"unknown kind": {0, 0, 0, 2, 'x', 1},
		"cut short":    {0, 0, 0, 9, 't', '{'},
	} {
		_, _, err := ReadFrame(bufio.NewReader(bytes.NewReader(raw)))
		if err == nil || err == io.EOF {
			t.Errorf("%s: %v, want an error", name, err)
		}
	}
}
