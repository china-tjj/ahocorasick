package ahocorasick

type DType int

const (
	DTypeAuto DType = iota
	DTypeUint8
	DTypeUint16
	DTypeUint32
	DTypeUint64
)

func (t DType) String() string {
	switch t {
	case DTypeAuto:
		return "Auto"
	case DTypeUint8:
		return "Uint8"
	case DTypeUint16:
		return "Uint16"
	case DTypeUint32:
		return "Uint32"
	case DTypeUint64:
		return "Uint64"
	default:
		return "Unknown"
	}
}
