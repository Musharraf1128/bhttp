package hpack

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestGoldenIndexed(t *testing.T) {
	got, err := Encode(nil, []Field{{"host", "localhost:9000"}})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x81, 0x0e}, "localhost:9000"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x", got)
	}
}

func TestGoldenLiteral(t *testing.T) {
	got, _ := Encode(nil, []Field{{"X-Foo", "bar"}})
	want := append([]byte{0x00, 0x05}, "x-foo"...)
	want = append(want, 0x03)
	want = append(want, "bar"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x", got)
	}
}

func TestRoundTripCaseAndOrder(t *testing.T) {
	in := []Field{{"Content-Type", "text/html"}, {"x-a", "1"}, {"x-a", "2"}, {"etag", ""}}
	enc, err := Encode(nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if enc[0] != 0x85 {
		t.Fatalf("content-type should be index 5, first byte %#x", enc[0])
	}
	out, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	want := []Field{{"content-type", "text/html"}, {"x-a", "1"}, {"x-a", "2"}, {"etag", ""}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v", out)
	}
}

func TestLiteralWithStaticNameDecodes(t *testing.T) {
	// A sloppy peer sends "host" as a literal. We accept it.
	b := append([]byte{0x00, 0x04}, "host"...)
	b = append(b, 0x01, 'x')
	out, err := Decode(b)
	if err != nil || len(out) != 1 || out[0] != (Field{"host", "x"}) {
		t.Fatalf("%v %v", out, err)
	}
}

func TestMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"truncated value", []byte{0x81, 0x05, 'a'}, ErrTruncated},
		{"missing vlen", []byte{0x81}, ErrTruncated},
		{"truncated literal name", []byte{0x00, 0x05, 'a'}, ErrTruncated},
		{"index 0", []byte{0x80, 0x00}, ErrBadIndex},
		{"index 11", []byte{0x8b, 0x00}, ErrBadIndex},
		{"reserved first byte", []byte{0x01}, ErrReserved},
		{"empty name", []byte{0x00, 0x00, 0x00}, ErrEmptyName},
	}
	for _, c := range cases {
		if _, err := Decode(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestEncodeLimits(t *testing.T) {
	if _, err := Encode(nil, []Field{{"host", strings.Repeat("a", 256)}}); !errors.Is(err, ErrTooLong) {
		t.Fatalf("long value: %v", err)
	}
	if _, err := Encode(nil, []Field{{strings.Repeat("a", 256), "v"}}); !errors.Is(err, ErrTooLong) {
		t.Fatalf("long name: %v", err)
	}
	if _, err := Encode(nil, []Field{{"", "v"}}); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name: %v", err)
	}
	pre := []byte{1, 2, 3}
	out, err := Encode(pre, []Field{{"host", "ok"}, {"", "bad"}})
	if err == nil || len(out) != 3 {
		t.Fatalf("failed encode must not leave partial output, len=%d", len(out))
	}
}

// The number for the writeup: bytes on the wire vs HTTP/1.1 text headers.
func TestSizeVsText(t *testing.T) {
	fs := []Field{
		{"host", "localhost:9000"},
		{"user-agent", "bcurl/1"},
		{"accept", "*/*"},
		{"if-none-match", `"abc123"`},
	}
	enc, _ := Encode(nil, fs)
	text := 0
	for _, f := range fs {
		text += len(f.Name) + 2 + len(f.Value) + 2 // "Name: value\r\n"
	}
	t.Logf("binary=%d text=%d saved=%.0f%%", len(enc), text, 100*(1-float64(len(enc))/float64(text)))
	if len(enc) != 40 || text != 81 {
		t.Fatalf("binary=%d text=%d", len(enc), text)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x81, 0x01, 'x'})
	f.Add([]byte{0x00, 0x03, 'a', 'b', 'c', 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		fields, err := Decode(b) // must never panic
		if err != nil {
			return
		}
		enc, err := Encode(nil, fields)
		if err != nil {
			t.Fatalf("decoded fields failed to re-encode: %v", err)
		}
		if len(enc) > len(b) {
			t.Fatalf("canonical form larger than input: %d > %d", len(enc), len(b))
		}
		back, err := Decode(enc)
		if err != nil || !reflect.DeepEqual(back, fields) {
			t.Fatalf("roundtrip mismatch: %v vs %v (%v)", back, fields, err)
		}
	})
}
