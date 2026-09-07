package ahocorasick

import (
	"math"
	"unicode/utf8"
)

type Option func(opt *option)

// WithOutputLink 构建AC自动机时生成输出链接(字典后缀链接)，可以加快匹配速度，代价是额外O(N)的空间复杂度
func WithOutputLink() Option {
	return func(opt *option) {
		opt.withOutputLink = true
	}
}

type DType int

const (
	DTypeAuto DType = iota
	DTypeUint8
	DTypeUint16
	DTypeUint32
	DTypeUint64
)

// WithDType 指定节点索引等字段的数据类型，默认会自动指定保证不会溢出，使用不当可能会导致行为不符合预期甚至panic，谨慎使用
func WithDType(dt DType) Option {
	return func(opt *option) {
		opt.dType = dt
	}
}

type option struct {
	trieInitCap    int
	withOutputLink bool
	dType          DType
}

func (opt *option) init(terms []string, options ...Option) {
	for _, f := range options {
		if f == nil {
			continue
		}
		f(opt)
	}

	maxNodeCnt := 1
	maxTermLen := 0
	for _, term := range terms {
		maxNodeCnt += utf8.RuneCountInString(term)
		maxTermLen = max(maxTermLen, len(term))
	}
	if opt.trieInitCap <= 0 {
		opt.trieInitCap = maxNodeCnt
	}
	if opt.dType < DTypeUint8 || opt.dType > DTypeUint64 {
		if maxNodeCnt < math.MaxUint8 && maxTermLen <= math.MaxUint8 {
			opt.dType = DTypeUint8
		} else if maxNodeCnt < math.MaxUint16 && maxTermLen <= math.MaxUint16 {
			opt.dType = DTypeUint16
		} else if maxNodeCnt < math.MaxUint32 && maxTermLen <= math.MaxUint32 {
			opt.dType = DTypeUint32
		} else {
			opt.dType = DTypeUint64
		}
	}
}
