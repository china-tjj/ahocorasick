package ahocorasick

const hashIndexThreshold = 16

type termMetadata[U uints] struct {
	len U // term 的长度
	idx U // term 在建树 terms 中的索引，可以用来关联一些业务信息
}

type linkedTermMetadata[U uints] struct {
	len  U // term 的长度
	next U // 下一个 term 的索引
	// trie.terms 和建树 terms 一一对应，所以不用存 idx
}

type trieNode[U uints] struct {
	firstEdge U // 链表存储时为链表头下标；map 存储时为 trie.childrenMaps 的下标
	firstTerm U // 链表头下标
}

// 仅用于辅助构建 compactTrie，未实现 ITrie
type trie[U uints] struct {
	// 根节点是 0，子节点一定在父节点后面
	nodes []trieNode[U]
	// 边以链表储存，使用三个数组而不是一个结构体，这样内存对齐没有 padding
	edgesChar  []rune
	edgesChild []U
	edgesNext  []U
	// 边数 > hashIndexThreshold 后以 map 存储
	childrenMaps []map[rune]U
	// 切换到 map 的标记
	mapFlags bitmap
	// 切换到 map 后释放链表，记录一下空位方便复用
	freeEdges deque[U]
	// 节点关联的 term 以链表存储
	terms []linkedTermMetadata[U]
}

func (t *trie[U]) build(terms []string, opt *option) {
	initCap := max(1, opt.trieInitCap)
	t.nodes = make([]trieNode[U], 0, initCap)
	// 边数 = 节点数 - 1
	t.edgesChar = make([]rune, 0, initCap-1)
	t.edgesChild = make([]U, 0, initCap-1)
	t.edgesNext = make([]U, 0, initCap-1)
	// 和建树 terms 一一对应
	t.terms = make([]linkedTermMetadata[U], 0, len(terms))
	t.mapFlags = bitmap{}
	t.mapFlags.Reserve(initCap)
	// 先放 root
	t.newNode()
	// 建树
	for _, term := range terms {
		// 忽略空字符串
		if term == "" {
			continue
		}
		p := U(0)
		for _, char := range term {
			child, ok := t.getChild(p, char)
			if !ok {
				child = t.newNode()
				t.setChild(p, char, child)
			}
			p = child
		}
		t.addTerm(p, U(len(term)))
	}
	t.freeEdges = deque[U]{} // 后面不会使用了，可以提前释放
}

func (t *trie[U]) newNode() U {
	idx := U(len(t.nodes))
	t.nodes = append(t.nodes, trieNode[U]{
		firstEdge: ^U(0),
		firstTerm: ^U(0),
	})
	return idx
}

// childrenMap 返回节点的哈希索引（已切换到 map 时返回，否则 nil）
func (t *trie[U]) childrenMap(p U) map[rune]U {
	if !t.mapFlags.Get(int(p)) {
		return nil
	}
	return t.childrenMaps[t.nodes[p].firstEdge]
}

func (t *trie[U]) getChild(p U, char rune) (U, bool) {
	if m := t.childrenMap(p); m != nil {
		child, ok := m[char]
		return child, ok
	}
	for i := t.nodes[p].firstEdge; i != ^U(0); i = t.edgesNext[i] {
		if t.edgesChar[i] == char {
			return t.edgesChild[i], true
		}
	}
	return 0, false
}

func (t *trie[U]) setChild(p U, char rune, child U) {
	node := &t.nodes[p]

	// 1) 已经切换到 map：只写 map
	if m := t.childrenMap(p); m != nil {
		m[char] = child
		return
	}

	// 2) 链表模式 & 未跨阈值：头插到链表（优先复用回收槽位）
	cnt := t.childrenLen(p)
	if cnt < hashIndexThreshold {
		var idx U
		if t.freeEdges.Len() > 0 {
			idx = t.freeEdges.Front()
			t.freeEdges.PopFront()
			t.edgesChar[idx] = char
			t.edgesChild[idx] = child
			t.edgesNext[idx] = node.firstEdge
		} else {
			idx = U(len(t.edgesChar))
			t.edgesChar = append(t.edgesChar, char)
			t.edgesChild = append(t.edgesChild, child)
			t.edgesNext = append(t.edgesNext, node.firstEdge)
		}
		node.firstEdge = idx
		return
	}

	// 3) 跨阈值：把现有链表整体迁移到 map，槽位回收到 freeEdges，新边只入 map
	m := make(map[rune]U, cnt+1)
	for i := node.firstEdge; i != ^U(0); i = t.edgesNext[i] {
		m[t.edgesChar[i]] = t.edgesChild[i]
		t.freeEdges.PushBack(i)
	}
	m[char] = child
	t.mapFlags.Set(int(p), true)
	node.firstEdge = U(len(t.childrenMaps))
	t.childrenMaps = append(t.childrenMaps, m)
}

func (t *trie[U]) addTerm(p U, termLen U) {
	node := &t.nodes[p]
	newIdx := U(len(t.terms))
	t.terms = append(t.terms, linkedTermMetadata[U]{
		len:  termLen,
		next: node.firstTerm,
	})
	node.firstTerm = newIdx
}

// 获取节点的子节点数量
func (t *trie[U]) childrenLen(p U) U {
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
func (t *trie[U]) getEdges(p U, edgesCharBuf []rune, edgesChildBuf []U) ([]rune, []U) {
	if m := t.childrenMap(p); m != nil {
		for char, child := range m {
			edgesCharBuf = append(edgesCharBuf, char)
			edgesChildBuf = append(edgesChildBuf, child)
		}
	} else {
		for i := t.nodes[p].firstEdge; i != ^U(0); i = t.edgesNext[i] {
			edgesCharBuf = append(edgesCharBuf, t.edgesChar[i])
			edgesChildBuf = append(edgesChildBuf, t.edgesChild[i])
		}
	}
	return edgesCharBuf, edgesChildBuf
}

func (t *trie[U]) getTerm(i U) termMetadata[U] {
	return termMetadata[U]{
		len: t.terms[i].len,
		idx: i,
	}
}

// 返回按 char 升序排列的边数组，append 到 buf 数组的末尾，建议提前为 buf 数组分配好容量
func (t *trie[U]) getSortedEdges(p U, edgesCharBuf []rune, edgesChildBuf []U) ([]rune, []U) {
	beginChar := len(edgesCharBuf)
	beginChild := len(edgesChildBuf)
	edgesCharBuf, edgesChildBuf = t.getEdges(p, edgesCharBuf, edgesChildBuf)
	edgesChar := edgesCharBuf[beginChar:]
	edgesChild := edgesChildBuf[beginChild:]
	sortKVList(edgesChar, edgesChild)
	return edgesCharBuf, edgesChildBuf
}

// 按插入顺序返回节点的 terms 切片，append 到 buf 数组的末尾，建议提前为 buf 数组分配好容量
func (t *trie[U]) getTerms(p U, buf []termMetadata[U]) []termMetadata[U] {
	begin := len(buf)
	for i := t.nodes[p].firstTerm; i != ^U(0); i = t.terms[i].next {
		buf = append(buf, t.getTerm(i))
	}
	reverse(buf[begin:]) // 采用头插的，需反向一下保证插入顺序
	return buf
}

func reverse[U any](s []U) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// 交换两个节点的元信息
func (t *trie[U]) swapNodes(i, j U) {
	flagI, flagJ := t.mapFlags.Get(int(i)), t.mapFlags.Get(int(j))
	t.mapFlags.Set(int(i), flagJ)
	t.mapFlags.Set(int(j), flagI)
	t.nodes[i], t.nodes[j] = t.nodes[j], t.nodes[i]
}

// 根据 mapping 重新映射所有出边的 child，需配合 swapNodes 保证最终正确性
func (t *trie[U]) mapChildren(p U, mapping map[U]U) {
	if m := t.childrenMap(p); m != nil {
		for char, child := range m {
			if newChild, ok := mapping[child]; ok {
				m[char] = newChild
			}
		}
		return
	}
	for i := t.nodes[p].firstEdge; i != ^U(0); i = t.edgesNext[i] {
		if newChild, ok := mapping[t.edgesChild[i]]; ok {
			t.edgesChild[i] = newChild
		}
	}
}

// 根据 mapper 重新映射所有出边的 char
func (t *trie[U]) mapChars(p U, mapper func(rune) rune) {
	node := &t.nodes[p]
	if oldMap := t.childrenMap(p); oldMap != nil {
		newMap := make(map[rune]U, len(oldMap))
		for char, child := range oldMap {
			newMap[mapper(char)] = child
		}
		t.childrenMaps[node.firstEdge] = newMap
		return
	}
	for i := node.firstEdge; i != ^U(0); i = t.edgesNext[i] {
		t.edgesChar[i] = mapper(t.edgesChar[i])
	}
}

func (t *trie[U]) freeNode(p U) {
	node := &t.nodes[p]
	if t.mapFlags.Get(int(p)) {
		t.childrenMaps[node.firstEdge] = nil
	}
	// 链表就不用free了，不会减小实际内存大小
}
