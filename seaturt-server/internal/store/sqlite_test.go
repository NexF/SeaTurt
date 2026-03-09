package store

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore creates a temporary Store for testing.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDB, err := os.CreateTemp("", "store-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	tmpDB.Close()

	s, err := New(dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		s.Close()
		os.Remove(dbPath)
	})
	return s
}

// newTestAgent creates a test agent with a temp workspace and registers it in the store.
func newTestAgent(t *testing.T, s *Store) *agent.Agent {
	t.Helper()
	wsPath, err := os.MkdirTemp("", "store-ws-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsPath) })

	ag := &agent.Agent{
		ID:            "agent_test_1",
		Name:          "test-agent",
		Status:        agent.StatusRunning,
		WorkspacePath: wsPath,
		Config:        agent.AgentConfig{Model: "test-model"},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	require.NoError(t, s.CreateAgent(ag))
	return ag
}

// newTestSession creates a test session for the given agent.
func newTestSession(t *testing.T, s *Store, agentID string) *agent.Session {
	t.Helper()
	now := time.Now()
	sess := &agent.Session{
		ID:        "sess_test_1",
		AgentID:   agentID,
		Title:     "新对话",
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))
	return sess
}

// IT-20: multimodal message store and retrieve roundtrip
func TestMessageStoreRoundtrip_TextOnly(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)
	sess := newTestSession(t, s, ag.ID)

	msg := &agent.Message{
		ID:        "msg_1",
		AgentID:   ag.ID,
		SessionID: sess.ID,
		Role:      "user",
		Content:   llm.Content{llm.NewTextContent("hello world")},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	messages, err := s.ListMessagesBySession(sess.ID)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	assert.Equal(t, "msg_1", messages[0].ID)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, sess.ID, messages[0].SessionID)
	require.Len(t, messages[0].Content, 1)
	assert.Equal(t, "text", messages[0].Content[0].Type)
	assert.Equal(t, "hello world", messages[0].Content[0].Text)
}

// IT-22: image auto-externalization to workspace
func TestMessageStoreRoundtrip_ImageExternalized(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)
	sess := newTestSession(t, s, ag.ID)

	// Create a small PNG-like base64 data
	rawImage := []byte("fake-png-data-for-testing")
	b64Data := base64.StdEncoding.EncodeToString(rawImage)

	msg := &agent.Message{
		ID:        "msg_img_1",
		AgentID:   ag.ID,
		SessionID: sess.ID,
		Role:      "user",
		Content: llm.Content{
			llm.NewTextContent("check this image"),
			llm.NewImageContent(b64Data, "image/png"),
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	// Verify file was created in uploads dir
	uploadsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "uploads")
	entries, err := os.ReadDir(uploadsDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "should have exactly one uploaded file")
	assert.Contains(t, entries[0].Name(), ".png")

	// Verify file content matches original
	filePath := filepath.Join(uploadsDir, entries[0].Name())
	fileData, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, rawImage, fileData)

	// Retrieve and verify roundtrip
	messages, err := s.ListMessagesBySession(sess.ID)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 2)

	// Text block
	assert.Equal(t, "text", messages[0].Content[0].Type)
	assert.Equal(t, "check this image", messages[0].Content[0].Text)

	// Image block — Data should be restored from file
	imgBlock := messages[0].Content[1]
	assert.Equal(t, "image", imgBlock.Type)
	require.NotNil(t, imgBlock.Image)
	assert.Equal(t, "image/png", imgBlock.Image.MimeType)
	assert.Equal(t, b64Data, imgBlock.Image.Data, "base64 data should be restored from file")
}

// IT-22b: multiple images externalized separately
func TestMessageStoreRoundtrip_MultipleImages(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)
	sess := newTestSession(t, s, ag.ID)

	img1 := base64.StdEncoding.EncodeToString([]byte("image-one"))
	img2 := base64.StdEncoding.EncodeToString([]byte("image-two"))

	msg := &agent.Message{
		ID:        "msg_multi_img",
		AgentID:   ag.ID,
		SessionID: sess.ID,
		Role:      "tool",
		Content: llm.Content{
			llm.NewTextContent("here are two images"),
			llm.NewImageContent(img1, "image/jpeg"),
			llm.NewImageContent(img2, "image/png"),
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	// Verify two files created
	uploadsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "uploads")
	entries, err := os.ReadDir(uploadsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "should have two uploaded files")

	// Verify roundtrip
	messages, err := s.ListMessagesBySession(sess.ID)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 3)

	assert.Equal(t, img1, messages[0].Content[1].Image.Data)
	assert.Equal(t, "image/jpeg", messages[0].Content[1].Image.MimeType)
	assert.Equal(t, img2, messages[0].Content[2].Image.Data)
	assert.Equal(t, "image/png", messages[0].Content[2].Image.MimeType)
}

// IT-20b: text-only message does not create any upload files
func TestMessageStore_TextOnly_NoUploads(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)
	sess := newTestSession(t, s, ag.ID)

	msg := &agent.Message{
		ID:        "msg_text",
		AgentID:   ag.ID,
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   llm.Content{llm.NewTextContent("just text")},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	// uploads dir should not exist
	uploadsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "uploads")
	_, err := os.Stat(uploadsDir)
	assert.True(t, os.IsNotExist(err), "uploads dir should not be created for text-only messages")
}

// IT-20c: delete messages by agent
func TestMessageStore_DeleteMessages(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)
	sess := newTestSession(t, s, ag.ID)

	msg := &agent.Message{
		ID:        "msg_del",
		AgentID:   ag.ID,
		SessionID: sess.ID,
		Role:      "user",
		Content:   llm.Content{llm.NewTextContent("to be deleted")},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	messages, err := s.ListMessages(ag.ID)
	require.NoError(t, err)
	assert.Len(t, messages, 1)

	require.NoError(t, s.DeleteMessages(ag.ID))

	messages, err = s.ListMessages(ag.ID)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

// --- Session CRUD tests ---

func TestSessionCRUD_CreateAndList(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess1 := &agent.Session{
		ID: "sess_1", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	sess2 := &agent.Session{
		ID: "sess_2", AgentID: ag.ID, Title: "代码审查",
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}

	require.NoError(t, s.CreateSession(sess1))
	require.NoError(t, s.CreateSession(sess2))

	sessions, err := s.ListSessions(ag.ID)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	// Should be ordered by updated_at DESC
	assert.Equal(t, "sess_2", sessions[0].ID)
	assert.Equal(t, "sess_1", sessions[1].ID)
}

func TestSessionCRUD_GetSession(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_get", AgentID: ag.ID, Title: "测试会话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	got, err := s.GetSession("sess_get")
	require.NoError(t, err)
	assert.Equal(t, "测试会话", got.Title)
	assert.Equal(t, ag.ID, got.AgentID)
}

func TestSessionCRUD_GetSession_NotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	_, err := s.GetSession("nonexistent")
	assert.Error(t, err)
}

func TestSessionCRUD_UpdateSession(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_upd", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	sess.Title = "帮我写代码"
	sess.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, s.UpdateSession(sess))

	got, err := s.GetSession("sess_upd")
	require.NoError(t, err)
	assert.Equal(t, "帮我写代码", got.Title)
}

func TestSessionCRUD_DeleteSession(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_del", AgentID: ag.ID, Title: "即将删除",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	require.NoError(t, s.DeleteSession("sess_del"))

	_, err := s.GetSession("sess_del")
	assert.Error(t, err)
}

// Session 级联清理 messages 测试
func TestSessionDelete_CascadeMessages(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_cascade", AgentID: ag.ID, Title: "级联测试",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	// Create messages in this session
	for i := 0; i < 3; i++ {
		msg := &agent.Message{
			ID:        fmt.Sprintf("msg_cascade_%d", i),
			AgentID:   ag.ID,
			SessionID: sess.ID,
			Role:      "user",
			Content:   llm.Content{llm.NewTextContent(fmt.Sprintf("message %d", i))},
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, s.CreateMessage(msg))
	}

	// Verify messages exist
	messages, err := s.ListMessagesBySession(sess.ID)
	require.NoError(t, err)
	assert.Len(t, messages, 3)

	// Delete session (should cascade delete messages)
	require.NoError(t, s.DeleteSession(sess.ID))

	// Verify messages are gone
	messages, err = s.ListMessagesBySession(sess.ID)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

// Message 按 session_id 隔离查询测试
func TestMessageIsolationBySession(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess1 := &agent.Session{
		ID: "sess_iso_1", AgentID: ag.ID, Title: "会话1",
		CreatedAt: now, UpdatedAt: now,
	}
	sess2 := &agent.Session{
		ID: "sess_iso_2", AgentID: ag.ID, Title: "会话2",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess1))
	require.NoError(t, s.CreateSession(sess2))

	// Add messages to different sessions
	msg1 := &agent.Message{
		ID: "msg_iso_1", AgentID: ag.ID, SessionID: sess1.ID,
		Role: "user", Content: llm.Content{llm.NewTextContent("hello from session 1")},
		CreatedAt: now,
	}
	msg2 := &agent.Message{
		ID: "msg_iso_2", AgentID: ag.ID, SessionID: sess2.ID,
		Role: "user", Content: llm.Content{llm.NewTextContent("hello from session 2")},
		CreatedAt: now,
	}
	require.NoError(t, s.CreateMessage(msg1))
	require.NoError(t, s.CreateMessage(msg2))

	// Verify isolation
	msgs1, err := s.ListMessagesBySession(sess1.ID)
	require.NoError(t, err)
	require.Len(t, msgs1, 1)
	assert.Equal(t, "hello from session 1", msgs1[0].Content[0].Text)

	msgs2, err := s.ListMessagesBySession(sess2.ID)
	require.NoError(t, err)
	require.Len(t, msgs2, 1)
	assert.Equal(t, "hello from session 2", msgs2[0].Content[0].Text)

	// ListMessages by agent should return both
	allMsgs, err := s.ListMessages(ag.ID)
	require.NoError(t, err)
	assert.Len(t, allMsgs, 2)
}

// Delete messages by session only
func TestDeleteMessagesBySession(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	sess1 := &agent.Session{
		ID: "sess_dms_1", AgentID: ag.ID, Title: "会话1",
		CreatedAt: now, UpdatedAt: now,
	}
	sess2 := &agent.Session{
		ID: "sess_dms_2", AgentID: ag.ID, Title: "会话2",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess1))
	require.NoError(t, s.CreateSession(sess2))

	msg1 := &agent.Message{
		ID: "msg_dms_1", AgentID: ag.ID, SessionID: sess1.ID,
		Role: "user", Content: llm.Content{llm.NewTextContent("sess1 msg")},
		CreatedAt: now,
	}
	msg2 := &agent.Message{
		ID: "msg_dms_2", AgentID: ag.ID, SessionID: sess2.ID,
		Role: "user", Content: llm.Content{llm.NewTextContent("sess2 msg")},
		CreatedAt: now,
	}
	require.NoError(t, s.CreateMessage(msg1))
	require.NoError(t, s.CreateMessage(msg2))

	// Delete only session 1 messages
	require.NoError(t, s.DeleteMessagesBySession(sess1.ID))

	msgs1, err := s.ListMessagesBySession(sess1.ID)
	require.NoError(t, err)
	assert.Empty(t, msgs1)

	msgs2, err := s.ListMessagesBySession(sess2.ID)
	require.NoError(t, err)
	assert.Len(t, msgs2, 1)
}
