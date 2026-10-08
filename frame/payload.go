package frame

import "io"

// ReadPayload reads h.Length bytes into buf (cap(buf) must be >= MaxPayload).
// Use after ReadHeader when the caller wants to borrow its buffer late.
// io.ReadFull returns plain io.EOF on zero bytes; after a header that is a
// truncated frame, so remap it (same trap as ReadFrame).
func ReadPayload(r io.Reader, h Header, buf []byte) ([]byte, error) {
	if h.Length > MaxPayload {
		return nil, ErrFrameTooLarge
	}
	p := buf[:h.Length]
	if _, err := io.ReadFull(r, p); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return p, nil
}
