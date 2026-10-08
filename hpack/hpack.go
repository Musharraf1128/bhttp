package hpack

import "errors"

const (
	MaxName  = 255
	MaxValue = 255
)

type Field struct {
	Name, Value string
}

// Index 0 is unused so an index byte of 0x80 is invalid.
var static = [...]string{
	1: "host", 2: "user-agent", 3: "accept", 4: "if-none-match",
	5: "content-type", 6: "content-length", 7: "etag",
	8: "cache-control", 9: "last-modified", 10: "server",
}

var staticIdx = func() map[string]byte {
	m := make(map[string]byte, len(static))
	for i := 1; i < len(static); i++ {
		m[static[i]] = byte(i)
	}
	return m
}()

var (
	ErrTruncated = errors.New("hpack: field runs past end of block")
	ErrBadIndex  = errors.New("hpack: static index out of range")
	ErrReserved  = errors.New("hpack: reserved representation")
	ErrEmptyName = errors.New("hpack: empty literal name")
	ErrTooLong   = errors.New("hpack: name or value exceeds 255 bytes")
)

// lowerASCII is deliberately not strings.ToLower: that can change the byte
// length of non-ASCII input, which would break the 255 limit and round-trips.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if 'A' <= b[j] && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// Encode appends the header block for fields to dst. On error dst is
// returned unchanged in length.
func Encode(dst []byte, fields []Field) ([]byte, error) {
	start := len(dst)
	for _, f := range fields {
		name := lowerASCII(f.Name)
		if len(f.Value) > MaxValue {
			return dst[:start], ErrTooLong
		}
		if i, ok := staticIdx[name]; ok {
			dst = append(dst, 0x80|i, byte(len(f.Value)))
			dst = append(dst, f.Value...)
			continue
		}
		if len(name) == 0 {
			return dst[:start], ErrEmptyName
		}
		if len(name) > MaxName {
			return dst[:start], ErrTooLong
		}
		dst = append(dst, 0x00, byte(len(name)))
		dst = append(dst, name...)
		dst = append(dst, byte(len(f.Value)))
		dst = append(dst, f.Value...)
	}
	return dst, nil
}

// Decode parses a whole header block. Output size is bounded by len(b).
func Decode(b []byte) ([]Field, error) {
	var out []Field
	for len(b) > 0 {
		c := b[0]
		b = b[1:]
		var name string
		switch {
		case c&0x80 != 0:
			i := int(c & 0x7f)
			if i < 1 || i >= len(static) {
				return nil, ErrBadIndex
			}
			name = static[i]
		case c == 0x00:
			if len(b) < 1 {
				return nil, ErrTruncated
			}
			n := int(b[0])
			b = b[1:]
			if n == 0 {
				return nil, ErrEmptyName
			}
			if len(b) < n {
				return nil, ErrTruncated
			}
			name = lowerASCII(string(b[:n]))
			b = b[n:]
		default:
			return nil, ErrReserved
		}
		if len(b) < 1 {
			return nil, ErrTruncated
		}
		n := int(b[0])
		b = b[1:]
		if len(b) < n {
			return nil, ErrTruncated
		}
		out = append(out, Field{Name: name, Value: string(b[:n])})
		b = b[n:]
	}
	return out, nil
}
