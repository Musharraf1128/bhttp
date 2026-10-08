package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"bhttp/server"
)

func main() {
	host := flag.String("host", "127.0.0.1", "listen address (use 0.0.0.0 for interop)")
	maxConns := flag.Int("max-conns", 10000, "max concurrent connections (0 = unlimited)")
	idle := flag.Duration("idle", 60*time.Second, "close connections idle this long")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: bserve [flags] <root> <port>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	root, port := flag.Arg(0), flag.Arg(1)

	srv, err := server.New(server.Config{Root: root, MaxConns: *maxConns, IdleTimeout: *idle})
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(*host, port))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("bserve: serving %s on %s", root, ln.Addr())
	log.Fatal(srv.Serve(ln))
}
