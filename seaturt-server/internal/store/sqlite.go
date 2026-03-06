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
	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
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
		`INSERT INTO messages (id, agent_id, role, content, tool_calls, tool_call_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.AgentID, m.Role, string(contentJSON), m.ToolCalls, m.ToolCallID, m.CreatedAt,
	)
	return err
}

func (s *Store) ListMessages(agentID string) ([]*agent.Message, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, role, content, tool_calls, tool_call_id, created_at
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
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Role, &contentJSON, &m.ToolCalls, &m.ToolCallID, &m.CreatedAt); err != nil {
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

func (s *Store) DeleteMessages(agentID string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE agent_id = ?`, agentID)
	return err
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
