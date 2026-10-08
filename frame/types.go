package frame

const (
	TypeRequest  Type = 0x01
	TypeResponse Type = 0x02
	TypeData     Type = 0x03
	TypeError    Type = 0x04
)

const FlagEndStream Flags = 0x01

const MethodGET uint8 = 0x01

type ErrorCode uint16

const (
	ErrCodeProtocol  ErrorCode = 1
	ErrCodeFrameSize ErrorCode = 2
	ErrCodeTimeout   ErrorCode = 3
	ErrCodeInternal  ErrorCode = 4
)

func (t Type) String() string {
	switch t {
	case TypeRequest:
		return "REQUEST"
	case TypeResponse:
		return "RESPONSE"
	case TypeData:
		return "DATA"
	case TypeError:
		return "ERROR"
	}
	return "UNKNOWN"
}

func (c ErrorCode) String() string {
	switch c {
	case ErrCodeProtocol:
		return "PROTOCOL"
	case ErrCodeFrameSize:
		return "FRAME_SIZE"
	case ErrCodeTimeout:
		return "TIMEOUT"
	case ErrCodeInternal:
		return "INTERNAL"
	}
	return "PROTOCOL" // unknown codes are treated as PROTOCOL (spec 4)
}
