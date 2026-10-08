package frame

import (
	"errors"
	"io"
)

const (
	PrefaceSize = 8
	Version     = 1
)

var magic = [4]byte{'B', 'H', 'T', 'P'}

var ErrBadPreface = errors.New("frame: bad preface magic")

func WritePreface(w io.Writer, version uint8) error {
	b := [PrefaceSize]byte{magic[0], magic[1], magic[2], magic[3], version}
	_, err := w.Write(b[:])
	return err
}

// ReadPreface returns the peer's version. Reserved bytes are ignored.
func ReadPreface(r io.Reader) (uint8, error) {
	var b [PrefaceSize]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	if [4]byte(b[0:4]) != magic {
		return 0, ErrBadPreface
	}
	return b[4], nil
}
