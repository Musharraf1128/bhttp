package frame

import (
	"encoding/binary"
	"errors"
	"fmt"

	"bhttp/hpack"
)

var (
	ErrMalformed = errors.New("frame: malformed payload") // stream error: answer 400
	ErrBadMethod = errors.New("frame: unsupported method")
	ErrBadPath   = errors.New("frame: path must be non-empty and start with /")
	ErrBadStatus = errors.New("frame: status must be 100..599")
)

type Request struct {
	Method  uint8
	Path    string
	Headers []hpack.Field
}

type Response struct {
	Status  uint16
	Headers []hpack.Field
}

// AppendRequest appends the REQUEST payload to dst. On error dst is returned
// at its original length, so a failed encode never leaves partial bytes.
func AppendRequest(dst []byte, r Request) ([]byte, error) {
	start := len(dst)
	if r.Method != MethodGET {
		return dst, ErrBadMethod
	}
	if len(r.Path) == 0 || r.Path[0] != '/' {
		return dst, ErrBadPath
	}
	if len(r.Path) > MaxPayload {
		return dst, ErrFrameTooLarge
	}
	dst = append(dst, r.Method)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(r.Path)))
	dst = append(dst, r.Path...)
	dst, err := hpack.Encode(dst, r.Headers)
	if err != nil {
		return dst[:start], err
	}
	if len(dst)-start > MaxPayload {
		return dst[:start], ErrFrameTooLarge
	}
	return dst, nil
}

// ParseRequest decodes a REQUEST payload. Every failure wraps ErrMalformed.
func ParseRequest(p []byte) (Request, error) {
	if len(p) < 3 {
		return Request{}, fmt.Errorf("%w: short request", ErrMalformed)
	}
	method := p[0]
	n := int(binary.BigEndian.Uint16(p[1:3]))
	p = p[3:]
	if method != MethodGET {
		return Request{}, fmt.Errorf("%w: method %#x", ErrMalformed, method)
	}
	if n == 0 || len(p) < n {
		return Request{}, fmt.Errorf("%w: path length %d", ErrMalformed, n)
	}
	path := string(p[:n])
	if path[0] != '/' {
		return Request{}, fmt.Errorf("%w: path must start with /", ErrMalformed)
	}
	hdrs, err := hpack.Decode(p[n:])
	if err != nil {
		return Request{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return Request{Method: method, Path: path, Headers: hdrs}, nil
}

func AppendResponse(dst []byte, r Response) ([]byte, error) {
	start := len(dst)
	if r.Status < 100 || r.Status > 599 {
		return dst, ErrBadStatus
	}
	dst = binary.BigEndian.AppendUint16(dst, r.Status)
	dst, err := hpack.Encode(dst, r.Headers)
	if err != nil {
		return dst[:start], err
	}
	if len(dst)-start > MaxPayload {
		return dst[:start], ErrFrameTooLarge
	}
	return dst, nil
}

func ParseResponse(p []byte) (Response, error) {
	if len(p) < 2 {
		return Response{}, fmt.Errorf("%w: short response", ErrMalformed)
	}
	status := binary.BigEndian.Uint16(p[0:2])
	if status < 100 || status > 599 {
		return Response{}, fmt.Errorf("%w: status %d", ErrMalformed, status)
	}
	hdrs, err := hpack.Decode(p[2:])
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return Response{Status: status, Headers: hdrs}, nil
}
