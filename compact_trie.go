package ahocorasick

import (
	"io"
	"unicode/utf8"
)

// 子节点数量 <= 8：线性索引
// 8 < 子节点数量 <= 2048：二分索引
// 2048 < 子节点数量：哈希索引
const (
	binSearchThreshold  = 8            // 二分阈值
	hashSearchThreshold = uint64(2048) // 哈希阈值
)

type compactTrie[U uints] struct {
	// 根节点不一定是0，不保证子节点在父节点后面
	root  U
	nodes []trieNode[U]
	// 节点的边和term一起压缩存储，指针更少，内存更小
	childrenMaps []map[rune]U // 哈希节点的子节点map
	edgesChar    []rune       // 线性节点/二分节点的子节点数组
	edgesChild   []U          // 线性节点/二分节点的子节点数组
	terms        []termMetadata[U]
}

func newCompactTrie[U uints](terms []string, opt *option) *compactTrie[U] {
	var ct compactTrie[U]
	ct.build(terms, opt)
	return &ct
}

func (ct *compactTrie[U]) build(terms []string, opt *option) {
	var t trie[U]
	t.build(terms, opt)
	nodeCnt := U(len(t.nodes))
	// 对nodes进行排序，使得哈希节点在左边，其余节点在右边
	// 哈希节点少，用map更省内存
	ori2cur := make(map[U]U) // 原始索引 -> 当前索引
	cur2ori := make(map[U]U) // 当前索引 -> 原始索引
	getCurPos := func(p U) U {
		if p2, ok := ori2cur[p]; ok {
			return p2
		}
		return p
	}
	getOriPos := func(p U) U {
		if p2, ok := cur2ori[p]; ok {
			return p2
		}
		return p
	}
	nextHash := U(0)
	for i := U(0); i < nodeCnt; i++ {
		if uint64(t.childrenLen(i)) <= hashSearchThreshold {
			continue
		}
		if nextHash == i {
			nextHash++
			continue
		}
		// 换位
		oriNextHash, oriI := getOriPos(nextHash), getOriPos(i)
		cur2ori[nextHash], cur2ori[i] = oriI, oriNextHash
		ori2cur[oriNextHash], ori2cur[oriI] = i, nextHash
		t.swapNodes(nextHash, i)
		nextHash++
	}
	if len(ori2cur) > 0 {
		// 如果发生了换位，修正root和children
		ct.root = getCurPos(ct.root)
		for i := U(0); i < nodeCnt; i++ {
			t.mapChildren(i, ori2cur)
		}
	}
	// 压缩存储
	ct.nodes = make([]trieNode[U], nodeCnt)
	mergeEdgesNum := U(0)
	mergeTermsNum := len(t.terms)
	for i := U(0); i < nodeCnt; i++ {
		childrenLen := t.childrenLen(i)
		if uint64(childrenLen) <= hashSearchThreshold {
			mergeEdgesNum += childrenLen
		}
	}
	if nextHash > 0 {
		ct.childrenMaps = make([]map[rune]U, nextHash)
	}
	if mergeEdgesNum > 0 {
		ct.edgesChar = make([]rune, 0, mergeEdgesNum)
		ct.edgesChild = make([]U, 0, mergeEdgesNum)
	}
	if mergeTermsNum > 0 {
		ct.terms = make([]termMetadata[U], 0, mergeTermsNum)
	}
	for i := U(0); i < nodeCnt; i++ {
		node := &ct.nodes[i]
		childrenLen := t.childrenLen(i)
		// 压缩存储边
		if childrenLen <= binSearchThreshold {
			// 线性查找
			node.firstEdge = U(len(ct.edgesChar))
			ct.edgesChar, ct.edgesChild = t.getEdges(i, ct.edgesChar, ct.edgesChild)
		} else if uint64(childrenLen) <= hashSearchThreshold {
			// 二分查找
			node.firstEdge = U(len(ct.edgesChar))
			ct.edgesChar, ct.edgesChild = t.getSortedEdges(i, ct.edgesChar, ct.edgesChild)
		} else {
			// 哈希查找，哈希节点一定在最前面，直接赋值
			// HashSearchThreshold > HashIndexThreshold，此时一定已经切为了哈希索引，直接拿即可
			ct.childrenMaps[i] = t.childrenMap(i)
		}
		// 压缩存储term
		node.firstTerm = U(len(ct.terms))
		ct.terms = t.getTerms(i, ct.terms)
		t.freeNode(i)
	}
}

func (ct *compactTrie[U]) nodeCnt() U {
	return U(len(ct.nodes))
}

// 获取线性节点/二分节点的边数
func (ct *compactTrie[U]) getNonHashEdgesLen(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.edgesChar)) - ct.nodes[p].firstEdge
	}
	// 前面保证了哈希节点在前，下一个节点肯定也是线性节点/二分节点
	return ct.nodes[p+1].firstEdge - ct.nodes[p].firstEdge
}

func (ct *compactTrie[U]) getTermsLen(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.terms)) - ct.nodes[p].firstTerm
	}
	return ct.nodes[p+1].firstTerm - ct.nodes[p].firstTerm
}

func (ct *compactTrie[U]) getChild(p U, char rune) (U, bool) {
	if p < U(len(ct.childrenMaps)) {
		// 哈希查找
		child, ok := ct.childrenMaps[p][char]
		return child, ok
	}
	edgesLen := ct.getNonHashEdgesLen(p)
	// 线性查找
	if edgesLen <= binSearchThreshold {
		edgesStart := ct.nodes[p].firstEdge
		edgesEnd := edgesStart + edgesLen
		for i := edgesStart; i < edgesEnd; i++ {
			if ct.edgesChar[i] == char {
				return ct.edgesChild[i], true
			}
		}
		return 0, false
	}
	// 二分查找
	i := ct.nodes[p].firstEdge
	j := i + edgesLen
	for i < j {
		m := mid(i, j)
		if ct.edgesChar[m] == char {
			return ct.edgesChild[m], true
		} else if ct.edgesChar[m] < char {
			i = m + 1
		} else {
			j = m
		}
	}
	return 0, false
}

func (ct *compactTrie[U]) getTerms(p U) []termMetadata[U] {
	termsStart := ct.nodes[p].firstTerm
	termsEnd := termsStart + ct.getTermsLen(p)
	return ct.terms[termsStart:termsEnd]
}

func (ct *compactTrie[U]) getFirstTerm(p U) (meta termMetadata[U], ok bool) {
	if ct.getTermsLen(p) == 0 {
		return meta, false
	}
	return ct.terms[ct.nodes[p].firstTerm], true
}

func (ct *compactTrie[U]) rangeChildren(p U, yield func(char rune, child U) bool) {
	if p < U(len(ct.childrenMaps)) {
		// 哈希索引
		for char, child := range ct.childrenMaps[p] {
			if !yield(char, child) {
				return
			}
		}
	} else {
		// 线性/二分索引
		edgesStart := ct.nodes[p].firstEdge
		edgesEnd := edgesStart + ct.getNonHashEdgesLen(p)
		for i := edgesStart; i < edgesEnd; i++ {
			if !yield(ct.edgesChar[i], ct.edgesChild[i]) {
				return
			}
		}
	}
}

func (ct *compactTrie[U]) save(w io.Writer) error {
	if err := write(w, ct.root); err != nil {
		return err
	}
	if err := write(w, U(len(ct.nodes))); err != nil {
		return err
	}
	for _, node := range ct.nodes {
		if err := write(w, node.firstEdge); err != nil {
			return err
		}
		if err := write(w, node.firstTerm); err != nil {
			return err
		}
	}
	if err := write(w, U(len(ct.childrenMaps))); err != nil {
		return err
	}
	for _, childrenMap := range ct.childrenMaps {
		if err := writeMap[rune, U, U](w, childrenMap); err != nil {
			return err
		}
	}
	if err := write(w, U(len(ct.edgesChar))); err != nil {
		return err
	}
	if err := writeSliceRaw(w, ct.edgesChar); err != nil {
		return err
	}
	if err := writeSliceRaw(w, ct.edgesChild); err != nil {
		return err
	}
	if err := write(w, U(len(ct.terms))); err != nil {
		return err
	}
	for _, term := range ct.terms {
		if err := write(w, term.idx); err != nil {
			return err
		}
		if err := write(w, term.len); err != nil {
			return err
		}
	}
	return nil
}

func (ct *compactTrie[U]) load(r io.Reader) error {
	var err error
	if ct.root, err = read[U](r); err != nil {
		return err
	}
	var nodeCnt U
	if nodeCnt, err = read[U](r); err != nil {
		return err
	}
	if nodeCnt > 0 {
		ct.nodes = make([]trieNode[U], nodeCnt)
	}
	for i := U(0); i < nodeCnt; i++ {
		if ct.nodes[i].firstEdge, err = read[U](r); err != nil {
			return err
		}
		if ct.nodes[i].firstTerm, err = read[U](r); err != nil {
			return err
		}
	}
	var childrenMapCnt U
	if childrenMapCnt, err = read[U](r); err != nil {
		return err
	}
	if childrenMapCnt > 0 {
		ct.childrenMaps = make([]map[rune]U, childrenMapCnt)
	}
	for i := U(0); i < childrenMapCnt; i++ {
		if ct.childrenMaps[i], err = readMap[rune, U, U](r); err != nil {
			return err
		}
	}
	var edgeCnt U
	if edgeCnt, err = read[U](r); err != nil {
		return err
	}
	if ct.edgesChar, err = readSliceRaw[rune, U](r, edgeCnt); err != nil {
		return err
	}
	if ct.edgesChild, err = readSliceRaw[U, U](r, edgeCnt); err != nil {
		return err
	}
	var termCnt U
	if termCnt, err = read[U](r); err != nil {
		return err
	}
	if termCnt > 0 {
		ct.terms = make([]termMetadata[U], termCnt)
	}
	for i := U(0); i < termCnt; i++ {
		if ct.terms[i].idx, err = read[U](r); err != nil {
			return err
		}
		if ct.terms[i].len, err = read[U](r); err != nil {
			return err
		}
	}
	return nil
}

func (ct *compactTrie[U]) PreMatchFirst(query string) (result MatchResult, ok bool) {
	p := ct.root
	for byteIdx, char := range query {
		// 匹配成功，移动到子节点，失配时，break
		var found bool
		p, found = ct.getChild(p, char)
		if !found {
			break
		}
		if term, ok := ct.getFirstTerm(p); ok {
			endIdx := byteIdx + utf8.RuneLen(char)
			return makeMatchResult(query, term, endIdx), true
		}
	}
	return MatchResult{}, false
}

func (ct *compactTrie[U]) PreMatchAll(query string) []MatchResult {
	var results []MatchResult
	p := ct.root
	for byteIdx, char := range query {
		// 匹配成功，移动到子节点，失配时，break
		var found bool
		p, found = ct.getChild(p, char)
		if !found {
			break
		}
		endIdx := byteIdx + utf8.RuneLen(char)
		for _, term := range ct.getTerms(p) {
			results = append(results, makeMatchResult(query, term, endIdx))
		}
	}
	return results
}

func (ct *compactTrie[U]) Save(w io.Writer) error {
	if err := writeHeader[U](w, typeCompactTrie); err != nil {
		return err
	}
	return ct.save(w)
}

func (ct *compactTrie[U]) SaveToFile(filename string) error {
	return saveToFile(filename, ct.Save)
}
