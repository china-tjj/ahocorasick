package ahocorasick

import (
	"encoding/binary"
	"io"
	"unsafe"
)

//go:nosplit
func noEscapePtr[T any](p *T) *T {
	x := uintptr(unsafe.Pointer(p))
	return (*T)(unsafe.Pointer(x ^ 0))
}

//go:nosplit
func noEscapeSlice[T any](s []T) []T {
	return unsafe.Slice(noEscapePtr(unsafe.SliceData(s)), len(s))
}

func write[T ints](w io.Writer, v T) error {
	switch unsafe.Sizeof(v) {
	case 1:
		var data [1]byte
		data[0] = byte(v)
		_, err := w.Write(noEscapeSlice(data[:]))
		return err
	case 2:
		var data [2]byte
		binary.LittleEndian.PutUint16(data[:], uint16(v))
		_, err := w.Write(noEscapeSlice(data[:]))
		return err
	case 4:
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], uint32(v))
		_, err := w.Write(noEscapeSlice(data[:]))
		return err
	case 8:
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], uint64(v))
		_, err := w.Write(noEscapeSlice(data[:]))
		return err
	default:
		panic("unreachable")
	}
}

func read[T ints](r io.Reader) (v T, err error) {
	switch unsafe.Sizeof(v) {
	case 1:
		var data [1]byte
		_, err = io.ReadFull(r, noEscapeSlice(data[:]))
		if err != nil {
			return v, err
		}
		return T(data[0]), nil
	case 2:
		var data [2]byte
		_, err = io.ReadFull(r, noEscapeSlice(data[:]))
		if err != nil {
			return v, err
		}
		return T(binary.LittleEndian.Uint16(data[:])), nil
	case 4:
		var data [4]byte
		_, err = io.ReadFull(r, noEscapeSlice(data[:]))
		if err != nil {
			return v, err
		}
		return T(binary.LittleEndian.Uint32(data[:])), nil
	case 8:
		var data [8]byte
		_, err = io.ReadFull(r, noEscapeSlice(data[:]))
		if err != nil {
			return v, err
		}
		return T(binary.LittleEndian.Uint64(data[:])), nil
	default:
		panic("unreachable")
	}
}

func writeSlice[T, L ints](w io.Writer, s []T) error {
	length := L(len(s))
	if err := write(w, length); err != nil {
		return err
	}
	return writeSliceRaw(w, s)
}

func writeSliceRaw[T ints](w io.Writer, s []T) error {
	for _, t := range s {
		if err := write(w, t); err != nil {
			return err
		}
	}
	return nil
}

func readSlice[T, L ints](r io.Reader) ([]T, error) {
	length, err := read[L](r)
	if err != nil {
		return nil, err
	}
	return readSliceRaw[T](r, length)
}

func readSliceRaw[T, L ints](r io.Reader, length L) ([]T, error) {
	if length == 0 {
		return nil, nil
	}
	s := make([]T, length)
	for i := range s {
		var err error
		s[i], err = read[T](r)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func writeMap[K, V, L ints](w io.Writer, m map[K]V) error {
	length := L(len(m))
	if err := write(w, length); err != nil {
		return err
	}
	return writeMapRaw(w, m)
}

func writeMapRaw[K, V ints](w io.Writer, m map[K]V) error {
	for k, v := range m {
		if err := write(w, k); err != nil {
			return err
		}
		if err := write(w, v); err != nil {
			return err
		}
	}
	return nil
}

func readMap[K, V, L ints](r io.Reader) (map[K]V, error) {
	length, err := read[L](r)
	if err != nil {
		return nil, err
	}
	return readMapRaw[K, V, L](r, length)
}

func readMapRaw[K, V, L ints](r io.Reader, length L) (map[K]V, error) {
	if length == 0 {
		return nil, nil
	}
	m := make(map[K]V, length)
	for i := L(0); i < length; i++ {
		k, err := read[K](r)
		if err != nil {
			return nil, err
		}
		v, err := read[V](r)
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}
