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
	binSearchThreshold  = 8         // 二分阈值
	hashSearchThreshold = int(2048) // 哈希阈值
	emptyEdge           = rune(-1)  // 空边占位符
)

type compactTrieNode[U uints] struct {
	edgeOffset U // 在 edges 数组中的偏移量
	output     U // 未开启 WithTermIdx 时存 term 长度；开启后存 term 数组中的偏移量
}

// 线性/二分节点，创建边时同时创建节点，保证 nodeIdx = edgeIdx + 1 （因为前面有个根节点），只需存 edge 对应的 r
// 哈希节点，在 edges 数组中占用对应的位置，保证线性/二分节点的公式正确
// 在哈希节点对应的edges切片中，满足 edgeIdx = hash(r) 与 nodeIdx = edgeIdx + 1，hash 采用简单的取模实现
// 对有冲突的边额外用 map 来维护索引，对应的节点编号依次占用剩余的空槽
type compactTrie[U uints] struct {
	// 根节点一定是 0，按 bfs 顺序排列
	nodes []compactTrieNode[U]
	// 压缩存储 edge
	edges           []rune           // 边数组，只存边对应的字符，nodeIdx = edgeIdx + 1（哈希溢出边例外）
	overflowNodes   []U              // 有溢出边的哈希节点下标数组（有序）
	overflowEdges   []map[rune]U     // 哈希节点的溢出边 []map[rune]childIdx
	overflowEdgeMap map[U]map[rune]U // 哈希节点过多时，用二维map存溢出边 map[nodeIdx]map[rune]childIdx
	// 压缩存储 term（开启 WithTermIdx 后才有值）
	// 对于每个节点对应的子数组 nodeTerms：无 term 时 len(nodeTerms) = 0；
	// 否则 nodeTerms[0] 保存公共的 term 字节长度，nodeTerms[1:] 保存原始 term 下标
	terms []U
}

func newCompactTrie[U uints](terms []string, params *buildParams) *compactTrie[U] {
	var ct compactTrie[U]
	ct.build(terms, params)
	return &ct
}

func (ct *compactTrie[U]) build(terms []string, params *buildParams) {
	if params.fastBuild {
		ct.fastBuild(terms, params)
	} else {
		ct.lowAllocBuild(terms, params)
	}
}

func (ct *compactTrie[U]) fastBuild(terms []string, params *buildParams) {
	// 用trie来辅助构建
	var t trie[U]
	t.build(terms, params)

	nodeCnt := U(len(t.nodes))
	hashNodeCnt := U(0)
	termNodeCnt := 0
	maxChildCnt := U(0)
	for p := U(0); p < nodeCnt; p++ {
		childCnt := t.childCnt(p)
		if int(childCnt) > hashSearchThreshold {
			hashNodeCnt++
		}
		if params.withTermIdx && t.nodes[p].firstTerm != ^U(0) {
			termNodeCnt++
		}
		maxChildCnt = max(maxChildCnt, childCnt)
	}
	ct.nodes = make([]compactTrieNode[U], nodeCnt)
	ct.edges = make([]rune, 0, nodeCnt-1)
	if int(hashNodeCnt) <= hashSearchThreshold {
		ct.overflowNodes = make([]U, 0, hashNodeCnt)
		ct.overflowEdges = make([]map[rune]U, 0, hashNodeCnt)
	} else {
		ct.overflowEdgeMap = make(map[U]map[rune]U, hashNodeCnt)
	}
	if params.withTermIdx {
		ct.terms = make([]U, 0, params.termCnt+termNodeCnt)
	}

	// ct.nodes 最终按 bfs 顺序排列，构建完成前，暂用 edgeOffset 保存辅助 Trie 的节点下标，避免额外维护 bfs 队列
	ct.nodes[0].edgeOffset = 0 // 先放入根节点
	// 复用局部 buffer，减少构建过程中的内存申请
	runes := make([]rune, 0, maxChildCnt)
	children := make([]U, 0, maxChildCnt)
	var hashChildren []U
	var collisions []U
	if hashNodeCnt > 0 {
		hashChildren = make([]U, maxChildCnt)
		collisions = make([]U, 0, maxChildCnt)
	}
	for p := U(0); p < nodeCnt; p++ {
		node := &ct.nodes[p]
		helperNode := node.edgeOffset
		node.edgeOffset = U(len(ct.edges))
		if params.withTermIdx {
			node.output = U(len(ct.terms))
			if firstTerm := t.nodes[helperNode].firstTerm; firstTerm != ^U(0) {
				ct.terms = append(ct.terms, U(len(terms[firstTerm])))
				ct.terms = t.getTermIndexes(helperNode, ct.terms)
			}
		} else {
			node.output = t.nodes[helperNode].firstTerm
		}

		childCnt := t.childCnt(helperNode)
		if childCnt == 0 {
			t.freeNode(helperNode)
			continue
		}
		runes = runes[:0]
		children = children[:0]
		if childCnt <= binSearchThreshold || int(childCnt) > hashSearchThreshold {
			runes, children = t.getEdges(helperNode, runes, children)
		} else {
			runes, children = t.getSortedEdges(helperNode, runes, children) // 二分节点需保证有序
		}
		t.freeNode(helperNode)

		if int(childCnt) <= hashSearchThreshold {
			ct.edges = append(ct.edges, runes...)
			for i, child := range children {
				ct.nodes[node.edgeOffset+U(i)+1].edgeOffset = child
			}
			continue
		}

		for i := U(0); i < childCnt; i++ {
			ct.edges = append(ct.edges, emptyEdge)
		}
		collisions = collisions[:0]
		// 先尝试将每条边放入首选哈希槽位，并记录发生冲突的边
		for i, r := range runes {
			slot := U(uint64(r) % uint64(childCnt))
			if ct.edges[node.edgeOffset+slot] != emptyEdge {
				collisions = append(collisions, U(i))
				continue
			}
			ct.edges[node.edgeOffset+slot] = r
			hashChildren[slot] = children[i]
		}
		if len(collisions) > 0 {
			// 将冲突边依次放入剩余空槽，并记录字符到子节点的映射
			overflow := make(map[rune]U, len(collisions))
			collisionIdx := 0
			for i, r := range ct.edges[node.edgeOffset:] {
				if r != emptyEdge {
					continue
				}
				childIdx := collisions[collisionIdx]
				hashChildren[i] = children[childIdx]
				overflow[runes[childIdx]] = node.edgeOffset + U(i) + 1
				collisionIdx++
			}
			if int(hashNodeCnt) <= hashSearchThreshold {
				ct.overflowNodes = append(ct.overflowNodes, p)
				ct.overflowEdges = append(ct.overflowEdges, overflow)
			} else {
				ct.overflowEdgeMap[p] = overflow
			}
		}
		for i, child := range hashChildren[:childCnt] {
			ct.nodes[node.edgeOffset+U(i)+1].edgeOffset = child
		}
	}
}

func (ct *compactTrie[U]) lowAllocBuild(terms []string, params *buildParams) {
	termIndex := make([]U, 0, params.termCnt)
	for i, term := range terms {
		if term == "" || !utf8.ValidString(term) {
			continue
		}
		termIndex = append(termIndex, U(i))
	}
	if len(termIndex) == 0 {
		// 放个根节点
		ct.nodes = make([]compactTrieNode[U], 1)
		return
	}

	// 字典序排序，使得有相同前缀的字符串排列在一起
	sort.SliceStable(termIndex, func(i, j int) bool {
		return terms[termIndex[i]] < terms[termIndex[j]]
	})
	// scanNode 扫描共享同一前缀的 term 区间，并按下一字符划分子区间
	// lowAllocBuild 会重复扫描部分区间，以增加少量计算换取更少的中间内存
	scanNode := func(left, right, offset U, visitChild func(r rune, childLeft, childRight, childOffset U)) (termCnt, childCnt U) {
		i := left
		// 字典序排序，前面可能有长度不足的 term，挂载到本层节点上
		for i < right && offset == U(len(terms[termIndex[i]])) {
			termCnt++
			i++
		}
		if i == right {
			return termCnt, childCnt
		}
		curRuneBegin := i
		curRune, curRuneSize := utf8.DecodeRuneInString(terms[termIndex[i]][offset:])
		i++
		for ; ; i++ {
			var r rune
			var size int
			if i < right {
				r, size = utf8.DecodeRuneInString(terms[termIndex[i]][offset:])
				if r == curRune {
					continue
				}
			}
			childCnt++
			if visitChild != nil {
				visitChild(curRune, curRuneBegin, i, offset+U(curRuneSize))
			}
			if i == right {
				return termCnt, childCnt
			}
			curRuneBegin, curRune, curRuneSize = i, r, size
		}
	}

	// 统计节点个数
	nodeCnt := U(0)
	hashNodeCnt := U(0)
	termNodeCnt := 0
	maxChildCnt := U(0)
	var dfsCount func(left, right, offset U)
	dfsCount = func(left, right, offset U) {
		nodeCnt++
		termCnt, childCnt := scanNode(left, right, offset, func(_ rune, childLeft, childRight, childOffset U) {
			dfsCount(childLeft, childRight, childOffset)
		})
		if termCnt > 0 {
			termNodeCnt++
		}
		if int(childCnt) > hashSearchThreshold {
			hashNodeCnt++
		}
		maxChildCnt = max(maxChildCnt, childCnt)
	}
	dfsCount(0, U(len(termIndex)), 0)

	ct.nodes = make([]compactTrieNode[U], nodeCnt)
	ct.edges = make([]rune, 0, nodeCnt-1)
	if int(hashNodeCnt) <= hashSearchThreshold {
		ct.overflowNodes = make([]U, 0, hashNodeCnt)
		ct.overflowEdges = make([]map[rune]U, 0, hashNodeCnt)
	} else {
		ct.overflowEdgeMap = make(map[U]map[rune]U, hashNodeCnt)
	}
	if params.withTermIdx {
		ct.terms = make([]U, 0, len(termIndex)+termNodeCnt)
	}

	// ct.nodes 最终按 bfs 顺序排列，构建完成前，暂用 edgeOffset/output 保存对应 term 区间的 left/right，只需额外维护 offset 队列
	var offsets deque[U]
	offsets.Reserve(params.termCnt) // bfs 队列的元素个数不超过 term 个数
	// 先放入根节点
	ct.nodes[0] = compactTrieNode[U]{
		edgeOffset: 0,
		output:     U(len(termIndex)),
	}
	offsets.PushBack(0)

	// 仅哈希节点需要按物理槽位暂存 offset，按需申请
	var slotOffsets []U
	if hashNodeCnt > 0 {
		slotOffsets = make([]U, maxChildCnt)
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
				ct.terms = append(ct.terms, termIndex[left:left+termCnt]...)
			}
		} else if termCnt > 0 {
			node.output = offset
		} else {
			node.output = 0
		}
		if childCnt == 0 {
			continue
		}

		if int(childCnt) <= hashSearchThreshold {
			childIdx := node.edgeOffset + 1
			scanNode(left, right, offset, func(r rune, childLeft, childRight, childOffset U) {
				ct.edges = append(ct.edges, r) // 已经是有序的了，无需再排序
				childNode := &ct.nodes[childIdx]
				childNode.edgeOffset = childLeft
				childNode.output = childRight
				offsets.PushBack(childOffset)
				childIdx++
			})
			continue
		}

		for i := U(0); i < childCnt; i++ {
			ct.edges = append(ct.edges, emptyEdge)
		}
		collisionCnt := 0
		// 尝试将每个子节点直接写入首选哈希槽位，暂不处理冲突项
		scanNode(left, right, offset, func(r rune, childLeft, childRight, childOffset U) {
			slot := U(uint64(r) % uint64(childCnt))
			if ct.edges[node.edgeOffset+slot] != emptyEdge {
				collisionCnt++
				return
			}
			ct.edges[node.edgeOffset+slot] = r
			childNode := &ct.nodes[node.edgeOffset+slot+1]
			childNode.edgeOffset = childLeft
			childNode.output = childRight
			slotOffsets[slot] = childOffset
		})

		if collisionCnt > 0 {
			// 再次扫描冲突项，将其依次放入剩余空槽
			overflow := make(map[rune]U, collisionCnt)
			emptySlot := U(0)
			scanNode(left, right, offset, func(r rune, childLeft, childRight, childOffset U) {
				slot := U(uint64(r) % uint64(childCnt))
				if ct.edges[node.edgeOffset+slot] == r {
					return
				}
				for ct.edges[node.edgeOffset+emptySlot] != emptyEdge {
					emptySlot++
				}
				childNode := &ct.nodes[node.edgeOffset+emptySlot+1]
				childNode.edgeOffset = childLeft
				childNode.output = childRight
				slotOffsets[emptySlot] = childOffset
				overflow[r] = node.edgeOffset + emptySlot + 1
				emptySlot++
			})
			if int(hashNodeCnt) <= hashSearchThreshold {
				ct.overflowNodes = append(ct.overflowNodes, p)
				ct.overflowEdges = append(ct.overflowEdges, overflow)
			} else {
				ct.overflowEdgeMap[p] = overflow
			}
		}
		// 必须按物理槽位顺序入队，使 offset 队列顺序与 bfs 节点编号一致
		for _, childOffset := range slotOffsets[:childCnt] {
			offsets.PushBack(childOffset)
		}
	}
}

func (ct *compactTrie[U]) nodeCnt() U {
	return U(len(ct.nodes))
}

// 获取节点的边数
func (ct *compactTrie[U]) getEdgesLen(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.edges)) - ct.nodes[p].edgeOffset
	}
	return ct.nodes[p+1].edgeOffset - ct.nodes[p].edgeOffset
}

// p 必须是哈希节点
func (ct *compactTrie[U]) getHashOverflowMap(p U) map[rune]U {
	if len(ct.overflowNodes) > 0 {
		if i, ok := binSearch(ct.overflowNodes, 0, U(len(ct.overflowNodes)), p); ok {
			return ct.overflowEdges[i]
		}
		return nil
	}
	if len(ct.overflowEdgeMap) > 0 {
		return ct.overflowEdgeMap[p]
	}
	return nil
}

func (ct *compactTrie[U]) getChild(p U, r rune) (U, bool) {
	edgesStart := ct.nodes[p].edgeOffset
	edgesLen := ct.getEdgesLen(p)
	// 线性查找
	if edgesLen <= binSearchThreshold {
		for i := edgesStart; i < edgesStart+edgesLen; i++ {
			if ct.edges[i] == r {
				return i + 1, true
			}
		}
		return 0, false
	}
	// 二分查找
	if int(edgesLen) <= hashSearchThreshold {
		edgeIdx, ok := binSearch(ct.edges, edgesStart, edgesStart+edgesLen, r)
		if !ok {
			return 0, false
		}
		return edgeIdx + 1, true
	}
	// 哈希查找
	slot := edgesStart + U(uint32(r)%uint32(edgesLen))
	if ct.edges[slot] == r {
		return slot + 1, true
	}
	child, ok := ct.getHashOverflowMap(p)[r]
	return child, ok
}

func (ct *compactTrie[U]) hasTerm(p U) bool {
	if len(ct.terms) > 0 { // withTermIdx
		return ct.getTermDataLen(p) > 0
	}
	return ct.nodes[p].output > 0
}

// 需开启 withTermIdx 时才能调用
func (ct *compactTrie[U]) getTermDataLen(p U) U {
	// 是最后一个节点
	if p == U(len(ct.nodes)-1) {
		return U(len(ct.terms)) - ct.nodes[p].output
	}
	return ct.nodes[p+1].output - ct.nodes[p].output
}

// 需开启 withTermIdx 时才能调用
func (ct *compactTrie[U]) getTerms(p U) (termLen U, termIndexes []U) {
	termDataLen := ct.getTermDataLen(p)
	if termDataLen == 0 {
		return 0, nil
	}
	termOffset := ct.nodes[p].output
	return ct.terms[termOffset], ct.terms[termOffset+1 : termOffset+termDataLen]
}

// 需开启 withTermIdx 时才能调用
func (ct *compactTrie[U]) getFirstTerm(p U) (termLen, termIdx U, ok bool) {
	if ct.getTermDataLen(p) == 0 {
		return 0, 0, false
	}
	termOffset := ct.nodes[p].output
	return ct.terms[termOffset], ct.terms[termOffset+1], true
}

func (ct *compactTrie[U]) rangeChildren(p U, yield func(r rune, child U) bool) {
	edgesStart := ct.nodes[p].edgeOffset
	edgesLen := ct.getEdgesLen(p)
	edgesEnd := edgesStart + edgesLen
	if int(edgesLen) <= hashSearchThreshold {
		// 线性 / 二分
		for i := edgesStart; i < edgesEnd; i++ {
			if !yield(ct.edges[i], i+1) {
				return
			}
		}
	} else {
		// 哈希
		for i := edgesStart; i < edgesEnd; i++ {
			if ct.edges[i] != emptyEdge && !yield(ct.edges[i], i+1) {
				return
			}
		}
		for r, child := range ct.getHashOverflowMap(p) {
			if !yield(r, child) {
				return
			}
		}
	}
}

func (ct *compactTrie[U]) save(w io.Writer) error {
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
	for _, overflowEdge := range ct.overflowEdges {
		if err := writeMap[rune, U, U](w, overflowEdge); err != nil {
			return err
		}
	}
	if err := write(w, U(len(ct.overflowEdgeMap))); err != nil {
		return err
	}
	for nodeIdx, overflowEdge := range ct.overflowEdgeMap {
		if err := write(w, nodeIdx); err != nil {
			return err
		}
		if err := writeMap[rune, U, U](w, overflowEdge); err != nil {
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

func (ct *compactTrie[U]) load(r io.Reader) error {
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
		ct.overflowEdges = make([]map[rune]U, len(ct.overflowNodes))
		for i := range ct.overflowNodes {
			if ct.overflowEdges[i], err = readMap[rune, U, U](r); err != nil {
				return err
			}
		}
	}
	var hashOverflowEdgesCnt U
	if hashOverflowEdgesCnt, err = read[U](r); err != nil {
		return err
	}
	if hashOverflowEdgesCnt > 0 {
		ct.overflowEdgeMap = make(map[U]map[rune]U, hashOverflowEdgesCnt)
		for i := U(0); i < hashOverflowEdgesCnt; i++ {
			var nodeIdx U
			if nodeIdx, err = read[U](r); err != nil {
				return err
			}
			var overflowEdge map[rune]U
			if overflowEdge, err = readMap[rune, U, U](r); err != nil {
				return err
			}
			ct.overflowEdgeMap[nodeIdx] = overflowEdge
		}
	}
	var edgeCnt U
	if edgeCnt, err = read[U](r); err != nil {
		return err
	}
	if ct.edges, err = readSliceRaw[rune, U](r, edgeCnt); err != nil {
		return err
	}
	ct.terms, err = readSlice[U, U](r)
	return err
}

func (ct *compactTrie[U]) PreMatchFirst(query string) (result MatchResult, ok bool) {
	var p U
	for byteIdx := 0; byteIdx < len(query); {
		r, size := utf8.DecodeRuneInString(query[byteIdx:])
		byteIdx += size
		// 遇到非法字符，break
		if r == utf8.RuneError && size == 1 {
			break
		}
		// 匹配成功，移动到子节点，失配时，break
		var found bool
		p, found = ct.getChild(p, r)
		if !found {
			break
		}
		if len(ct.terms) > 0 { // withTermIdx
			if termLen, termIdx, ok := ct.getFirstTerm(p); ok {
				return makeMatchResultWithTermIdx(query, termLen, termIdx, byteIdx), true
			}
		} else if termLen := ct.nodes[p].output; termLen > 0 {
			return makeMatchResult(query, termLen, byteIdx), true
		}
	}
	return MatchResult{}, false
}

func (ct *compactTrie[U]) PreMatchAll(query string) []MatchResult {
	var results []MatchResult
	var p U
	for byteIdx := 0; byteIdx < len(query); {
		r, size := utf8.DecodeRuneInString(query[byteIdx:])
		byteIdx += size
		// 遇到非法字符，break
		if r == utf8.RuneError && size == 1 {
			break
		}
		// 匹配成功，移动到子节点，失配时，break
		var found bool
		p, found = ct.getChild(p, r)
		if !found {
			break
		}
		if len(ct.terms) > 0 { // withTermIdx
			termLen, termIndexes := ct.getTerms(p)
			for _, termIdx := range termIndexes {
				results = append(results, makeMatchResultWithTermIdx(query, termLen, termIdx, byteIdx))
			}
		} else if termLen := ct.nodes[p].output; termLen > 0 {
			results = append(results, makeMatchResult(query, termLen, byteIdx))
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
