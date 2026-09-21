// Package shortterm 短期记忆：固定窗口的对话历史。
//
// ShortTerm 维护最近 MaxTurns 轮（每轮 user + assistant 两条）的对话上下文，
// 超出窗口时自动丢弃最早记录。不持久化——进程消亡即清空。
//
// 并发安全：所有公共方法持锁。直接读 .Messages 字段（旧代码 / JSON 序列化）请用 Snapshot()。
package shortterm

import (
	"sync"
	"time"
)

// ConversationMessage 是单条对话记录
type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// ShortTerm 维护最近 MaxTurns 轮的对话上下文
type ShortTerm struct {
	mu       sync.RWMutex
	Messages []ConversationMessage `json:"messages"`
	MaxTurns int                   `json:"max_turns"`
	// Summary 是被窗口淘汰的更早对话的增量压缩摘要（LLM 生成，可能为空）。
	// 与窗口一起构成完整会话记忆：摘要覆盖"更早"，窗口覆盖"最近"。
	Summary string `json:"summary,omitempty"`
	// evicted 暂存被窗口挤出的消息，供 application 层异步压缩成摘要。
	// 由 Add 在淘汰时累积（锁内），TakeEvicted 一次性取走并清空。
	evicted []ConversationMessage
}

// New 创建短期记忆，maxTurns 为保留的最大对话轮数
func New(maxTurns int) *ShortTerm {
	return &ShortTerm{MaxTurns: maxTurns}
}

// Add 追加一条消息，超出窗口时自动丢弃最早记录。
// 被挤出的消息不直接丢弃——暂存进 evicted 缓冲，等待上层压缩为会话摘要。
func (m *ShortTerm) Add(role, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Messages = append(m.Messages, ConversationMessage{
		Role:      role,
		Content:   content,
		Timestamp: time.Now().Format("15:04:05"),
	})
	max := m.MaxTurns * 2 // 每轮 = user + assistant 两条
	if len(m.Messages) > max {
		m.evicted = append(m.evicted, m.Messages[:len(m.Messages)-max]...)
		m.Messages = m.Messages[len(m.Messages)-max:]
	}
}

// PeekEvicted 只读查看当前淘汰缓冲（拷贝），供上层判断是否有待压缩内容。
func (m *ShortTerm) PeekEvicted() []ConversationMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConversationMessage, len(m.evicted))
	copy(out, m.evicted)
	return out
}

// ClearEvicted 清空淘汰缓冲。摘要成功持久化后调用；
// LLM 失败时不调用，缓冲保留等待下轮重试，被淘汰的消息不会丢。
func (m *ShortTerm) ClearEvicted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evicted = nil
}

// SummarySnapshot 只读返回当前会话摘要（可能为空）。
func (m *ShortTerm) SummarySnapshot() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Summary
}

// SetSummary 覆盖写入会话摘要（由 application 层在 LLM 压缩完成后调用）。
func (m *ShortTerm) SetSummary(summary string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Summary = summary
}

// Hydrate 从持久层批量灌入历史消息（用户首次活动时从 chat_history 表预热用）。
//
// 与 Add 的差异：
//   - 不更新 Timestamp（保留入参中的原值，反映消息真实发生时间）
//   - 直接覆盖 Messages 而非 append——调用方应该已经按时间正序裁好了 N 条
//   - 自动按 MaxTurns*2 截断，避免 PG 给的条数超过窗口
//
// 仅在桶刚创建（len(Messages)==0）时安全使用——重复 Hydrate 会丢失运行时增量。
func (m *ShortTerm) Hydrate(history []ConversationMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	max := m.MaxTurns * 2
	if max > 0 && len(history) > max {
		history = history[len(history)-max:]
	}
	m.Messages = append(m.Messages[:0], history...)
}

// Snapshot 返回当前 Messages 的副本（持读锁）
func (m *ShortTerm) Snapshot() []ConversationMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]ConversationMessage, len(m.Messages))
	copy(cp, m.Messages)
	return cp
}

// Count 返回当前消息数（持读锁）
func (m *ShortTerm) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.Messages)
}
