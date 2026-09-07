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
	version     = uint8(1)

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

func writeHeader[U uints](w io.Writer, typ uint8) error {
	if err := write(w, magicNumber); err != nil {
		return err
	}
	if err := write(w, version); err != nil {
		return err
	}
	if err := write(w, typ); err != nil {
		return err
	}
	if err := write(w, uint8(unsafe.Sizeof(U(0)))); err != nil {
		return err
	}
	return nil
}

func readHeader(r io.Reader) (uint8, uint8, error) {
	magic, err := read[int32](r)
	if err != nil {
		return 0, 0, err
	}
	if magic != magicNumber {
		return 0, 0, errors.New("magic number mismatch")
	}
	v, err := read[uint8](r)
	if err != nil {
		return 0, 0, err
	}
	if v != version {
		return 0, 0, fmt.Errorf("version mismatch, expected %x, got %x", version, v)
	}
	typ, err := read[uint8](r)
	if err != nil {
		return 0, 0, err
	}
	dSize, err := read[uint8](r)
	if err != nil {
		return 0, 0, err
	}
	return typ, dSize, nil
}

func loadCompactTrie(r io.Reader, dSize uint8) (ITrie, error) {
	switch dSize {
	case 1:
		var ct compactTrie[uint8]
		if err := ct.load(r); err != nil {
			return nil, err
		}
		return &ct, nil
	case 2:
		var ct compactTrie[uint16]
		if err := ct.load(r); err != nil {
			return nil, err
		}
		return &ct, nil
	case 4:
		var ct compactTrie[uint32]
		if err := ct.load(r); err != nil {
			return nil, err
		}
		return &ct, nil
	case 8:
		var ct compactTrie[uint64]
		if err := ct.load(r); err != nil {
			return nil, err
		}
		return &ct, nil
	default:
		return nil, fmt.Errorf("unknown data size %d", dSize)
	}
}

func loadAcAutomaton(r io.Reader, dSize uint8) (IAcAutomaton, error) {
	switch dSize {
	case 1:
		var ac acAutomaton[uint8]
		if err := ac.load(r); err != nil {
			return nil, err
		}
		return &ac, nil
	case 2:
		var ac acAutomaton[uint16]
		if err := ac.load(r); err != nil {
			return nil, err
		}
		return &ac, nil
	case 4:
		var ac acAutomaton[uint32]
		if err := ac.load(r); err != nil {
			return nil, err
		}
		return &ac, nil
	case 8:
		var ac acAutomaton[uint64]
		if err := ac.load(r); err != nil {
			return nil, err
		}
		return &ac, nil
	default:
		return nil, fmt.Errorf("unknown data size %d", dSize)
	}
}
