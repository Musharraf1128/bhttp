package frame

import (
	"encoding/binary"
	"io"
	"net"
)

// ReadFrame reads one whole frame into buf (len(buf) must be >= MaxPayload;
// use a pooled buffer). Oversize Length is rejected BEFORE any payload read.
// EOF between frames is io.EOF; EOF anywhere inside a frame is
// io.ErrUnexpectedEOF. Note io.ReadFull returns plain io.EOF if it reads
// zero bytes, so a peer dying right after the header must be remapped.
func ReadFrame(r io.Reader, buf []byte) (Header, []byte, error) {
	h, err := ReadHeader(r)
	if err != nil {
		return h, nil, err
	}
	if h.Length == 0 {
		return h, buf[:0], nil
	}
	p := buf[:h.Length]
	if _, err := io.ReadFull(r, p); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return h, nil, err
	}
	return h, p, nil
}

// WriteFrame sends header+payload with one writev on a TCP conn.
func WriteFrame(w io.Writer, h Header, payload []byte) error {
	h.Length = uint16(len(payload))
	if len(payload) > MaxPayload {
		return ErrFrameTooLarge
	}
	hb, err := h.Encode()
	if err != nil {
		return err
	}
	bufs := net.Buffers{hb[:], payload}
	_, err = bufs.WriteTo(w)
	return err
}

// WriteError sends a connection-level ERROR frame on stream 0.
func WriteError(w io.Writer, code ErrorCode, msg string) error {
	if len(msg) > 256 {
		msg = msg[:256]
	}
	p := make([]byte, 2, 2+len(msg))
	binary.BigEndian.PutUint16(p, uint16(code))
	p = append(p, msg...)
	return WriteFrame(w, Header{Type: TypeError, StreamID: 0}, p)
}

// ParseError decodes an ERROR payload. Short payloads are PROTOCOL.
func ParseError(p []byte) (ErrorCode, string) {
	if len(p) < 2 {
		return ErrCodeProtocol, ""
	}
	return ErrorCode(binary.BigEndian.Uint16(p)), string(p[2:])
}
