package ahocorasick

import (
	"math"
	"unicode/utf8"
)

type buildParams struct {
	options
	charCnt    int // 字符个数
	termCnt    int // 合法term数
	maxTermLen int
	maxTermIdx int
}

func (params *buildParams) init(terms []string, options ...Option) {
	for _, f := range options {
		if f == nil {
			continue
		}
		f(&params.options)
	}
	params.scanTerms(terms)
	params.selectDType()
}

func (params *buildParams) scanTerms(terms []string) {
	params.maxTermIdx = -1

	if params.charType == CharTypeByte {
		for i, term := range terms {
			// 忽略空字符串
			if term == "" {
				continue
			}
			params.charCnt += len(term)
			params.termCnt++
			params.maxTermLen = max(params.maxTermLen, len(term))
			params.maxTermIdx = max(params.maxTermIdx, i)
		}
		return
	}

	if params.charType == CharTypeRune {
		for i, term := range terms {
			// rune粒度建索引时，还要忽略非法字符串
			if term == "" || !utf8.ValidString(term) {
				continue
			}
			params.charCnt += utf8.RuneCountInString(term)
			params.termCnt++
			params.maxTermLen = max(params.maxTermLen, len(term))
			params.maxTermIdx = max(params.maxTermIdx, i)
		}
		return
	}

	byteCnt, runeCnt, hasInvalidTerm := 0, 0, false
	for i, term := range terms {
		if term == "" {
			continue
		}
		byteCnt += len(term)
		params.termCnt++
		params.maxTermLen = max(params.maxTermLen, len(term))
		params.maxTermIdx = max(params.maxTermIdx, i)
		if hasInvalidTerm {
			continue
		}
		if !utf8.ValidString(term) {
			hasInvalidTerm = true
			continue
		}
		runeCnt += utf8.RuneCountInString(term)
	}
	// term 含有非法 utf8 字符或平均字符长度 < 1.5 时选择 byte 粒度索引，否则选择 rune 粒度索引
	if hasInvalidTerm || runeCnt == 0 || float64(byteCnt)/float64(runeCnt) < 1.5 {
		params.charType = CharTypeByte
		params.charCnt = byteCnt
	} else {
		params.charType = CharTypeRune
		params.charCnt = runeCnt
	}
}

func (params *buildParams) selectDType() {
	if DTypeUint8 <= params.dType && params.dType <= DTypeUint64 {
		return
	}
	// 最大节点数=字符数+1(包括根节点)，需存储节点索引(最大索引=个数-1)，还要额外留个最大值表示 nil，故 1+params.charCnt
	// 节点需挂载 term 长度，故 maxTermLen
	// lowAllocBuild 需要用 term 索引数组来辅助构建，故 maxTermIdx；fastBuild 虽无相关用法，但是为保证不同构建方式下产物等价，一起加上该限制
	maxValue := max(1+params.charCnt, params.maxTermLen, params.maxTermIdx)
	if params.withTermIdx {
		// terms 为每个 term 保存一个原始索引，并为每个终止节点额外保存一次长度；容量上界为 2*termCnt
		// 辅助 Trie 直接使用原始索引串联 term，需为 nil 哨兵预留最大值
		maxValue = max(maxValue, 2*params.termCnt, params.maxTermIdx+1)
	}
	if maxValue <= math.MaxUint8 {
		params.dType = DTypeUint8
	} else if maxValue <= math.MaxUint16 {
		params.dType = DTypeUint16
	} else if int64(maxValue) <= math.MaxUint32 { // 直接使用 maxValue <= math.MaxUint32 会导致在32位系统上无法编译
		params.dType = DTypeUint32
	} else {
		params.dType = DTypeUint64
	}
}
