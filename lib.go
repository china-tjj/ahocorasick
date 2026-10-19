package ahocorasick

import (
	"fmt"
	"io"
)

func NewTrie(terms []string, options ...Option) ITrie {
	var params buildParams
	params.init(terms, options...)
	return newCompactTrie(terms, &params)
}

func LoadTrieFromFile(filename string) (ITrie, error) {
	return loadFromFile(filename, LoadTrie)
}

func LoadTrie(r io.Reader) (ITrie, error) {
	typ, dType, charType, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	switch typ {
	case typeCompactTrie:
		return loadCompactTrie(r, dType, charType)
	case typeAcAutomaton:
		return loadAcAutomaton(r, dType, charType)
	default:
		return nil, fmt.Errorf("unknown struct type %d", typ)
	}
}

func NewAcAutomaton(terms []string, options ...Option) IAcAutomaton {
	var params buildParams
	params.init(terms, options...)
	return newAcAutomaton(terms, &params)
}

func LoadAcAutomatonFromFile(filename string) (IAcAutomaton, error) {
	return loadFromFile(filename, LoadAcAutomaton)
}

func LoadAcAutomaton(r io.Reader) (IAcAutomaton, error) {
	typ, dType, charType, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	switch typ {
	case typeCompactTrie:
		return nil, fmt.Errorf("this is a trie instead of an AC automaton, use LoadTrie instead")
	case typeAcAutomaton:
		return loadAcAutomaton(r, dType, charType)
	default:
		return nil, fmt.Errorf("unknown struct type %d", typ)
	}
}
