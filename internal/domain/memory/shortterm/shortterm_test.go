package shortterm

import "testing"

// TestEvictedBuffer 验证窗口淘汰的消息会累积进缓冲，
// PeekEvicted 只读不取走，ClearEvicted 才清空——保证 LLM 失败时消息不丢。
func TestEvictedBuffer(t *testing.T) {
	st := New(2) // 窗口 2 轮 = 4 条消息
	st.Add("user", "msg1")
	st.Add("assistant", "a1")
	st.Add("user", "msg2")
	st.Add("assistant", "a2")

	// 未超窗：缓冲应为空
	if got := st.PeekEvicted(); len(got) != 0 {
		t.Fatalf("未超窗时 evicted 应为空，得到 %d 条", len(got))
	}

	// 第 3 轮进入：最早 2 条被挤出
	st.Add("user", "msg3")
	st.Add("assistant", "a3")

	evicted := st.PeekEvicted()
	if len(evicted) != 2 {
		t.Fatalf("淘汰缓冲应有 2 条，得到 %d 条", len(evicted))
	}
	if evicted[0].Content != "msg1" || evicted[1].Content != "a1" {
		t.Fatalf("淘汰顺序错误：得到 %v", evicted)
	}

	// Peek 只读：再 Peek 一次数量不变
	if again := st.PeekEvicted(); len(again) != 2 {
		t.Fatalf("PeekEvicted 应只读，第二次看到 %d 条", len(again))
	}

	// Clear 后清空
	st.ClearEvicted()
	if got := st.PeekEvicted(); len(got) != 0 {
		t.Fatalf("Clear 后缓冲应为空，得到 %d 条", len(got))
	}
}

// TestSummaryReadWrite 验证摘要的写入与只读快照。
func TestSummaryReadWrite(t *testing.T) {
	st := New(2)
	if got := st.SummarySnapshot(); got != "" {
		t.Fatalf("初始摘要应为空，得到 %q", got)
	}
	st.SetSummary("用户之前聊了 Go 并发")
	if got := st.SummarySnapshot(); got != "用户之前聊了 Go 并发" {
		t.Fatalf("摘要读写不一致，得到 %q", got)
	}
	st.SetSummary("更新后的摘要")
	if got := st.SummarySnapshot(); got != "更新后的摘要" {
		t.Fatalf("覆盖写失败，得到 %q", got)
	}
}
