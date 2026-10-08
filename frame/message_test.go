package frame

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"bhttp/hpack"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenRequestFrame(t *testing.T) {
	p, err := AppendRequest(nil, Request{
		Method:  MethodGET,
		Path:    "/index.html",
		Headers: []hpack.Field{{Name: "host", Value: "localhost:9000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := WriteFrame(&b, Header{Type: TypeRequest, Flags: FlagEndStream, StreamID: 1}, p); err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "001e010100000001 01 000b 2f696e6465782e68746d6c 810e 6c6f63616c686f73743a39303030")
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got  % x\nwant % x", b.Bytes(), want)
	}
	if b.Len() != 38 {
		t.Fatalf("len=%d", b.Len())
	}
	t.Logf("binary=%d http1.1=%d", b.Len(), len("GET /index.html HTTP/1.1\r\nHost: localhost:9000\r\n\r\n"))
}

func TestGoldenResponseFrame(t *testing.T) {
	p, err := AppendResponse(nil, Response{Status: 200, Headers: []hpack.Field{
		{Name: "content-length", Value: "5"},
		{Name: "server", Value: "bserve"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	WriteFrame(&b, Header{Type: TypeResponse, StreamID: 1}, p) // no END_STREAM: DATA follows
	want := unhex(t, "000d020000000001 00c8 860135 8a0662736572766 5")
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got  % x\nwant % x", b.Bytes(), want)
	}
}

func TestRequestRoundTripThroughWire(t *testing.T) {
	in := Request{Method: MethodGET, Path: "/a/b.css", Headers: []hpack.Field{
		{Name: "host", Value: "h"}, {Name: "x-trace", Value: "42"},
	}}
	p, _ := AppendRequest(nil, in)
	var b bytes.Buffer
	WriteFrame(&b, Header{Type: TypeRequest, Flags: FlagEndStream, StreamID: 7}, p)
	h, got, err := ReadFrame(&b, make([]byte, MaxPayload))
	if err != nil || h.StreamID != 7 || h.Type != TypeRequest {
		t.Fatalf("%+v %v", h, err)
	}
	out, err := ParseRequest(got)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Fatalf("got %+v err %v", out, err)
	}
}

func TestParseRequestMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"short":            "01 00",
		"method 2":         "02 0001 2f",
		"zero pathlen":     "01 0000",
		"path overruns":    "01 0005 2f61",
		"no leading slash": "01 0001 61",
		"bad hdr index":    "01 0001 2f 8b00",
		"hdr truncated":    "01 0001 2f 8105 61",
		"reserved hdr":     "01 0001 2f 01",
	}
	for name, h := range cases {
		if _, err := ParseRequest(unhex(t, h)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestParseResponseMalformed(t *testing.T) {
	for name, h := range map[string]string{
		"empty": "", "one byte": "00", "status 99": "0063", "status 600": "0258", "bad hdr": "00c8 01",
	} {
		if _, err := ParseResponse(unhex(t, h)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestEncodeRejects(t *testing.T) {
	if _, err := AppendRequest(nil, Request{Method: 2, Path: "/"}); !errors.Is(err, ErrBadMethod) {
		t.Errorf("method: %v", err)
	}
	if _, err := AppendRequest(nil, Request{Method: MethodGET, Path: "x"}); !errors.Is(err, ErrBadPath) {
		t.Errorf("path: %v", err)
	}
	if _, err := AppendResponse(nil, Response{Status: 99}); !errors.Is(err, ErrBadStatus) {
		t.Errorf("status: %v", err)
	}
	big := "/" + strings.Repeat("a", MaxPayload)
	out, err := AppendRequest([]byte{9}, Request{Method: MethodGET, Path: big})
	if !errors.Is(err, ErrFrameTooLarge) || len(out) != 1 {
		t.Errorf("oversize: %v len=%d", err, len(out))
	}
}

func FuzzParseRequest(f *testing.F) {
	f.Add(unhex(f, "01 000b 2f696e6465782e68746d6c 810e 6c6f63616c686f73743a39303030"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxPayload {
			return
		}
		r, err := ParseRequest(b) // must never panic
		if err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("error does not wrap ErrMalformed: %v", err)
			}
			return
		}
		enc, err := AppendRequest(nil, r)
		if err != nil {
			t.Fatalf("accepted request failed to re-encode: %v", err)
		}
		back, err := ParseRequest(enc)
		if err != nil || !reflect.DeepEqual(back, r) {
			t.Fatalf("roundtrip mismatch: %+v vs %+v (%v)", back, r, err)
		}
	})
}
