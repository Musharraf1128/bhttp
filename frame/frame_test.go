package frame

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPreface(t *testing.T) {
	var b bytes.Buffer
	WritePreface(&b, 1)
	want := []byte{0x42, 0x48, 0x54, 0x50, 1, 0, 0, 0}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got % x", b.Bytes())
	}
	v, err := ReadPreface(&b)
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	_, err = ReadPreface(bytes.NewReader([]byte("GET / HT")))
	if !errors.Is(err, ErrBadPreface) {
		t.Fatalf("http/1.1 client: got %v", err)
	}
}

func TestPrefaceReservedIgnored(t *testing.T) {
	v, err := ReadPreface(bytes.NewReader([]byte{'B', 'H', 'T', 'P', 1, 9, 9, 9}))
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func TestErrorFrameGolden(t *testing.T) {
	var b bytes.Buffer
	if err := WriteError(&b, ErrCodeProtocol, ""); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 2, 4, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got % x", b.Bytes())
	}
}

func TestUnknownTypeSkipped(t *testing.T) {
	var b bytes.Buffer
	WriteFrame(&b, Header{Type: 0x7f, StreamID: 1}, []byte("future!"))
	WriteFrame(&b, Header{Type: TypeData, StreamID: 1, Flags: FlagEndStream}, []byte("hi"))

	buf := make([]byte, MaxPayload)
	var got []byte
	for {
		h, p, err := ReadFrame(&b, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch h.Type {
		case TypeData:
			got = append(got, p...)
		default: // unknown: payload already consumed, keep going
		}
	}
	if string(got) != "hi" {
		t.Fatalf("got %q: unknown frame desynced the stream", got)
	}
}

func TestMidFrameEOF(t *testing.T) {
	buf := make([]byte, MaxPayload)
	// header says 5 bytes, only 2 follow
	in := []byte{0, 5, 3, 0, 0, 0, 0, 1, 'a', 'b'}
	if _, _, err := ReadFrame(bytes.NewReader(in), buf); err != io.ErrUnexpectedEOF {
		t.Fatalf("got %v", err)
	}
	// header says 5 bytes, zero follow (the ReadFull EOF trap)
	in = []byte{0, 5, 3, 0, 0, 0, 0, 1}
	if _, _, err := ReadFrame(bytes.NewReader(in), buf); err != io.ErrUnexpectedEOF {
		t.Fatalf("zero-payload trap: got %v", err)
	}
}

func TestOversizeNotRead(t *testing.T) {
	// Length 16385; reader would yield payload but we must stop first.
	r := bytes.NewReader(append([]byte{0x40, 0x01, 3, 0, 0, 0, 0, 1}, make([]byte, 16385)...))
	buf := make([]byte, MaxPayload)
	_, _, err := ReadFrame(r, buf)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v", err)
	}
	if r.Len() != 16385 {
		t.Fatalf("payload was consumed (%d left)", r.Len())
	}
}

func TestParseErrorShort(t *testing.T) {
	if c, _ := ParseError([]byte{1}); c != ErrCodeProtocol {
		t.Fatal("short payload must map to PROTOCOL")
	}
	if ErrorCode(999).String() != "PROTOCOL" {
		t.Fatal("unknown code must read as PROTOCOL")
	}
}
