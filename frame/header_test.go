package frame

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestGolden(t *testing.T) {
	h := Header{Length: 30, Type: 1, Flags: 1, StreamID: 1}
	got, err := h.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := [8]byte{0x00, 0x1e, 0x01, 0x01, 0x00, 0x00, 0x00, 0x01}
	if got != want {
		t.Fatalf("got % x want % x", got, want)
	}
	back, err := DecodeHeader(got)
	if err != nil || back != h {
		t.Fatalf("roundtrip: %+v %v", back, err)
	}
}

func TestReservedBitIgnored(t *testing.T) {
	h, err := DecodeHeader([8]byte{0, 0, 9, 0, 0x80, 0, 0, 1})
	if err != nil || h.StreamID != 1 {
		t.Fatalf("R bit not ignored: %+v %v", h, err)
	}
}

func TestOversizeRejected(t *testing.T) {
	_, err := DecodeHeader([8]byte{0x40, 0x01}) // 16385
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v", err)
	}
	if _, err := (Header{Length: 16385}).Encode(); err == nil {
		t.Fatal("encode accepted oversize")
	}
}

func TestPartialRead(t *testing.T) {
	if _, err := ReadHeader(bytes.NewReader(nil)); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	if _, err := ReadHeader(bytes.NewReader([]byte{0, 1, 2})); err != io.ErrUnexpectedEOF {
		t.Fatalf("want UnexpectedEOF, got %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0, 0x1e, 1, 1, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, in []byte) {
		var b [8]byte
		copy(b[:], in)
		h, err := DecodeHeader(b)
		if err == nil && (h.Length > MaxPayload || h.StreamID > MaxStreamID) {
			t.Fatalf("invariant broken: %+v", h)
		}
	})
}
