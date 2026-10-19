package ahocorasick

import (
	"io"
	"sort"
	"unicode/utf8"
)

// 子节点数量 <= 8：线性索引
// 8 < 子节点数量 <= 2048：二分索引
// 2048 < 子节点数量：哈希索引
const (
	linear2binThreshold = 8         // 线性切二分阈值
	bin2hashThreshold   = int(2048) // 二分切哈希阈值（显式 int，避免 uint8 溢出）
)

type compactTrieNode[U uints] struct {
	edgeOffset U // 在 edges 数组中的偏移量
	output     U // 未开启 WithTermIdx 时存 term 长度；开启后存 term 数组中的偏移量
}

// 线性/二分节点，创建边时同时创建节点，保证 nodeIdx = edgeIdx + 1 （因为前面有个根节点），只需存 edge 对应的 c
// 哈希节点，在 edges 数组中占用对应的位置，保证线性/二分节点的公式正确
// 在哈希节点对应的edges切片中，满足 edgeIdx = hash(c) 与 nodeIdx = edgeIdx + 1，hash 采用简单的取模实现
// 对有冲突的边额外用 map 来维护索引，对应的节点编号依次占用剩余的空槽
type compactTrie[U uints, C char] struct {
	// 根节点一定是 0，按 bfs 顺序排列
	nodes []compactTrieNode[U]
	// 压缩存储 edge
	edges         []C           // 边数组，只存边对应的字符，nodeIdx = edgeIdx + 1（哈希溢出边例外）
	overflowNodes []U           // 有溢出边的哈希节点下标数组（有序）
	overflows     []map[C]U     // 哈希节点的溢出边 []map[C]childIdx
	overflowMap   map[U]map[C]U // 哈希节点过多时，用二维map存溢出边 map[nodeIdx]map[C]childIdx
	// 压缩存储 term（开启 WithTermIdx 后才有值）
	// 对于每个节点对应的子数组 nodeTerms：无 term 时 len(nodeTerms) = 0；
	// 否则 nodeTerms[0] 保存公共的 term 字节长度，nodeTerms[1:] 保存原始 term 下标
	terms []U
}

func newCompactTrie(terms []string, params *buildParams) ITrie {
	switch params.dType {
	case DTypeUint8:
		return newCompactTrieD[uint8](terms, params)
	case DTypeUint16:
		return newCompactTrieD[uint16](terms, params)
	case DTypeUint32:
		return newCompactTrieD[uint32](terms, params)
	case DTypeUint64:
		return newCompactTrieD[uint64](terms, params)
	default:
		panic("unreachable")
	}
}

func newCompactTrieD[U uints](terms []string, params *buildParams) ITrie {
	switch params.charType {
	case CharTypeByte:
		return newCompactTrieDC[U, byte](terms, params)
	case CharTypeRune:
		return newCompactTrieDC[U, rune](terms, params)
	default:
		panic("unreachable")
	}
}

func newCompactTrieDC[U uints, C char](terms []string, params *buildParams) *compactTrie[U, C] {
	var ct compactTrie[U, C]
	ct.build(terms, params)
	return &ct
}

func loadCompactTrie(r io.Reader, dType DType, charType CharType) (ITrie, error) {
	switch dType {
	case DTypeUint8:
		return loadCompactTrieD[uint8](r, charType)
	case DTypeUint16:
		return loadCompactTrieD[uint16](r, charType)
	case DTypeUint32:
		return loadCompactTrieD[uint32](r, charType)
	case DTypeUint64:
		return loadCompactTrieD[uint64](r, charType)
	default:
		panic("unreachable")
	}
}

func loadCompactTrieD[U uints](r io.Reader, charType CharType) (ITrie, error) {
	switch charType {
	case CharTypeByte:
		return loadCompactTrieDC[U, byte](r)
	case CharTypeRune:
		return loadCompactTrieDC[U, rune](r)
	default:
		panic("unreachable")
	}
}

func loadCompactTrieDC[U uints, C char](r io.Reader) (*compactTrie[U, C], error) {
	var ct compactTrie[U, C]
	if err := ct.load(r); err != nil {
		return nil, err
	}
	return &ct, nil
}

func (ct *compactTrie[U, C]) build(terms []string, params *buildParams) {
	if params.fastBuild {
		ct.fastBuild(terms, params)
	} else {
		ct.lowAllocBuild(terms, params)
	}

	// 溢出节点过多时，将有序切片转为 map，加速查找
	if len(ct.overflowNodes) > bin2hashThreshold {
		ct.overflowMap = make(map[U]map[C]U, len(ct.overflowNodes))
		for i, p := range ct.overflowNodes {
			ct.overflowMap[p] = ct.overflows[i]
		}
		ct.overflowNodes = nil
		ct.overflows = nil
	}
}

func (ct *compactTrie[U, C]) fastBuild(terms []string, params *buildParams) {
	// 用trie来辅助构建
	var t trie[U, C]
	t.build(terms, params)

	// 统计节点个数
	nodeCnt := U(len(t.nodes))
	var termNodeCnt, hashNodeCnt U
	for p := U(0); p < nodeCnt; p++ {
		if params.withTermIdx && t.nodes[p].firstTerm != ^U(0) {
			termNodeCnt++
		}
		if int(t.getChildCnt(p)) > bin2hashThreshold {
			hashNodeCnt++
		}
	}
	ct.reserve(params, nodeCnt, termNodeCnt, hashNodeCnt)

	// ct.nodes 最终按 bfs 顺序排列，构建完成前，暂用 edgeOffset 保存辅助 Trie 的节点下标，避免额外维护 bfs 队列
	ct.nodes[0].edgeOffset = 0 // 先放入根节点
	for p := U(0); p < nodeCnt; p++ {
		node := &ct.nodes[p]
		trieNodeIdx := node.edgeOffset
		node.edgeOffset = U(len(ct.edges))
		if params.withTermIdx {
			node.output = U(len(ct.terms))
			if firstTerm := t.nodes[trieNodeIdx].firstTerm; firstTerm != ^U(0) {
				ct.terms = append(ct.terms, U(len(terms[firstTerm])))
				ct.terms = t.getTermIndexes(trieNodeIdx, ct.terms)
			}
		} else {
			node.output = t.nodes[trieNodeIdx].firstTerm
		}

		childCnt := t.getChildCnt(trieNodeIdx)
		if childCnt == 0 {
			t.freeNode(trieNodeIdx)
			continue
		}

		if int(childCnt) <= bin2hashThreshold {
			i := U(0)
			t.rangeChildren(trieNodeIdx, func(c C, child U) bool {
				ct.edges = append(ct.edges, c)
				ct.nodes[node.edgeOffset+i+1].edgeOffset = child
				i++
				return true
			})

			// 二分节点还要排序
			if childCnt > linear2binThreshold {
				curEdges := ct.edges[len(ct.edges)-int(childCnt):]
				curChildren := ct.nodes[node.edgeOffset+1 : node.edgeOffset+1+childCnt]
				sortWithSwap(curEdges, func(i, j int) {
					curChildren[i].edgeOffset, curChildren[j].edgeOffset = curChildren[j].edgeOffset, curChildren[i].edgeOffset
				})
			}
			t.freeNode(trieNodeIdx)
			continue
		}

		ct.buildHashNode(p, childCnt, func(yield func(c C, child compactTrieNode[U])) {
			t.rangeChildren(trieNodeIdx, func(c C, child U) bool {
				yield(c, compactTrieNode[U]{edgeOffset: child})
				return true
			})
		})
		t.freeNode(trieNodeIdx)
	}
}

func (ct *compactTrie[U, C]) lowAllocBuild(terms []string, params *buildParams) {
	termIndexes := make([]U, 0, params.termCnt)
	if isByte[C]() {
		for i, term := range terms {
			// 忽略空字符串
			if term == "" {
				continue
			}
			termIndexes = append(termIndexes, U(i))
		}
	} else {
		for i, term := range terms {
			// 忽略空字符串和非法字符串
			if term == "" || !utf8.ValidString(term) {
				continue
			}
			termIndexes = append(termIndexes, U(i))
		}
	}
	if len(termIndexes) == 0 {
		// 放个根节点
		ct.nodes = make([]compactTrieNode[U], 1)
		return
	}

	// 字典序排序，使得有相同前缀的字符串排列在一起
	sort.SliceStable(termIndexes, func(i, j int) bool {
		return terms[termIndexes[i]] < terms[termIndexes[j]]
	})

	// 统计节点个数：相邻 term 的 LCP 为 l 时，term 相对前一个 term 新增 len-l 个节点
	nodeCnt := U(1) // 根节点
	var termNodeCnt U
	prev, prevCharCnt := "", 0
	for _, termIdx := range termIndexes {
		term := terms[termIdx]
		termCharCnt := charLen[C](term)
		lcp := lcpCharLen[C](prev, term)
		nodeCnt += U(termCharCnt - lcp)
		if lcp < termCharCnt || lcp < prevCharCnt {
			termNodeCnt++
		}
		prev, prevCharCnt = term, termCharCnt
	}
	// 无法低成本统计哈希节点个数，先 append 到数组里，最后个数过多再转 map
	ct.reserve(params, nodeCnt, termNodeCnt, 0)

	// ct.nodes 最终按 bfs 顺序排列，构建完成前，暂用 edgeOffset/output 保存对应 term 区间的 left/right，只需额外维护 offset 队列
	var offsets deque[U]
	offsets.Reserve(params.termCnt) // bfs 队列的元素个数不超过 term 个数
	// 先放入根节点
	ct.nodes[0] = compactTrieNode[U]{
		edgeOffset: 0,
		output:     U(len(termIndexes)),
	}
	offsets.PushBack(0)

	// scanNode 扫描共享同一前缀的 term 区间，并按下一字符划分子区间
	// lowAllocBuild 会重复扫描部分区间，以增加少量计算换取更少的中间内存
	scanNode := func(left, right, offset U, visitChild func(c C, childLeft, childRight, childOffset U)) (termCnt, childCnt U) {
		i := left
		// 字典序排序，前面可能有长度不足的 term，挂载到本层节点上
		for i < right && offset == U(len(terms[termIndexes[i]])) {
			termCnt++
			i++
		}
		if i == right {
			return termCnt, childCnt
		}
		curCharBegin := i
		curChar, curCharSize, _ := decodeCharInString[C](terms[termIndexes[i]][offset:])
		i++
		for ; ; i++ {
			var c C
			var size int
			if i < right {
				c, size, _ = decodeCharInString[C](terms[termIndexes[i]][offset:])
				if c == curChar {
					continue
				}
			}
			childCnt++
			if visitChild != nil {
				visitChild(curChar, curCharBegin, i, offset+U(curCharSize))
			}
			if i == right {
				return termCnt, childCnt
			}
			curCharBegin, curChar, curCharSize = i, c, size
		}
	}

	for p := U(0); p < nodeCnt; p++ {
		node := &ct.nodes[p]
		left, right := node.edgeOffset, node.output
		offset := offsets.Front()
		offsets.PopFront()

		termCnt, childCnt := scanNode(left, right, offset, nil)
		node.edgeOffset = U(len(ct.edges))
		if params.withTermIdx {
			node.output = U(len(ct.terms))
			if termCnt > 0 {
				ct.terms = append(ct.terms, offset)
				ct.terms = append(ct.terms, termIndexes[left:left+termCnt]...)
			}
		} else if termCnt > 0 {
			node.output = offset
		} else {
			node.output = 0
		}
		if childCnt == 0 {
			continue
		}

		if int(childCnt) <= bin2hashThreshold {
			childIdx := node.edgeOffset + 1
			scanNode(left, right, offset, func(c C, childLeft, childRight, childOffset U) {
				ct.edges = append(ct.edges, c) // 已经是有序的了，无需再排序
				childNode := &ct.nodes[childIdx]
				childNode.edgeOffset = childLeft
				childNode.output = childRight
				offsets.PushBack(childOffset)
				childIdx++
			})
			continue
		}

		ct.buildHashNode(p, childCnt, func(yield func(c C, child compactTrieNode[U])) {
			scanNode(left, right, offset, func(c C, childLeft, childRight, _ U) {
				yield(c, compactTrieNode[U]{
					edgeOffset: childLeft,
					output:     childRight,
				})
			})
		})
		// 必须按物理槽位顺序入队，使 offset 队列顺序与 bfs 节点编号一致
		for i := U(0); i < childCnt; i++ {
			childLeft := ct.nodes[node.edgeOffset+i+1].edgeOffset
			_, childSize, _ := decodeCharInString[C](terms[termIndexes[childLeft]][offset:])
			offsets.PushBack(offset + U(childSize))
		}
	}
}

func (ct *compactTrie[U, C]) buildHashNode(
	p, childCnt U,
	rangeChildren func(yield func(c C, child compactTrieNode[U])),
) {
	// 填入空边占位符，只有 rune 会走到这里，^rune(0) = -1
	for i := U(0); i < childCnt; i++ {
		ct.edges = append(ct.edges, ^C(0))
	}

	// 将边和子节点写入哈希槽位，并统计冲突数
	node := &ct.nodes[p]
	collisionCnt := 0
	rangeChildren(func(c C, child compactTrieNode[U]) {
		edgeIdx := ct.hash(c, node.edgeOffset, childCnt)
		if ct.edges[edgeIdx] == ^C(0) {
			ct.edges[edgeIdx] = c
			ct.nodes[edgeIdx+1] = child
		} else {
			collisionCnt++
		}
	})

	// 无冲突直接返回
	if collisionCnt == 0 {
		return
	}

	// 将冲突边放到溢出表，对应节点依次放入剩余空槽
	overflow := make(map[C]U, collisionCnt)
	nextEdgeIdx := node.edgeOffset
	rangeChildren(func(c C, child compactTrieNode[U]) {
		edgeIdx := ct.hash(c, node.edgeOffset, childCnt)
		if ct.edges[edgeIdx] == c {
			return
		}
		for ct.edges[nextEdgeIdx] != ^C(0) {
			nextEdgeIdx++
		}
		overflow[c] = nextEdgeIdx + 1
		ct.nodes[nextEdgeIdx+1] = child
		nextEdgeIdx++
	})
	ct.overflowNodes = append(ct.overflowNodes, p)
	ct.overflows = append(ct.overflows, overflow)
}

func (ct *compactTrie[U, C]) reserve(params *buildParams, nodeCnt, termNodeCnt, hashNodeCnt U) {
	ct.nodes = make([]compactTrieNode[U], nodeCnt)
	ct.edges = make([]C, 0, nodeCnt-1)
	if params.withTermIdx {
		ct.terms = make([]U, 0, params.termCnt+int(termNodeCnt))
	}
	if hashNodeCnt > 0 {
		ct.overflowNodes = make([]U, 0, hashNodeCnt)
		ct.overflows = make([]map[C]U, 0, hashNodeCnt)
	}
}

// 获取节点的子节点数
func (ct *compactTrie[U, C]) getChildCnt(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.edges)) - ct.nodes[p].edgeOffset
	}
	return ct.nodes[p+1].edgeOffset - ct.nodes[p].edgeOffset
}

// p 必须是哈希节点
func (ct *compactTrie[U, C]) getHashOverflowMap(p U) map[C]U {
	if len(ct.overflowNodes) > 0 {
		if i, ok := binSearch(ct.overflowNodes, 0, U(len(ct.overflowNodes)), p); ok {
			return ct.overflows[i]
		}
		return nil
	}
	if len(ct.overflowMap) > 0 {
		return ct.overflowMap[p]
	}
	return nil
}

func (ct *compactTrie[U, C]) hash(c C, edgeOffset, childCnt U) U {
	return edgeOffset + U(uint32(c)%uint32(childCnt)) // childCnt 的理论最大值字符集大小，最小 int32 即可保证不溢出（仅考虑合法 rune）
}

func (ct *compactTrie[U, C]) getChild(p U, c C) (U, bool) {
	edgesStart := ct.nodes[p].edgeOffset
	childCnt := ct.getChildCnt(p)
	// 线性查找
	if childCnt <= linear2binThreshold {
		for i := edgesStart; i < edgesStart+childCnt; i++ {
			if ct.edges[i] == c {
				return i + 1, true
			}
		}
		return 0, false
	}
	// 二分查找
	if int(childCnt) <= bin2hashThreshold {
		edgeIdx, ok := binSearch(ct.edges, edgesStart, edgesStart+childCnt, c)
		if !ok {
			return 0, false
		}
		return edgeIdx + 1, true
	}
	// 哈希查找
	edgeIdx := ct.hash(c, edgesStart, childCnt)
	if ct.edges[edgeIdx] == c {
		return edgeIdx + 1, true
	}
	child, ok := ct.getHashOverflowMap(p)[c]
	return child, ok
}

func (ct *compactTrie[U, C]) rangeChildren(p U, yield func(c C, child U)) {
	edgesStart := ct.nodes[p].edgeOffset
	edgesLen := ct.getChildCnt(p)
	edgesEnd := edgesStart + edgesLen
	if int(edgesLen) <= bin2hashThreshold {
		// 线性 / 二分
		for i := edgesStart; i < edgesEnd; i++ {
			yield(ct.edges[i], i+1)
		}
	} else {
		// 哈希
		for i := edgesStart; i < edgesEnd; i++ {
			if ct.edges[i] != ^C(0) {
				yield(ct.edges[i], i+1)
			}
		}
		for c, child := range ct.getHashOverflowMap(p) {
			yield(c, child)
		}
	}
}

func (ct *compactTrie[U, C]) hasTerm(p U) bool {
	if len(ct.terms) > 0 { // withTermIdx
		return ct.getTermDataLen(p) > 0
	}
	return ct.nodes[p].output > 0
}

// 需开启 withTermIdx 时才能调用
func (ct *compactTrie[U, C]) getTermDataLen(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.terms)) - ct.nodes[p].output
	}
	return ct.nodes[p+1].output - ct.nodes[p].output
}

func (ct *compactTrie[U, C]) rangeTerms(p U, query string, endIdx int, yield func(r MatchResult) bool) bool {
	if len(ct.terms) > 0 { // withTermIdx
		termDataLen := ct.getTermDataLen(p)
		if termDataLen == 0 {
			return true
		}
		termOffset := ct.nodes[p].output
		termLen := ct.terms[termOffset]
		for _, termIdx := range ct.terms[termOffset+1 : termOffset+termDataLen] {
			if !yield(makeMatchResultWithTermIdx(query, termLen, termIdx, endIdx)) {
				return false
			}
		}
		return true
	}
	if termLen := ct.nodes[p].output; termLen > 0 {
		return yield(makeMatchResult(query, termLen, endIdx))
	}
	return true
}

func (ct *compactTrie[U, C]) preMatch(query string, yield func(r MatchResult) bool) {
	p := U(0)
	for i := 0; i < len(query); {
		c, size, ok := decodeCharInString[C](query[i:])
		// 遇到非法字符，break
		if !ok {
			break
		}
		i += size
		// 匹配成功，移动到子节点，失配时，break
		var found bool
		p, found = ct.getChild(p, c)
		if !found {
			break
		}
		if !ct.rangeTerms(p, query, i, yield) {
			break
		}
	}
}

func (ct *compactTrie[U, C]) save(w io.Writer) error {
	if err := write(w, U(len(ct.nodes))); err != nil {
		return err
	}
	for _, node := range ct.nodes {
		if err := write(w, node.edgeOffset); err != nil {
			return err
		}
		if err := write(w, node.output); err != nil {
			return err
		}
	}
	if err := writeSlice[U, U](w, ct.overflowNodes); err != nil {
		return err
	}
	for _, overflow := range ct.overflows {
		if err := writeMap[C, U, U](w, overflow); err != nil {
			return err
		}
	}
	if err := write(w, U(len(ct.overflowMap))); err != nil {
		return err
	}
	for nodeIdx, overflow := range ct.overflowMap {
		if err := write(w, nodeIdx); err != nil {
			return err
		}
		if err := writeMap[C, U, U](w, overflow); err != nil {
			return err
		}
	}
	if err := write(w, U(len(ct.edges))); err != nil {
		return err
	}
	if err := writeSliceRaw(w, ct.edges); err != nil {
		return err
	}
	return writeSlice[U, U](w, ct.terms)
}

func (ct *compactTrie[U, C]) load(r io.Reader) error {
	var err error
	var nodeCnt U
	if nodeCnt, err = read[U](r); err != nil {
		return err
	}
	if nodeCnt > 0 {
		ct.nodes = make([]compactTrieNode[U], nodeCnt)
	}
	for i := U(0); i < nodeCnt; i++ {
		if ct.nodes[i].edgeOffset, err = read[U](r); err != nil {
			return err
		}
		if ct.nodes[i].output, err = read[U](r); err != nil {
			return err
		}
	}
	if ct.overflowNodes, err = readSlice[U, U](r); err != nil {
		return err
	}
	if len(ct.overflowNodes) > 0 {
		ct.overflows = make([]map[C]U, len(ct.overflowNodes))
		for i := range ct.overflowNodes {
			if ct.overflows[i], err = readMap[C, U, U](r); err != nil {
				return err
			}
		}
	}
	var overflowMapSize U
	if overflowMapSize, err = read[U](r); err != nil {
		return err
	}
	if overflowMapSize > 0 {
		ct.overflowMap = make(map[U]map[C]U, overflowMapSize)
		for i := U(0); i < overflowMapSize; i++ {
			var nodeIdx U
			if nodeIdx, err = read[U](r); err != nil {
				return err
			}
			var overflow map[C]U
			if overflow, err = readMap[C, U, U](r); err != nil {
				return err
			}
			ct.overflowMap[nodeIdx] = overflow
		}
	}
	var edgeCnt U
	if edgeCnt, err = read[U](r); err != nil {
		return err
	}
	if ct.edges, err = readSliceRaw[C, U](r, edgeCnt); err != nil {
		return err
	}
	ct.terms, err = readSlice[U, U](r)
	return err
}

func (ct *compactTrie[U, C]) PreMatchFirst(query string) (result MatchResult, ok bool) {
	ct.preMatch(query, func(r MatchResult) bool {
		result, ok = r, true
		return false
	})
	return result, ok
}

func (ct *compactTrie[U, C]) PreMatchAll(query string) []MatchResult {
	var results []MatchResult
	ct.preMatch(query, func(r MatchResult) bool {
		results = append(results, r)
		return true
	})
	return results
}

func (ct *compactTrie[U, C]) PreMatchIter(query string) func(yield func(r MatchResult) bool) {
	return func(yield func(r MatchResult) bool) {
		ct.preMatch(query, yield)
	}
}

func (ct *compactTrie[U, C]) Save(w io.Writer) error {
	if err := writeHeader[U, C](w, typeCompactTrie); err != nil {
		return err
	}
	return ct.save(w)
}

func (ct *compactTrie[U, C]) SaveToFile(filename string) error {
	return saveToFile(filename, ct.Save)
}
