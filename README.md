# ahocorasick

中文场景内存占用十分优秀的 AC 自动机实现。

## 特性

- **极低内存**：rune 级 Trie + 分级压缩布局（线性 / 二分 / 哈希三档子节点索引），索引宽度按词典规模在 `uint8/16/32/64`
  间自动选择。未使用双数组，但中文场景内存显著优于双数组实现。
- **支持二进制序列化**：`Save` / `SaveToFile` / `Load*`，进程冷启动直接加载，无需重建。
- **匹配结果带词 ID**：`MatchResult.TermIdx` 直达建树时 `terms` 数组的下标，便于关联业务信息。
- **构建后只读**：不可增删，并发匹配安全。
- **零第三方依赖**，要求 Go 1.21+。

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
	m := ac.NewAcAutomaton(terms)
	for _, r := range m.MatchAll("他说的话有道理") {
		fmt.Printf("%q termIdx=%d [%d,%d)\n", r.Term, r.TermIdx, r.StartIdx, r.EndIdx)
	}
}

```

主要 API：

| 方法                                                                   | 说明                                    |
|----------------------------------------------------------------------|---------------------------------------|
| `NewTrie(terms, opts...) ITrie`                                      | 前缀树                                   |
| `NewAcAutomaton(terms, opts...) IAcAutomaton`                        | AC 自动机                                |
| `WithOutputLink()`                                                   | 构建期开启输出链接（字典后缀链接），命中密集场景更快，代价 O(N) 空间 |
| `MatchFirst / MatchAll / MatchAllUnique`                             | 多模式匹配                                 |
| `PreMatchFirst / PreMatchAll`                                        | 前缀匹配                                  |
| `Save / SaveToFile`、`LoadAcAutomaton(FromFile) / LoadTrie(FromFile)` | 序列化                                   |

## 中文场景内存占用 Benchmark

| Library           | 100     | 1,000   | 10,000   | 100,000  |
|-------------------|---------|---------|----------|----------|
| china-tjj         | 3.8KB   | 34.7KB  | 370.5KB  | 4.40MB   |
| china-tjj(SL)     | 4.1KB   | 39.9KB  | 418.6KB  | 5.10MB   |
| china-tjj(U64)    | 11.1KB  | 106.3KB | 1.04MB   | 8.13MB   |
| china-tjj(SL+U64) | 13.1KB  | 126.5KB | 1.22MB   | 9.52MB   |
| BobuSumisu-ac     | 1.34MB  | 12.00MB | 110.61MB | 914.27MB |
| BobuSumisu-go-ac  | 32.8KB  | 280.1KB | 2.53MB   | 19.40MB  |
| anknown           | 1.42MB  | 1.58MB  | 3.76MB   | 17.24MB  |
| sepetrov          | 44.3KB  | 425.8KB | 3.77MB   | 26.35MB  |
| cloudflare        | 3.23MB  | 31.77MB | 317.17MB | 3.097GB  |
| petar-dambovaliev | 96.5KB  | 667.1KB | 5.60MB   | 50.38MB  |
| iohub             | 52.6KB  | 428.1KB | 3.33MB   | 26.60MB  |
| ClarkThan         | 41.5KB  | 398.9KB | 3.54MB   | 25.26MB  |
| pgavlin           | 102.9KB | 661.6KB | 5.60MB   | 50.38MB  |
| gnames            | 157.5KB | 1.37MB  | 12.53MB  | 100.12MB |

对比库：

| 库                                                                                   | Benchmark 名       | 说明                                    |
|-------------------------------------------------------------------------------------|-------------------|---------------------------------------|
| [china-tjj/ahocorasick](https://github.com/china-tjj/ahocorasick)                   | china-tjj         | 紧凑 Trie，三级索引（线性/二分/哈希），自动选择最小 uint 类型 |
| 同上                                                                                  | china-tjj(SL)     | 同上 + 构建后缀链接（加速匹配，额外 O(N) 空间）          |
| 同上                                                                                  | china-tjj(U64)    | 同上，手动指定 uint64 索引（与其他库同一维度对比）         |
| 同上                                                                                  | china-tjj(SL+U64) | 后缀链接 + uint64 索引                      |
| [BobuSumisu/aho-corasick](https://github.com/BobuSumisu/aho-corasick)               | BobuSumisu-ac     | DFA 矩阵实现，构建快，int 索引                   |
| [BobuSumisu/go-ahocorasick](https://github.com/BobuSumisu/go-ahocorasick)           | BobuSumisu-go-ac  | 双数组 Trie，作者旧版实现                       |
| [anknown/ahocorasick](https://github.com/anknown/ahocorasick)                       | anknown           | 双数组 Trie，int 索引，内存占用低                 |
| [sepetrov/ahocorasick](https://github.com/sepetrov/ahocorasick)                     | sepetrov          | 标准 Trie，map 存储子节点                     |
| [cloudflare/ahocorasick](https://github.com/cloudflare/ahocorasick)                 | cloudflare        | 标准 Trie，[]byte 匹配，int 索引              |
| [petar-dambovaliev/aho-corasick](https://github.com/petar-dambovaliev/aho-corasick) | petar-dambovaliev | 移植自 Rust BurntSushi 库，NFA 模式          |
| [iohub/ahocorasick](https://github.com/iohub/ahocorasick)                           | iohub             | cedar 双数组实现                           |
| [ClarkThan/ahocorasick](https://github.com/ClarkThan/ahocorasick)                   | ClarkThan         | 标准 Trie，map[rune]*Node                |
| [pgavlin/aho-corasick](https://github.com/pgavlin/aho-corasick)                     | pgavlin           | 源自 petar-dambovaliev，支持 NFA/DFA 切换    |
| [gnames/aho_corasick](https://github.com/gnames/aho_corasick)                       | gnames            | 标准 Trie，字节级匹配，含后缀链接                   |

详细测试方法及测试结论见 [china-tjj/acbenchmark](https://github.com/china-tjj/acbenchmark)。

## License

[MIT](./LICENSE)
