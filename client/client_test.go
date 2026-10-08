package client

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"bhttp/frame"
	"bhttp/hpack"
	"bhttp/server"
)

func startServer(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644)
	big := make([]byte, 40000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	os.WriteFile(filepath.Join(root, "big.bin"), big, 0o644)
	srv, err := server.New(server.Config{Root: root, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close(); srv.Close() })
	return ln.Addr().String()
}

func get(t *testing.T, addr, path string) (Result, []byte, error) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	SetTotalTimeout(c, 5e9)
	var b bytes.Buffer
	r, err := Get(c, addr, path, nil, &b, nil)
	return r, b.Bytes(), err
}

func TestAgainstRealServer(t *testing.T) {
	addr := startServer(t)
	r, body, err := get(t, addr, "/index.html")
	if err != nil || r.Status != 200 || string(body) != "hello" {
		t.Fatalf("%+v %q %v", r, body, err)
	}
	r, body, err = get(t, addr, "/big.bin")
	if err != nil || r.Status != 200 || len(body) != 40000 {
		t.Fatalf("big: %+v %d %v", r, len(body), err)
	}
	r, _, err = get(t, addr, "/missing")
	if err != nil || r.Status != 404 {
		t.Fatalf("404: %+v %v", r, err)
	}
}

// fake serves one canned script after the preface exchange.
func fake(t *testing.T, script func(c net.Conn)) string {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		frame.ReadPreface(c)
		frame.WritePreface(c, 1)
		frame.ReadFrame(c, make([]byte, frame.MaxPayload)) // the request
		script(c)
	}()
	return ln.Addr().String()
}

func resp(t *testing.T, c net.Conn, status uint16, flags frame.Flags, hs ...hpack.Field) {
	p, err := frame.AppendResponse(nil, frame.Response{Status: status, Headers: hs})
	if err != nil {
		t.Error(err)
	}
	frame.WriteFrame(c, frame.Header{Type: frame.TypeResponse, Flags: flags, StreamID: 1}, p)
}

func TestShortBodyIsFailure(t *testing.T) {
	addr := fake(t, func(c net.Conn) {
		resp(t, c, 200, 0, hpack.Field{Name: "content-length", Value: "100"})
		frame.WriteFrame(c, frame.Header{Type: frame.TypeData, StreamID: 1}, []byte("part"))
		// close without END_STREAM
	})
	_, _, err := get(t, addr, "/x")
	if !errors.Is(err, ErrShortBody) {
		t.Fatalf("got %v", err)
	}
}

func TestErrorAfter200IsFailure(t *testing.T) {
	addr := fake(t, func(c net.Conn) {
		resp(t, c, 200, 0, hpack.Field{Name: "content-length", Value: "100"})
		frame.WriteError(c, frame.ErrCodeInternal, "file changed")
	})
	_, _, err := get(t, addr, "/x")
	if !errors.Is(err, ErrServerError) {
		t.Fatalf("got %v", err)
	}
}

func TestLengthMismatch(t *testing.T) {
	addr := fake(t, func(c net.Conn) {
		resp(t, c, 200, 0, hpack.Field{Name: "content-length", Value: "10"})
		frame.WriteFrame(c, frame.Header{Type: frame.TypeData, Flags: frame.FlagEndStream, StreamID: 1}, []byte("abc"))
	})
	_, _, err := get(t, addr, "/x")
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownFramesAndOtherStreamsSkipped(t *testing.T) {
	addr := fake(t, func(c net.Conn) {
		frame.WriteFrame(c, frame.Header{Type: 0x7f, StreamID: 1}, []byte("future"))
		resp(t, c, 200, 0, hpack.Field{Name: "content-length", Value: "2"})
		frame.WriteFrame(c, frame.Header{Type: frame.TypeData, StreamID: 9}, []byte("zz"))
		frame.WriteFrame(c, frame.Header{Type: frame.TypeData, Flags: frame.FlagEndStream, StreamID: 1}, []byte("ok"))
	})
	r, body, err := get(t, addr, "/x")
	if err != nil || string(body) != "ok" || r.Status != 200 {
		t.Fatalf("%+v %q %v", r, body, err)
	}
}

func TestBadServerVersion(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		frame.ReadPreface(c)
		frame.WritePreface(c, 2)
		c.Close()
	}()
	_, _, err := get(t, ln.Addr().String(), "/x")
	if !errors.Is(err, ErrBadVersion) {
		t.Fatalf("got %v", err)
	}
}
