// session_summary.go — 会话摘要：把窗口淘汰的旧对话增量压缩成摘要，
// 与窗口共同构成会话记忆（摘要管"更早"，窗口管"最近"）。
//
// 设计：
//   - 触发：finalize 异步检查淘汰缓冲，非空才生成（LLM 调用不阻塞回答）
//   - 增量合并：新摘要 = LLM(旧摘要 + 新淘汰对话)，避免每次全量重摘要
//   - 持久化：写回内存 Summary + PG chat_summary，重启随 hydrator 恢复
//   - 降级：LLM 失败 / 空结果时保留淘汰缓冲不清空，下轮重试；
//     全程不影响主流程——最坏情况退化为纯窗口行为
package chat

import (
	"fmt"
	"strings"

	"agi-assistant/internal/infrastructure/llm"
	"agi-assistant/internal/pkg/logger"
)

// summarySystemPrompt 约束快模型输出可拼接的纯文本摘要
const summarySystemPrompt = `你是会话摘要器。把"已有摘要"和"新增对话"合并成一段连贯的中文摘要。
要求：
- 保留关键事实、结论、用户偏好与未完成事项，去掉寒暄与重复
- 300 字以内
- 只输出摘要正文，不要任何前缀、解释或 markdown`

// maybeSummarizeSession 在 finalize 异步调用：窗口有淘汰才触发，否则零成本跳过。
func (a *UnifiedAgent) maybeSummarizeSession(userID string) {
	if userID == "" || !a.cfg.IsRealLLM() {
		return
	}
	stm := a.mem.STM(userID)
	if stm == nil {
		return
	}

	// ① 两段式：先 Peek 后 Clear——LLM 失败时缓冲保留，被淘汰的消息不丢
	evicted := stm.PeekEvicted()
	if len(evicted) == 0 {
		return
	}
	oldSummary := stm.SummarySnapshot()

	// ② 渲染被淘汰的对话为文本（超长截断，保护 prompt 预算）
	var b strings.Builder
	for _, m := range evicted {
		b.WriteString(m.Role + ": " + m.Content + "\n")
	}
	evictedText := b.String()
	if runes := len([]rune(evictedText)); runes > 6000 {
		evictedText = string([]rune(evictedText)[runes-6000:])
	}

	// ③ 快模型增量合并：旧摘要 + 新淘汰对话 → 新摘要
	newSummary := a.llm.ChatFast(summarySystemPrompt, []llm.Message{{
		Role: "user",
		Content: fmt.Sprintf("已有摘要：%s\n\n新增对话：\n%s",
			orEmptySummary(oldSummary), evictedText),
	}})
	if strings.TrimSpace(newSummary) == "" {
		logger.L().Warn("session summary empty from LLM, keep evicted buffer for retry")
		return // 不清缓冲，下轮 finalize 重试
	}

	// ④ 成功：写回内存 + 持久化 + 清缓冲
	stm.SetSummary(newSummary)
	stm.ClearEvicted()
	a.repos.chat.SaveSummary(userID, newSummary)
	logger.L().Info("session summary updated", "user_id", userID, "len", len([]rune(newSummary)))
}

func orEmptySummary(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（无）"
	}
	return s
}
