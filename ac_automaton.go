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

func newAcAutomaton[U uints](terms []string, opt *option) *acAutomaton[U] {
	var ac acAutomaton[U]
	ac.build(terms, opt)
	return &ac
}

func (ac *acAutomaton[U]) build(terms []string, opt *option) {
	ac.compactTrie.build(terms, opt)
	// bfs 构建fail和outputLink
	root := ac.root
	ac.fail = make([]U, ac.nodeCnt())
	ac.fail[root] = ^U(0)
	if opt.withOutputLink {
		ac.outputLink = make([]U, ac.nodeCnt())
		ac.outputLink[root] = ^U(0)
	} else {
		ac.outputLink = ac.fail
	}
	var queue deque[U]
	queue.PushBack(root)
	for queue.Len() > 0 {
		p := queue.Front()
		queue.PopFront()
		ac.rangeChildren(p, func(char rune, child U) bool {
			// 计算fail：节点的fail指针默认为根，从父节点的fail往上找第一个存在的相同child（字符相同），把这个child作为fail指针
			fail := root
			for f := ac.fail[p]; f != ^U(0); f = ac.fail[f] {
				if fc, ok := ac.getChild(f, char); ok {
					fail = fc
					break
				}
			}
			ac.fail[child] = fail
			// 计算outputLink
			if opt.withOutputLink {
				if ac.getTermsLen(fail) > 0 {
					ac.outputLink[child] = fail
				} else {
					ac.outputLink[child] = ac.outputLink[fail]
				}
			}
			queue.PushBack(child)
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
	p := ac.root
outer:
	for byteIdx, char := range query {
		// 匹配成功，移动到子节点，失配时，沿fail链回退
		for {
			if child, ok := ac.getChild(p, char); ok {
				p = child
				break
			}
			if p == ac.root {
				continue outer
			}
			p = ac.fail[p]
		}
		// 沿输出链回溯第一个匹配结果
		for node := p; node != ^U(0); node = ac.outputLink[node] {
			if term, ok := ac.getFirstTerm(node); ok {
				endIdx := byteIdx + utf8.RuneLen(char)
				return makeMatchResult(query, term, endIdx), true
			}
		}
	}
	return MatchResult{}, false
}

func (ac *acAutomaton[U]) MatchAll(query string) []MatchResult {
	var result []MatchResult
	p := ac.root
outer:
	for byteIdx, char := range query {
		// 匹配成功，移动到子节点，失配时，沿fail链回退
		for {
			if child, ok := ac.getChild(p, char); ok {
				p = child
				break
			}
			if p == ac.root {
				continue outer
			}
			p = ac.fail[p]
		}
		endIdx := byteIdx + utf8.RuneLen(char)
		// 沿输出链回溯所有匹配结果
		for node := p; node != ^U(0); node = ac.outputLink[node] {
			for _, term := range ac.getTerms(node) {
				result = append(result, makeMatchResult(query, term, endIdx))
			}
		}
	}
	return result
}

func (ac *acAutomaton[U]) MatchAllUnique(query string) []MatchResult {
	var result []MatchResult
	visit := make(map[U]struct{})
	p := ac.root
outer:
	for byteIdx, char := range query {
		// 匹配成功，移动到子节点，失配时，沿fail链回退
		for {
			if child, ok := ac.getChild(p, char); ok {
				p = child
				break
			}
			if p == ac.root {
				continue outer
			}
			p = ac.fail[p]
		}
		endIdx := byteIdx + utf8.RuneLen(char)
		// 沿输出链回溯所有匹配结果
		for node := p; node != ^U(0); node = ac.outputLink[node] {
			if _, ok := visit[node]; ok {
				// 当节点已经访问时，其输出链上的必然也访问过了，直接break
				break
			}
			visit[node] = struct{}{}
			for _, term := range ac.getTerms(node) {
				result = append(result, makeMatchResult(query, term, endIdx))
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
