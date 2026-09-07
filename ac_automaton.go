package ahocorasick

import (
	"io"
	"unicode/utf8"
	"unsafe"
)

type acAutomaton[U uints] struct {
	compactTrie[U]
	fail       []U
	outputLink []U
}

func newAcAutomaton[U uints](terms []string, params *buildParams) *acAutomaton[U] {
	var ac acAutomaton[U]
	ac.build(terms, params)
	return &ac
}

func (ac *acAutomaton[U]) build(terms []string, params *buildParams) {
	ac.compactTrie.build(terms, params)
	// bfs 构建 fail 和 outputLink
	ac.fail = make([]U, ac.nodeCnt())
	ac.fail[0] = ^U(0)
	if params.withOutputLink {
		ac.outputLink = make([]U, ac.nodeCnt())
		ac.outputLink[0] = ^U(0)
	} else {
		ac.outputLink = ac.fail
	}
	// ac.nodes是bfs顺序的，直接遍历即可，无需额外维护 bfs 队列
	for i := range ac.nodes {
		ac.rangeChildren(U(i), func(r rune, child U) bool {
			// 计算fail：节点的 fail 指针默认为根，从父节点的 fail 往上找第一个存在的相同 child（字符相同），把这个 child 作为 fail 指针
			var fail U
			for f := ac.fail[i]; f != ^U(0); f = ac.fail[f] {
				if fc, ok := ac.getChild(f, r); ok {
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
			return true
		})
	}
}

func (ac *acAutomaton[U]) save(w io.Writer) error {
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

func (ac *acAutomaton[U]) load(r io.Reader) error {
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

func (ac *acAutomaton[U]) MatchFirst(query string) (result MatchResult, ok bool) {
	p := U(0)
outer:
	for byteIdx := 0; byteIdx < len(query); {
		r, size := utf8.DecodeRuneInString(query[byteIdx:])
		byteIdx += size
		// 遇到非法字符，回退到root
		if r == utf8.RuneError && size == 1 {
			p = 0
			continue
		}
		// 匹配成功，移动到子节点，失配时，沿fail链回退
		for {
			if child, ok := ac.getChild(p, r); ok {
				p = child
				break
			}
			if p == 0 {
				continue outer
			}
			p = ac.fail[p]
		}
		// 沿输出链回溯第一个匹配结果
		if len(ac.terms) > 0 { // withTermIdx
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				if termLen, termIdx, ok := ac.getFirstTerm(i); ok {
					return makeMatchResultWithTermIdx(query, termLen, termIdx, byteIdx), true
				}
			}
		} else {
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				if termLen := ac.nodes[i].output; termLen > 0 {
					return makeMatchResult(query, termLen, byteIdx), true
				}
			}
		}
	}
	return MatchResult{}, false
}

func (ac *acAutomaton[U]) MatchAll(query string) []MatchResult {
	var result []MatchResult
	p := U(0)
outer:
	for byteIdx := 0; byteIdx < len(query); {
		r, size := utf8.DecodeRuneInString(query[byteIdx:])
		byteIdx += size
		// 遇到非法字符，回退到 root
		if r == utf8.RuneError && size == 1 {
			p = 0
			continue
		}
		// 匹配成功，移动到子节点，失配时，沿 fail 链回退
		for {
			if child, ok := ac.getChild(p, r); ok {
				p = child
				break
			}
			if p == 0 {
				continue outer
			}
			p = ac.fail[p]
		}
		// 沿输出链回溯所有匹配结果
		if len(ac.terms) > 0 { // withTermIdx
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				termLen, termIndexes := ac.getTerms(i)
				for _, termIdx := range termIndexes {
					result = append(result, makeMatchResultWithTermIdx(query, termLen, termIdx, byteIdx))
				}
			}
		} else {
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				if termLen := ac.nodes[i].output; termLen > 0 {
					result = append(result, makeMatchResult(query, termLen, byteIdx))
				}
			}
		}
	}
	return result
}

func (ac *acAutomaton[U]) MatchAllUnique(query string) []MatchResult {
	var result []MatchResult
	visit := make(map[U]struct{})
	p := U(0)
outer:
	for byteIdx := 0; byteIdx < len(query); {
		r, size := utf8.DecodeRuneInString(query[byteIdx:])
		byteIdx += size
		// 遇到非法字符，回退到root
		if r == utf8.RuneError && size == 1 {
			p = 0
			continue
		}
		// 匹配成功，移动到子节点，失配时，沿 fail 链回退
		for {
			if child, ok := ac.getChild(p, r); ok {
				p = child
				break
			}
			if p == 0 {
				continue outer
			}
			p = ac.fail[p]
		}
		// 沿输出链回溯所有匹配结果
		if len(ac.terms) > 0 { // withTermIdx
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				if _, ok := visit[i]; ok {
					// 当节点已经访问时，其输出链上的必然也访问过了，直接break
					break
				}
				visit[i] = struct{}{}
				termLen, termIndexes := ac.getTerms(i)
				for _, termIdx := range termIndexes {
					result = append(result, makeMatchResultWithTermIdx(query, termLen, termIdx, byteIdx))
				}
			}
		} else {
			for i := p; i != ^U(0); i = ac.outputLink[i] {
				if _, ok := visit[i]; ok {
					// 当节点已经访问时，其输出链上的必然也访问过了，直接break
					break
				}
				visit[i] = struct{}{}
				if termLen := ac.nodes[i].output; termLen > 0 {
					result = append(result, makeMatchResult(query, termLen, byteIdx))
				}
			}
		}
	}
	return result
}

func (ac *acAutomaton[U]) Save(w io.Writer) error {
	if err := writeHeader[U](w, typeAcAutomaton); err != nil {
		return err
	}
	return ac.save(w)
}

func (ac *acAutomaton[U]) SaveToFile(filename string) error {
	return saveToFile(filename, ac.Save)
}
