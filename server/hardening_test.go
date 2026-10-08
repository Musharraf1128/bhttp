package server

import (
	"net"
	"testing"
	"time"

	"bhttp/frame"
)

func TestSlowlorisSlotsAreReclaimed(t *testing.T) {
	const slots = 20
	addr := newServer(t, func(c *Config) {
		c.MaxConns = slots
		c.HandshakeTimeout = 300 * time.Millisecond
	})
	for i := 0; i < slots; i++ { // every slot taken by a client that stalls mid-preface
		c, err := net.Dial("tcp", addr)
		must(t, err)
		t.Cleanup(func() { c.Close() })
		c.Write([]byte("BH"))
	}
	start := time.Now()
	k := dial(t, addr) // waits in the backlog until a slot frees
	r, body := k.get("/index.html")
	el := time.Since(start)
	if r.Status != 200 || string(body) != "hello" {
		t.Fatalf("%d %q", r.Status, body)
	}
	if el < 200*time.Millisecond {
		t.Fatalf("served in %v: the slots were never full, test proves nothing", el)
	}
	if el > 2*time.Second {
		t.Fatalf("stalled clients held slots for %v", el)
	}
	t.Logf("legit client waited %v behind %d stalled peers", el, slots)
}

func TestSlowReaderIsCut(t *testing.T) {
	addr := newServer(t, func(c *Config) {
		c.MaxConns = 1
		c.WriteTimeout = 300 * time.Millisecond
	})
	slow, err := net.Dial("tcp", addr)
	must(t, err)
	t.Cleanup(func() { slow.Close() })
	slow.SetDeadline(time.Now().Add(5 * time.Second))
	must(t, frame.WritePreface(slow, 1))
	_, err = frame.ReadPreface(slow)
	must(t, err)
	pl, err := frame.AppendRequest(nil, frame.Request{Method: frame.MethodGET, Path: "/huge.bin"})
	must(t, err)
	must(t, frame.WriteFrame(slow, frame.Header{Type: frame.TypeRequest, Flags: frame.FlagEndStream, StreamID: 1}, pl))
	// ...and never read a byte.

	time.Sleep(100 * time.Millisecond) // let the server fill the socket buffers and block
	start := time.Now()
	k := dial(t, addr) // only gets a slot once the slow reader is cut
	r, body := k.get("/index.html")
	el := time.Since(start)
	if r.Status != 200 || string(body) != "hello" {
		t.Fatalf("%d %q", r.Status, body)
	}
	if el > 3*time.Second {
		t.Fatalf("slow reader pinned the slot for %v", el)
	}
	t.Logf("slow reader cut; next client served after %v", el)
}
