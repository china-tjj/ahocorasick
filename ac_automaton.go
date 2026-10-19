package ahocorasick

import (
	"io"
	"unsafe"
)

type acAutomaton[U uints, C char] struct {
	compactTrie[U, C]
	fail       []U
	outputLink []U
}

func newAcAutomaton(terms []string, params *buildParams) IAcAutomaton {
	switch params.dType {
	case DTypeUint8:
		return newAcAutomatonD[uint8](terms, params)
	case DTypeUint16:
		return newAcAutomatonD[uint16](terms, params)
	case DTypeUint32:
		return newAcAutomatonD[uint32](terms, params)
	case DTypeUint64:
		return newAcAutomatonD[uint64](terms, params)
	default:
		panic("unreachable")
	}
}

func newAcAutomatonD[U uints](terms []string, params *buildParams) IAcAutomaton {
	switch params.charType {
	case CharTypeByte:
		return newAcAutomatonDC[U, byte](terms, params)
	case CharTypeRune:
		return newAcAutomatonDC[U, rune](terms, params)
	default:
		panic("unreachable")
	}
}

func newAcAutomatonDC[U uints, C char](terms []string, params *buildParams) *acAutomaton[U, C] {
	var ac acAutomaton[U, C]
	ac.build(terms, params)
	return &ac
}

func loadAcAutomaton(r io.Reader, dType DType, charType CharType) (IAcAutomaton, error) {
	switch dType {
	case DTypeUint8:
		return loadAcAutomatonD[uint8](r, charType)
	case DTypeUint16:
		return loadAcAutomatonD[uint16](r, charType)
	case DTypeUint32:
		return loadAcAutomatonD[uint32](r, charType)
	case DTypeUint64:
		return loadAcAutomatonD[uint64](r, charType)
	default:
		panic("unreachable")
	}
}

func loadAcAutomatonD[U uints](r io.Reader, charType CharType) (IAcAutomaton, error) {
	switch charType {
	case CharTypeByte:
		return loadAcAutomatonDC[U, byte](r)
	case CharTypeRune:
		return loadAcAutomatonDC[U, rune](r)
	default:
		panic("unreachable")
	}
}

func loadAcAutomatonDC[U uints, C char](r io.Reader) (*acAutomaton[U, C], error) {
	var ac acAutomaton[U, C]
	if err := ac.load(r); err != nil {
		return nil, err
	}
	return &ac, nil
}

func (ac *acAutomaton[U, C]) build(terms []string, params *buildParams) {
	ac.compactTrie.build(terms, params)
	// bfs 构建 fail 和 outputLink
	ac.fail = make([]U, len(ac.nodes))
	ac.fail[0] = ^U(0)
	if params.withOutputLink {
		ac.outputLink = make([]U, len(ac.nodes))
		ac.outputLink[0] = ^U(0)
	} else {
		ac.outputLink = ac.fail
	}
	// ac.nodes是bfs顺序的，直接遍历即可，无需额外维护 bfs 队列
	for i := range ac.nodes {
		ac.rangeChildren(U(i), func(c C, child U) {
			// 计算fail：节点的 fail 指针默认为根，从父节点的 fail 往上找第一个存在的相同 child（字符相同），把这个 child 作为 fail 指针
			var fail U
			for f := ac.fail[i]; f != ^U(0); f = ac.fail[f] {
				if fc, ok := ac.getChild(f, c); ok {
					fail = fc
					break
				}
			}
			ac.fail[child] = fail
			// 计算outputLink
			if params.withOutputLink {
				if ac.hasTerm(fail) {
					ac.outputLink[child] = fail
				} else {
					ac.outputLink[child] = ac.outputLink[fail]
				}
			}
		})
	}
}

func (ac *acAutomaton[U, C]) match(query string, unique bool, yield func(r MatchResult) bool) {
	var visit map[U]struct{}
	if unique {
		visit = make(map[U]struct{})
	}
	p := U(0)
outer:
	for i := 0; i < len(query); {
		c, size, ok := decodeCharInString[C](query[i:])
		i += size
		// 遇到非法字符，回退到 root
		if !ok {
			p = 0
			continue
		}
		// 匹配成功，移动到子节点，失配时，沿fail链回退
		for {
			if child, ok := ac.getChild(p, c); ok {
				p = child
				break
			}
			if p == 0 {
				continue outer
			}
			p = ac.fail[p]
		}
		// 沿输出链回溯
		for q := p; q != ^U(0); q = ac.outputLink[q] {
			if unique {
				if _, ok := visit[q]; ok {
					// 当节点已经访问时，其输出链上的必然也访问过了，直接跳过
					continue outer
				}
				visit[q] = struct{}{}
			}
			if !ac.rangeTerms(q, query, i, yield) {
				return
			}
		}
	}
}

func (ac *acAutomaton[U, C]) save(w io.Writer) error {
	if err := ac.compactTrie.save(w); err != nil {
		return err
	}
	if err := writeSlice[U, U](w, ac.fail); err != nil {
		return err
	}
	if len(ac.fail) == 0 {
		return nil
	}
	if unsafe.SliceData(ac.fail) == unsafe.SliceData(ac.outputLink) {
		if err := write(w, uint8(0)); err != nil {
			return err
		}
		return nil
	}
	if err := write(w, uint8(1)); err != nil {
		return err
	}
	if err := writeSliceRaw(w, ac.outputLink); err != nil {
		return err
	}
	return nil
}

func (ac *acAutomaton[U, C]) load(r io.Reader) error {
	var err error
	if err = ac.compactTrie.load(r); err != nil {
		return err
	}
	if ac.fail, err = readSlice[U, U](r); err != nil {
		return err
	}
	if len(ac.fail) == 0 {
		return nil
	}
	var withOutputLink uint8
	if withOutputLink, err = read[uint8](r); err != nil {
		return err
	}
	if withOutputLink == 0 {
		ac.outputLink = ac.fail
		return nil
	}
	if ac.outputLink, err = readSliceRaw[U, U](r, U(len(ac.fail))); err != nil {
		return err
	}
	return nil
}

func (ac *acAutomaton[U, C]) MatchFirst(query string) (result MatchResult, ok bool) {
	ac.match(query, false, func(r MatchResult) bool {
		result, ok = r, true
		return false
	})
	return result, ok
}

func (ac *acAutomaton[U, C]) MatchAll(query string) []MatchResult {
	var results []MatchResult
	ac.match(query, false, func(r MatchResult) bool {
		results = append(results, r)
		return true
	})
	return results
}

func (ac *acAutomaton[U, C]) MatchAllUnique(query string) []MatchResult {
	var results []MatchResult
	ac.match(query, true, func(r MatchResult) bool {
		results = append(results, r)
		return true
	})
	return results
}

func (ac *acAutomaton[U, C]) MatchIter(query string) func(yield func(r MatchResult) bool) {
	return func(yield func(r MatchResult) bool) {
		ac.match(query, false, yield)
	}
}

func (ac *acAutomaton[U, C]) MatchUniqueIter(query string) func(yield func(r MatchResult) bool) {
	return func(yield func(r MatchResult) bool) {
		ac.match(query, true, yield)
	}
}

func (ac *acAutomaton[U, C]) Save(w io.Writer) error {
	if err := writeHeader[U, C](w, typeAcAutomaton); err != nil {
		return err
	}
	return ac.save(w)
}

func (ac *acAutomaton[U, C]) SaveToFile(filename string) error {
	return saveToFile(filename, ac.Save)
}
