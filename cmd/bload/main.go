package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bhttp/client"
	"bhttp/frame"
)

func die(args ...any) {
	fmt.Fprintln(os.Stderr, append([]any{"bload:"}, args...)...)
	os.Exit(1)
}

func rssKiB(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		die(err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if f := strings.Fields(ln); len(f) >= 2 && f[0] == "VmRSS:" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return n
		}
	}
	die("no VmRSS in /proc status")
	return 0
}

func open(addr string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := frame.WritePreface(c, frame.Version); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := frame.ReadPreface(c); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return c, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "bserve address")
	n := flag.Int("n", 10000, "idle connections to hold")
	pid := flag.Int("pid", 0, "bserve pid (to read its RSS)")
	flag.Parse()
	if *pid == 0 {
		die("need -pid <bserve pid>")
	}

	base := rssKiB(*pid)
	conns := make([]net.Conn, *n)
	var failed atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)
	t0 := time.Now()
	for i := range conns {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := open(*addr)
			if err != nil {
				failed.Add(1)
				return
			}
			conns[i] = c
		}()
	}
	wg.Wait()
	time.Sleep(2 * time.Second) // let the server settle
	live := int64(*n) - failed.Load()
	after := rssKiB(*pid)

	fmt.Printf("idle conns held:  %d (failed %d) in %v\n", live, failed.Load(), time.Since(t0).Round(time.Millisecond))
	fmt.Printf("server RSS:       %d KiB -> %d KiB (+%d KiB)\n", base, after, after-base)
	if live > 0 {
		fmt.Printf("per connection:   %.1f KiB (RSS only; kernel socket buffers are not counted)\n", float64(after-base)/float64(live))
	}

	c, err := net.DialTimeout("tcp", *addr, 5*time.Second)
	if err != nil {
		die("fresh dial under load:", err)
	}
	client.SetTotalTimeout(c, 10*time.Second)
	start := time.Now()
	res, err := client.Get(c, *addr, "/index.html", nil, io.Discard, nil)
	c.Close()
	if err != nil {
		die("fresh GET under load:", err)
	}
	fmt.Printf("fresh GET under load: status %d in %v\n", res.Status, time.Since(start))

	for _, c := range conns {
		if c != nil {
			c.Close()
		}
	}
}
