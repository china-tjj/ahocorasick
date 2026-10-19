# ahocorasick

[![Go Reference](https://pkg.go.dev/badge/github.com/china-tjj/ahocorasick.svg)](https://pkg.go.dev/github.com/china-tjj/ahocorasick)
[![Release](https://img.shields.io/github/v/release/china-tjj/ahocorasick)](https://github.com/china-tjj/ahocorasick/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

内存表现十分优秀的 Aho-Corasick 自动机，采用紧凑型数据结构，内存占用持平无空槽的双数组实现（Double-Array Trie），
并且构建速度更快，匹配速度介于双数组实现与朴素 map 实现之间。

## 核心优势

- **产物内存低**：采用紧凑型数据结构，内存占用持平无空槽的双数组实现（Double-Array Trie），详见[实现简述](#实现简述)与[Benchmark](#Benchmark)。
- **自适应数据类型**：字符解析粒度支持 `byte` 和 `rune`，数组数据类型支持 `uint8`、`uint16`、`uint32` 或 `uint64`，默认会自动选择最优类型；
- **构建申请少**：构建期仅需额外申请 20%~30% 的内存，也可通过 `WithFastBuild` 以更多构建内存换取更快的构建速度。
- **功能完整**：支持 Trie 前缀匹配、AC 多模式匹配、首个/全部/去重结果、二进制序列化和文件加载；可选返回 term 索引以及构建字典后缀链接。

> 缺点：不支持动态更新，构建完成后不允许修改。

## 安装

```bash
go get github.com/china-tjj/ahocorasick
```

## 快速开始

```go
package main

import (
	"fmt"

	ac "github.com/china-tjj/ahocorasick"
)

func main() {
	terms := []string{"她", "他说", "说的", "的话"}
	m := ac.NewAcAutomaton(terms, ac.WithTermIdx())
	for _, r := range m.MatchAll("他说的话有道理") {
		fmt.Printf("%q termIdx=%d [%d,%d)\n", r.Term, r.TermIdx, r.StartIdx, r.EndIdx)
	}
}

```

主要 API：

| 方法                                                                     | 说明                                    |
|------------------------------------------------------------------------|---------------------------------------|
| `NewTrie(terms, opts...) ITrie`                                        | 前缀树                                   |
| `NewAcAutomaton(terms, opts...) IAcAutomaton`                          | AC 自动机                                |
| `PreMatchFirst / PreMatchAll / PreMatchIter`                           | 前缀匹配                                  |
| `MatchFirst / MatchAll / MatchAllUnique / MatchIter / MatchUniqueIter` | 多模式匹配                                 |
| `Save`、`LoadTrie`、`LoadAcAutomaton`                                    | 二进制序列化/反序列化                           |
| `WithTermIdx()`                                                        | 匹配结果包含 term 索引，可以关联业务信息，代价 O(N) 空间    |
| `WithOutputLink()`                                                     | 额外构建字典后缀链接，后缀命中密集时可减少输出链遍历，代价 O(N) 空间 |
| `WithFastBuild()`                                                      | 启用快速构建模式，以更多构建内存换取更快的构建速度             |
| `WithDType(dType)`                                                     | 手动指定数组数据类型                            |
| `WithCharType(charType)`                                               | 手动指定字符解析粒度                            |

## 实现简述

节点按照 BFS 顺序排列，边只保存字符，并利用 `childNodeIdx = edgeIdx + 1` 隐式得到子节点编号。

所有边存放在连续数组中，节点保存起始边偏移量，通过相邻节点的偏移差得到区间长度。

未开启 `WithTermIdx` 时，终止节点只保存 term 的字节长度，匹配时直接从 query 中截取完整 term。

此时每个节点需存储 `edgeOffset`、`output`、`fail` 三个字段，及每条边存储一个 `rune`：
使用 `uint32` 索引时约为 `4 + 4 + 4 + 4 = 16 B/state`，使用 `uint64` 索引时约为 `8 + 8 + 8 + 4 = 28 B/state`。

（开启 `WithTermIdx` 后，将所有终止节点的 `term 长度` 和 `termIdx 数组` 紧凑排列到一个公共数组，终止节点存数组中的偏移量。）

为兼顾查询速度和内存占用，保证 O(1) 状态转移，子节点查询根据节点出度采用不同策略：

- 子节点数量不超过 8：线性查找
- 子节点数量为 9～2048：二分查找
- 子节点数量超过 2048：取模哈希；使用 map 存储有哈希冲突的边

> 在绝大部分场景下，该内存布局比双数组更节省内存，然而如果大出度节点比较多，并且哈希碰撞率高，该实现会退化为朴素 map 实现。

## Benchmark

测试数据：100,000 个长度为 2～4 个汉字的中文词条。匹配文本长约 100 KB 。

| 测试库                  | 建树产物大小   | 建树内存申请量  | 建树耗时     | 匹配耗时    |
|----------------------|----------|----------|----------|---------|
| china-tjj            | 2.81MB   | 3.58MB   | 47.48ms  | 1.12ms  |
| china-tjj(FB)        | 2.81MB   | 9.14MB   | 20.46ms  | 1.12ms  |
| china-tjj(U64)       | 4.91MB   | 6.44MB   | 47.80ms  | 1.09ms  |
| china-tjj(U64+OL)    | 6.29MB   | 7.82MB   | 48.23ms  | 1.09ms  |
| china-tjj(U64+TI)    | 6.44MB   | 7.97MB   | 47.77ms  | 1.13ms  |
| china-tjj(U64+OL+TI) | 7.82MB   | 9.35MB   | 47.64ms  | 1.10ms  |
| BobuSumisu-ac        | 914.27MB | 1.007GB  | 283.30ms | 2.00ms  |
| BobuSumisu-go-ac     | 19.40MB  | 73.78MB  | 43.245s  | 1.08ms  |
| anknown              | 17.24MB  | 110.84MB | 56.48ms  | 928.0µs |
| TheFutureIsOurs      | 8.19MB   | 69.22MB  | 78.26ms  | 361.0µs |
| orbit-w              | 9.83MB   | 39.88MB  | 1.251s   | 367.3µs |
| sepetrov             | 26.35MB  | 36.96MB  | 36.00ms  | 1.88ms  |
| cloudflare           | 3.097GB  | 3.132GB  | 986.62ms | 2.52ms  |
| petar-dambovaliev    | 50.38MB  | 229.44MB | 204.44ms | 5.22ms  |
| iohub                | 26.60MB  | 78.90MB  | 53.67ms  | 1.41ms  |
| ClarkThan            | 25.26MB  | 29.39MB  | 34.85ms  | 1.82ms  |
| pgavlin              | 50.38MB  | 228.11MB | 218.36ms | 5.24ms  |
| gnames               | 100.12MB | 138.67MB | 142.91ms | 5.84ms  |

对比库：

| 库                                                                                   | Benchmark 名          | 实现算法            | 索引粒度 | 完整匹配结果 | 后缀链接 | 其它                                                                             |
|-------------------------------------------------------------------------------------|----------------------|-----------------|------|--------|------|--------------------------------------------------------------------------------|
| [china-tjj/ahocorasick](https://github.com/china-tjj/ahocorasick)                   | china-tjj            | 紧凑 Trie         | auto | 完整     | 无    | 紧凑数据结构，自动选择最小 uint 类型                                                          |
| 同上                                                                                  | china-tjj(FB)        | 紧凑 Trie         | auto | 完整     | 无    | 紧凑数据结构，自动选择最小 uint 类型，启用快速构建模式                                                 |
| 同上                                                                                  | china-tjj(U64)       | 紧凑 Trie         | auto | 完整     | 无    | 紧凑数据结构，手动指定 uint64 类型                                                          |
| 同上                                                                                  | china-tjj(U64+OL)    | 紧凑 Trie         | auto | 完整     | 有    | 紧凑数据结构，手动指定 uint64 类型，开启后缀链接                                                   |
| 同上                                                                                  | china-tjj(U64+TI)    | 紧凑 Trie         | auto | 完整     | 无    | 紧凑数据结构，手动指定 uint64 类型，返回结果含pattern id                                          |
| 同上                                                                                  | china-tjj(U64+OL+TI) | 紧凑 Trie         | auto | 完整     | 有    | 紧凑数据结构，手动指定 uint64 类型，开启后缀链接，返回结果含pattern id                                   |
| [BobuSumisu/aho-corasick](https://github.com/BobuSumisu/aho-corasick)               | BobuSumisu-ac        | 标准Trie + 预计算转移表 | byte | 完整     | 有    | 返回Match含pattern id与位置；sync.Pool复用；支持Encode/Decode(gzip)                        |
| [BobuSumisu/go-ahocorasick](https://github.com/BobuSumisu/go-ahocorasick)           | BobuSumisu-go-ac     | 双数组Trie(DAT)    | byte | 完整     | 有    | SaveTrie/LoadTrie；Match结果不含pattern索引(仅pos+match bytes)                         |
| [anknown/ahocorasick](https://github.com/anknown/ahocorasick)                       | anknown              | 双数组Trie(DAT)    | rune | 完整     | 有    | 输出在构建时合并到output(等价后缀链效果)；Term不含pattern索引(靠Word)                                |
| [TheFutureIsOurs/ahocorasick](https://github.com/TheFutureIsOurs/ahocorasick)       | TheFutureIsOurs      | 双数组Trie(DAT)    | rune | 不完整    | 有    | output压缩：同终点/后缀只保留最长term；主打低内存/低GC                                             |
| [orbit-w/aho_corasick](https://github.com/orbit-w/aho_corasick)                     | orbit-w              | 双数组Trie(DAT)    | rune | 完整     | 有    | Trie构建时把fail节点output合并到当前节点；提供Validate/Replace等                                |
| [sepetrov/ahocorasick](https://github.com/sepetrov/ahocorasick)                     | sepetrov             | 标准Trie          | rune | 完整     | 有    | 返回map[patternIndex][]pos(位置是byte offset)；含suffixLink+dictSuffixLink            |
| [cloudflare/ahocorasick](https://github.com/cloudflare/ahocorasick)                 | cloudflare           | 标准Trie          | byte | 不完整    | 有    | Match返回命中过的pattern集合(去重、无位置)；有MatchThreadSafe/Contains                         |
| [petar-dambovaliev/aho-corasick](https://github.com/petar-dambovaliev/aho-corasick) | petar-dambovaliev    | NFA/DFA         | byte | 完整     | 有    | 多种MatchKind；Standard可完整/可重叠；LeftMost*会裁剪；含prefilter/Replace/迭代器                |
| [iohub/ahocorasick](https://github.com/iohub/ahocorasick)                           | iohub                | cedar 双数组Trie   | byte | 完整     | 有    | value可存任意interface{}；迭代器输出；sync.Pool复用；graphviz可视化                             |
| [ClarkThan/ahocorasick](https://github.com/ClarkThan/ahocorasick)                   | ClarkThan            | 标准Trie          | rune | 完整     | 有    | SearchAppend/SearchIndexedAppend支持buf复用；输出不含pattern索引(只有Hit{Start,Len}或string) |
| [pgavlin/aho-corasick](https://github.com/pgavlin/aho-corasick)                     | pgavlin              | NFA/DFA         | byte | 完整     | 有    | 基本同petar分支；Standard可完整；LeftMost*会裁剪                                            |
| [gnames/aho_corasick](https://github.com/gnames/aho_corasick)                       | gnames               | 标准Trie          | byte | 不完整    | 有    | dict link实现为单跳(仅linkDict一次，不会多级追溯)，可能漏更深后缀命中；Match含pattern索引+start/end         |

详细测试方法及测试结论见 [china-tjj/acbenchmark](https://github.com/china-tjj/acbenchmark)。

## License

[MIT](./LICENSE)
