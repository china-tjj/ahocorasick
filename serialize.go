package ahocorasick

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"
)

const (
	magicNumber = (int32('t') | int32('j')<<8 | int32('j')<<16 | int32('n')<<24) ^ (int32('b') | int32('6')<<8 | int32('6')<<16 | int32('6')<<24)
	version     = uint8(2)

	typeCompactTrie = uint8(iota)
	typeAcAutomaton
)

func saveToFile(filename string, saver func(w io.Writer) error) (err error) {
	var f *os.File
	f, err = os.Create(filename)
	if err != nil {
		return err
	}
	// 不忽略close返回的err，可能表示没有正确写入
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	bw := bufio.NewWriter(f)
	if err = saver(bw); err != nil {
		return err
	}
	return bw.Flush()
}

func loadFromFile[T any](filename string, loader func(r io.Reader) (T, error)) (T, error) {
	f, err := os.Open(filename)
	if err != nil {
		var zero T
		return zero, err
	}
	// 忽略close返回的err
	defer f.Close()
	br := bufio.NewReader(f)
	return loader(br)
}

func writeHeader[U uints, C char](w io.Writer, typ uint8) error {
	if err := write(w, magicNumber); err != nil {
		return err
	}
	if err := write(w, version); err != nil {
		return err
	}
	if err := write(w, typ); err != nil {
		return err
	}
	if err := writeDType[U](w); err != nil {
		return err
	}
	if err := writeCharType[C](w); err != nil {
		return err
	}
	return nil
}

func writeDType[U uints](w io.Writer) error {
	switch unsafe.Sizeof(U(0)) {
	case 1:
		return write(w, uint8(DTypeUint8))
	case 2:
		return write(w, uint8(DTypeUint16))
	case 4:
		return write(w, uint8(DTypeUint32))
	case 8:
		return write(w, uint8(DTypeUint64))
	default:
		panic("unreachable")
	}
}

func writeCharType[C char](w io.Writer) error {
	switch unsafe.Sizeof(C(0)) {
	case 1:
		return write(w, uint8(CharTypeByte))
	case 4:
		return write(w, uint8(CharTypeRune))
	default:
		panic("unreachable")
	}
}

func readHeader(r io.Reader) (uint8, DType, CharType, error) {
	magic, err := read[int32](r)
	if err != nil {
		return 0, 0, 0, err
	}
	if magic != magicNumber {
		return 0, 0, 0, errors.New("magic number mismatch")
	}
	v, err := read[uint8](r)
	if err != nil {
		return 0, 0, 0, err
	}
	if v != version {
		return 0, 0, 0, fmt.Errorf("version mismatch, expected %x, got %x", version, v)
	}
	typ, err := read[uint8](r)
	if err != nil {
		return 0, 0, 0, err
	}
	dType, err := readDType(r)
	if err != nil {
		return 0, 0, 0, err
	}
	charType, err := readCharType(r)
	if err != nil {
		return 0, 0, 0, err
	}
	return typ, dType, charType, nil
}

func readDType(r io.Reader) (DType, error) {
	v, err := read[uint8](r)
	if err != nil {
		return 0, err
	}
	dType := DType(v)
	if dType < DTypeUint8 || dType > DTypeUint64 {
		return 0, fmt.Errorf("unknown data type %d", dType)
	}
	return dType, nil
}

func readCharType(r io.Reader) (CharType, error) {
	v, err := read[uint8](r)
	if err != nil {
		return 0, err
	}
	charType := CharType(v)
	if charType != CharTypeByte && charType != CharTypeRune {
		return 0, fmt.Errorf("unknown char type %d", charType)
	}
	return charType, nil
}
