package ahocorasick

import (
	"fmt"
	"io"
)

func NewTrie(terms []string, options ...Option) ITrie {
	var opt option
	opt.init(terms, options...)
	switch opt.dType {
	case DTypeUint8:
		return newCompactTrie[uint8](terms, &opt)
	case DTypeUint16:
		return newCompactTrie[uint16](terms, &opt)
	case DTypeUint32:
		return newCompactTrie[uint32](terms, &opt)
	case DTypeUint64:
		return newCompactTrie[uint64](terms, &opt)
	default:
		panic("unreachable")
	}
}

func LoadTrieFromFile(filename string) (ITrie, error) {
	return loadFromFile(filename, LoadTrie)
}

func LoadTrie(r io.Reader) (ITrie, error) {
	typ, dSize, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	switch typ {
	case typeCompactTrie:
		return loadCompactTrie(r, dSize)
	case typeAcAutomaton:
		return loadAcAutomaton(r, dSize)
	default:
		return nil, fmt.Errorf("unknown struct type %d", typ)
	}
}

func NewAcAutomaton(terms []string, options ...Option) IAcAutomaton {
	var opt option
	opt.init(terms, options...)
	switch opt.dType {
	case DTypeUint8:
		return newAcAutomaton[uint8](terms, &opt)
	case DTypeUint16:
		return newAcAutomaton[uint16](terms, &opt)
	case DTypeUint32:
		return newAcAutomaton[uint32](terms, &opt)
	case DTypeUint64:
		return newAcAutomaton[uint64](terms, &opt)
	default:
		panic("unreachable")
	}
}

func LoadAcAutomatonFromFile(filename string) (IAcAutomaton, error) {
	return loadFromFile(filename, LoadAcAutomaton)
}

func LoadAcAutomaton(r io.Reader) (IAcAutomaton, error) {
	typ, dSize, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	switch typ {
	case typeCompactTrie:
		return nil, fmt.Errorf("this is a trie instead of an AC automaton, please use LoadTrie")
	case typeAcAutomaton:
		return loadAcAutomaton(r, dSize)
	default:
		return nil, fmt.Errorf("unknown struct type %d", typ)
	}
}
