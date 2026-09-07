package ahocorasick

import "unicode/utf8"

const hashIndexThreshold = 16

type trieNode[U uints] struct {
	firstEdge U // 链表存储时为链表头下标；map 存储时为 trie.childrenMaps 的下标
	firstTerm U // 未开启 WithTermIdx 时存 term 长度；开启后 trie.nextTermIdx 的下标
}

// 仅用于辅助构建 compactTrie，未实现 ITrie
type trie[U uints] struct {
	// 根节点是 0，子节点一定在父节点后面
	nodes []trieNode[U]
	// 边以链表储存，使用三个数组而不是一个结构体，这样内存对齐没有 padding
	edgesRune  []rune
	edgesChild []U
	edgesNext  []U
	// 边数 > hashIndexThreshold 后以 map 存储
	childrenMaps []map[rune]U
	// 切换到 map 的标记
	mapFlags bitmap
	// 切换到 map 后释放链表，记录一下空位方便复用
	freeEdges deque[U]
	// 与建树 terms 一一对应，以链表维护节点挂载的 terms
	nextTermIdx []U
}

func (t *trie[U]) build(terms []string, params *buildParams) {
	initCap := max(1, 1+params.runeCnt)
	t.nodes = make([]trieNode[U], 0, initCap)
	// 边数 = 节点数 - 1
	t.edgesRune = make([]rune, 0, initCap-1)
	t.edgesChild = make([]U, 0, initCap-1)
	t.edgesNext = make([]U, 0, initCap-1)
	if params.withTermIdx {
		// 和建树 terms 一一对应
		t.nextTermIdx = make([]U, len(terms))
	}
	t.mapFlags = bitmap{}
	t.mapFlags.Reserve(initCap)
	// 先放 root
	t.newNode()
	// 建树
	for i, term := range terms {
		// 忽略空字符串和非法字符串
		if term == "" || !utf8.ValidString(term) {
			continue
		}
		p := U(0)
		for _, r := range term {
			child, ok := t.getChild(p, r)
			if !ok {
				child = t.newNode()
				t.setChild(p, r, child)
			}
			p = child
		}
		t.addTerm(p, U(i), U(len(term)))
	}
	t.freeEdges = deque[U]{} // 后面不会使用了，可以提前释放
}

func (t *trie[U]) newNode() U {
	idx := U(len(t.nodes))
	if t.nextTermIdx != nil { // withTermIdx
		t.nodes = append(t.nodes, trieNode[U]{
			firstEdge: ^U(0),
			firstTerm: ^U(0),
		})
	} else {
		t.nodes = append(t.nodes, trieNode[U]{
			firstEdge: ^U(0),
		})
	}
	return idx
}

// childrenMap 返回节点的哈希索引（已切换到 map 时返回，否则 nil）
func (t *trie[U]) childrenMap(p U) map[rune]U {
	if !t.mapFlags.Get(int(p)) {
		return nil
	}
	return t.childrenMaps[t.nodes[p].firstEdge]
}

func (t *trie[U]) getChild(p U, r rune) (U, bool) {
	if m := t.childrenMap(p); m != nil {
		child, ok := m[r]
		return child, ok
	}
	for i := t.nodes[p].firstEdge; i != ^U(0); i = t.edgesNext[i] {
		if t.edgesRune[i] == r {
			return t.edgesChild[i], true
		}
	}
	return 0, false
}

func (t *trie[U]) setChild(p U, r rune, child U) {
	node := &t.nodes[p]

	// 1) 已经切换到 map：只写 map
	if m := t.childrenMap(p); m != nil {
		m[r] = child
		return
	}

	// 2) 链表模式 & 未跨阈值：头插到链表（优先复用回收槽位）
	cnt := t.childCnt(p)
	if cnt < hashIndexThreshold {
		var idx U
		if t.freeEdges.Len() > 0 {
			idx = t.freeEdges.Front()
			t.freeEdges.PopFront()
			t.edgesRune[idx] = r
			t.edgesChild[idx] = child
			t.edgesNext[idx] = node.firstEdge
		} else {
			idx = U(len(t.edgesRune))
			t.edgesRune = append(t.edgesRune, r)
			t.edgesChild = append(t.edgesChild, child)
			t.edgesNext = append(t.edgesNext, node.firstEdge)
		}
		node.firstEdge = idx
		return
	}

	// 3) 跨阈值：把现有链表整体迁移到 map，槽位回收到 freeEdges，新边只入 map
	m := make(map[rune]U, cnt+1)
	for i := node.firstEdge; i != ^U(0); i = t.edgesNext[i] {
		m[t.edgesRune[i]] = t.edgesChild[i]
		t.freeEdges.PushBack(i)
	}
	m[r] = child
	t.mapFlags.Set(int(p), true)
	node.firstEdge = U(len(t.childrenMaps))
	t.childrenMaps = append(t.childrenMaps, m)
}

func (t *trie[U]) addTerm(p, i, termLen U) {
	node := &t.nodes[p]
	if t.nextTermIdx != nil { // withTermIdx
		t.nextTermIdx[i] = node.firstTerm
		node.firstTerm = i
	} else {
		node.firstTerm = termLen
	}
}

// 获取节点的子节点数量
func (t *trie[U]) childCnt(p U) U {
	if m := t.childrenMap(p); m != nil {
		return U(len(m))
	}
	node := &t.nodes[p]
	cnt := U(0)
	for i := node.firstEdge; i != ^U(0); i = t.edgesNext[i] {
		cnt++
	}
	return cnt
}

// 返回节点的边数组，append 到 buf 数组的末尾，建议提前为 buf 数组分配好容量
func (t *trie[U]) getEdges(p U, edgesRuneBuf []rune, edgesChildBuf []U) ([]rune, []U) {
	if m := t.childrenMap(p); m != nil {
		for r, child := range m {
			edgesRuneBuf = append(edgesRuneBuf, r)
			edgesChildBuf = append(edgesChildBuf, child)
		}
	} else {
		for i := t.nodes[p].firstEdge; i != ^U(0); i = t.edgesNext[i] {
			edgesRuneBuf = append(edgesRuneBuf, t.edgesRune[i])
			edgesChildBuf = append(edgesChildBuf, t.edgesChild[i])
		}
	}
	return edgesRuneBuf, edgesChildBuf
}

// 返回按 r 升序排列的边数组，append 到 buf 数组的末尾，建议提前为 buf 数组分配好容量
func (t *trie[U]) getSortedEdges(p U, edgesRuneBuf []rune, edgesChildBuf []U) ([]rune, []U) {
	beginRune := len(edgesRuneBuf)
	beginChild := len(edgesChildBuf)
	edgesRuneBuf, edgesChildBuf = t.getEdges(p, edgesRuneBuf, edgesChildBuf)
	edgesRune := edgesRuneBuf[beginRune:]
	edgesChild := edgesChildBuf[beginChild:]
	sortKVList(edgesRune, edgesChild)
	return edgesRuneBuf, edgesChildBuf
}

// 需开启 withTermIdx 时才能调用，按插入顺序返回节点的 term 下标，append 到 buf 末尾
func (t *trie[U]) getTermIndexes(p U, buf []U) []U {
	begin := len(buf)
	for i := t.nodes[p].firstTerm; i != ^U(0); i = t.nextTermIdx[i] {
		buf = append(buf, i)
	}
	reverse(buf[begin:]) // 采用头插，需反向以恢复插入顺序
	return buf
}

func (t *trie[U]) freeNode(p U) {
	node := &t.nodes[p]
	if t.mapFlags.Get(int(p)) {
		t.childrenMaps[node.firstEdge] = nil
	}
	// 链表就不用free了，不会减小实际内存大小
}
