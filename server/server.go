package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"bhttp/frame"
	"bhttp/hpack"
)

const (
	timeFormat  = "Mon, 02 Jan 2006 15:04:05 GMT"
	lingerTime  = time.Second
	lingerBytes = 64 << 10
)

var (
	serverHdr      = []hpack.Field{{Name: "server", Value: "bserve"}}
	errFileChanged = errors.New("server: file changed during transfer")
)

type Config struct {
	Root             string
	MaxConns         int           // 0 = unlimited
	HandshakeTimeout time.Duration // preface exchange
	IdleTimeout      time.Duration // waiting for the next frame header
	FrameTimeout     time.Duration // payload after its header
	WriteTimeout     time.Duration // each frame write
	CacheControl     string
	Logf             func(format string, args ...any)
}

func (c Config) withDefaults() Config {
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = 5 * time.Second
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 60 * time.Second
	}
	if c.FrameTimeout == 0 {
		c.FrameTimeout = 10 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.CacheControl == "" {
		c.CacheControl = "public, max-age=60"
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
	return c
}

type Server struct {
	cfg  Config
	root *os.Root
	pool sync.Pool
	sem  chan struct{}
}

func New(cfg Config) (*Server, error) {
	cfg = cfg.withDefaults()
	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, root: root}
	s.pool.New = func() any { b := make([]byte, frame.MaxPayload); return &b }
	if cfg.MaxConns > 0 {
		s.sem = make(chan struct{}, cfg.MaxConns)
	}
	return s, nil
}

func (s *Server) Close() error { return s.root.Close() }

func (s *Server) acquire() {
	if s.sem != nil {
		s.sem <- struct{}{}
	}
}

func (s *Server) release() {
	if s.sem != nil {
		<-s.sem
	}
}

// Serve accepts until ln is closed. When MaxConns is reached we stop calling
// Accept, so excess clients wait in the kernel backlog (backpressure).
func (s *Server) Serve(ln net.Listener) error {
	var backoff time.Duration
	for {
		s.acquire()
		c, err := ln.Accept()
		if err != nil {
			s.release()
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// EMFILE and friends: back off and retry, never die.
			backoff = min(max(backoff*2, 5*time.Millisecond), time.Second)
			s.cfg.Logf("accept: %v; retrying in %v", err, backoff)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		go func() {
			defer s.release()
			s.serveConn(c)
		}()
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	defer func() {
		if r := recover(); r != nil {
			s.cfg.Logf("panic serving %v: %v", c.RemoteAddr(), r)
		}
	}()

	// Preface: client first. Anything wrong => close silently (spec 2).
	c.SetDeadline(time.Now().Add(s.cfg.HandshakeTimeout))
	cv, err := frame.ReadPreface(c)
	if err != nil || cv == 0 {
		return
	}
	if err := frame.WritePreface(c, min(cv, frame.Version)); err != nil {
		return
	}
	c.SetDeadline(time.Time{})

	var last uint32 // highest request stream ID seen
	for {
		c.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		h, err := frame.ReadHeader(c)
		if err != nil {
			s.readFailed(c, err)
			return
		}
		c.SetReadDeadline(time.Now().Add(s.cfg.FrameTimeout))

		switch h.Type {
		case frame.TypeRequest:
			if h.StreamID == 0 || h.StreamID%2 == 0 || h.StreamID <= last {
				s.fail(c, frame.ErrCodeProtocol, "bad request stream id")
				return
			}
			last = h.StreamID

			bp := s.pool.Get().(*[]byte)
			p, err := frame.ReadPayload(c, h, *bp)
			if err != nil {
				s.pool.Put(bp)
				s.readFailed(c, err)
				return
			}
			// ParseRequest copies everything out of p, so the buffer can
			// go back to the pool before we touch the filesystem.
			req, perr := frame.ParseRequest(p)
			s.pool.Put(bp)
			if perr == nil && h.Flags&frame.FlagEndStream == 0 {
				perr = fmt.Errorf("%w: request without END_STREAM", frame.ErrMalformed)
			}
			if perr != nil { // stream error: framing intact, stay open
				s.cfg.Logf("stream=%d 400: %v", h.StreamID, perr)
				if s.respond(c, h.StreamID, 400, serverHdr, true) != nil {
					return
				}
				continue
			}
			if err := s.serveFile(c, h.StreamID, req); err != nil {
				if errors.Is(err, errFileChanged) {
					s.fail(c, frame.ErrCodeInternal, "file changed during transfer")
				}
				return // write errors: the stream is torn, just close
			}

		case frame.TypeData:
			if h.StreamID == 0 {
				s.fail(c, frame.ErrCodeProtocol, "DATA on stream 0")
				return
			}
			if err := discard(c, h.Length); err != nil { // v1 requests have no body
				s.readFailed(c, err)
				return
			}

		case frame.TypeResponse:
			s.fail(c, frame.ErrCodeProtocol, "RESPONSE sent to server")
			return

		case frame.TypeError:
			if h.StreamID != 0 {
				s.fail(c, frame.ErrCodeProtocol, "ERROR on non-zero stream")
				return
			}
			bp := s.pool.Get().(*[]byte)
			if p, err := frame.ReadPayload(c, h, *bp); err == nil {
				code, msg := frame.ParseError(p)
				s.cfg.Logf("peer %v sent ERROR %v: %q", c.RemoteAddr(), code, msg)
			}
			s.pool.Put(bp)
			return

		default: // unknown type: skip exactly Length bytes (spec 1)
			if err := discard(c, h.Length); err != nil {
				s.readFailed(c, err)
				return
			}
		}
	}
}

func discard(c net.Conn, n uint16) error {
	_, err := io.CopyN(io.Discard, c, int64(n))
	return err
}

func (s *Server) readFailed(c net.Conn, err error) {
	var ne net.Error
	switch {
	case errors.Is(err, frame.ErrFrameTooLarge):
		s.fail(c, frame.ErrCodeFrameSize, "length exceeds 16384")
	case errors.As(err, &ne) && ne.Timeout():
		s.fail(c, frame.ErrCodeTimeout, "")
	}
	// EOF, reset, ...: the peer is gone; nothing to say.
}

// fail sends a best-effort ERROR, then closes lingeringly.
func (s *Server) fail(c net.Conn, code frame.ErrorCode, msg string) {
	c.SetWriteDeadline(time.Now().Add(time.Second))
	frame.WriteError(c, code, msg)
	lingerClose(c)
}

// lingerClose: half-close, drain briefly, close. Without the drain, unread
// input makes the kernel send RST and the peer may lose our ERROR frame.
func lingerClose(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
	c.SetReadDeadline(time.Now().Add(lingerTime))
	io.Copy(io.Discard, io.LimitReader(c, lingerBytes))
	c.Close()
}

func (s *Server) wd(c net.Conn) { c.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout)) }

func (s *Server) respond(c net.Conn, id uint32, status uint16, hdrs []hpack.Field, end bool) error {
	bp := s.pool.Get().(*[]byte)
	defer s.pool.Put(bp)
	p, err := frame.AppendResponse((*bp)[:0], frame.Response{Status: status, Headers: hdrs})
	if err != nil {
		return err
	}
	var fl frame.Flags
	if end {
		fl = frame.FlagEndStream
	}
	s.wd(c)
	return frame.WriteFrame(c, frame.Header{Type: frame.TypeResponse, Flags: fl, StreamID: id}, p)
}

// open maps a request path to a regular file under the root. Every failure
// looks the same to the caller (404).
func (s *Server) open(reqPath string) (*os.File, fs.FileInfo, error) {
	name := strings.TrimPrefix(reqPath, "/")
	if !fs.ValidPath(name) || strings.IndexByte(name, 0) >= 0 {
		return nil, nil, fs.ErrNotExist
	}
	f, err := s.root.Open(name) // os.Root: no escape via .. or symlinks, no TOCTOU
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, fi, nil
}

func (s *Server) serveFile(c net.Conn, id uint32, req frame.Request) (err error) {
	start := time.Now()
	status := 404
	var size int64
	defer func() {
		s.cfg.Logf("stream=%d GET %q -> %d %dB %v err=%v", id, req.Path, status, size, time.Since(start), err)
	}()

	f, fi, err := s.open(req.Path)
	if err != nil {
		return s.respond(c, id, 404, serverHdr, true)
	}
	defer f.Close()

	size = fi.Size()
	mt := fi.ModTime()
	etag := fmt.Sprintf(`"%x-%x"`, mt.Unix(), size)
	hdrs := []hpack.Field{
		{Name: "server", Value: "bserve"},
		{Name: "etag", Value: etag},
		{Name: "last-modified", Value: mt.UTC().Format(timeFormat)},
		{Name: "cache-control", Value: s.cfg.CacheControl},
	}
	if inm, ok := headerValue(req.Headers, "if-none-match"); ok && etagMatch(inm, etag) {
		status, size = 304, 0
		return s.respond(c, id, 304, hdrs, true)
	}
	hdrs = append(hdrs,
		hpack.Field{Name: "content-type", Value: contentType(req.Path)},
		hpack.Field{Name: "content-length", Value: strconv.FormatInt(size, 10)},
	)
	status = 200
	if err := s.respond(c, id, 200, hdrs, size == 0); err != nil {
		return err
	}
	return s.sendBody(c, id, f, size)
}

// sendBody streams exactly size bytes in <=16 KiB DATA frames. Memory is
// one pooled buffer no matter how large the file is.
func (s *Server) sendBody(c net.Conn, id uint32, f *os.File, size int64) error {
	bp := s.pool.Get().(*[]byte)
	defer s.pool.Put(bp)
	for remaining := size; remaining > 0; {
		n := int(min(remaining, int64(frame.MaxPayload)))
		if _, err := io.ReadFull(f, (*bp)[:n]); err != nil {
			// We already promised content-length. Can't change the status now.
			return fmt.Errorf("%w: %v", errFileChanged, err)
		}
		remaining -= int64(n)
		var fl frame.Flags
		if remaining == 0 {
			fl = frame.FlagEndStream
		}
		s.wd(c)
		if err := frame.WriteFrame(c, frame.Header{Type: frame.TypeData, Flags: fl, StreamID: id}, (*bp)[:n]); err != nil {
			return err
		}
	}
	return nil
}

func headerValue(hs []hpack.Field, name string) (string, bool) {
	for _, f := range hs {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

func etagMatch(inm, etag string) bool {
	for _, t := range strings.Split(inm, ",") {
		if t = strings.TrimSpace(t); t == "*" || t == etag {
			return true
		}
	}
	return false
}

func contentType(p string) string {
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}
