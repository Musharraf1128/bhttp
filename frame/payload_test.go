package frame

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadPayload(t *testing.T) {
	buf := make([]byte, MaxPayload)
	p, err := ReadPayload(bytes.NewReader([]byte("abc")), Header{Length: 3}, buf)
	if err != nil || string(p) != "abc" {
		t.Fatalf("%q %v", p, err)
	}
	p, err = ReadPayload(bytes.NewReader(nil), Header{Length: 0}, buf)
	if err != nil || len(p) != 0 {
		t.Fatalf("zero length: %q %v", p, err)
	}
	if _, err = ReadPayload(bytes.NewReader(nil), Header{Length: 3}, buf); err != io.ErrUnexpectedEOF {
		t.Fatalf("EOF trap: %v", err)
	}
	if _, err = ReadPayload(bytes.NewReader(nil), Header{Length: MaxPayload + 1}, buf); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
}
