package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"bhttp/frame"
	"bhttp/hpack"
)

var (
	ErrServerError    = errors.New("client: server sent ERROR")
	ErrShortBody      = errors.New("client: body ended before END_STREAM")
	ErrLengthMismatch = errors.New("client: body length differs from content-length")
	ErrBadVersion     = errors.New("client: unsupported server version")
)

// Trace receives every frame sent or received. dir is ">" or "<".
type Trace func(dir string, h frame.Header, payload []byte)

type Result struct {
	Status  uint16
	Headers []hpack.Field
	Bytes   int64
}

// Get performs one GET on conn. conn must be a fresh connection; the caller
// owns closing it. The body is streamed to body as DATA frames arrive.
func Get(conn net.Conn, host, path string, hdrs []hpack.Field, body io.Writer, trace Trace) (Result, error) {
	var res Result
	buf := make([]byte, frame.MaxPayload)

	if err := frame.WritePreface(conn, frame.Version); err != nil {
		return res, err
	}
	sv, err := frame.ReadPreface(conn)
	if err != nil {
		return res, fmt.Errorf("preface: %w", err)
	}
	if sv == 0 || sv > frame.Version {
		return res, fmt.Errorf("%w: %d", ErrBadVersion, sv)
	}

	all := append([]hpack.Field{{Name: "host", Value: host}, {Name: "user-agent", Value: "bcurl/1"}, {Name: "accept", Value: "*/*"}}, hdrs...)
	pl, err := frame.AppendRequest(nil, frame.Request{Method: frame.MethodGET, Path: path, Headers: all})
	if err != nil {
		return res, err
	}
	const id = 1
	rh := frame.Header{Type: frame.TypeRequest, Flags: frame.FlagEndStream, StreamID: id}
	if err := frame.WriteFrame(conn, rh, pl); err != nil {
		return res, err
	}
	if trace != nil {
		rh.Length = uint16(len(pl))
		trace(">", rh, pl)
	}

	var (
		gotResp  bool
		wantLen  int64 = -1
		finished bool
	)
	for !finished {
		h, p, err := frame.ReadFrame(conn, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				if gotResp {
					return res, ErrShortBody
				}
				return res, fmt.Errorf("connection closed before response: %w", err)
			}
			return res, err
		}
		if trace != nil {
			trace("<", h, p)
		}
		switch h.Type {
		case frame.TypeResponse:
			if h.StreamID != id || gotResp {
				continue
			}
			r, err := frame.ParseResponse(p)
			if err != nil {
				return res, err
			}
			gotResp = true
			res.Status, res.Headers = r.Status, r.Headers
			for _, f := range r.Headers {
				if f.Name == "content-length" {
					if n, err := strconv.ParseInt(f.Value, 10, 64); err == nil {
						wantLen = n
					}
				}
			}
			finished = h.Flags&frame.FlagEndStream != 0
		case frame.TypeData:
			if h.StreamID != id || !gotResp {
				continue // other stream, or DATA before RESPONSE: ignore
			}
			if _, err := body.Write(p); err != nil {
				return res, err
			}
			res.Bytes += int64(len(p))
			finished = h.Flags&frame.FlagEndStream != 0
		case frame.TypeError:
			code, msg := frame.ParseError(p)
			return res, fmt.Errorf("%w: %v %q", ErrServerError, code, msg)
		case frame.TypeRequest:
			return res, errors.New("client: server sent REQUEST (protocol error)")
		default: // unknown type: already consumed, keep going
		}
	}
	if wantLen >= 0 && res.Status == 200 && res.Bytes != wantLen {
		return res, fmt.Errorf("%w: got %d want %d", ErrLengthMismatch, res.Bytes, wantLen)
	}
	return res, nil
}

// Deadline helper so callers don't forget one.
func SetTotalTimeout(c net.Conn, d time.Duration) {
	if d > 0 {
		c.SetDeadline(time.Now().Add(d))
	}
}
