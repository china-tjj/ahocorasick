package ahocorasick

import (
	"io"
)

type ITrie interface {
	// PreMatchFirst 前缀匹配第一个 term
	PreMatchFirst(query string) (MatchResult, bool)
	// PreMatchAll 前缀匹配所有的 term
	PreMatchAll(query string) []MatchResult
	// Save 序列化
	Save(w io.Writer) error
	// SaveToFile 序列化并保存到文件
	SaveToFile(filename string) error
}

type IAcAutomaton interface {
	ITrie
	// MatchFirst 多模式匹配第一个 term
	MatchFirst(query string) (MatchResult, bool)
	// MatchAll 多模式匹配所有的 term
	MatchAll(query string) []MatchResult
	// MatchAllUnique 多模式匹配所有的 term，每个 term 最多匹配一次
	MatchAllUnique(query string) []MatchResult
}
