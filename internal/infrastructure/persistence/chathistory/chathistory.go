// Package chathistory 是聊天记录仓储。
//
// PG 实现：write to chat_history (user_id, role, content, created_at).
// 多租户：所有写入/读取都按 user_id 隔离。
package chathistory

import (
	"database/sql"

	"agi-assistant/internal/pkg/logger"
)

// Entry 是一条聊天记录的领域模型
type Entry struct {
	Role      string
	Content   string
	CreatedAt string // 'HH:MM:SS' 形式（用于回显）
}

// Repo 聊天记录仓储接口
type Repo interface {
	// Save 持久化一条聊天记录。userID 空字符串退化为 'legacy' 兼容老数据，
	// 但生产路径上 application 层应已强制 userID 非空。
	Save(userID, role, content string)
	// Load 加载某用户最近 N 条聊天记录（按时间正序返回）。
	Load(userID string, limit int) []Entry
	// SaveSummary 持久化某用户的会话摘要（upsert，每个用户最多一条）。
	SaveSummary(userID, summary string)
	// LoadSummary 加载某用户的会话摘要；无记录返回空字符串。
	LoadSummary(userID string) string
}

// PGRepo 是 Postgres 实现
type PGRepo struct {
	db *sql.DB
}

// NewPGRepo 创建 PG 实现；db 为 nil 时返回的实例所有方法都是无操作降级。
func NewPGRepo(db *sql.DB) *PGRepo { return &PGRepo{db: db} }

// Save 持久化一条聊天记录
func (r *PGRepo) Save(userID, role, content string) {
	if r.db == nil {
		return
	}
	if userID == "" {
		userID = "legacy"
	}
	_, err := r.db.Exec(
		`INSERT INTO chat_history (user_id, role, content) VALUES ($1, $2, $3)`,
		userID, role, content,
	)
	if err != nil {
		logger.L().Warn("chat history save to PG failed", "user_id", userID, "err", err)
	}
}

// Load 加载最近 N 条聊天记录（按时间正序返回）
func (r *PGRepo) Load(userID string, limit int) []Entry {
	if r.db == nil || userID == "" {
		return nil
	}
	rows, err := r.db.Query(
		`SELECT role, content, TO_CHAR(created_at, 'HH24:MI:SS')
		 FROM chat_history WHERE user_id = $1 ORDER BY id DESC LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		logger.L().Warn("chat history load failed", "user_id", userID, "err", err)
		return nil
	}
	defer rows.Close()
	var result []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Role, &e.Content, &e.CreatedAt); err == nil {
			result = append(result, e)
		}
	}
	// 反转为时间正序
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// SaveSummary 持久化会话摘要（upsert：每个 user_id 只保留最新一条）。
// db 不可用时静默降级——摘要丢失只影响长对话上下文，不损坏主流程。
func (r *PGRepo) SaveSummary(userID, summary string) {
	if r.db == nil || userID == "" {
		return
	}
	_, err := r.db.Exec(
		`INSERT INTO chat_summary (user_id, summary, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET summary = EXCLUDED.summary, updated_at = NOW()`,
		userID, summary,
	)
	if err != nil {
		logger.L().Warn("chat summary save to PG failed", "user_id", userID, "err", err)
	}
}

// LoadSummary 加载某用户的会话摘要；无记录或 db 不可用返回空字符串。
func (r *PGRepo) LoadSummary(userID string) string {
	if r.db == nil || userID == "" {
		return ""
	}
	var summary string
	err := r.db.QueryRow(
		`SELECT summary FROM chat_summary WHERE user_id = $1`, userID,
	).Scan(&summary)
	if err != nil {
		if err != sql.ErrNoRows {
			logger.L().Warn("chat summary load failed", "user_id", userID, "err", err)
		}
		return ""
	}
	return summary
}
