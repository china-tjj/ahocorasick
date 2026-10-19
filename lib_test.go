package ahocorasick

import (
	"bytes"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// ============================================================
// 朴素参考实现：与库实现完全独立，用于差分对拍
// ============================================================

// testTermAllowed 去重规则：未开启 termIdx 时相同 term 只算一次；开启后每个 termIdx 独立
func testTermAllowed(term string, termIdx int, withTermIdx bool, seen map[string]bool, seenIdx map[int]bool) bool {
	if withTermIdx {
		if seenIdx[termIdx] {
			return false
		}
		seenIdx[termIdx] = true
		return true
	}
	if seen[term] {
		return false
	}
	seen[term] = true
	return true
}

// testValidTerms 返回实际被建索引的 term 及其原始下标，与构建期的过滤规则一致
func testValidTerms(terms []string, charType CharType) ([]string, []int) {
	var validTerms []string
	var indexes []int
	for i, term := range terms {
		// 空串总是被忽略；rune 粒度还会忽略非法 utf8 字符串
		if term == "" || charType == CharTypeRune && !utf8.ValidString(term) {
			continue
		}
		validTerms = append(validTerms, term)
		indexes = append(indexes, i)
	}
	return validTerms, indexes
}

// testDecodeChar 返回位置 i 处的字符宽度；rune 粒度下非法字符返回 ok=false
func testDecodeChar(s string, charType CharType) (size int, ok bool) {
	if charType == CharTypeByte {
		return 1, true
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return 0, false
	}
	return size, true
}

// testNaivePrefix 朴素前缀匹配
func testNaivePrefix(query string, terms []string, withTermIdx bool, charType CharType) []MatchResult {
	validTerms, indexes := testValidTerms(terms, charType)
	var results []MatchResult
	for i := 0; i < len(query); {
		seen := make(map[string]bool)
		seenIdx := make(map[int]bool)
		size, ok := testDecodeChar(query[i:], charType)
		if !ok {
			break // 前缀匹配遇到非法字符即终止
		}
		i += size
		for j, term := range validTerms {
			// 前缀匹配：term 必须是从位置 0 开始的完整前缀
			if term == query[:i] && testTermAllowed(term, indexes[j], withTermIdx, seen, seenIdx) {
				results = append(results, testMakeResult(query, term, indexes[j], withTermIdx, i))
			}
		}
	}
	return results
}

// testNaiveAll 朴素多模式匹配
func testNaiveAll(query string, terms []string, withTermIdx bool, charType CharType) []MatchResult {
	validTerms, indexes := testValidTerms(terms, charType)
	var results []MatchResult
	for i := 0; i < len(query); {
		seen := make(map[string]bool)
		seenIdx := make(map[int]bool)
		size, ok := testDecodeChar(query[i:], charType)
		if !ok { // 非法字符处不可能成为匹配起点，跳过
			i++
			continue
		}
		for j, term := range validTerms {
			if strings.HasPrefix(query[i:], term) && testTermAllowed(term, indexes[j], withTermIdx, seen, seenIdx) {
				results = append(results, testMakeResult(query, term, indexes[j], withTermIdx, i+len(term)))
			}
		}
		i += size
	}
	return results
}

func testMakeResult(query, term string, termIdx int, withTermIdx bool, endIdx int) MatchResult {
	if !withTermIdx {
		termIdx = -1
	}
	return MatchResult{
		Term:     term,
		TermIdx:  termIdx,
		StartIdx: endIdx - len(term),
		EndIdx:   endIdx,
	}
}

// ============================================================
// 断言辅助
// ============================================================

type testMatchKey struct {
	term     string
	termIdx  int
	startIdx int
	endIdx   int
}

func testKey(r MatchResult) testMatchKey {
	return testMatchKey{r.Term, r.TermIdx, r.StartIdx, r.EndIdx}
}

func testSortedKeys(results []MatchResult) []testMatchKey {
	keys := make([]testMatchKey, len(results))
	for i, result := range results {
		keys[i] = testKey(result)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.endIdx != b.endIdx {
			return a.endIdx < b.endIdx
		}
		if a.startIdx != b.startIdx {
			return a.startIdx < b.startIdx
		}
		if a.termIdx != b.termIdx {
			return a.termIdx < b.termIdx
		}
		return a.term < b.term
	})
	return keys
}

// testAssertResults 比对两组结果（无序），并校验每个结果的基本不变量
func testAssertResults(t *testing.T, query string, got, want []MatchResult, terms []string, withTermIdx bool, charType CharType) {
	t.Helper()
	for _, r := range got {
		if r.StartIdx < 0 || r.StartIdx >= r.EndIdx || r.EndIdx > len(query) {
			t.Fatalf("query %q: invalid range [%d, %d)", query, r.StartIdx, r.EndIdx)
		}
		if r.Term != query[r.StartIdx:r.EndIdx] {
			t.Fatalf("query %q: Term %q != query[%d:%d] %q", query, r.Term, r.StartIdx, r.EndIdx, query[r.StartIdx:r.EndIdx])
		}
		if withTermIdx {
			if r.TermIdx < 0 || r.TermIdx >= len(terms) || terms[r.TermIdx] != r.Term {
				t.Fatalf("query %q: invalid TermIdx %d for term %q", query, r.TermIdx, r.Term)
			}
		} else if r.TermIdx != -1 {
			t.Fatalf("query %q: TermIdx should be -1, got %d", query, r.TermIdx)
		}
		if charType == CharTypeRune && !utf8.ValidString(r.Term) {
			t.Fatalf("query %q: term %q is not valid utf8 in rune mode", query, r.Term)
		}
	}
	gotKeys, wantKeys := testSortedKeys(got), testSortedKeys(want)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("query %q: result mismatch:\n got: %v\nwant: %v", query, got, want)
	}
}

// testAssertFirst 校验 MatchFirst/PreMatchFirst：结束位置最早，并列时取最长
func testAssertFirst(t *testing.T, query string, got MatchResult, ok bool, all []MatchResult) {
	t.Helper()
	if len(all) == 0 {
		if ok {
			t.Fatalf("query %q: expect no match, got %v", query, got)
		}
		return
	}
	if !ok {
		t.Fatalf("query %q: expect match, got nothing", query)
	}
	minEnd := all[0].EndIdx
	for _, r := range all {
		minEnd = min(minEnd, r.EndIdx)
	}
	maxLen, found := 0, false
	for _, r := range all {
		if r.EndIdx != minEnd {
			continue
		}
		maxLen = max(maxLen, len(r.Term))
		if testKey(r) == testKey(got) {
			found = true
		}
	}
	if !found || got.EndIdx != minEnd || len(got.Term) != maxLen {
		t.Fatalf("query %q: unexpected first match %v, all: %v", query, got, all)
	}
}

// testAssertUnique 校验 Match：与全量匹配去重后一致
func testAssertUnique(t *testing.T, query string, got, all []MatchResult, withTermIdx bool) {
	t.Helper()
	var want []MatchResult
	seen := make(map[string]bool)
	seenIdx := make(map[int]bool)
	for _, r := range all {
		if testTermAllowed(r.Term, r.TermIdx, withTermIdx, seen, seenIdx) {
			want = append(want, r)
		}
	}
	gotKeys, wantKeys := testSortedKeys(got), testSortedKeys(want)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("query %q: unique result mismatch:\n got: %v\nwant: %v", query, got, want)
	}
}

// ============================================================
// 构建配置与类型探测
// ============================================================

type testConfig struct {
	charType   CharType
	fastBuild  bool
	outputLink bool
	termIdx    bool
}

func (c testConfig) String() string {
	return fmt.Sprintf("char=%v,fast=%v,outputLink=%v,termIdx=%v", c.charType, c.fastBuild, c.outputLink, c.termIdx)
}

func (c testConfig) options() []Option {
	options := []Option{WithCharType(c.charType)}
	if c.fastBuild {
		options = append(options, WithFastBuild())
	}
	if c.outputLink {
		options = append(options, WithOutputLink())
	}
	if c.termIdx {
		options = append(options, WithTermIdx())
	}
	return options
}

// testConfigs 全量组合：字符粒度 × 构建模式 × 输出链接 × termIdx
func testConfigs() []testConfig {
	var configs []testConfig
	for _, charType := range []CharType{CharTypeAuto, CharTypeByte, CharTypeRune} {
		for _, fastBuild := range []bool{false, true} {
			for _, outputLink := range []bool{false, true} {
				for _, termIdx := range []bool{false, true} {
					configs = append(configs, testConfig{charType, fastBuild, outputLink, termIdx})
				}
			}
		}
	}
	return configs
}

// testEffectiveCharType 复制自动字符粒度选择逻辑，用于驱动朴素实现
func testEffectiveCharType(terms []string, charType CharType) CharType {
	if charType == CharTypeByte || charType == CharTypeRune {
		return charType
	}
	byteCnt, runeCnt := 0, 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if !utf8.ValidString(term) {
			return CharTypeByte
		}
		byteCnt += len(term)
		runeCnt += utf8.RuneCountInString(term)
	}
	if runeCnt == 0 || float64(byteCnt)/float64(runeCnt) < 1.5 {
		return CharTypeByte
	}
	return CharTypeRune
}

// testConcreteTypes 探测构建产物的具体类型（DType 与字符粒度）
func testConcreteTypes(v any) (DType, CharType) {
	switch v.(type) {
	case *compactTrie[uint8, byte], *acAutomaton[uint8, byte]:
		return DTypeUint8, CharTypeByte
	case *compactTrie[uint8, rune], *acAutomaton[uint8, rune]:
		return DTypeUint8, CharTypeRune
	case *compactTrie[uint16, byte], *acAutomaton[uint16, byte]:
		return DTypeUint16, CharTypeByte
	case *compactTrie[uint16, rune], *acAutomaton[uint16, rune]:
		return DTypeUint16, CharTypeRune
	case *compactTrie[uint32, byte], *acAutomaton[uint32, byte]:
		return DTypeUint32, CharTypeByte
	case *compactTrie[uint32, rune], *acAutomaton[uint32, rune]:
		return DTypeUint32, CharTypeRune
	case *compactTrie[uint64, byte], *acAutomaton[uint64, byte]:
		return DTypeUint64, CharTypeByte
	case *compactTrie[uint64, rune], *acAutomaton[uint64, rune]:
		return DTypeUint64, CharTypeRune
	default:
		return DTypeAuto, CharTypeAuto
	}
}

// ============================================================
// 1. 匹配正确性：所有公开匹配接口 × 全量配置组合，与朴素实现对拍
// ============================================================

type testDataset struct {
	name    string
	terms   []string
	queries []string
}

func testDatasets() []testDataset {
	invalid1 := string([]byte{0xff})
	invalid2 := string([]byte{0xe4, 0xb8})
	// 300 个 term，跨越 uint8 下标上限（termIndexes 溢出回归）
	manyTerms := make([]string, 300)
	for i := range manyTerms {
		manyTerms[i] = fmt.Sprintf("t%d", i)
	}
	// 2049 个不同首字符，根节点触发哈希索引；{1, 2050} 取模 2049 冲突，覆盖溢出表
	var hashChars []rune
	for c := rune(1); len(hashChars) < 2048; c++ {
		if utf8.ValidRune(c) {
			hashChars = append(hashChars, c)
		}
	}
	hashChars = append(hashChars, 2050)
	hashRoot := make([]string, len(hashChars))
	hashMiddle := make([]string, len(hashChars))
	for i, c := range hashChars {
		hashRoot[i] = string(c)
		hashMiddle[i] = "P" + string(c)
	}
	return []testDataset{
		{"empty", nil, []string{"", "a", "中"}},
		{"allEmpty", []string{"", "", ""}, []string{"", "a"}},
		{"allInvalid", []string{invalid1, invalid2, ""}, []string{"a" + invalid1, invalid2, ""}},
		{"classic", []string{"he", "she", "his", "hers", "ush", "你", "你们", "你们好", "好", ""},
			[]string{"ushers", "他说你们好", "his中her", "", "你"}},
		{"prefixChain", []string{"a", "aa", "aaa", "aaaa", "aaaaa"}, []string{"aaaaa", "aaaaaa", "b"}},
		{"duplicates", []string{"ab", "ab", "abc", "ab", "中", "中"}, []string{"abcab中", "abab"}},
		{"invalidUtf8", []string{"a" + invalid1, invalid2 + "b", "abc", "中"},
			[]string{"a" + invalid1 + "b", invalid2 + "bc", "abc中"}},
		{"longTerm", []string{strings.Repeat("a", 300), strings.Repeat("a", 299) + "b"},
			[]string{strings.Repeat("a", 300), strings.Repeat("a", 299) + "bc"}},
		{"manyTerms", manyTerms, []string{"t0t1t2", "xt100", "t299", "t10t20t30"}},
		{"hashRoot", hashRoot, []string{string(rune(1)), string(rune(2050)), "x" + string(rune(2048))}},
		{"hashMiddle", hashMiddle, []string{"P" + string(rune(1)), "P" + string(rune(2050)) + "x", "P"}},
		{"unicodeEdge", []string{string(rune(0x10FFFF)), "\x00", "a" + string(rune(0x10FFFF)), string(rune(0xFFFD))},
			[]string{"a" + string(rune(0x10FFFF)), "\x00\x00", string(rune(0xFFFD))}},
		// 多个 term 共享 U+FFFD 前缀：检验 nodeCnt 对 3 字节 RuneError 字符的计数
		{"runeError", []string{string(rune(0xFFFD)), "a" + string(rune(0xFFFD)), string(rune(0xFFFD)) + "a",
			string(rune(0xFFFD)) + string(rune(0xFFFD)), "ab" + string(rune(0xFFFD))},
			[]string{"a" + string(rune(0xFFFD)), string(rune(0xFFFD)) + string(rune(0xFFFD)), "x" + string(rune(0xFFFD)) + "a"}},
	}
}

func testExerciseMatchers(t *testing.T, terms, queries []string) {
	t.Helper()
	for _, cfg := range testConfigs() {
		t.Run(cfg.String(), func(t *testing.T) {
			options := cfg.options()
			effective := testEffectiveCharType(terms, cfg.charType)
			ac := NewAcAutomaton(terms, options...)
			trie := NewTrie(terms, options...)
			for _, query := range queries {
				all := testNaiveAll(query, terms, cfg.termIdx, effective)
				testAssertResults(t, query, ac.MatchAll(query), all, terms, cfg.termIdx, effective)
				got, ok := ac.MatchFirst(query)
				testAssertFirst(t, query, got, ok, all)
				testAssertUnique(t, query, ac.MatchAllUnique(query), all, cfg.termIdx)

				prefix := testNaivePrefix(query, terms, cfg.termIdx, effective)
				testAssertResults(t, query, trie.PreMatchAll(query), prefix, terms, cfg.termIdx, effective)
				got, ok = trie.PreMatchFirst(query)
				testAssertFirst(t, query, got, ok, prefix)
			}
		})
	}
}

func TestMatching(t *testing.T) {
	for _, ds := range testDatasets() {
		t.Run(ds.name, func(t *testing.T) {
			testExerciseMatchers(t, ds.terms, ds.queries)
		})
	}
	// 随机对拍：混合字母表（含非法 utf8 / 空串 / 重复），固定 seed 保证可复现
	t.Run("random", func(t *testing.T) {
		r := rand.New(rand.NewSource(42))
		alphabets := [][]string{
			{"a", "b", "c", "ab", ""},
			{"中", "国", "人", "ab", "中A", string(rune(0x10FFFF)), ""},
			{"a", "中", string([]byte{0xff}), string([]byte{0xe4}), "xy", ""},
		}
		gen := func(n int, alphabet []string) []string {
			terms := make([]string, 0, n)
			for i := 0; i < n; i++ {
				var b strings.Builder
				for j, l := 0, r.Intn(6); j < l; j++ {
					b.WriteString(alphabet[r.Intn(len(alphabet))])
				}
				terms = append(terms, b.String())
			}
			return terms
		}
		for ai, alphabet := range alphabets {
			for _, n := range []int{1, 5, 50, 300} {
				terms := gen(n, alphabet)
				queries := []string{"", "a", "中国人", "abc中ab", string([]byte{0xff})}
				for q := 0; q < 3; q++ {
					queries = append(queries, gen(1, alphabet)[0]+"中a")
				}
				t.Run(fmt.Sprintf("alphabet=%d,n=%d", ai, n), func(t *testing.T) {
					testExerciseMatchers(t, terms, queries)
				})
			}
		}
	})
}

// ============================================================
// 2. 字符粒度与数据类型的自动选择
// ============================================================

func TestAutoSelection(t *testing.T) {
	t.Run("charType", func(t *testing.T) {
		cases := []struct {
			name    string
			terms   []string
			options []Option
			want    CharType
		}{
			{"ascii", []string{"a", "bc"}, nil, CharTypeByte},
			{"cjk", []string{"中", "中国"}, nil, CharTypeRune},
			{"avgLen_1.5", []string{"abc", "中"}, nil, CharTypeRune},  // 6B/4R = 1.5，不小于 1.5
			{"avgLen_1.4", []string{"abcd", "中"}, nil, CharTypeByte}, // 7B/5R = 1.4
			{"invalidTerm", []string{"中", string([]byte{0xff})}, nil, CharTypeByte},
			{"explicitByte", []string{"中"}, []Option{WithCharType(CharTypeByte)}, CharTypeByte},
			{"explicitRune", []string{"a"}, []Option{WithCharType(CharTypeRune)}, CharTypeRune},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if _, charType := testConcreteTypes(NewTrie(c.terms, c.options...)); charType != c.want {
					t.Fatalf("expect %v, got %v", c.want, charType)
				}
			})
		}
	})
	t.Run("dType", func(t *testing.T) {
		// 指定下标位置放置合法 term，其余填空串
		termsWithIdx := func(idx int, term string) []string {
			terms := make([]string, idx+1)
			terms[idx] = term
			return terms
		}
		cases := []struct {
			name    string
			terms   []string
			options []Option
			want    DType
		}{
			// 节点数路径：maxValue = 1 + charCnt
			{"charCnt_uint8max", []string{strings.Repeat("a", 254)}, nil, DTypeUint8},
			{"charCnt_uint16min", []string{strings.Repeat("a", 255)}, nil, DTypeUint16},
			// term 长度路径：rune 粒度下 maxTermLen 为字节长度，可超过 1+charCnt
			{"termLen_uint8max", []string{strings.Repeat("中", 85)}, nil, DTypeUint8},
			{"termLen_uint16min", []string{strings.Repeat("中", 86)}, nil, DTypeUint16},
			// term 下标路径：未开启 termIdx 时 termIndexes 也需存储原始下标
			{"termIdx_uint8max", termsWithIdx(255, "x"), nil, DTypeUint8},
			{"termIdx_uint16min", termsWithIdx(256, "x"), nil, DTypeUint16},
			// term 数据路径：开启 termIdx 时 terms 数组容量上界为 2*termCnt
			{"termData_uint8max", make([]string, 127), []Option{WithTermIdx()}, DTypeUint8},
			{"termData_uint16min", make([]string, 128), []Option{WithTermIdx()}, DTypeUint16},
			// uint32 边界
			{"uint16_noTermIdx", termsWithIdx(65535, "x"), nil, DTypeUint16},
			{"uint32_termIdx", termsWithIdx(65535, "x"), []Option{WithTermIdx()}, DTypeUint32},
			// 强制指定
			{"forced", []string{"a", "b"}, []Option{WithDType(DTypeUint64)}, DTypeUint64},
		}
		// termData 用例需要非空 term
		for i := range cases {
			if strings.HasPrefix(cases[i].name, "termData_") {
				for j := range cases[i].terms {
					cases[i].terms[j] = "a"
				}
			}
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if dType, _ := testConcreteTypes(NewTrie(c.terms, c.options...)); dType != c.want {
					t.Fatalf("trie: expect %v, got %v", c.want, dType)
				}
				if dType, _ := testConcreteTypes(NewAcAutomaton(c.terms, c.options...)); dType != c.want {
					t.Fatalf("ac: expect %v, got %v", c.want, dType)
				}
			})
		}
	})
}

// ============================================================
// 3. 序列化：往返一致、截断/损坏必报错、交叉加载、文件读写
// ============================================================

func TestSerialization(t *testing.T) {
	terms := []string{"he", "she", "his", "hers", "你", "你们", "", string([]byte{0xff}), "ab", "ab"}
	queries := []string{"ushers", "你们好", "a" + string([]byte{0xff}) + "b", ""}

	t.Run("roundTrip", func(t *testing.T) {
		for _, cfg := range testConfigs() {
			t.Run(cfg.String(), func(t *testing.T) {
				options := cfg.options()
				effective := testEffectiveCharType(terms, cfg.charType)

				ac := NewAcAutomaton(terms, options...)
				var buf bytes.Buffer
				if err := ac.Save(&buf); err != nil {
					t.Fatal(err)
				}
				ac2, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
				if err != nil {
					t.Fatal(err)
				}

				trie := NewTrie(terms, options...)
				buf.Reset()
				if err := trie.Save(&buf); err != nil {
					t.Fatal(err)
				}
				trie2, err := LoadTrie(bytes.NewReader(buf.Bytes()))
				if err != nil {
					t.Fatal(err)
				}

				for _, query := range queries {
					all := testNaiveAll(query, terms, cfg.termIdx, effective)
					testAssertResults(t, query, ac2.MatchAll(query), all, terms, cfg.termIdx, effective)
					got, ok := ac2.MatchFirst(query)
					testAssertFirst(t, query, got, ok, all)
					testAssertUnique(t, query, ac2.MatchAllUnique(query), all, cfg.termIdx)

					prefix := testNaivePrefix(query, terms, cfg.termIdx, effective)
					testAssertResults(t, query, trie2.PreMatchAll(query), prefix, terms, cfg.termIdx, effective)
					got, ok = trie2.PreMatchFirst(query)
					testAssertFirst(t, query, got, ok, prefix)
				}
			})
		}
	})

	t.Run("truncated", func(t *testing.T) {
		// 任意前缀截断都必须报错，不允许静默成功
		ac := NewAcAutomaton(terms, WithTermIdx(), WithOutputLink())
		var buf bytes.Buffer
		if err := ac.Save(&buf); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		for i := 0; i < len(data); i++ {
			if _, err := LoadAcAutomaton(bytes.NewReader(data[:i])); err == nil {
				t.Fatalf("expect error for truncated data at %d/%d", i, len(data))
			}
			if _, err := LoadTrie(bytes.NewReader(data[:i])); err == nil {
				t.Fatalf("expect error for truncated data at %d/%d", i, len(data))
			}
		}
	})

	t.Run("corruptedHeader", func(t *testing.T) {
		// 头部 8 字节（magic/version/type/dType/charType）任意损坏都必须报错
		ac := NewAcAutomaton(terms, WithTermIdx(), WithOutputLink())
		var buf bytes.Buffer
		if err := ac.Save(&buf); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		for i := 0; i < 8; i++ {
			corrupt := bytes.Clone(data)
			corrupt[i] ^= 0xFF
			if _, err := LoadAcAutomaton(bytes.NewReader(corrupt)); err == nil {
				t.Fatalf("expect error for corrupted header byte %d", i)
			}
		}
	})

	t.Run("crossLoad", func(t *testing.T) {
		ac := NewAcAutomaton(terms)
		var buf bytes.Buffer
		if err := ac.Save(&buf); err != nil {
			t.Fatal(err)
		}
		// LoadTrie 加载 AC 数据：返回的 ITrie 实际为 AC 自动机
		if v, err := LoadTrie(bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("LoadTrie on ac data should succeed, got %v", err)
		} else if _, ok := v.(IAcAutomaton); !ok {
			t.Fatalf("LoadTrie on ac data should return an IAcAutomaton")
		}

		trie := NewTrie(terms)
		buf.Reset()
		if err := trie.Save(&buf); err != nil {
			t.Fatal(err)
		}
		// LoadAcAutomaton 加载 Trie 数据：报错
		if _, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes())); err == nil {
			t.Fatalf("LoadAcAutomaton on trie data should fail")
		}
	})

	t.Run("file", func(t *testing.T) {
		ac := NewAcAutomaton(terms, WithTermIdx(), WithOutputLink())
		filename := t.TempDir() + "/ac.bin"
		if err := ac.SaveToFile(filename); err != nil {
			t.Fatal(err)
		}
		ac2, err := LoadAcAutomatonFromFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range queries {
			testAssertResults(t, query, ac2.MatchAll(query), ac.MatchAll(query), terms, true, testEffectiveCharType(terms, CharTypeAuto))
		}
		if _, err := LoadAcAutomatonFromFile(filename + ".notexist"); err == nil {
			t.Fatalf("expect error for nonexistent file")
		}

		trie := NewTrie(terms, WithTermIdx())
		filename = t.TempDir() + "/trie.bin"
		if err := trie.SaveToFile(filename); err != nil {
			t.Fatal(err)
		}
		trie2, err := LoadTrieFromFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range queries {
			testAssertResults(t, query, trie2.PreMatchAll(query), trie.PreMatchAll(query), terms, true, testEffectiveCharType(terms, CharTypeAuto))
		}
	})
}

// ============================================================
// 4. 哈希节点溢出表：哈希节点数 > 2048 时切换到 overflowMap
// ============================================================

func TestHashNodeOverflow(t *testing.T) {
	// 取 {1..2048, 2050}：2050 % 2049 == 1，与 rune(1) 必然冲突；
	// 根节点与 2049 个中间节点均为哈希节点，总数超过 2048，触发 overflowMap 路径
	var runes []rune
	for c := rune(1); len(runes) < 2048; c++ {
		if utf8.ValidRune(c) {
			runes = append(runes, c)
		}
	}
	runes = append(runes, 2050)
	terms := make([]string, 0, len(runes)*len(runes))
	for _, a := range runes {
		for _, b := range runes {
			terms = append(terms, string(a)+string(b))
		}
	}
	queries := []string{
		string(runes[0]) + string(runes[1]),
		string(runes[2048]) + string(runes[2047]) + "x",
		string(runes[100]),
	}
	for _, fastBuild := range []bool{false, true} {
		t.Run(fmt.Sprintf("fastBuild=%v", fastBuild), func(t *testing.T) {
			options := []Option{WithCharType(CharTypeRune)}
			if fastBuild {
				options = append(options, WithFastBuild())
			}
			ac := NewAcAutomaton(terms, options...)
			if testOverflowMapSize(ac) == 0 {
				t.Fatal("expect overflowMap to be used")
			}
			for _, query := range queries {
				testAssertResults(t, query, ac.MatchAll(query), testNaiveAll(query, terms, false, CharTypeRune), terms, false, CharTypeRune)
			}
			// 序列化往返
			var buf bytes.Buffer
			if err := ac.Save(&buf); err != nil {
				t.Fatal(err)
			}
			ac2, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range queries {
				testAssertResults(t, query, ac2.MatchAll(query), ac.MatchAll(query), terms, false, CharTypeRune)
			}
		})
	}
}

// testOverflowMapSize 返回自动机的 overflowMap 大小，-1 表示类型不匹配
func testOverflowMapSize(ac IAcAutomaton) int {
	switch ac := ac.(type) {
	case *acAutomaton[uint8, rune]:
		return len(ac.overflowMap)
	case *acAutomaton[uint16, rune]:
		return len(ac.overflowMap)
	case *acAutomaton[uint32, rune]:
		return len(ac.overflowMap)
	case *acAutomaton[uint64, rune]:
		return len(ac.overflowMap)
	default:
		return -1
	}
}

// ============================================================
// 5. 并发只读访问
// ============================================================

func TestConcurrentMatching(t *testing.T) {
	terms := []string{"he", "she", "his", "hers", "ush", "你", "你们", "你们好", "好", "ab", "ab"}
	queries := []string{"ushers", "他说你们好", "his中her", "", "你", "abab"}
	cfg := testConfig{charType: CharTypeAuto, fastBuild: false, outputLink: true, termIdx: true}
	ac := NewAcAutomaton(terms, cfg.options()...)
	trie := NewTrie(terms, cfg.options()...)
	effective := testEffectiveCharType(terms, cfg.charType)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, query := range queries {
				all := testNaiveAll(query, terms, cfg.termIdx, effective)
				testAssertResults(t, query, ac.MatchAll(query), all, terms, cfg.termIdx, effective)
				got, ok := ac.MatchFirst(query)
				testAssertFirst(t, query, got, ok, all)
				testAssertUnique(t, query, ac.MatchAllUnique(query), all, cfg.termIdx)

				prefix := testNaivePrefix(query, terms, cfg.termIdx, effective)
				testAssertResults(t, query, trie.PreMatchAll(query), prefix, terms, cfg.termIdx, effective)
				got, ok = trie.PreMatchFirst(query)
				testAssertFirst(t, query, got, ok, prefix)
			}
		}()
	}
	wg.Wait()
}

// ============================================================
// 6. lcpCharLen 单元测试：字符身份与 decodeCharInString 一致（只看 rune 值）
// ============================================================

func TestLcpCharLen(t *testing.T) {
	runeError := string(rune(0xFFFD))
	invalidByte := string([]byte{0x80})
	cases := []struct {
		a, b     string
		wantRune int
		wantByte int
	}{
		{"abc", "abd", 2, 2},
		{"", "a", 0, 0},
		{"a", "a", 1, 1},
		// 不同 rune 共享前导字节：rune 粒度必须按解码比较，不能按字节
		{string(rune(0x80)), string(rune(0xBF)), 0, 1}, // 共享字节 0xC2
		{"中", "主", 0, 2},                               // 共享字节 0xE4 0xB8
		{"中国人", "中华", 1, 4},                            // 国/华 共享前导字节 0xE5，字节 LCP 会越过 rune 边界
		// 真 U+FFFD（3 字节）与非法字节（1 字节）同为 RuneError，是同一 trie 字符
		{"a" + runeError, "a" + invalidByte, 2, 1},
		{runeError, runeError + "b", 1, 3},
		// 两个不同的非法字节也同为 RuneError
		{invalidByte, string([]byte{0x81}), 1, 0},
	}
	for _, c := range cases {
		if got := lcpCharLen[rune](c.a, c.b); got != c.wantRune {
			t.Errorf("lcpCharLen[rune](%q, %q) = %d, want %d", c.a, c.b, got, c.wantRune)
		}
		if got := lcpCharLen[byte](c.a, c.b); got != c.wantByte {
			t.Errorf("lcpCharLen[byte](%q, %q) = %d, want %d", c.a, c.b, got, c.wantByte)
		}
	}
}

// ============================================================
// 7. 流式接口（Match / MatchUnique / PreMatch）
// ============================================================

// testCollectStream 收集流式迭代器的全部结果
func testCollectStream(t *testing.T, stream func(yield func(result MatchResult) bool)) []MatchResult {
	t.Helper()
	var results []MatchResult
	stream(func(r MatchResult) bool {
		results = append(results, r)
		return true
	})
	return results
}

// testExerciseStreamMatchers 流式接口与对应的批量接口结果完全一致（含顺序），并与朴素实现对拍
func testExerciseStreamMatchers(t *testing.T, terms, queries []string) {
	t.Helper()
	for _, cfg := range testConfigs() {
		t.Run(cfg.String(), func(t *testing.T) {
			options := cfg.options()
			effective := testEffectiveCharType(terms, cfg.charType)
			ac := NewAcAutomaton(terms, options...)
			trie := NewTrie(terms, options...)
			for _, query := range queries {
				// Match ≡ MatchAll（结果与顺序一致）
				streamAll := testCollectStream(t, ac.MatchIter(query))
				if all := ac.MatchAll(query); !slices.Equal(streamAll, all) {
					t.Fatalf("query %q: Match %v != MatchAll %v", query, streamAll, all)
				}
				testAssertResults(t, query, streamAll, testNaiveAll(query, terms, cfg.termIdx, effective), terms, cfg.termIdx, effective)

				// MatchUnique ≡ Match（结果与顺序一致）
				streamUnique := testCollectStream(t, ac.MatchUniqueIter(query))
				if unique := ac.MatchAllUnique(query); !slices.Equal(streamUnique, unique) {
					t.Fatalf("query %q: MatchUnique %v != Match %v", query, streamUnique, unique)
				}
				testAssertUnique(t, query, streamUnique, testNaiveAll(query, terms, cfg.termIdx, effective), cfg.termIdx)

				// PreMatch ≡ PreMatchAll（结果与顺序一致）
				streamPre := testCollectStream(t, trie.PreMatchIter(query))
				if pre := trie.PreMatchAll(query); !slices.Equal(streamPre, pre) {
					t.Fatalf("query %q: PreMatch %v != PreMatchAll %v", query, streamPre, pre)
				}
				testAssertResults(t, query, streamPre, testNaivePrefix(query, terms, cfg.termIdx, effective), terms, cfg.termIdx, effective)
			}
		})
	}
}

// TestStreamMatching 流式接口正确性：全量数据集 × 全量配置组合
func TestStreamMatching(t *testing.T) {
	for _, ds := range testDatasets() {
		t.Run(ds.name, func(t *testing.T) {
			testExerciseStreamMatchers(t, ds.terms, ds.queries)
		})
	}
	// 随机对拍：与 TestMatching 相同的生成方式，固定 seed 保证可复现
	t.Run("random", func(t *testing.T) {
		r := rand.New(rand.NewSource(42))
		alphabets := [][]string{
			{"a", "b", "c", "ab", ""},
			{"中", "国", "人", "ab", "中A", string(rune(0x10FFFF)), ""},
			{"a", "中", string([]byte{0xff}), string([]byte{0xe4}), "xy", ""},
		}
		gen := func(n int, alphabet []string) []string {
			terms := make([]string, 0, n)
			for i := 0; i < n; i++ {
				var b strings.Builder
				for j, l := 0, r.Intn(6); j < l; j++ {
					b.WriteString(alphabet[r.Intn(len(alphabet))])
				}
				terms = append(terms, b.String())
			}
			return terms
		}
		for ai, alphabet := range alphabets {
			for _, n := range []int{1, 5, 50, 300} {
				terms := gen(n, alphabet)
				queries := []string{"", "a", "中国人", "abc中ab", string([]byte{0xff})}
				for q := 0; q < 3; q++ {
					queries = append(queries, gen(1, alphabet)[0]+"中a")
				}
				t.Run(fmt.Sprintf("alphabet=%d,n=%d", ai, n), func(t *testing.T) {
					testExerciseStreamMatchers(t, terms, queries)
				})
			}
		}
	})
}

// TestStreamEarlyStop 提前终止：yield 返回 false 后不得再产出结果
func TestStreamEarlyStop(t *testing.T) {
	terms := []string{"he", "she", "his", "hers", "ush", "你", "你们", "你们好", "好", "ab", "ab"}
	queries := []string{"ushers", "他说你们好", "his中her", "", "你", "abab"}
	for _, cfg := range testConfigs() {
		t.Run(cfg.String(), func(t *testing.T) {
			options := cfg.options()
			ac := NewAcAutomaton(terms, options...)
			trie := NewTrie(terms, options...)
			for _, query := range queries {
				streams := []struct {
					name  string
					seq   func(yield func(result MatchResult) bool)
					total int
				}{
					{"Match", ac.MatchIter(query), len(ac.MatchAll(query))},
					{"MatchUnique", ac.MatchUniqueIter(query), len(ac.MatchAllUnique(query))},
					{"PreMatch", trie.PreMatchIter(query), len(trie.PreMatchAll(query))},
				}
				for _, s := range streams {
					for _, limit := range []int{0, 1, 2, 5} {
						got := 0
						s.seq(func(r MatchResult) bool {
							got++
							return got < limit
						})
						// 首次 yield 必然发生，只能在返回 false 后停止，故实际收到 min(max(limit,1), total) 个
						if want := min(max(limit, 1), s.total); got != want {
							t.Fatalf("%s query %q limit %d: got %d results, want %d", s.name, query, limit, got, want)
						}
					}
				}
			}
		})
	}
}

// TestStreamReusable 返回的迭代器可重复调用，每次产出一致（每次调用状态独立）
func TestStreamReusable(t *testing.T) {
	terms := []string{"he", "she", "his", "hers", "你", "你们", "你们好", "ab", "ab"}
	queries := []string{"ushers", "他说你们好", "abab"}
	for _, cfg := range testConfigs() {
		t.Run(cfg.String(), func(t *testing.T) {
			options := cfg.options()
			ac := NewAcAutomaton(terms, options...)
			trie := NewTrie(terms, options...)
			for _, query := range queries {
				streams := []struct {
					name string
					seq  func(yield func(result MatchResult) bool)
				}{
					{"Match", ac.MatchIter(query)},
					{"MatchUnique", ac.MatchUniqueIter(query)},
					{"PreMatch", trie.PreMatchIter(query)},
				}
				for _, s := range streams {
					first := testCollectStream(t, s.seq)
					second := testCollectStream(t, s.seq)
					if !slices.Equal(first, second) {
						t.Fatalf("%s query %q: iterator not reusable, first %v, second %v", s.name, query, first, second)
					}
				}
			}
		})
	}
}
