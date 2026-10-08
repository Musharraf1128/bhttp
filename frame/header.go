package frame

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	HeaderSize  = 8
	MaxPayload  = 16384
	MaxStreamID = 1<<31 - 1
)

type Type uint8
type Flags uint8

type Header struct {
	Length   uint16
	Type     Type
	Flags    Flags
	StreamID uint32 // 31 bits
}

var (
	ErrFrameTooLarge = errors.New("frame: length exceeds MaxPayload")
	ErrBadStreamID   = errors.New("frame: stream id exceeds 31 bits")
)

// Encode returns the 8 wire bytes. R bit is always 0.
func (h Header) Encode() ([HeaderSize]byte, error) {
	var b [HeaderSize]byte
	if h.Length > MaxPayload {
		return b, ErrFrameTooLarge
	}
	if h.StreamID > MaxStreamID {
		return b, ErrBadStreamID
	}
	binary.BigEndian.PutUint16(b[0:2], h.Length)
	b[2] = byte(h.Type)
	b[3] = byte(h.Flags)
	binary.BigEndian.PutUint32(b[4:8], h.StreamID)
	return b, nil
}

// DecodeHeader parses 8 bytes. It ignores the R bit (spec 1) and rejects
// oversized lengths BEFORE the caller reads any payload.
func DecodeHeader(b [HeaderSize]byte) (Header, error) {
	h := Header{
		Length:   binary.BigEndian.Uint16(b[0:2]),
		Type:     Type(b[2]),
		Flags:    Flags(b[3]),
		StreamID: binary.BigEndian.Uint32(b[4:8]) & MaxStreamID,
	}
	if h.Length > MaxPayload {
		return h, ErrFrameTooLarge
	}
	return h, nil
}

// ReadHeader reads exactly one header. Returns io.EOF on a clean close
// between frames and io.ErrUnexpectedEOF if the peer died mid-header.
func ReadHeader(r io.Reader) (Header, error) {
	var b [HeaderSize]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Header{}, err
	}
	return DecodeHeader(b)
}
