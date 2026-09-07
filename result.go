package ahocorasick

type MatchResult struct {
	Term     string
	TermIdx  int // 命中的 term 在建树 terms 数组中的索引，可以用来关联业务信息，需通过 WithTermIdx 开启，未开启时固定为 -1
	StartIdx int // term 在 query 中的起始索引（[]byte索引），闭区间
	EndIdx   int // term 在 query 中的结束索引（[]byte索引），开区间
}

func makeMatchResult[U uints](query string, termLen U, endIdx int) MatchResult {
	startIdx := endIdx - int(termLen)
	return MatchResult{
		Term:     query[startIdx:endIdx],
		TermIdx:  -1,
		StartIdx: startIdx,
		EndIdx:   endIdx,
	}
}

func makeMatchResultWithTermIdx[U uints](query string, termLen, termIdx U, endIdx int) MatchResult {
	startIdx := endIdx - int(termLen)
	return MatchResult{
		Term:     query[startIdx:endIdx],
		TermIdx:  int(termIdx),
		StartIdx: startIdx,
		EndIdx:   endIdx,
	}
}
