package ahocorasick

type Option func(opt *options)

// WithTermIdx 匹配结果 MatchResult 输出命中的 term 在建树 terms 数组中的索引，可以用来关联业务信息；
// 开启后在 MatchAllUnique 去重时，建树时 terms 数组里的每个 term 均被视为不同的 term（因为可能会关联不同的业务信息）；
// 代价是额外 O(N) 的空间复杂度
func WithTermIdx() Option {
	return func(opt *options) {
		opt.withTermIdx = true
	}
}

// WithOutputLink 构建AC自动机时生成输出链接(字典后缀链接)，可以加快匹配速度，代价是额外 O(N) 的空间复杂度
func WithOutputLink() Option {
	return func(opt *options) {
		opt.withOutputLink = true
	}
}

// WithDType 指定节点索引等字段的数据类型，默认会自动指定保证不会溢出，使用不当可能会导致行为不符合预期甚至panic，谨慎使用
func WithDType(dt DType) Option {
	return func(opt *options) {
		opt.dType = dt
	}
}

// WithFastBuild 启用快速构建模式，该模式将构建时间复杂度从 O(NlogN) 降低到 O(N)，实测构建耗时可减少约 0%~50%；
// 代价是辅助构建的临时内存开销由额外约 20% 增加到额外约 165%
func WithFastBuild() Option {
	return func(opt *options) {
		opt.fastBuild = true
	}
}

type options struct {
	withTermIdx    bool
	withOutputLink bool
	dType          DType
	fastBuild      bool
}
