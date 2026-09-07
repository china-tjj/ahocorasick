package ahocorasick

import (
	"math"
	"unicode/utf8"
)

type buildParams struct {
	options
	runeCnt int // 字符个数
	termCnt int // 非空term数
}

func (params *buildParams) init(terms []string, options ...Option) {
	for _, f := range options {
		if f == nil {
			continue
		}
		f(&params.options)
	}

	params.runeCnt = 0
	maxTermLen := 0
	maxValidIdx := -1
	for i, term := range terms {
		if term == "" || !utf8.ValidString(term) {
			continue
		}
		params.runeCnt += utf8.RuneCountInString(term)
		params.termCnt++
		maxTermLen = max(maxTermLen, len(term))
		maxValidIdx = max(maxValidIdx, i)
	}
	if params.dType < DTypeUint8 || params.dType > DTypeUint64 {
		// 最大节点数=字符数+1(包括根节点)，需存储节点索引(最大索引=个数-1)，还要额外留个最大值表示 nil，故 1+opt.runeCnt
		// 节点需挂载 term 长度，故 maxTermLen
		maxValue := max(1+params.runeCnt, maxTermLen)
		if params.withTermIdx {
			// terms 为每个 term 保存一个原始索引，并为每个终止节点额外保存一次长度；容量上界为 2*termCnt
			// 辅助 Trie 直接使用原始索引串联 term，需为 nil 哨兵预留最大值
			maxValue = max(maxValue, 2*params.termCnt, maxValidIdx+1)
		}
		if maxValue <= math.MaxUint8 {
			params.dType = DTypeUint8
		} else if maxValue <= math.MaxUint16 {
			params.dType = DTypeUint16
		} else if maxValue <= math.MaxUint32 {
			params.dType = DTypeUint32
		} else {
			params.dType = DTypeUint64
		}
	}
}
