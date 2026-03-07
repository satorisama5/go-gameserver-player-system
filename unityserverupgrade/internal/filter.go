package internal

import (
	"regexp"
	"strings"
	"sync"
)

// 全局过滤器实例
var WordFilter *FilterManager

type FilterManager struct {
	trieRoot *TrieNode
	lock     sync.RWMutex
}

// DFA 算法的节点结构 (字典树)
type TrieNode struct {
	children map[rune]*TrieNode
	isEnd    bool
}

func NewFilterManager() *FilterManager {
	return &FilterManager{
		trieRoot: &TrieNode{children: make(map[rune]*TrieNode)},
	}
}

// 1. 构建字典树 (将数据库查出来的词列表塞入内存)
func (this *FilterManager) Build(words []string) {
	this.lock.Lock()
	defer this.lock.Unlock()

	this.trieRoot = &TrieNode{children: make(map[rune]*TrieNode)} // 重置树

	for _, word := range words {
		node := this.trieRoot
		// 将字符串转为 rune 数组 (处理中文)
		runes := []rune(word)
		for _, r := range runes {
			if _, ok := node.children[r]; !ok {
				node.children[r] = &TrieNode{children: make(map[rune]*TrieNode)}
			}
			node = node.children[r]
		}
		node.isEnd = true
	}
}

// 2. 敏感词替换 (DFA 算法核心)
func (this *FilterManager) Censor(text string) string {
	this.lock.RLock()
	defer this.lock.RUnlock()

	chars := []rune(text)
	length := len(chars)
	var sb strings.Builder

	for i := 0; i < length; {
		node := this.trieRoot
		j := i
		matchLen := 0

		// 尝试匹配最长敏感词
		for j < length {
			next, ok := node.children[chars[j]]
			if !ok {
				break
			}
			node = next
			j++
			if node.isEnd {
				matchLen = j - i // 记录匹配长度
			}
		}

		if matchLen > 0 {
			// 发现敏感词，替换为 *
			sb.WriteString("***")
			i += matchLen // 跳过敏感词部分
		} else {
			// 不是敏感词，原样追加
			sb.WriteRune(chars[i])
			i++
		}
	}
	return sb.String()
}

// 3. 移除 HTML 标签 (防止 <color=red> 破坏 UI)
// 使用正则将 <...> 的内容全部替换为空
func (this *FilterManager) StripTags(text string) string {
	// 正则表达式：匹配 < 开头，中间任意字符，> 结尾
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(text, "")
}

// 综合处理入口
func (this *FilterManager) Handle(text string) string {
	// 先剥离 HTML 标签 (防止有人利用标签绕过屏蔽，比如 <c>操</c>作)
	cleanText := this.StripTags(text)
	// 再过滤敏感词
	safeText := this.Censor(cleanText)
	return safeText
}
