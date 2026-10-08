package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"bhttp/client"
	"bhttp/frame"
)

func main() {
	verbose := flag.Bool("v", false, "hexdump every frame to stderr")
	timeout := flag.Duration("timeout", 30*time.Second, "total time limit")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: bcurl [-v] [-timeout 30s] host:port/path")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	hostport, path := flag.Arg(0), "/"
	if i := strings.IndexByte(hostport, '/'); i >= 0 {
		hostport, path = hostport[:i], hostport[i:]
	}
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		fmt.Fprintln(os.Stderr, "bcurl: need host:port:", err)
		os.Exit(2)
	}

	conn, err := net.DialTimeout("tcp", hostport, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcurl:", err)
		os.Exit(1)
	}
	defer conn.Close()
	client.SetTotalTimeout(conn, *timeout)

	var trace client.Trace
	if *verbose {
		trace = dump
	}
	res, err := client.Get(conn, hostport, path, nil, os.Stdout, trace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcurl:", err)
		os.Exit(1)
	}
	if *verbose {
		fmt.Fprintf(os.Stderr, "-- status %d, %d body bytes\n", res.Status, res.Bytes)
	}
	if res.Status >= 400 {
		os.Exit(1)
	}
}

func dump(dir string, h frame.Header, p []byte) {
	hb, _ := h.Encode()
	fmt.Fprintf(os.Stderr, "%s %v flags=%#02x stream=%d len=%d\n", dir, h.Type, uint8(h.Flags), h.StreamID, h.Length)
	fmt.Fprint(os.Stderr, indent(hex.Dump(hb[:])))
	if len(p) > 0 {
		fmt.Fprint(os.Stderr, indent(hex.Dump(p[:min(len(p), 256)])))
		if len(p) > 256 {
			fmt.Fprintf(os.Stderr, "    ... %d more bytes\n", len(p)-256)
		}
	}
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ") + "\n"
}
