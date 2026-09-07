package ahocorasick

import (
	"bytes"
	"fmt"
	"maps"
	"math/rand"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

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

func testAssertResults(t *testing.T, query string, got, want []MatchResult) {
	t.Helper()
	for _, result := range got {
		if result.StartIdx < 0 || result.StartIdx > result.EndIdx || result.EndIdx > len(query) {
			t.Fatalf("invalid result range: %+v queryLen=%d", result, len(query))
		}
		if query[result.StartIdx:result.EndIdx] != result.Term {
			t.Fatalf("result term mismatch: %+v", result)
		}
	}
	gotKeys, wantKeys := testSortedKeys(got), testSortedKeys(want)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("result mismatch:\n got: %v\nwant: %v", gotKeys, wantKeys)
	}
}

func testAssertFirst(t *testing.T, query string, got MatchResult, ok bool, all []MatchResult) {
	t.Helper()
	if len(all) == 0 {
		if ok {
			t.Fatalf("unexpected first result: %+v", got)
		}
		return
	}
	if !ok {
		t.Fatal("expected first result")
	}
	minEnd := all[0].EndIdx
	found := false
	for _, result := range all {
		if result.EndIdx < minEnd {
			minEnd = result.EndIdx
		}
		if testKey(result) == testKey(got) {
			found = true
		}
	}
	if !found || got.EndIdx != minEnd {
		t.Fatalf("invalid first result: got=%+v minEnd=%d", got, minEnd)
	}
	testAssertResults(t, query, []MatchResult{got}, []MatchResult{got})
}

func testValidSegmentEnd(s string, start int) int {
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		start += size
	}
	return start
}

func testNaiveAll(query string, terms []string, withTermIdx bool) []MatchResult {
	var results []MatchResult
	for segmentStart := 0; segmentStart < len(query); {
		r, size := utf8.DecodeRuneInString(query[segmentStart:])
		if r == utf8.RuneError && size == 1 {
			segmentStart++
			continue
		}
		segmentEnd := testValidSegmentEnd(query, segmentStart)
		for start := segmentStart; start < segmentEnd; {
			seenTerms := make(map[string]bool)
			for termIdx, term := range terms {
				if term == "" || !utf8.ValidString(term) || len(term) > segmentEnd-start || (!withTermIdx && seenTerms[term]) {
					continue
				}
				if query[start:start+len(term)] == term {
					resultTermIdx := -1
					if withTermIdx {
						resultTermIdx = termIdx
					}
					results = append(results, MatchResult{
						Term: term, TermIdx: resultTermIdx, StartIdx: start, EndIdx: start + len(term),
					})
					seenTerms[term] = true
				}
			}
			_, width := utf8.DecodeRuneInString(query[start:])
			start += width
		}
		segmentStart = segmentEnd
	}
	return results
}

func testNaivePrefix(query string, terms []string, withTermIdx bool) []MatchResult {
	if len(query) == 0 {
		return nil
	}
	segmentEnd := testValidSegmentEnd(query, 0)
	var results []MatchResult
	seenTerms := make(map[string]bool)
	for termIdx, term := range terms {
		if term == "" || !utf8.ValidString(term) || len(term) > segmentEnd || (!withTermIdx && seenTerms[term]) {
			continue
		}
		if query[:len(term)] == term {
			resultTermIdx := -1
			if withTermIdx {
				resultTermIdx = termIdx
			}
			results = append(results, MatchResult{
				Term: term, TermIdx: resultTermIdx, StartIdx: 0, EndIdx: len(term),
			})
			seenTerms[term] = true
		}
	}
	return results
}

func testAssertUnique(t *testing.T, query string, got, all []MatchResult, withTermIdx bool) {
	t.Helper()
	available := make(map[testMatchKey]bool, len(all))
	type identity struct {
		term    string
		termIdx int
	}
	want := make(map[identity]bool)
	for _, result := range all {
		available[testKey(result)] = true
		id := identity{term: result.Term}
		if withTermIdx {
			id.termIdx = result.TermIdx
		}
		want[id] = true
	}
	seen := make(map[identity]bool, len(got))
	for _, result := range got {
		id := identity{term: result.Term}
		if withTermIdx {
			id.termIdx = result.TermIdx
		}
		if seen[id] {
			t.Fatalf("duplicate term in MatchAllUnique: %+v", id)
		}
		if !available[testKey(result)] {
			t.Fatalf("MatchAllUnique returned a non-match: %+v", result)
		}
		seen[id] = true
	}
	if !maps.Equal(seen, want) {
		t.Fatalf("unique term mismatch: got=%v want=%v", seen, want)
	}
	testAssertResults(t, query, got, got)
}

func testOptions(fastBuild, outputLink, withTermIdx bool, dtype DType) []Option {
	var options []Option
	if fastBuild {
		options = append(options, WithFastBuild())
	}
	if outputLink {
		options = append(options, WithOutputLink())
	}
	if withTermIdx {
		options = append(options, WithTermIdx())
	}
	if dtype != DTypeAuto {
		options = append(options, WithDType(dtype))
	}
	return options
}

func testExerciseMatchers(t *testing.T, terms []string, queries []string) {
	t.Helper()
	for _, withTermIdx := range []bool{false, true} {
		for _, fastBuild := range []bool{false, true} {
			name := fmt.Sprintf("termIdx=%v/fast=%v", withTermIdx, fastBuild)
			t.Run("trie/"+name, func(t *testing.T) {
				trie := NewTrie(terms, testOptions(fastBuild, false, withTermIdx, DTypeAuto)...)
				for i, query := range queries {
					t.Run(fmt.Sprintf("query=%d", i), func(t *testing.T) {
						want := testNaivePrefix(query, terms, withTermIdx)
						testAssertResults(t, query, trie.PreMatchAll(query), want)
						got, ok := trie.PreMatchFirst(query)
						testAssertFirst(t, query, got, ok, want)
					})
				}
			})
			for _, outputLink := range []bool{false, true} {
				name := fmt.Sprintf("termIdx=%v/fast=%v/output=%v", withTermIdx, fastBuild, outputLink)
				t.Run("ac/"+name, func(t *testing.T) {
					ac := NewAcAutomaton(terms, testOptions(fastBuild, outputLink, withTermIdx, DTypeAuto)...)
					for i, query := range queries {
						t.Run(fmt.Sprintf("query=%d", i), func(t *testing.T) {
							wantAll := testNaiveAll(query, terms, withTermIdx)
							testAssertResults(t, query, ac.MatchAll(query), wantAll)
							got, ok := ac.MatchFirst(query)
							testAssertFirst(t, query, got, ok, wantAll)
							testAssertUnique(t, query, ac.MatchAllUnique(query), wantAll, withTermIdx)
						})
					}
				})
			}
		}
	}
}

func TestMatching(t *testing.T) {
	invalid := string([]byte{0xff})
	terms := []string{
		"", invalid, "he", "she", "hers", "his", "中", "中国", "国", "中国人", "国人", "人",
		"a", "aa", "aaa", "ab", "ab", "ba", "cba", "dcba", "\x00", "a\x00b", "�", string(utf8.MaxRune),
	}
	queries := []string{
		"", "中国人", "ushers中国", "aaaa", "dcba dcba", "abab", "x\x00a\x00b", string(utf8.MaxRune),
		"a" + invalid + "b ab�hishe", invalid + invalid + "abc", invalid + "�", "nothing",
	}
	testExerciseMatchers(t, terms, queries)
	t.Run("empty_terms", func(t *testing.T) {
		testExerciseMatchers(t, nil, []string{"", "x", invalid, "中国"})
	})
	t.Run("no_valid_terms", func(t *testing.T) {
		testExerciseMatchers(t, []string{"", invalid}, []string{"", "x", invalid})
	})

	alphabet := []rune("abc中文你我他�")
	for seed := int64(0); seed < 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		terms := make([]string, 24)
		for i := range terms {
			switch {
			case i%11 == 0:
				terms[i] = ""
			case i%13 == 0:
				terms[i] = string([]byte{'x', 0xff})
			case i > 0 && i%7 == 0:
				terms[i] = terms[i-1]
			default:
				length := 1 + rng.Intn(5)
				runes := make([]rune, length)
				for j := range runes {
					runes[j] = alphabet[rng.Intn(len(alphabet))]
				}
				terms[i] = string(runes)
			}
		}
		var query strings.Builder
		for i := 0; i < 80; i++ {
			switch rng.Intn(12) {
			case 0:
				query.WriteByte(0xff)
			case 1, 2:
				term := terms[rng.Intn(len(terms))]
				if utf8.ValidString(term) {
					query.WriteString(term)
				}
			default:
				query.WriteRune(alphabet[rng.Intn(len(alphabet))])
			}
		}
		t.Run(fmt.Sprintf("random/seed=%d", seed), func(t *testing.T) {
			testExerciseMatchers(t, terms, []string{query.String()})
		})
	}
}

func TestBuildBoundaries(t *testing.T) {
	indexedTerm := func(index int) []string {
		terms := make([]string, index+1)
		terms[index] = "x"
		return terms
	}
	ignoredTail := func(length int) []string {
		terms := make([]string, length)
		terms[0] = "x"
		for i := 1; i < len(terms); i++ {
			terms[i] = string([]byte{0xff})
		}
		return terms
	}
	distinctTerms := func(count int) []string {
		terms := make([]string, count)
		for i := range terms {
			terms[i] = string(rune(0x4e00 + i))
		}
		return terms
	}
	cases := []struct {
		name    string
		terms   []string
		options []Option
		want    DType
	}{
		{"uint8_node_limit", []string{strings.Repeat("a", 254)}, nil, DTypeUint8},
		{"uint16_node_limit", []string{strings.Repeat("a", 255)}, nil, DTypeUint16},
		{"uint8_byte_length", []string{strings.Repeat("中", 85)}, nil, DTypeUint8},
		{"uint8_byte_length_fast", []string{strings.Repeat("中", 85)}, []Option{WithFastBuild()}, DTypeUint8},
		{"uint16_byte_length", []string{strings.Repeat("中", 86)}, nil, DTypeUint16},
		{"uint8_term_storage", distinctTerms(127), []Option{WithTermIdx()}, DTypeUint8},
		{"uint16_term_storage", distinctTerms(128), []Option{WithTermIdx()}, DTypeUint16},
		{"uint8_term_index_low", indexedTerm(254), []Option{WithTermIdx()}, DTypeUint8},
		{"uint8_term_index_fast", indexedTerm(254), []Option{WithFastBuild(), WithTermIdx()}, DTypeUint8},
		{"uint16_term_index", indexedTerm(255), []Option{WithFastBuild(), WithTermIdx()}, DTypeUint16},
		{"uint16_large_term_index", indexedTerm(65534), []Option{WithFastBuild(), WithTermIdx()}, DTypeUint16},
		{"uint32_large_term_index", indexedTerm(65535), []Option{WithFastBuild(), WithTermIdx()}, DTypeUint32},
		{"ignored_terms_do_not_widen", ignoredTail(300), []Option{WithFastBuild(), WithTermIdx()}, DTypeUint8},
		{"nil_option", []string{"x"}, []Option{nil}, DTypeUint8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var params buildParams
			params.init(tc.terms, tc.options...)
			if params.dType != tc.want {
				t.Errorf("dtype mismatch: got=%v want=%v", params.dType, tc.want)
			}
			for termIdx, term := range tc.terms {
				if term == "" || !utf8.ValidString(term) {
					continue
				}
				wantIdx := -1
				if params.withTermIdx {
					wantIdx = termIdx
				}
				got, ok := NewTrie(tc.terms, tc.options...).PreMatchFirst(term)
				if !ok || got.TermIdx != wantIdx {
					t.Fatalf("term index mismatch: got=%+v ok=%v want=%d", got, ok, wantIdx)
				}
				break
			}
		})
	}

	for _, fastBuild := range []bool{false, true} {
		t.Run(fmt.Sprintf("compressed_terms/fast=%v", fastBuild), func(t *testing.T) {
			terms := []string{"a", "a", "ab", "中", "", string([]byte{0xff})}
			options := testOptions(fastBuild, false, true, DTypeAuto)
			var params buildParams
			params.init(terms, options...)
			trie := newCompactTrie[uint16](terms, &params)
			if got, want := len(trie.terms), 7; got != want { // 4 个合法 term + 3 个终止节点的共享长度
				t.Fatalf("compressed terms length: got=%d want=%d", got, want)
			}
			for p := range trie.nodes {
				termLen, termIndexes := trie.getTerms(uint16(p))
				for _, termIdx := range termIndexes {
					if int(termIdx) >= len(terms) || len(terms[termIdx]) != int(termLen) {
						t.Fatalf("invalid compressed term at node %d: len=%d indexes=%v", p, termLen, termIndexes)
					}
				}
			}
		})
	}

	branchCases := []struct {
		name       string
		childCount int
		collide    bool
	}{
		{"linear_min", 1, false},
		{"linear", 8, false},
		{"binary_min", 9, false},
		{"binary_max", 2048, false},
		{"hash_min", 2049, false},
		{"hash_collision", 2049, true},
	}
	for _, tc := range branchCases {
		terms := make([]string, 0, tc.childCount)
		if tc.collide {
			for bucket := rune(1); len(terms) < tc.childCount; bucket++ {
				for r := bucket; r <= utf8.MaxRune && len(terms) < tc.childCount; r += rune(tc.childCount) {
					if utf8.ValidRune(r) {
						terms = append(terms, string(r))
					}
				}
			}
		} else {
			for i := 0; i < tc.childCount; i++ {
				terms = append(terms, string(rune(0x1000+i)))
			}
		}

		for _, fastBuild := range []bool{false, true} {
			name := fmt.Sprintf("%s/fast=%v", tc.name, fastBuild)
			t.Run(name, func(t *testing.T) {
				options := testOptions(fastBuild, false, true, DTypeAuto)
				var params buildParams
				params.init(terms, options...)
				trie := newCompactTrie[uint32](terms, &params)
				indices := []int{0, tc.childCount / 2, tc.childCount - 1}
				if tc.collide {
					indices = make([]int, tc.childCount)
					for i := range indices {
						indices[i] = i
					}
					if len(trie.overflowNodes) != 1 || len(trie.overflowEdges) != 1 {
						t.Fatalf("unexpected hash layout: nodes=%d overflows=%d", len(trie.overflowNodes), len(trie.overflowEdges))
					}
					slots := make(map[uint32]struct{}, tc.childCount)
					for _, term := range terms {
						r, _ := utf8.DecodeRuneInString(term)
						slots[uint32(r)%uint32(tc.childCount)] = struct{}{}
					}
					if got, want := len(trie.overflowEdges[0]), tc.childCount-len(slots); got != want {
						t.Fatalf("overflow size mismatch: got=%d want=%d", got, want)
					}
				}
				for _, index := range indices {
					query := terms[index]
					got, ok := trie.PreMatchFirst(query)
					if !ok || got.TermIdx != index || got.Term != query {
						t.Fatalf("branch lookup failed at index %d: %+v ok=%v", index, got, ok)
					}
				}
				if tc.collide {
					ac := NewAcAutomaton(terms, options...)
					for index, query := range terms {
						got, ok := ac.MatchFirst(query)
						if !ok || got.TermIdx != index || got.Term != query {
							t.Fatalf("AC branch lookup failed at index %d: %+v ok=%v", index, got, ok)
						}
					}
				}
			})
		}
	}
}

func TestSerializationAndConcurrency(t *testing.T) {
	terms := []string{"", "中", "中国", "he", "she", "hers", "ab", "ab", "�"}
	query := "ushers中国 ab�"
	configs := []struct {
		name       string
		options    []Option
		concurrent bool
	}{
		{"auto/low", nil, false},
		{"uint8/fast/output/term_idx", testOptions(true, true, true, DTypeUint8), false},
		{"uint16/low/output", testOptions(false, true, false, DTypeUint16), false},
		{"uint32/fast/term_idx", testOptions(true, false, true, DTypeUint32), false},
		{"uint64/fast/output/term_idx/concurrent", testOptions(true, true, true, DTypeUint64), true},
	}
	for _, tc := range configs {
		t.Run(tc.name, func(t *testing.T) {
			ac := NewAcAutomaton(terms, tc.options...)
			want := ac.MatchAll(query)
			var data bytes.Buffer
			if err := ac.Save(&data); err != nil {
				t.Fatalf("save AC: %v", err)
			}
			loaded, err := LoadAcAutomaton(bytes.NewReader(data.Bytes()))
			if err != nil {
				t.Fatalf("load AC: %v", err)
			}
			testAssertResults(t, query, loaded.MatchAll(query), want)
			if !tc.concurrent {
				return
			}

			wantFirst, wantFirstOK := loaded.MatchFirst(query)
			wantUnique := testSortedKeys(loaded.MatchAllUnique(query))
			var wg sync.WaitGroup
			errs := make(chan string, 8)
			for i := 0; i < cap(errs); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 50; i++ {
						if !slices.Equal(testSortedKeys(loaded.MatchAll(query)), testSortedKeys(want)) {
							errs <- "concurrent MatchAll mismatch"
							return
						}
						gotFirst, gotFirstOK := loaded.MatchFirst(query)
						if gotFirstOK != wantFirstOK || testKey(gotFirst) != testKey(wantFirst) {
							errs <- "concurrent MatchFirst mismatch"
							return
						}
						if got := testSortedKeys(loaded.MatchAllUnique(query)); !slices.Equal(got, wantUnique) {
							errs <- "concurrent MatchAllUnique mismatch"
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}

	trie := NewTrie(terms)
	var data bytes.Buffer
	if err := trie.Save(&data); err != nil {
		t.Fatalf("save trie: %v", err)
	}
	loadedTrie, err := LoadTrie(bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatalf("load trie: %v", err)
	}
	testAssertResults(t, "中国人", loadedTrie.PreMatchAll("中国人"), trie.PreMatchAll("中国人"))

	dir := t.TempDir()
	trieFile := filepath.Join(dir, "trie.bin")
	if err := trie.SaveToFile(trieFile); err != nil {
		t.Fatalf("save trie file: %v", err)
	}
	fileTrie, err := LoadTrieFromFile(trieFile)
	if err != nil {
		t.Fatalf("load trie file: %v", err)
	}
	testAssertResults(t, "中国人", fileTrie.PreMatchAll("中国人"), trie.PreMatchAll("中国人"))

	acFile := filepath.Join(dir, "ac.bin")
	ac := NewAcAutomaton(terms, WithFastBuild(), WithOutputLink())
	if err := ac.SaveToFile(acFile); err != nil {
		t.Fatalf("save AC file: %v", err)
	}
	fileAC, err := LoadAcAutomatonFromFile(acFile)
	if err != nil {
		t.Fatalf("load AC file: %v", err)
	}
	testAssertResults(t, query, fileAC.MatchAll(query), ac.MatchAll(query))
	if _, err := LoadAcAutomaton(bytes.NewReader(data.Bytes())); err == nil {
		t.Fatal("expected type mismatch when loading trie as AC automaton")
	}

	serialized := data.Bytes()
	for length := 0; length < len(serialized); length++ {
		if _, err := LoadTrie(bytes.NewReader(serialized[:length])); err == nil {
			t.Fatalf("expected error for truncated trie at length %d", length)
		}
	}
	for name, mutate := range map[string]func([]byte){
		"magic":   func(b []byte) { b[0] ^= 1 },
		"version": func(b []byte) { b[4]++ },
		"type":    func(b []byte) { b[5] = 0xff },
		"dtype":   func(b []byte) { b[6] = 3 },
	} {
		t.Run("invalid_header/"+name, func(t *testing.T) {
			bad := append([]byte(nil), serialized...)
			mutate(bad)
			if _, err := LoadTrie(bytes.NewReader(bad)); err == nil {
				t.Fatal("expected invalid header error")
			}
		})
	}
}
