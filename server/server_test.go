package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bhttp/frame"
	"bhttp/hpack"
)

func quiet(string, ...any) {}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func newServer(t testing.TB, mod func(*Config)) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "www")
	must(t, os.Mkdir(root, 0o755))
	must(t, os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOP SECRET"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(root, "big.bin"), pattern(40000), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "huge.bin"), nil, 0o644))
	must(t, os.Truncate(filepath.Join(root, "huge.bin"), 64<<20)) // sparse, bigger than any socket buffer
	must(t, os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(root, "leak")))
	must(t, os.Symlink("index.html", filepath.Join(root, "alias.html")))

	cfg := Config{Root: root, Logf: quiet}
	if mod != nil {
		mod(&cfg)
	}
	srv, err := New(cfg)
	must(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close(); srv.Close() })
	return ln.Addr().String()
}

type client struct {
	t          *testing.T
	c          net.Conn
	nextID     uint32
	buf        []byte
	dataFrames int
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	must(t, err)
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	must(t, frame.WritePreface(c, 1))
	v, err := frame.ReadPreface(c)
	must(t, err)
	if v != 1 {
		t.Fatalf("server version %d", v)
	}
	return &client{t: t, c: c, nextID: 1, buf: make([]byte, frame.MaxPayload)}
}

func (k *client) send(typ frame.Type, flags frame.Flags, id uint32, payload []byte) {
	k.t.Helper()
	must(k.t, frame.WriteFrame(k.c, frame.Header{Type: typ, Flags: flags, StreamID: id}, payload))
}

func (k *client) sendGet(id uint32, p string, hs ...hpack.Field) {
	k.t.Helper()
	pl, err := frame.AppendRequest(nil, frame.Request{Method: frame.MethodGET, Path: p, Headers: hs})
	must(k.t, err)
	k.send(frame.TypeRequest, frame.FlagEndStream, id, pl)
}

func (k *client) get(p string, hs ...hpack.Field) (frame.Response, []byte) {
	k.t.Helper()
	id := k.nextID
	k.nextID += 2
	k.sendGet(id, p, hs...)
	return k.readResponse(id)
}

func (k *client) readResponse(id uint32) (frame.Response, []byte) {
	k.t.Helper()
	h, p, err := frame.ReadFrame(k.c, k.buf)
	must(k.t, err)
	if h.Type != frame.TypeResponse || h.StreamID != id {
		k.t.Fatalf("want RESPONSE on stream %d, got %v on %d", id, h.Type, h.StreamID)
	}
	resp, err := frame.ParseResponse(p)
	must(k.t, err)
	var body []byte
	k.dataFrames = 0
	for end := h.Flags&frame.FlagEndStream != 0; !end; {
		h, p, err = frame.ReadFrame(k.c, k.buf)
		must(k.t, err)
		if h.Type != frame.TypeData || h.StreamID != id {
			k.t.Fatalf("want DATA on stream %d, got %v on %d", id, h.Type, h.StreamID)
		}
		k.dataFrames++
		body = append(body, p...)
		end = h.Flags&frame.FlagEndStream != 0
	}
	return resp, body
}

// expectError: next frame is ERROR(want) on stream 0, then the conn closes.
func (k *client) expectError(want frame.ErrorCode) {
	k.t.Helper()
	h, p, err := frame.ReadFrame(k.c, k.buf)
	must(k.t, err)
	if h.Type != frame.TypeError || h.StreamID != 0 {
		k.t.Fatalf("want ERROR on stream 0, got %v on %d", h.Type, h.StreamID)
	}
	if code, _ := frame.ParseError(p); code != want {
		k.t.Fatalf("got %v want %v", code, want)
	}
	if _, _, err := frame.ReadFrame(k.c, k.buf); err == nil {
		k.t.Fatal("connection still open after ERROR")
	}
}

func hv(r frame.Response, name string) string {
	for _, f := range r.Headers {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestServeFile(t *testing.T) {
	k := dial(t, newServer(t, nil))
	r, body := k.get("/index.html")
	if r.Status != 200 || string(body) != "hello" {
		t.Fatalf("%d %q", r.Status, body)
	}
	if hv(r, "content-length") != "5" || hv(r, "server") != "bserve" || hv(r, "etag") == "" {
		t.Fatalf("headers: %+v", r.Headers)
	}
	if !strings.HasPrefix(hv(r, "content-type"), "text/html") {
		t.Fatalf("content-type %q", hv(r, "content-type"))
	}
}

func TestKeepAliveOneConnection(t *testing.T) {
	k := dial(t, newServer(t, nil))
	for i := 0; i < 5; i++ {
		if r, _ := k.get("/index.html"); r.Status != 200 {
			t.Fatalf("req %d: %d", i, r.Status)
		}
	}
	if r, body := k.get("/nope"); r.Status != 404 || len(body) != 0 {
		t.Fatalf("404: %d %q", r.Status, body)
	}
	if r, _ := k.get("/index.html"); r.Status != 200 { // 404 must not close the conn
		t.Fatalf("after 404: %d", r.Status)
	}
}

func TestBigFileChunked(t *testing.T) {
	k := dial(t, newServer(t, nil))
	r, body := k.get("/big.bin")
	if r.Status != 200 || hv(r, "content-length") != "40000" {
		t.Fatalf("%d %v", r.Status, r.Headers)
	}
	if !bytes.Equal(body, pattern(40000)) {
		t.Fatal("body mismatch")
	}
	if k.dataFrames != 3 { // 16384 + 16384 + 7232
		t.Fatalf("data frames = %d", k.dataFrames)
	}
}

func TestEmptyFile(t *testing.T) {
	k := dial(t, newServer(t, nil))
	r, body := k.get("/empty.txt")
	if r.Status != 200 || len(body) != 0 || k.dataFrames != 0 || hv(r, "content-length") != "0" {
		t.Fatalf("%d %q frames=%d", r.Status, body, k.dataFrames)
	}
}

func TestSymlinkInsideRootAllowed(t *testing.T) {
	k := dial(t, newServer(t, nil))
	if r, body := k.get("/alias.html"); r.Status != 200 || string(body) != "hello" {
		t.Fatalf("%d %q", r.Status, body)
	}
}

func TestTraversalAndOddPaths(t *testing.T) {
	k := dial(t, newServer(t, nil))
	for _, p := range []string{
		"/../secret.txt", "/index.html/../../secret.txt", "/leak", // symlink escape
		"//index.html", "/./index.html", "/", "/.", "/index.html/",
		"/%2e%2e/secret.txt", "/index.html\x00",
	} {
		r, body := k.get(p)
		if r.Status != 404 || len(body) != 0 {
			t.Errorf("%q: got %d %q", p, r.Status, body)
		}
		if bytes.Contains(body, []byte("SECRET")) {
			t.Fatalf("%q leaked the secret", p)
		}
	}
	if r, _ := k.get("/index.html"); r.Status != 200 {
		t.Fatalf("connection unusable after probes: %d", r.Status)
	}
}

func TestMalformedIs400AndKeepsConn(t *testing.T) {
	k := dial(t, newServer(t, nil))
	k.send(frame.TypeRequest, frame.FlagEndStream, 1, []byte{0x02, 0x00, 0x01, '/'}) // method 2
	if r, _ := k.readResponse(1); r.Status != 400 {
		t.Fatalf("bad method: %d", r.Status)
	}
	p, _ := frame.AppendRequest(nil, frame.Request{Method: frame.MethodGET, Path: "/index.html"})
	k.send(frame.TypeRequest, 0, 3, p) // no END_STREAM
	if r, _ := k.readResponse(3); r.Status != 400 {
		t.Fatalf("no END_STREAM: %d", r.Status)
	}
	k.nextID = 5
	if r, body := k.get("/index.html"); r.Status != 200 || string(body) != "hello" {
		t.Fatalf("conn not usable after 400s: %d", r.Status)
	}
}

func TestUnknownAndClientDataSkipped(t *testing.T) {
	k := dial(t, newServer(t, nil))
	k.send(0x7f, 0, 0, []byte("future frame"))
	k.send(frame.TypeData, 0, 99, []byte("junk body"))
	if r, body := k.get("/index.html"); r.Status != 200 || string(body) != "hello" {
		t.Fatalf("desynced: %d %q", r.Status, body)
	}
}

func TestOversizeFrameGetsFrameSizeError(t *testing.T) {
	k := dial(t, newServer(t, nil))
	hdr := []byte{0x40, 0x01, byte(frame.TypeRequest), 0, 0, 0, 0, 1} // Length 16385
	_, err := k.c.Write(append(hdr, make([]byte, 16385)...))
	must(t, err)
	k.expectError(frame.ErrCodeFrameSize) // lingering close keeps this from becoming an RST
}

func TestStreamIDViolations(t *testing.T) {
	addr := newServer(t, nil)
	t.Run("even", func(t *testing.T) {
		k := dial(t, addr)
		k.sendGet(2, "/index.html")
		k.expectError(frame.ErrCodeProtocol)
	})
	t.Run("zero", func(t *testing.T) {
		k := dial(t, addr)
		k.sendGet(0, "/index.html")
		k.expectError(frame.ErrCodeProtocol)
	})
	t.Run("decreasing", func(t *testing.T) {
		k := dial(t, addr)
		k.sendGet(5, "/index.html")
		k.readResponse(5)
		k.sendGet(3, "/index.html")
		k.expectError(frame.ErrCodeProtocol)
	})
	t.Run("repeated", func(t *testing.T) {
		k := dial(t, addr)
		k.sendGet(1, "/index.html")
		k.readResponse(1)
		k.sendGet(1, "/index.html")
		k.expectError(frame.ErrCodeProtocol)
	})
}

func TestWrongDirectionAndStreamZeroRules(t *testing.T) {
	addr := newServer(t, nil)
	for _, c := range []struct {
		name string
		typ  frame.Type
		id   uint32
	}{
		{"DATA on stream 0", frame.TypeData, 0},
		{"RESPONSE to server", frame.TypeResponse, 1},
		{"ERROR on stream 1", frame.TypeError, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			k := dial(t, addr)
			k.send(c.typ, 0, c.id, []byte{0, 0})
			k.expectError(frame.ErrCodeProtocol)
		})
	}
}

func TestBadPrefaceClosesSilently(t *testing.T) {
	c, err := net.Dial("tcp", newServer(t, nil))
	must(t, err)
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	n, err := c.Read(make([]byte, 64))
	if n != 0 || err == nil { // EOF or reset are both fine; bytes are not
		t.Fatalf("server answered a non-BHTP peer: n=%d err=%v", n, err)
	}
}

func TestTimeoutsSendErrorThenClose(t *testing.T) {
	addr := newServer(t, func(c *Config) {
		c.IdleTimeout = 150 * time.Millisecond
		c.FrameTimeout = 150 * time.Millisecond
	})
	t.Run("silent client", func(t *testing.T) {
		dial(t, addr).expectError(frame.ErrCodeTimeout)
	})
	t.Run("stalled mid-header", func(t *testing.T) {
		k := dial(t, addr)
		k.c.Write([]byte{0, 0, 1})
		k.expectError(frame.ErrCodeTimeout)
	})
	t.Run("stalled mid-payload", func(t *testing.T) {
		k := dial(t, addr)
		k.c.Write([]byte{0, 100, byte(frame.TypeData), 0, 0, 0, 0, 1, 'a', 'b'})
		k.expectError(frame.ErrCodeTimeout)
	})
}

func TestConditionalGet(t *testing.T) {
	k := dial(t, newServer(t, nil))
	r, _ := k.get("/index.html")
	etag := hv(r, "etag")

	r, body := k.get("/index.html", hpack.Field{Name: "if-none-match", Value: etag})
	if r.Status != 304 || len(body) != 0 || k.dataFrames != 0 || hv(r, "etag") != etag {
		t.Fatalf("304: %d %q", r.Status, body)
	}
	r, body = k.get("/index.html", hpack.Field{Name: "if-none-match", Value: `"stale"`})
	if r.Status != 200 || string(body) != "hello" {
		t.Fatalf("stale etag: %d %q", r.Status, body)
	}
}
