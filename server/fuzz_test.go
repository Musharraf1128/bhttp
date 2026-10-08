package server

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bhttp/frame"
)

// FuzzOpen: no string, however hostile, may open anything outside the root.
func FuzzOpen(f *testing.F) {
	base := f.TempDir()
	root := filepath.Join(base, "www")
	evil := filepath.Join(base, "www-evil") // shares the root's name as a prefix
	for _, d := range []string{filepath.Join(root, "sub"), evil} {
		must(f, os.MkdirAll(d, 0o755))
	}
	must(f, os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOP SECRET"), 0o644))
	must(f, os.WriteFile(filepath.Join(evil, "x"), []byte("TOP SECRET"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644))
	must(f, os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(root, "leak")))
	must(f, os.Symlink("../www-evil", filepath.Join(root, "evil")))
	must(f, os.Symlink("..", filepath.Join(root, "up")))

	srv, err := New(Config{Root: root, Logf: quiet})
	must(f, err)
	f.Cleanup(func() { srv.Close() })

	for _, s := range []string{
		"/index.html", "/sub/a.txt", "/leak", "/evil/x", "/up/secret.txt",
		"/up/www/index.html", "/../secret.txt", "/../www-evil/x", "//index.html",
		"/sub/../index.html", "/%2e%2e/secret.txt", "/index.html\x00", "/.", "/sub/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		file, fi, err := srv.open(p)
		if err != nil {
			return
		}
		defer file.Close()
		if !fi.Mode().IsRegular() {
			t.Fatalf("%q opened a non-regular file", p)
		}
		if !fs.ValidPath(strings.TrimPrefix(p, "/")) {
			t.Fatalf("%q opened despite failing ValidPath", p)
		}
		b, _ := io.ReadAll(file)
		if bytes.Contains(b, []byte("SECRET")) {
			t.Fatalf("%q ESCAPED THE ROOT", p)
		}
	})
}

// FuzzLive: arbitrary bytes after a valid preface, against a real server.
func FuzzLive(f *testing.F) {
	var panicked atomic.Bool
	addr := newServer(f, func(c *Config) {
		c.IdleTimeout = 300 * time.Millisecond
		c.FrameTimeout = 300 * time.Millisecond
		c.Logf = func(format string, _ ...any) {
			if strings.HasPrefix(format, "panic") {
				panicked.Store(true)
			}
		}
	})

	var b bytes.Buffer
	p, _ := frame.AppendRequest(nil, frame.Request{Method: frame.MethodGET, Path: "/index.html"})
	frame.WriteFrame(&b, frame.Header{Type: frame.TypeRequest, Flags: frame.FlagEndStream, StreamID: 1}, p)
	f.Add(b.Bytes())
	f.Add([]byte{0x40, 0x01, 1, 0, 0, 0, 0, 1})             // Length 16385
	f.Add([]byte{0, 3, 0x7f, 0, 0, 0, 0, 1, 'a', 'b', 'c'}) // unknown type
	f.Add([]byte{0, 2, 4, 0, 0, 0, 0, 0, 0, 1})             // ERROR on stream 0
	f.Add([]byte{0, 4, 1, 1, 0, 0, 0, 1, 2, 0, 1, '/'})     // bad method
	f.Add([]byte{0, 100, 3, 0, 0, 0, 0, 1, 'x'})            // truncated payload

	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) > 60000 {
			return
		}
		c, err := net.Dial("tcp", addr)
		must(t, err)
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		frame.WritePreface(c, 1)
		c.Write(in) // may fail if the server hangs up early (FRAME_SIZE): fine
		c.(*net.TCPConn).CloseWrite()

		out, rerr := io.ReadAll(c)
		var ne net.Error
		if errors.As(rerr, &ne) && ne.Timeout() {
			t.Fatalf("server did not close after our EOF (%d bytes received)", len(out))
		}
		if rerr == nil { // a reset legitimately truncates what we saw
			checkServerBytes(t, out)
		}
		if panicked.Load() {
			t.Fatal("server panicked")
		}
		k := dial(t, addr) // still alive and correct afterwards
		if r, body := k.get("/index.html"); r.Status != 200 || string(body) != "hello" {
			t.Fatalf("server unhealthy after input: %d %q", r.Status, body)
		}
	})
}

func checkServerBytes(t *testing.T, out []byte) {
	t.Helper()
	r := bytes.NewReader(out)
	if v, err := frame.ReadPreface(r); err != nil || v != 1 {
		t.Fatalf("bad server preface: v=%d err=%v", v, err)
	}
	buf := make([]byte, frame.MaxPayload)
	for {
		h, p, err := frame.ReadFrame(r, buf)
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("server emitted a malformed frame: %v", err)
		}
		switch h.Type {
		case frame.TypeResponse, frame.TypeData:
			if h.StreamID == 0 || h.StreamID%2 == 0 {
				t.Fatalf("%v on stream %d", h.Type, h.StreamID)
			}
		case frame.TypeError:
			if h.StreamID != 0 {
				t.Fatalf("ERROR on stream %d", h.StreamID)
			}
		default:
			t.Fatalf("server sent unexpected type %v", h.Type)
		}
		if bytes.Contains(p, []byte("SECRET")) {
			t.Fatal("server leaked the secret")
		}
	}
}
