package ahocorasick

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
	"unsafe"
)

// lib_test.go 覆盖 AC 自动机与 Trie 的整体行为。
// 分组结构：
//   1) tt* 辅助函数与暴力实现
//   2) AC 基本 API / MatchAll / MatchFirst / MatchAllUnique 正确性
//   3) UTF-8 / TermIdx / Trie 前缀匹配
//   4) OutputLink、并发安全与确定性
//   5) 序列化 / 文件 IO / 宽度分支 / 错误处理
//   6) 大规模构建与 fuzz round-trip

// ===== 1. 测试基础设施（暴力参考实现 + 随机生成） =====

// ttResultKey 用于忽略顺序比较 MatchResult 集合
type ttResultKey struct {
	TermIdx  int
	StartIdx int
	EndIdx   int
}

func ttToKey(r MatchResult) ttResultKey {
	return ttResultKey{r.TermIdx, r.StartIdx, r.EndIdx}
}

// ttBruteForceMatchAll 暴力扫描所有 (term, position) 命中，作为 ground truth
func ttBruteForceMatchAll(query string, terms []string) []MatchResult {
	var matches []MatchResult
	qb := []byte(query)
	for idx, term := range terms {
		if term == "" {
			continue
		}
		tb := []byte(term)
		for i := 0; i+len(tb) <= len(qb); i++ {
			if string(qb[i:i+len(tb)]) == term {
				matches = append(matches, MatchResult{
					Term:     term,
					TermIdx:  idx,
					StartIdx: i,
					EndIdx:   i + len(tb),
				})
			}
		}
	}
	return matches
}

// ttBruteForceMatchAllUnique 每个 TermIdx 只保留第一次出现
func ttBruteForceMatchAllUnique(query string, terms []string) []MatchResult {
	allMatches := ttBruteForceMatchAll(query, terms)
	seen := make(map[int]bool)
	var out []MatchResult
	// 按 EndIdx 升序、StartIdx 升序排序，模拟 AC 的发现顺序
	sort.SliceStable(allMatches, func(i, j int) bool {
		if allMatches[i].EndIdx != allMatches[j].EndIdx {
			return allMatches[i].EndIdx < allMatches[j].EndIdx
		}
		return allMatches[i].StartIdx < allMatches[j].StartIdx
	})
	for _, m := range allMatches {
		if seen[m.TermIdx] {
			continue
		}
		seen[m.TermIdx] = true
		out = append(out, m)
	}
	return out
}

// ttBruteForceMatchFirst 返回 EndIdx 最小、StartIdx 最大（最深 fail 链）的命中
// 这与 AC 的 MatchFirst 语义对齐：最早结束位置 + 最长 term
func ttBruteForceMatchFirst(query string, terms []string) (MatchResult, bool) {
	allMatches := ttBruteForceMatchAll(query, terms)
	if len(allMatches) == 0 {
		return MatchResult{}, false
	}
	best := allMatches[0]
	for _, m := range allMatches[1:] {
		if m.EndIdx < best.EndIdx ||
			(m.EndIdx == best.EndIdx && m.StartIdx < best.StartIdx) {
			best = m
		}
	}
	return best, true
}

// ttEqualSet 多重集合相等（忽略顺序，重复元素也要计数一致）
func ttEqualSet(a, b []MatchResult, t *testing.T) bool {
	t.Helper()
	if len(a) != len(b) {
		return false
	}
	ma := make(map[ttResultKey]int)
	for _, r := range a {
		ma[ttToKey(r)]++
	}
	mb := make(map[ttResultKey]int)
	for _, r := range b {
		mb[ttToKey(r)]++
	}
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// ttDumpDiff 给出两个集合的差异信息，帮助定位 bug
func ttDumpDiff(t *testing.T, got, want []MatchResult) {
	t.Helper()
	mg := make(map[ttResultKey]MatchResult, len(got))
	for _, r := range got {
		mg[ttToKey(r)] = r
	}
	mw := make(map[ttResultKey]MatchResult, len(want))
	for _, r := range want {
		mw[ttToKey(r)] = r
	}
	for k, r := range mw {
		if _, ok := mg[k]; !ok {
			t.Logf("  miss(want):  %+v", r)
		}
	}
	for k, r := range mg {
		if _, ok := mw[k]; !ok {
			t.Logf("  extra(got):  %+v", r)
		}
	}
}

// ttRandChinese 生成 [minRunes, maxRunes] 之间的随机中文字符串
func ttRandChinese(rng *rand.Rand, minRunes, maxRunes int) string {
	n := minRunes + rng.Intn(maxRunes-minRunes+1)
	rs := make([]rune, n)
	for i := range rs {
		rs[i] = rune(0x4E00 + rng.Intn(0x9FA5-0x4E00+1))
	}
	return string(rs)
}

// ttRandMixed 中文 + 英文混合
func ttRandMixed(rng *rand.Rand, minRunes, maxRunes int) string {
	n := minRunes + rng.Intn(maxRunes-minRunes+1)
	rs := make([]rune, n)
	for i := range rs {
		switch rng.Intn(3) {
		case 0:
			rs[i] = rune('a' + rng.Intn(26))
		case 1:
			rs[i] = rune('A' + rng.Intn(26))
		case 2:
			rs[i] = rune(0x4E00 + rng.Intn(0x9FA5-0x4E00+1))
		}
	}
	return string(rs)
}

// ttRandLatin 仅 ASCII
func ttRandLatin(rng *rand.Rand, minLen, maxLen int) string {
	n := minLen + rng.Intn(maxLen-minLen+1)
	bs := make([]byte, n)
	for i := range bs {
		bs[i] = byte('a' + rng.Intn(26))
	}
	return string(bs)
}

// ttEmbedTermsIntoQuery 把若干 term 随机插入随机噪声，便于产生 hot path
func ttEmbedTermsIntoQuery(rng *rand.Rand, terms []string, segments int, gen func(*rand.Rand, int, int) string, gMin, gMax int) string {
	var sb strings.Builder
	for i := 0; i < segments; i++ {
		sb.WriteString(gen(rng, gMin, gMax))
		sb.WriteString(terms[rng.Intn(len(terms))])
	}
	sb.WriteString(gen(rng, gMin, gMax))
	return sb.String()
}

// ttAllOptionVariants 用于把同一个用例在所有 Option 组合下跑一遍
func ttAllOptionVariants(t *testing.T, terms []string, body func(t *testing.T, ac IAcAutomaton)) {
	t.Helper()
	cases := []struct {
		name    string
		options []Option
	}{
		{"default", nil},
		{"with_output_link", []Option{WithOutputLink()}},
		{"explicitly_disabled_output_link", []Option{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ac := NewAcAutomaton(terms, c.options...)
			body(t, ac)
		})
	}
}

// ===== 2. AC 公共构造函数 / API 形态 =====

func TestAPI_NewAcAutomaton_EmptyTerms(t *testing.T) {
	ac := NewAcAutomaton(nil)
	if r := ac.MatchAll("hello"); len(r) != 0 {
		t.Fatalf("nil terms should match nothing, got %v", r)
	}

	ac2 := NewAcAutomaton([]string{})
	if r := ac2.MatchAll("hello"); len(r) != 0 {
		t.Fatalf("empty slice terms should match nothing, got %v", r)
	}

	if _, ok := ac.MatchFirst("hello"); ok {
		t.Fatal("MatchFirst on empty AC should return false")
	}
	if r := ac.MatchAllUnique("hello"); len(r) != 0 {
		t.Fatalf("MatchAllUnique on empty AC should be empty, got %v", r)
	}
}

func TestAPI_NewAcAutomaton_AllEmptyStrings(t *testing.T) {
	// 全是空串应该等价于 nil
	ac := NewAcAutomaton([]string{"", "", ""})
	if r := ac.MatchAll("anything"); len(r) != 0 {
		t.Fatalf("all-empty terms should match nothing, got %v", r)
	}
}

func TestAPI_NewAcAutomaton_QueryEmpty(t *testing.T) {
	ac := NewAcAutomaton([]string{"abc", "你好"})
	if r := ac.MatchAll(""); len(r) != 0 {
		t.Fatalf("empty query should match nothing, got %v", r)
	}
	if _, ok := ac.MatchFirst(""); ok {
		t.Fatal("empty query MatchFirst should return false")
	}
	if r := ac.MatchAllUnique(""); len(r) != 0 {
		t.Fatalf("empty query MatchAllUnique should be empty, got %v", r)
	}
}

func TestAPI_NewTrie_Basic(t *testing.T) {
	tr := NewTrie([]string{"中", "中国", "中国人", "中国人民"})
	results := tr.PreMatchAll("中国人民万岁")
	if len(results) != 4 {
		t.Fatalf("PreMatchAll expected 4, got %d: %v", len(results), results)
	}

	first, ok := tr.PreMatchFirst("中国人民万岁")
	if !ok || first.Term != "中" {
		t.Fatalf("PreMatchFirst expected '中', got %+v ok=%v", first, ok)
	}

	if _, ok := tr.PreMatchFirst("Zzz"); ok {
		t.Fatal("PreMatchFirst with no match should return false")
	}
}

func TestAPI_OptionsAreFunctional(t *testing.T) {
	terms := []string{"abc", "bcd"}
	// 同一份字典用不同 option 构造，匹配集合一致
	ac1 := NewAcAutomaton(terms)
	ac2 := NewAcAutomaton(terms, WithOutputLink())

	query := "xabcdxabcdef"
	r1 := ac1.MatchAll(query)
	r2 := ac2.MatchAll(query)
	if !ttEqualSet(r1, r2, t) {
		t.Fatalf("default vs WithOutputLink results differ: %v vs %v", r1, r2)
	}
}

// ===== 3. MatchAll 与暴力实现的对照（核心正确性） =====

func TestMatchAll_BruteForce_Chinese_LargeScale(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	numTerms := 3000
	terms := make([]string, numTerms)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 2, 6)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 50, ttRandChinese, 1, 3)

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("MatchAll mismatch: got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

func TestMatchAll_BruteForce_Latin(t *testing.T) {
	rng := rand.New(rand.NewSource(101))
	numTerms := 1000
	terms := make([]string, numTerms)
	for i := range terms {
		terms[i] = ttRandLatin(rng, 1, 5)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 80, ttRandLatin, 0, 4)

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("MatchAll mismatch: got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

func TestMatchAll_BruteForce_Mixed(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	numTerms := 1500
	terms := make([]string, numTerms)
	for i := range terms {
		terms[i] = ttRandMixed(rng, 1, 6)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 30, ttRandMixed, 0, 3)

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("MatchAll mismatch: got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

func TestMatchAll_BruteForce_RandomSeeds(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			n := 50 + rng.Intn(500)
			terms := make([]string, n)
			for i := range terms {
				switch rng.Intn(3) {
				case 0:
					terms[i] = ttRandChinese(rng, 1, 5)
				case 1:
					terms[i] = ttRandLatin(rng, 1, 5)
				default:
					terms[i] = ttRandMixed(rng, 1, 5)
				}
			}
			query := ttEmbedTermsIntoQuery(rng, terms, 5+rng.Intn(15), ttRandMixed, 0, 4)

			var opts []Option
			if seed%2 == 0 {
				opts = append(opts, WithOutputLink())
			}
			ac := NewAcAutomaton(terms, opts...)
			got := ac.MatchAll(query)
			want := ttBruteForceMatchAll(query, terms)
			if !ttEqualSet(got, want, t) {
				t.Errorf("seed=%d: AC=%d, BF=%d", seed, len(got), len(want))
				ttDumpDiff(t, got, want)
			}
		})
	}
}

// 子串关系 / 重叠匹配 / 嵌套

func TestMatchAll_SubstringRelations(t *testing.T) {
	terms := []string{
		"中", "中国", "中国人", "中国人民",
		"国", "国人", "民",
		"中国人民共和国",
	}
	queries := []string{
		"中国人民",
		"中国人民共和国",
		"我爱中国人民共和国万岁",
		"中中中国",
	}
	for _, query := range queries {
		q := query
		ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
			got := ac.MatchAll(q)
			want := ttBruteForceMatchAll(q, terms)
			if !ttEqualSet(got, want, t) {
				t.Errorf("query=%q: got=%d want=%d", q, len(got), len(want))
				ttDumpDiff(t, got, want)
			}
		})
	}
}

func TestMatchAll_Overlapping(t *testing.T) {
	// "去北京" / "北京大" / "京大学" 在 "去北京大学" 中位置重叠
	terms := []string{"去北京", "北京大", "京大学"}
	query := "我要去北京大学读书"
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

func TestMatchAll_TermAtBoundaries(t *testing.T) {
	terms := []string{"start", "end", "你好", "再见"}
	queries := []string{
		"start middle end",
		"你好 中间 再见",
		"start", // 整个 query 就是 term
		"你好",
	}
	for _, query := range queries {
		q := query
		ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
			got := ac.MatchAll(q)
			want := ttBruteForceMatchAll(q, terms)
			if !ttEqualSet(got, want, t) {
				t.Errorf("query=%q got=%d want=%d", q, len(got), len(want))
			}
		})
	}
}

// 4. 重复 term / 同一 term 关联多个业务 idx

func TestMatchAll_DuplicateTerms(t *testing.T) {
	// "北京" 出现在 idx 0/2/4，每次匹配应该报告 3 次
	terms := []string{"北京", "上海", "北京", "深圳", "北京", "上海"}
	query := "我在北京和上海，深圳也去过北京。"

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}

		// "北京" 在 query 中出现 2 次 × 3 个 idx = 6 个匹配
		count := 0
		for _, m := range got {
			if m.Term == "北京" {
				count++
			}
		}
		if count != 6 {
			t.Errorf("'北京' 期望 6 次 (2 出现 * 3 idx), 实际 %d", count)
		}

		// MatchAllUnique: 每个 idx 仅 1 次
		uniq := ac.MatchAllUnique(query)
		seen := map[int]bool{}
		for _, m := range uniq {
			if seen[m.TermIdx] {
				t.Errorf("MatchAllUnique 中 TermIdx=%d 出现多次", m.TermIdx)
			}
			seen[m.TermIdx] = true
		}
	})
}

// 5. 单字 / 长 term / Emoji / 多语种

func TestMatchAll_SingleCharTerms(t *testing.T) {
	terms := []string{"我", "你", "他"}
	query := "我和你以及他"
	want := ttBruteForceMatchAll(query, terms)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
		}
	})
}

func TestMatchAll_VeryLongTerms(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	terms := make([]string, 50)
	for i := range terms {
		// 30~80 字
		terms[i] = ttRandChinese(rng, 30, 80)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 5, ttRandChinese, 1, 5)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
		}
	})
}

func TestMatchAll_EmojiAndMultiLang(t *testing.T) {
	terms := []string{"🐱", "🐱猫", "猫", "ねこ", "고양이", "猫🐱"}
	query := "我家有🐱猫，叫ねこ，韩语 고양이"
	want := ttBruteForceMatchAll(query, terms)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

// 一些罕见 4 字节 UTF-8 字符
func TestMatchAll_4ByteUTF8(t *testing.T) {
	// 𝄞 (U+1D11E) 𓀀 (U+13000) 𠀀 (U+20000)
	terms := []string{"𝄞", "𓀀", "𠀀", "𝄞test"}
	query := "abc𝄞test𓀀xyz𠀀end𝄞"
	want := ttBruteForceMatchAll(query, terms)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

// 6. 高扇出 / 索引切换边界

func TestMatchAll_LinearSearchRange(t *testing.T) {
	// 根节点子节点 ≤ 8，触发线性扫描
	terms := []string{"a1", "b1", "c1", "d1", "e1", "f1"}
	query := "test a1 b1 c1 d1 e1 f1 done"
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
		}
	})
}

func TestMatchAll_BinarySearchRange(t *testing.T) {
	// 8 < 子节点 ≤ 2048，触发二分查找；用 16 个不同首字符
	rng := rand.New(rand.NewSource(123))
	terms := make([]string, 0, 100)
	for i := 0; i < 16; i++ {
		first := rune('a' + i)
		for j := 0; j < 5; j++ {
			terms = append(terms, string(first)+ttRandLatin(rng, 1, 4))
		}
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 30, ttRandLatin, 0, 3)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

func TestMatchAll_HashSearchRange_HighFanout(t *testing.T) {
	// 根节点子节点 > 2048，触发哈希索引
	rng := rand.New(rand.NewSource(3000))
	numTerms := 3000
	terms := make([]string, numTerms)
	for i := range terms {
		// 每个 term 用不同汉字开头，保证根的扇出 > 2048
		terms[i] = string(rune(0x4E00+i)) + ttRandChinese(rng, 1, 3)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 25, ttRandChinese, 0, 2)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
		}
	})
}

func TestMatchAll_RootRemappedAfterReorder(t *testing.T) {
	// 构造：根节点扇出小（线性/二分），但树中其他节点扇出 > 2048
	// 这种情况下根会从 index 0 被换到非哈希区
	rng := rand.New(rand.NewSource(4242))
	var terms []string
	// 根节点只有一个分支 'X'
	prefix := "X"
	for i := 0; i < 2100; i++ {
		// "X" + 不同汉字 + 噪声
		terms = append(terms, prefix+string(rune(0x4E00+i))+ttRandChinese(rng, 0, 2))
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 20, ttRandChinese, 0, 2)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
		}
	})
}

// 7. fail 链特殊形态：最坏情况 a, aa, aaa, aaaa...

func TestMatchAll_PathologicalFailChain(t *testing.T) {
	// fail 链最长的经典构造
	terms := []string{"a", "aa", "aaa", "aaaa", "aaaaa"}
	queries := []string{
		"aaaaa",
		"aaaaaaaaa",
		"baaaab",
		strings.Repeat("a", 50),
	}
	for _, query := range queries {
		q := query
		ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
			got := ac.MatchAll(q)
			want := ttBruteForceMatchAll(q, terms)
			if !ttEqualSet(got, want, t) {
				t.Errorf("query=%q got=%d want=%d", q, len(got), len(want))
				ttDumpDiff(t, got, want)
			}
		})
	}
}

func TestMatchAll_Pathological_OutputLinkBenefit(t *testing.T) {
	// 构造 fail 链很长但只有少数 term：验证 outputLink 不漏报
	// 形如：abbbbb...b 链上，term 仅在前后两端
	middle := strings.Repeat("b", 200)
	terms := []string{"a" + middle, middle + "c", "ab"}
	query := "ab" + middle + "ab" + middle + "c"
	want := ttBruteForceMatchAll(query, terms)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		if !ttEqualSet(got, want, t) {
			t.Errorf("got=%d want=%d", len(got), len(want))
			ttDumpDiff(t, got, want)
		}
	})
}

// 8. MatchFirst 语义

func TestMatchFirst_NoMatch(t *testing.T) {
	terms := []string{"苹果", "香蕉", "橘子"}
	query := "我喜欢吃西瓜和葡萄"
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		if _, ok := ac.MatchFirst(query); ok {
			t.Error("MatchFirst should return false")
		}
	})
}

func TestMatchFirst_ReturnsEarliestEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	numTerms := 500
	terms := make([]string, numTerms)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 1, 4)
	}

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		for round := 0; round < 50; round++ {
			var sb strings.Builder
			sb.WriteString(ttRandChinese(rng, 0, 5))
			sb.WriteString(terms[rng.Intn(numTerms)])
			sb.WriteString(ttRandChinese(rng, 0, 5))
			query := sb.String()

			got, ok1 := ac.MatchFirst(query)
			want, ok2 := ttBruteForceMatchFirst(query, terms)
			if ok1 != ok2 {
				t.Fatalf("round %d ok mismatch: ac=%v bf=%v query=%q", round, ok1, ok2, query)
			}
			if !ok1 {
				continue
			}
			// 至少 EndIdx 一致（最早结束位置）
			if got.EndIdx != want.EndIdx {
				t.Errorf("round %d: EndIdx mismatch ac=%d bf=%d query=%q",
					round, got.EndIdx, want.EndIdx, query)
			}
			// 该 EndIdx 上必然存在某个真匹配，且 AC 返回的也是合法匹配
			if query[got.StartIdx:got.EndIdx] != got.Term {
				t.Errorf("round %d: invalid slice %q vs %q",
					round, query[got.StartIdx:got.EndIdx], got.Term)
			}
			if got.Term != terms[got.TermIdx] {
				t.Errorf("round %d: term/idx不一致 %q vs terms[%d]=%q",
					round, got.Term, got.TermIdx, terms[got.TermIdx])
			}
		}
	})
}

func TestMatchFirst_QueryEqualsTerm(t *testing.T) {
	terms := []string{"abc"}
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		m, ok := ac.MatchFirst("abc")
		if !ok {
			t.Fatal("expected match")
		}
		if m.StartIdx != 0 || m.EndIdx != 3 || m.Term != "abc" {
			t.Errorf("unexpected: %+v", m)
		}
	})
}

// 9. MatchAllUnique 语义

func TestMatchAllUnique_NoDuplicate(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	terms := make([]string, 800)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 1, 3)
	}
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		for round := 0; round < 20; round++ {
			query := ttEmbedTermsIntoQuery(rng, terms, 30, ttRandChinese, 0, 2)
			uniq := ac.MatchAllUnique(query)
			seen := make(map[int]bool)
			for _, m := range uniq {
				if seen[m.TermIdx] {
					t.Errorf("TermIdx %d 重复", m.TermIdx)
				}
				seen[m.TermIdx] = true
			}

			// uniq 的 TermIdx 集合 == MatchAll 的 TermIdx 集合
			all := ac.MatchAll(query)
			allSet := make(map[int]bool)
			for _, m := range all {
				allSet[m.TermIdx] = true
			}
			if len(allSet) != len(seen) {
				t.Errorf("set size mismatch all=%d unique=%d", len(allSet), len(seen))
			}
			for k := range allSet {
				if !seen[k] {
					t.Errorf("MatchAll TermIdx %d 在 Unique 中缺失", k)
				}
			}
		}
	})
}

func TestMatchAllUnique_ConsistentWithBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2025))
	terms := make([]string, 200)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 1, 3)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 50, ttRandChinese, 0, 2)

	want := ttBruteForceMatchAllUnique(query, terms)
	wantSet := make(map[int]bool)
	for _, m := range want {
		wantSet[m.TermIdx] = true
	}

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAllUnique(query)
		gotSet := make(map[int]bool)
		for _, m := range got {
			gotSet[m.TermIdx] = true
		}
		if len(gotSet) != len(wantSet) {
			t.Errorf("Unique TermIdx 集大小 got=%d want=%d", len(gotSet), len(wantSet))
		}
		for k := range wantSet {
			if !gotSet[k] {
				t.Errorf("缺少 TermIdx %d", k)
			}
		}
	})
}

// 10. 整型宽度自适应 (uint8 / uint16 / uint32)

func TestWidth_Uint8Range(t *testing.T) {
	// 总 rune 数 < 255，应当走 uint8 分支
	terms := []string{"abc", "abd", "xyz", "你好", "世界"}
	ac := NewAcAutomaton(terms)
	query := "abc abd xyz 你好世界"
	got := ac.MatchAll(query)
	want := ttBruteForceMatchAll(query, terms)
	if !ttEqualSet(got, want, t) {
		t.Errorf("got=%d want=%d", len(got), len(want))
	}
}

func TestWidth_Uint16Range(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	terms := make([]string, 1000)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 2, 4)
	}
	ac := NewAcAutomaton(terms)
	query := ttEmbedTermsIntoQuery(rng, terms, 20, ttRandChinese, 0, 2)
	got := ac.MatchAll(query)
	want := ttBruteForceMatchAll(query, terms)
	if !ttEqualSet(got, want, t) {
		t.Errorf("got=%d want=%d", len(got), len(want))
	}
}

func TestWidth_NodeBoundary254(t *testing.T) {
	// 把 maxNodeCnt 调到 uint8 分支边界附近
	// nodeCnt = 1 + sum(rune count); 我们用 250 个不同的单字汉字 -> 251 nodes
	terms := make([]string, 250)
	for i := range terms {
		terms[i] = string(rune(0x4E00 + i))
	}
	ac := NewAcAutomaton(terms)
	query := string(rune(0x4E00)) + string(rune(0x4E00+1)) + string(rune(0x4E00+249))
	got := ac.MatchAll(query)
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}

func TestWidth_NodeJustOverUint8(t *testing.T) {
	// 强制走到 uint16 分支：~260 个不同首字符
	terms := make([]string, 260)
	for i := range terms {
		terms[i] = string(rune(0x4E00 + i))
	}
	ac := NewAcAutomaton(terms)
	query := string(rune(0x4E00)) + string(rune(0x4E00+259))
	got := ac.MatchAll(query)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}

func TestWidth_LongTermJustOverUint8(t *testing.T) {
	// term 字节数 > 255 应走 uint16 分支（256 字节 = 256 个 ASCII 字符）
	longTerm := strings.Repeat("a", 256)
	terms := []string{longTerm, "ab"}
	ac := NewAcAutomaton(terms)
	query := longTerm + "ab"
	got := ac.MatchAll(query)
	want := ttBruteForceMatchAll(query, terms)
	if !ttEqualSet(got, want, t) {
		t.Errorf("got=%d want=%d", len(got), len(want))
	}
}

// 11. UTF-8 / TermIdx 完整性

func TestUTF8_RuneBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(666))
	terms := make([]string, 500)
	for i := range terms {
		terms[i] = ttRandMixed(rng, 1, 5)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 30, ttRandMixed, 1, 3)

	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		got := ac.MatchAll(query)
		for _, m := range got {
			if !utf8.RuneStart(query[m.StartIdx]) {
				t.Errorf("StartIdx=%d 不在 rune 起始", m.StartIdx)
			}
			if m.EndIdx < len(query) && !utf8.RuneStart(query[m.EndIdx]) {
				t.Errorf("EndIdx=%d 不在 rune 起始", m.EndIdx)
			}
			if !utf8.ValidString(m.Term) {
				t.Errorf("Term=%q 非法 UTF-8", m.Term)
			}
		}
	})
}

func TestTermIdx_Integrity(t *testing.T) {
	terms := make([]string, 100)
	for i := range terms {
		terms[i] = fmt.Sprintf("词条%d号", i)
	}
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		for idx, term := range terms {
			query := "[[" + term + "]]"
			got := ac.MatchAll(query)
			found := false
			for _, m := range got {
				if m.TermIdx == idx {
					found = true
					if m.Term != term {
						t.Errorf("TermIdx %d Term 不一致: got=%q want=%q", idx, m.Term, term)
					}
					expStart := len("[[")
					expEnd := expStart + len(term)
					if m.StartIdx != expStart || m.EndIdx != expEnd {
						t.Errorf("TermIdx %d 位置错误 [%d,%d) vs [%d,%d)",
							idx, m.StartIdx, m.EndIdx, expStart, expEnd)
					}
					break
				}
			}
			if !found {
				t.Errorf("TermIdx %d term=%q 未找到", idx, term)
			}
		}
	})
}

func TestSlicing_QueryEqualsTermSliceFromIndex(t *testing.T) {
	// query[StartIdx:EndIdx] 必然等于 r.Term
	rng := rand.New(rand.NewSource(99))
	terms := make([]string, 200)
	for i := range terms {
		terms[i] = ttRandMixed(rng, 1, 4)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 20, ttRandMixed, 0, 2)
	ttAllOptionVariants(t, terms, func(t *testing.T, ac IAcAutomaton) {
		for _, m := range ac.MatchAll(query) {
			if query[m.StartIdx:m.EndIdx] != m.Term {
				t.Errorf("slice mismatch: got=%q want=%q",
					query[m.StartIdx:m.EndIdx], m.Term)
			}
			if m.EndIdx-m.StartIdx != len(m.Term) {
				t.Errorf("byte length mismatch")
			}
		}
	})
}

// 12. 前缀匹配（Trie）

func TestPreMatch_Basic(t *testing.T) {
	terms := []string{"中", "中国", "中国人", "中国人民", "中国人民共和国"}
	tr := NewTrie(terms)

	m, ok := tr.PreMatchFirst("中国人民共和国万岁")
	if !ok || m.Term != "中" {
		t.Fatalf("PreMatchFirst expected '中', got %+v ok=%v", m, ok)
	}

	all := tr.PreMatchAll("中国人民共和国万岁")
	if len(all) != 5 {
		t.Errorf("PreMatchAll expected 5, got %d", len(all))
	}
	for _, m := range all {
		if m.StartIdx != 0 {
			t.Errorf("PreMatch StartIdx 应为 0: got %d", m.StartIdx)
		}
	}
}

func TestPreMatch_NoMatch(t *testing.T) {
	terms := []string{"abc", "你好"}
	tr := NewTrie(terms)
	if _, ok := tr.PreMatchFirst("xyz"); ok {
		t.Error("PreMatchFirst should be false")
	}
	if matches := tr.PreMatchAll("xyz"); len(matches) != 0 {
		t.Errorf("PreMatchAll should be empty, got %v", matches)
	}
}

func TestPreMatch_Empty(t *testing.T) {
	tr := NewTrie([]string{})
	if _, ok := tr.PreMatchFirst("abc"); ok {
		t.Error("empty trie PreMatchFirst should be false")
	}
}

func TestPreMatch_WithDuplicates(t *testing.T) {
	terms := []string{"abc", "abc", "ab"}
	tr := NewTrie(terms)
	all := tr.PreMatchAll("abcd")
	// "ab" 一次, "abc" 两次 -> 共 3 次
	if len(all) != 3 {
		t.Errorf("expected 3 matches with duplicates, got %d: %+v", len(all), all)
	}
}

// 13. WithOutputLink 一致性 / 性能不变性

func TestOutputLink_ConsistentWithDefault(t *testing.T) {
	// 在多组随机数据上验证 with/without OutputLink 结果完全一致
	for seed := int64(0); seed < 20; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			n := 30 + rng.Intn(300)
			terms := make([]string, n)
			for i := range terms {
				terms[i] = ttRandMixed(rng, 1, 5)
			}
			query := ttEmbedTermsIntoQuery(rng, terms, 10+rng.Intn(20), ttRandMixed, 0, 3)

			ac1 := NewAcAutomaton(terms)
			ac2 := NewAcAutomaton(terms, WithOutputLink())

			r1 := ac1.MatchAll(query)
			r2 := ac2.MatchAll(query)
			if !ttEqualSet(r1, r2, t) {
				t.Errorf("MatchAll differs default=%d outputlink=%d", len(r1), len(r2))
				ttDumpDiff(t, r1, r2)
			}

			u1 := ac1.MatchAllUnique(query)
			u2 := ac2.MatchAllUnique(query)
			s1 := make(map[int]bool)
			s2 := make(map[int]bool)
			for _, m := range u1 {
				s1[m.TermIdx] = true
			}
			for _, m := range u2 {
				s2[m.TermIdx] = true
			}
			if len(s1) != len(s2) {
				t.Errorf("Unique TermIdx set size differs: %d vs %d", len(s1), len(s2))
			}
			for k := range s1 {
				if !s2[k] {
					t.Errorf("OutputLink Unique 缺少 TermIdx %d", k)
				}
			}

			f1, ok1 := ac1.MatchFirst(query)
			f2, ok2 := ac2.MatchFirst(query)
			if ok1 != ok2 {
				t.Errorf("MatchFirst ok mismatch %v vs %v", ok1, ok2)
			}
			if ok1 && f1.EndIdx != f2.EndIdx {
				t.Errorf("MatchFirst EndIdx differs %d vs %d", f1.EndIdx, f2.EndIdx)
			}
		})
	}
}

// 14. 并发查询安全性

func TestConcurrent_QueriesAreSafe(t *testing.T) {
	rng := rand.New(rand.NewSource(31415))
	terms := make([]string, 1000)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 1, 4)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 50, ttRandChinese, 0, 2)

	for _, withLink := range []bool{false, true} {
		withLink := withLink
		t.Run(fmt.Sprintf("with_output_link=%v", withLink), func(t *testing.T) {
			var opts []Option
			if withLink {
				opts = append(opts, WithOutputLink())
			}
			ac := NewAcAutomaton(terms, opts...)
			expected := ttBruteForceMatchAll(query, terms)

			const goroutines = 32
			const iters = 50
			var wg sync.WaitGroup
			errs := make(chan error, goroutines)
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < iters; i++ {
						got := ac.MatchAll(query)
						if !ttEqualSet(got, expected, t) {
							errs <- fmt.Errorf("concurrent mismatch: got=%d want=%d", len(got), len(expected))
							return
						}
						_ = ac.MatchAllUnique(query)
						_, _ = ac.MatchFirst(query)
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// 15. 确定性 / 重复构造

func TestDeterministic_TwoBuildsSameResults(t *testing.T) {
	rng := rand.New(rand.NewSource(8888))
	terms := make([]string, 500)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 1, 5)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 30, ttRandChinese, 0, 2)

	ac1 := NewAcAutomaton(terms, WithOutputLink())
	ac2 := NewAcAutomaton(terms, WithOutputLink())
	r1 := ac1.MatchAll(query)
	r2 := ac2.MatchAll(query)
	if !ttEqualSet(r1, r2, t) {
		t.Error("two identical builds should produce identical match sets")
	}
}

// 16. 模糊测试 (Go fuzz)

func FuzzMatchAll(f *testing.F) {
	seeds := []struct {
		t, q string
	}{
		{"abc|xyz|ab", "ab abc xyz"},
		{"中|中国|国人", "中国人民"},
		{"a", "aaa"},
		{"", ""},
	}
	for _, s := range seeds {
		f.Add(s.t, s.q)
	}
	f.Fuzz(func(t *testing.T, termsBlob string, query string) {
		// 用 '|' 分隔 terms；丢弃 NUL 与不合法 UTF-8 以避免 brute force 报错
		raw := strings.Split(termsBlob, "|")
		terms := make([]string, 0, len(raw))
		for _, s := range raw {
			if utf8.ValidString(s) && len(s) <= 64 {
				terms = append(terms, s)
			}
		}
		if len(terms) == 0 || !utf8.ValidString(query) || len(query) > 256 {
			return
		}
		ac := NewAcAutomaton(terms)
		ac2 := NewAcAutomaton(terms, WithOutputLink())
		got := ac.MatchAll(query)
		gotWithLink := ac2.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Errorf("default mismatch: terms=%v query=%q got=%d want=%d",
				terms, query, len(got), len(want))
		}
		if !ttEqualSet(gotWithLink, want, t) {
			t.Errorf("outputLink mismatch: terms=%v query=%q got=%d want=%d",
				terms, query, len(gotWithLink), len(want))
		}
	})
}

// 17. 内存 sanity check（防止严重退化）

func TestSanity_LargeBuildDoesNotPanic(t *testing.T) {
	// 5 万中文 term，构造与一次匹配应顺利完成
	rng := rand.New(rand.NewSource(2026))
	const n = 50000
	terms := make([]string, n)
	for i := range terms {
		terms[i] = ttRandChinese(rng, 2, 6)
	}
	ac := NewAcAutomaton(terms, WithOutputLink())
	query := ttEmbedTermsIntoQuery(rng, terms, 100, ttRandChinese, 0, 3)
	_ = ac.MatchAll(query)
	_ = ac.MatchAllUnique(query)
	_, _ = ac.MatchFirst(query)
}

// 18. MatchResult 字段类型 sanity

func TestMatchResult_FieldTypes(t *testing.T) {
	// 编译期防回退：确保 MatchResult 字段语义保持稳定
	var r MatchResult
	_ = r.Term
	_ = r.TermIdx
	_ = r.StartIdx
	_ = r.EndIdx
	if unsafe.Sizeof(r.StartIdx) < 8 {
		t.Skip("32-bit platform, skip")
	}
}

// 19. 序列化 / 反序列化基础

// TestSerialize_AcAutomaton_RoundTripBasic 验证 Save/Load 后 AC 在各 API 上与原对象等价。
func TestSerialize_AcAutomaton_RoundTripBasic(t *testing.T) {
	rng := rand.New(rand.NewSource(20260620))
	terms := make([]string, 200)
	for i := range terms {
		terms[i] = ttRandMixed(rng, 1, 6)
	}
	queries := []string{
		ttEmbedTermsIntoQuery(rng, terms, 10, ttRandMixed, 0, 3),
		ttEmbedTermsIntoQuery(rng, terms, 8, ttRandMixed, 0, 2),
		"纯中文" + ttRandChinese(rng, 1, 4),
		"only english words here",
	}

	for _, withLink := range []bool{false, true} {
		withLink := withLink
		t.Run(fmt.Sprintf("with_output_link=%v", withLink), func(t *testing.T) {
			var opts []Option
			if withLink {
				opts = append(opts, WithOutputLink())
			}
			ac := NewAcAutomaton(terms, opts...)

			var buf bytes.Buffer
			if err := ac.Save(&buf); err != nil {
				t.Fatalf("Save failed: %v", err)
			}
			ac2, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("LoadAcAutomaton failed: %v", err)
			}

			for _, query := range queries {
				got1 := ac.MatchAll(query)
				got2 := ac2.MatchAll(query)
				want := ttBruteForceMatchAll(query, terms)
				if !ttEqualSet(got1, want, t) {
					t.Fatalf("original mismatch with brute-force: got=%d want=%d", len(got1), len(want))
				}
				if !ttEqualSet(got2, want, t) {
					t.Fatalf("loaded mismatch with brute-force: got=%d want=%d", len(got2), len(want))
				}

				// MatchAllUnique 结果集合等价
				uniq1 := ac.MatchAllUnique(query)
				uniq2 := ac2.MatchAllUnique(query)
				set1 := make(map[int]struct{}, len(uniq1))
				set2 := make(map[int]struct{}, len(uniq2))
				for _, m := range uniq1 {
					set1[m.TermIdx] = struct{}{}
				}
				for _, m := range uniq2 {
					set2[m.TermIdx] = struct{}{}
				}
				if len(set1) != len(set2) {
					t.Fatalf("Unique TermIdx set size mismatch: original=%d loaded=%d", len(set1), len(set2))
				}
				for k := range set1 {
					if _, ok := set2[k]; !ok {
						t.Fatalf("Unique TermIdx %d missing after load", k)
					}
				}

				// MatchFirst 结果相同
				m1, ok1 := ac.MatchFirst(query)
				m2, ok2 := ac2.MatchFirst(query)
				if ok1 != ok2 {
					t.Fatalf("MatchFirst ok mismatch: %v vs %v", ok1, ok2)
				}
				if ok1 {
					if m1.StartIdx != m2.StartIdx || m1.EndIdx != m2.EndIdx || m1.TermIdx != m2.TermIdx || m1.Term != m2.Term {
						t.Fatalf("MatchFirst result mismatch: %#v vs %#v", m1, m2)
					}
				}
			}
		})
	}
}

// TestSerialize_Trie_RoundTripBasic 验证 Trie Save/Load 在前缀匹配语义上一致。
func TestSerialize_Trie_RoundTripBasic(t *testing.T) {
	terms := []string{"中", "中国", "中国人", "中国人民", "abc", "abcd"}
	tr := NewTrie(terms)

	var buf bytes.Buffer
	if err := tr.Save(&buf); err != nil {
		t.Fatalf("Trie Save failed: %v", err)
	}
	loaded, err := LoadTrie(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadTrie failed: %v", err)
	}

	queries := []string{
		"中国人民共和国万岁",
		"abcde",
		"xyz",
	}

	for _, query := range queries {
		p1, ok1 := tr.PreMatchFirst(query)
		p2, ok2 := loaded.PreMatchFirst(query)
		if ok1 != ok2 {
			t.Fatalf("PreMatchFirst ok mismatch for %q: %v vs %v", query, ok1, ok2)
		}
		if ok1 {
			if p1.Term != p2.Term || p1.StartIdx != p2.StartIdx || p1.EndIdx != p2.EndIdx || p1.TermIdx != p2.TermIdx {
				t.Fatalf("PreMatchFirst result mismatch for %q: %#v vs %#v", query, p1, p2)
			}
		}

		all1 := tr.PreMatchAll(query)
		all2 := loaded.PreMatchAll(query)
		if !ttEqualSet(all1, all2, t) {
			t.Fatalf("PreMatchAll mismatch for %q: %v vs %v", query, all1, all2)
		}
	}
}

// 20. 所有整型宽度的序列化分支

func ttSerializeAcWidth[U uints](t *testing.T) {
	t.Helper()
	terms := []string{"hello", "world", "中国", "人民"}
	var opts option
	opts.withOutputLink = true

	var ac acAutomaton[U]
	ac.build(terms, &opts)

	var buf bytes.Buffer
	if err := ac.Save(&buf); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadAcAutomaton failed: %v", err)
	}

	query := "hello, 中国人民!"
	got := loaded.MatchAll(query)
	want := ttBruteForceMatchAll(query, terms)
	if !ttEqualSet(got, want, t) {
		t.Fatalf("width %T round-trip mismatch: got=%d want=%d", U(0), len(got), len(want))
	}
}

func TestSerialize_AcAutomaton_AllWidths(t *testing.T) {
	t.Run("uint8", func(t *testing.T) { ttSerializeAcWidth[uint8](t) })
	t.Run("uint16", func(t *testing.T) { ttSerializeAcWidth[uint16](t) })
	t.Run("uint32", func(t *testing.T) { ttSerializeAcWidth[uint32](t) })
	t.Run("uint64", func(t *testing.T) { ttSerializeAcWidth[uint64](t) })
}

func ttSerializeTrieWidth[U uints](t *testing.T) {
	t.Helper()
	terms := []string{"a", "ab", "abc", "中国", "中国人"}
	var opts option

	var ct compactTrie[U]
	ct.build(terms, &opts)

	var buf bytes.Buffer
	if err := ct.Save(&buf); err != nil {
		t.Fatalf("Trie Save failed: %v", err)
	}

	loaded, err := LoadTrie(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadTrie failed: %v", err)
	}

	query := "中国人民abc"
	orig := ct.PreMatchAll(query)
	rt := loaded.PreMatchAll(query)
	if !ttEqualSet(orig, rt, t) {
		t.Fatalf("Trie width %T PreMatchAll mismatch: orig=%v rt=%v", U(0), orig, rt)
	}
}

func TestSerialize_Trie_AllWidths(t *testing.T) {
	t.Run("uint8", func(t *testing.T) { ttSerializeTrieWidth[uint8](t) })
	t.Run("uint16", func(t *testing.T) { ttSerializeTrieWidth[uint16](t) })
	t.Run("uint32", func(t *testing.T) { ttSerializeTrieWidth[uint32](t) })
	t.Run("uint64", func(t *testing.T) { ttSerializeTrieWidth[uint64](t) })
}

// 21. outputLink 持久化与别名关系

func ttAssertOutputLinkAlias[U uints](t *testing.T, ac *acAutomaton[U], withOutputLink bool) {
	t.Helper()
	if len(ac.fail) == 0 {
		t.Fatalf("ac.fail is empty")
	}
	sameBacking := unsafe.SliceData(ac.fail) == unsafe.SliceData(ac.outputLink)
	if withOutputLink && sameBacking {
		t.Errorf("WithOutputLink=true, but fail and outputLink share backing array after load")
	}
	if !withOutputLink && !sameBacking {
		t.Errorf("WithOutputLink=false, but fail and outputLink are not aliased after load")
	}
}

func TestSerialize_AcAutomaton_OutputLinkPreserved(t *testing.T) {
	terms := []string{"abc", "abcd", "中国", "中国人"}
	for _, withLink := range []bool{false, true} {
		withLink := withLink
		t.Run(fmt.Sprintf("with_output_link=%v", withLink), func(t *testing.T) {
			var opts []Option
			if withLink {
				opts = append(opts, WithOutputLink())
			}
			ac := NewAcAutomaton(terms, opts...)
			query := "我爱中国abc"
			want := ttBruteForceMatchAll(query, terms)

			var buf bytes.Buffer
			if err := ac.Save(&buf); err != nil {
				t.Fatalf("Save failed: %v", err)
			}
			loaded, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("LoadAcAutomaton failed: %v", err)
			}

			// 验证匹配语义一致
			got := loaded.MatchAll(query)
			if !ttEqualSet(got, want, t) {
				t.Fatalf("MatchAll mismatch after load: got=%d want=%d", len(got), len(want))
			}

			// 验证 fail/outputLink 的别名关系
			switch impl := loaded.(type) {
			case *acAutomaton[uint8]:
				ttAssertOutputLinkAlias(t, impl, withLink)
			case *acAutomaton[uint16]:
				ttAssertOutputLinkAlias(t, impl, withLink)
			case *acAutomaton[uint32]:
				ttAssertOutputLinkAlias(t, impl, withLink)
			case *acAutomaton[uint64]:
				ttAssertOutputLinkAlias(t, impl, withLink)
			default:
				t.Fatalf("unexpected concrete AC type %T", impl)
			}
		})
	}
}

// 22. 大规模序列化 / 并发安全 / 体积 sanity

func TestSerialize_AcAutomaton_LargeScaleAndSize(t *testing.T) {
	if testing.Short() {
		t.Skip("skip large-scale serialize test in -short mode")
	}
	rng := rand.New(rand.NewSource(20260621))
	const n = 20000
	terms := make([]string, n)
	totalBytes := 0
	for i := range terms {
		terms[i] = ttRandChinese(rng, 2, 6)
		totalBytes += len(terms[i])
	}
	ac := NewAcAutomaton(terms, WithOutputLink())

	query := ttEmbedTermsIntoQuery(rng, terms, 80, ttRandChinese, 0, 3)
	expected := ttBruteForceMatchAll(query, terms)

	var buf bytes.Buffer
	if err := ac.Save(&buf); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	data := buf.Bytes()
	if len(data) == 0 {
		t.Fatalf("serialized data is empty")
	}
	// 给一个宽松上限，避免严重退化（例如数十倍于原始数据）
	if len(data) > totalBytes*64 && len(data) > 2*1024*1024 {
		t.Fatalf("serialized data too large: size=%d, totalTermBytes=%d", len(data), totalBytes)
	}

	loaded, err := LoadAcAutomaton(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("LoadAcAutomaton failed: %v", err)
	}
	got := loaded.MatchAll(query)
	if !ttEqualSet(got, expected, t) {
		t.Fatalf("large-scale round-trip mismatch: got=%d want=%d", len(got), len(expected))
	}

	// 反序列化后的并发查询安全
	const goroutines = 8
	const iters = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if !ttEqualSet(loaded.MatchAll(query), expected, t) {
					errs <- fmt.Errorf("concurrent mismatch after round-trip")
					return
				}
				_ = loaded.MatchAllUnique(query)
				_, _ = loaded.MatchFirst(query)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// 23. SaveToFile / LoadFromFile 生命周期

func TestSerialize_SaveToFileAndLoadFromFile(t *testing.T) {
	terms := []string{"abc", "abcd", "中国", "中国人"}
	tr := NewTrie(terms)
	ac := NewAcAutomaton(terms, WithOutputLink())

	tmpDir := t.TempDir()
	trieFile := filepath.Join(tmpDir, "trie.bin")
	acFile := filepath.Join(tmpDir, "ac.bin")

	if err := tr.SaveToFile(trieFile); err != nil {
		t.Fatalf("Trie SaveToFile failed: %v", err)
	}
	if err := ac.SaveToFile(acFile); err != nil {
		t.Fatalf("AC SaveToFile failed: %v", err)
	}

	tr2, err := LoadTrieFromFile(trieFile)
	if err != nil {
		t.Fatalf("LoadTrieFromFile failed: %v", err)
	}
	ac2, err := LoadAcAutomatonFromFile(acFile)
	if err != nil {
		t.Fatalf("LoadAcAutomatonFromFile failed: %v", err)
	}

	query := "中国人民abc"
	origPre := tr.PreMatchAll(query)
	rtPre := tr2.PreMatchAll(query)
	if !ttEqualSet(origPre, rtPre, t) {
		t.Fatalf("PreMatchAll mismatch after file round-trip: %v vs %v", origPre, rtPre)
	}

	origAll := ac.MatchAll(query)
	rtAll := ac2.MatchAll(query)
	if !ttEqualSet(origAll, rtAll, t) {
		t.Fatalf("MatchAll mismatch after file round-trip: got=%d want=%d", len(rtAll), len(origAll))
	}
}

// 24. 异常输入处理：截断 / 非法 magic

func TestSerialize_AcAutomaton_ErrorHandling(t *testing.T) {
	terms := []string{"abc", "中国", "人民"}
	ac := NewAcAutomaton(terms, WithOutputLink())

	var buf bytes.Buffer
	if err := ac.Save(&buf); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	data := buf.Bytes()

	mutBadType := append([]byte{}, data...)
	if len(mutBadType) > 0 {
		mutBadType[0] = 0xFF
	}
	mutBadSize := append([]byte{}, data...)
	if len(mutBadSize) > 1 {
		mutBadSize[1] = 3 // 非法 dSize
	}

	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"short_header", data[:1]},
		{"truncated_body", data[:len(data)/2]},
		{"bad_type", mutBadType},
		{"bad_dsize", mutBadSize},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, err := LoadAcAutomaton(bytes.NewReader(c.raw)); err == nil {
				t.Fatalf("expected error for case %s", c.name)
			}
		})
	}
}

func TestSerialize_Trie_ErrorHandling(t *testing.T) {
	terms := []string{"abc", "中国", "人民"}
	tr := NewTrie(terms)

	var buf bytes.Buffer
	if err := tr.Save(&buf); err != nil {
		t.Fatalf("Trie Save failed: %v", err)
	}
	data := buf.Bytes()

	mutBadType := append([]byte{}, data...)
	if len(mutBadType) > 0 {
		mutBadType[0] = 0xFF
	}
	mutBadSize := append([]byte{}, data...)
	if len(mutBadSize) > 1 {
		mutBadSize[1] = 7 // 非法 dSize
	}

	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"short_header", data[:1]},
		{"truncated_body", data[:len(data)/2]},
		{"bad_type", mutBadType},
		{"bad_dsize", mutBadSize},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, err := LoadTrie(bytes.NewReader(c.raw)); err == nil {
				t.Fatalf("expected error for case %s", c.name)
			}
		})
	}
}

func TestSerialize_LoadAcAutomaton_OnTrieShouldError(t *testing.T) {
	terms := []string{"abc", "中国"}
	tr := NewTrie(terms)
	var buf bytes.Buffer
	if err := tr.Save(&buf); err != nil {
		t.Fatalf("Trie Save failed: %v", err)
	}
	if _, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected error when loading trie bytes via LoadAcAutomaton")
	}
}

// 25. 多轮 round-trip 稳定性

func TestSerialize_AcAutomaton_RepeatedRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20260622))
	terms := make([]string, 300)
	for i := range terms {
		terms[i] = ttRandMixed(rng, 1, 5)
	}
	query := ttEmbedTermsIntoQuery(rng, terms, 20, ttRandMixed, 0, 3)

	expected := ttBruteForceMatchAll(query, terms)
	ac, _ := NewAcAutomaton(terms, WithOutputLink()).(IAcAutomaton)

	for round := 0; round < 3; round++ {
		var buf bytes.Buffer
		if err := ac.Save(&buf); err != nil {
			t.Fatalf("round %d Save failed: %v", round, err)
		}
		var err error
		ac, err = LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("round %d LoadAcAutomaton failed: %v", round, err)
		}
		got := ac.MatchAll(query)
		if !ttEqualSet(got, expected, t) {
			t.Fatalf("round %d mismatch after round-trip: got=%d want=%d", round, len(got), len(expected))
		}
	}
}

// 26. Fuzz：序列化 round-trip

func FuzzRoundTripAcAutomaton(f *testing.F) {
	seeds := []struct {
		terms string
		query string
	}{
		{"abc|xyz|ab", "ab abc xyz"},
		{"中|中国|国人", "中国人民"},
		{"a", "aaa"},
		{"", ""},
	}
	for _, s := range seeds {
		f.Add(s.terms, s.query)
	}

	f.Fuzz(func(t *testing.T, termsBlob string, query string) {
		raw := strings.Split(termsBlob, "|")
		terms := make([]string, 0, len(raw))
		for _, s := range raw {
			if utf8.ValidString(s) && len(s) <= 64 {
				terms = append(terms, s)
			}
		}
		if len(terms) == 0 || !utf8.ValidString(query) || len(query) > 256 {
			return
		}

		ac := NewAcAutomaton(terms, WithOutputLink())
		var buf bytes.Buffer
		if err := ac.Save(&buf); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
		ac2, err := LoadAcAutomaton(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("LoadAcAutomaton failed: %v", err)
		}

		got := ac2.MatchAll(query)
		want := ttBruteForceMatchAll(query, terms)
		if !ttEqualSet(got, want, t) {
			t.Fatalf("after round-trip mismatch: got=%d want=%d", len(got), len(want))
		}
	})
}
