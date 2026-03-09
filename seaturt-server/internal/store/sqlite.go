package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	slog.Info("database initialized", "path", dbPath)
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			id             TEXT PRIMARY KEY,
			name           TEXT NOT NULL,
			status         TEXT NOT NULL DEFAULT 'created',
			container_id   TEXT NOT NULL DEFAULT '',
			image          TEXT NOT NULL DEFAULT '',
			workspace_path TEXT NOT NULL DEFAULT '',
			config         TEXT NOT NULL DEFAULT '{}',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id           TEXT PRIMARY KEY,
			agent_id     TEXT NOT NULL,
			role         TEXT NOT NULL,
			content      TEXT NOT NULL DEFAULT '',
			tool_calls   TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT NOT NULL DEFAULT '',
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_agent_id ON messages(agent_id, created_at)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", q[:40], err)
		}
	}

	// Safe column migration: add tool_call_id if it doesn't exist yet.
	// ALTER TABLE ... ADD COLUMN fails if the column already exists; just ignore.
	_, _ = s.db.Exec(`ALTER TABLE messages ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT ''`)

	// Add reasoning_content column for reasoning model support (v0.1.6)
	_, _ = s.db.Exec(`ALTER TABLE messages ADD COLUMN reasoning_content TEXT NOT NULL DEFAULT ''`)

	// Add session_id column for multi-session support (v0.2.0)
	_, _ = s.db.Exec(`ALTER TABLE messages ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id, created_at)`)

	// Create sessions table (v0.2.0)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT PRIMARY KEY,
		agent_id   TEXT NOT NULL,
		title      TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
	)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_agent_id ON sessions(agent_id, updated_at)`)

	// Create cron_jobs table (v0.3.0)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS cron_jobs (
		id               TEXT PRIMARY KEY,
		agent_id         TEXT NOT NULL,
		type             TEXT NOT NULL DEFAULT 'cron',
		cron_expr        TEXT NOT NULL DEFAULT '',
		run_at           DATETIME,
		prompt           TEXT NOT NULL DEFAULT '',
		session_strategy TEXT NOT NULL DEFAULT 'fixed',
		session_id       TEXT NOT NULL DEFAULT '',
		enabled          INTEGER NOT NULL DEFAULT 1,
		last_run_at      DATETIME,
		next_run_at      DATETIME,
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
	)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_cron_jobs_agent_id ON cron_jobs(agent_id)`)

	// Create cron_job_executions table (v0.3.0)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS cron_job_executions (
		id          TEXT PRIMARY KEY,
		cron_job_id TEXT NOT NULL,
		agent_id    TEXT NOT NULL,
		session_id  TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'success',
		error       TEXT NOT NULL DEFAULT '',
		duration    INTEGER NOT NULL DEFAULT 0,
		started_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (cron_job_id) REFERENCES cron_jobs(id) ON DELETE CASCADE
	)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_cron_job_executions_job_id ON cron_job_executions(cron_job_id, created_at)`)

	return nil
}

// --- Agent CRUD ---

func (s *Store) CreateAgent(a *agent.Agent) error {
	configJSON, err := json.Marshal(a.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO agents (id, name, status, container_id, image, workspace_path, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Status, a.ContainerID, a.Image, a.WorkspacePath,
		string(configJSON), a.CreatedAt, a.UpdatedAt,
	)
	return err
}

func (s *Store) GetAgent(id string) (*agent.Agent, error) {
	row := s.db.QueryRow(
		`SELECT id, name, status, container_id, image, workspace_path, config, created_at, updated_at
		 FROM agents WHERE id = ?`, id,
	)
	return scanAgent(row)
}

func (s *Store) ListAgents() ([]*agent.Agent, error) {
	rows, err := s.db.Query(
		`SELECT id, name, status, container_id, image, workspace_path, config, created_at, updated_at
		 FROM agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*agent.Agent
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) UpdateAgentStatus(id string, status agent.Status) error {
	_, err := s.db.Exec(
		`UPDATE agents SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now(), id,
	)
	return err
}

func (s *Store) UpdateAgentContainerID(id, containerID string) error {
	_, err := s.db.Exec(
		`UPDATE agents SET container_id = ?, updated_at = ? WHERE id = ?`,
		containerID, time.Now(), id,
	)
	return err
}

func (s *Store) DeleteAgent(id string) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE id = ?`, id)
	return err
}

// --- Message CRUD ---

func (s *Store) CreateMessage(m *agent.Message) error {
	// Externalize all image blocks to files
	content, err := s.externalizeImages(m.AgentID, m.Content)
	if err != nil {
		return fmt.Errorf("externalize images: %w", err)
	}

	contentJSON, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO messages (id, agent_id, session_id, role, content, reasoning_content, tool_calls, tool_call_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.AgentID, m.SessionID, m.Role, string(contentJSON), m.ReasoningContent, m.ToolCalls, m.ToolCallID, m.CreatedAt,
	)
	return err
}

func (s *Store) ListMessages(agentID string) ([]*agent.Message, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, session_id, role, content, reasoning_content, tool_calls, tool_call_id, created_at
		 FROM messages WHERE agent_id = ? ORDER BY created_at ASC`, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*agent.Message
	for rows.Next() {
		m := &agent.Message{}
		var contentJSON string
		if err := rows.Scan(&m.ID, &m.AgentID, &m.SessionID, &m.Role, &contentJSON, &m.ReasoningContent, &m.ToolCalls, &m.ToolCallID, &m.CreatedAt); err != nil {
			return nil, err
		}
		// Deserialize Content from JSON
		if err := json.Unmarshal([]byte(contentJSON), &m.Content); err != nil {
			// Fallback: treat as plain text (backward compat with v0.0.1 data)
			m.Content = llm.Content{llm.NewTextContent(contentJSON)}
		}
		// Reload externalized image data from files
		s.internalizeImages(m.Content)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *Store) ListMessagesBySession(sessionID string) ([]*agent.Message, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, session_id, role, content, reasoning_content, tool_calls, tool_call_id, created_at
		 FROM messages WHERE session_id = ? ORDER BY created_at ASC`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*agent.Message
	for rows.Next() {
		m := &agent.Message{}
		var contentJSON string
		if err := rows.Scan(&m.ID, &m.AgentID, &m.SessionID, &m.Role, &contentJSON, &m.ReasoningContent, &m.ToolCalls, &m.ToolCallID, &m.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(contentJSON), &m.Content); err != nil {
			m.Content = llm.Content{llm.NewTextContent(contentJSON)}
		}
		s.internalizeImages(m.Content)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *Store) DeleteMessages(agentID string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE agent_id = ?`, agentID)
	return err
}

func (s *Store) DeleteMessagesBySession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID)
	return err
}

// --- Session CRUD ---

func (s *Store) CreateSession(sess *agent.Session) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, agent_id, title, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sess.ID, sess.AgentID, sess.Title, sess.CreatedAt, sess.UpdatedAt,
	)
	return err
}

func (s *Store) GetSession(id string) (*agent.Session, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, title, created_at, updated_at FROM sessions WHERE id = ?`, id,
	)
	sess := &agent.Session{}
	err := row.Scan(&sess.ID, &sess.AgentID, &sess.Title, &sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) ListSessions(agentID string) ([]*agent.Session, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, title, created_at, updated_at
		 FROM sessions WHERE agent_id = ? ORDER BY updated_at DESC`, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*agent.Session
	for rows.Next() {
		sess := &agent.Session{}
		if err := rows.Scan(&sess.ID, &sess.AgentID, &sess.Title, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *Store) UpdateSession(sess *agent.Session) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		sess.Title, sess.UpdatedAt, sess.ID,
	)
	return err
}

func (s *Store) DeleteSession(id string) error {
	// Delete associated messages first
	if _, err := s.db.Exec(`DELETE FROM messages WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("delete session messages: %w", err)
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// --- CronJob CRUD ---

func (s *Store) CreateCronJob(job *agent.CronJob) error {
	_, err := s.db.Exec(
		`INSERT INTO cron_jobs (id, agent_id, type, cron_expr, run_at, prompt, session_strategy, session_id, enabled, last_run_at, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.AgentID, job.Type, job.CronExpr, job.RunAt, job.Prompt,
		job.SessionStrategy, job.SessionID, job.Enabled, job.LastRunAt, job.NextRunAt,
		job.CreatedAt, job.UpdatedAt,
	)
	return err
}

func (s *Store) GetCronJob(id string) (*agent.CronJob, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, type, cron_expr, run_at, prompt, session_strategy, session_id, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM cron_jobs WHERE id = ?`, id,
	)
	return scanCronJob(row)
}

func (s *Store) ListCronJobs(agentID string) ([]*agent.CronJob, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, type, cron_expr, run_at, prompt, session_strategy, session_id, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM cron_jobs WHERE agent_id = ? ORDER BY created_at DESC`, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCronJobRows(rows)
}

func (s *Store) ListAllEnabledCronJobs() ([]*agent.CronJob, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, type, cron_expr, run_at, prompt, session_strategy, session_id, enabled, last_run_at, next_run_at, created_at, updated_at
		 FROM cron_jobs WHERE enabled = 1`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCronJobRows(rows)
}

func (s *Store) UpdateCronJob(job *agent.CronJob) error {
	_, err := s.db.Exec(
		`UPDATE cron_jobs SET type = ?, cron_expr = ?, run_at = ?, prompt = ?, session_strategy = ?, session_id = ?, enabled = ?, last_run_at = ?, next_run_at = ?, updated_at = ? WHERE id = ?`,
		job.Type, job.CronExpr, job.RunAt, job.Prompt, job.SessionStrategy, job.SessionID,
		job.Enabled, job.LastRunAt, job.NextRunAt, job.UpdatedAt, job.ID,
	)
	return err
}

func (s *Store) DeleteCronJob(id string) error {
	// Delete associated executions first
	if _, err := s.db.Exec(`DELETE FROM cron_job_executions WHERE cron_job_id = ?`, id); err != nil {
		return fmt.Errorf("delete cron job executions: %w", err)
	}
	_, err := s.db.Exec(`DELETE FROM cron_jobs WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteCronJobsByAgent(agentID string) error {
	// Delete associated executions first
	if _, err := s.db.Exec(`DELETE FROM cron_job_executions WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete cron job executions by agent: %w", err)
	}
	_, err := s.db.Exec(`DELETE FROM cron_jobs WHERE agent_id = ?`, agentID)
	return err
}

// --- CronJobExecution CRUD ---

func (s *Store) CreateCronJobExecution(exec *agent.CronJobExecution) error {
	_, err := s.db.Exec(
		`INSERT INTO cron_job_executions (id, cron_job_id, agent_id, session_id, status, error, duration, started_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.CronJobID, exec.AgentID, exec.SessionID, exec.Status,
		exec.Error, exec.Duration, exec.StartedAt, exec.CreatedAt,
	)
	return err
}

func (s *Store) ListCronJobExecutions(cronJobID string) ([]*agent.CronJobExecution, error) {
	rows, err := s.db.Query(
		`SELECT id, cron_job_id, agent_id, session_id, status, error, duration, started_at, created_at
		 FROM cron_job_executions WHERE cron_job_id = ? ORDER BY created_at DESC`, cronJobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var executions []*agent.CronJobExecution
	for rows.Next() {
		exec := &agent.CronJobExecution{}
		if err := rows.Scan(&exec.ID, &exec.CronJobID, &exec.AgentID, &exec.SessionID, &exec.Status, &exec.Error, &exec.Duration, &exec.StartedAt, &exec.CreatedAt); err != nil {
			return nil, err
		}
		executions = append(executions, exec)
	}
	return executions, rows.Err()
}

func (s *Store) DeleteCronJobExecutions(cronJobID string) error {
	_, err := s.db.Exec(`DELETE FROM cron_job_executions WHERE cron_job_id = ?`, cronJobID)
	return err
}

func (s *Store) DeleteCronJobExecutionsByAgent(agentID string) error {
	_, err := s.db.Exec(`DELETE FROM cron_job_executions WHERE agent_id = ?`, agentID)
	return err
}

// --- CronJob scan helpers ---

func scanCronJob(row scannable) (*agent.CronJob, error) {
	job := &agent.CronJob{}
	var runAt, lastRunAt, nextRunAt sql.NullTime
	err := row.Scan(
		&job.ID, &job.AgentID, &job.Type, &job.CronExpr, &runAt, &job.Prompt,
		&job.SessionStrategy, &job.SessionID, &job.Enabled, &lastRunAt, &nextRunAt,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if runAt.Valid {
		job.RunAt = &runAt.Time
	}
	if lastRunAt.Valid {
		job.LastRunAt = &lastRunAt.Time
	}
	if nextRunAt.Valid {
		job.NextRunAt = &nextRunAt.Time
	}
	return job, nil
}

func scanCronJobRows(rows *sql.Rows) ([]*agent.CronJob, error) {
	var jobs []*agent.CronJob
	for rows.Next() {
		job := &agent.CronJob{}
		var runAt, lastRunAt, nextRunAt sql.NullTime
		if err := rows.Scan(
			&job.ID, &job.AgentID, &job.Type, &job.CronExpr, &runAt, &job.Prompt,
			&job.SessionStrategy, &job.SessionID, &job.Enabled, &lastRunAt, &nextRunAt,
			&job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if runAt.Valid {
			job.RunAt = &runAt.Time
		}
		if lastRunAt.Valid {
			job.LastRunAt = &lastRunAt.Time
		}
		if nextRunAt.Valid {
			job.NextRunAt = &nextRunAt.Time
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// --- Scan helpers ---

type scannable interface {
	Scan(dest ...any) error
}

func scanAgent(row scannable) (*agent.Agent, error) {
	a := &agent.Agent{}
	var configJSON string
	err := row.Scan(
		&a.ID, &a.Name, &a.Status, &a.ContainerID, &a.Image,
		&a.WorkspacePath, &configJSON, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(configJSON), &a.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return a, nil
}

func scanAgentRows(rows *sql.Rows) (*agent.Agent, error) {
	a := &agent.Agent{}
	var configJSON string
	err := rows.Scan(
		&a.ID, &a.Name, &a.Status, &a.ContainerID, &a.Image,
		&a.WorkspacePath, &configJSON, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(configJSON), &a.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return a, nil
}

// --- Image externalization helpers ---

// mimeToExt maps MIME types to file extensions.
var mimeToExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// externalizeImages saves all image blocks' base64 data to files under
// {workspacePath}/.seaturt/uploads/ and replaces Data with FilePath.
// Returns a copy of the content with images externalized.
func (s *Store) externalizeImages(agentID string, content llm.Content) (llm.Content, error) {
	if !content.HasType("image") {
		return content, nil
	}

	// Look up agent workspace path
	ag, err := s.GetAgent(agentID)
	if err != nil {
		return content, fmt.Errorf("get agent for workspace: %w", err)
	}

	uploadsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return content, fmt.Errorf("create uploads dir: %w", err)
	}

	// Make a copy so we don't mutate the caller's data
	result := make(llm.Content, len(content))
	copy(result, content)

	for i := range result {
		b := &result[i]
		if b.Type != "image" || b.Image == nil || b.Image.Data == "" {
			continue
		}

		// Decode base64 to raw bytes
		raw, err := base64.StdEncoding.DecodeString(b.Image.Data)
		if err != nil {
			slog.Warn("failed to decode image base64, keeping inline", "err", err)
			continue
		}

		// Determine file extension
		ext := mimeToExt[b.Image.MimeType]
		if ext == "" {
			ext = ".bin"
		}

		// Generate unique filename
		filename := randomHex(16) + ext
		filePath := filepath.Join(uploadsDir, filename)

		if err := os.WriteFile(filePath, raw, 0644); err != nil {
			slog.Warn("failed to write image file, keeping inline", "err", err, "path", filePath)
			continue
		}

		// Replace Data with FilePath
		b.Image = &llm.ImageData{
			MimeType: b.Image.MimeType,
			Detail:   b.Image.Detail,
			FilePath: filePath,
		}

		slog.Debug("image externalized", "path", filePath, "size", len(raw))
	}

	return result, nil
}

// internalizeImages loads image data from FilePath back into Data (base64).
// Modifies blocks in place.
func (s *Store) internalizeImages(content llm.Content) {
	for i := range content {
		b := &content[i]
		if b.Type != "image" || b.Image == nil || b.Image.FilePath == "" {
			continue
		}

		raw, err := os.ReadFile(b.Image.FilePath)
		if err != nil {
			slog.Warn("failed to read image file", "err", err, "path", b.Image.FilePath)
			continue
		}

		b.Image.Data = base64.StdEncoding.EncodeToString(raw)
	}
}

// randomHex returns a random hex string of n bytes (2n hex chars).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
