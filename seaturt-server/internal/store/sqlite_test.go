package store

import (
	"encoding/base64"
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

// IT-20: multimodal message store and retrieve roundtrip
func TestMessageStoreRoundtrip_TextOnly(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	msg := &agent.Message{
		ID:        "msg_1",
		AgentID:   ag.ID,
		Role:      "user",
		Content:   llm.Content{llm.NewTextContent("hello world")},
		CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateMessage(msg))

	messages, err := s.ListMessages(ag.ID)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	assert.Equal(t, "msg_1", messages[0].ID)
	assert.Equal(t, "user", messages[0].Role)
	require.Len(t, messages[0].Content, 1)
	assert.Equal(t, "text", messages[0].Content[0].Type)
	assert.Equal(t, "hello world", messages[0].Content[0].Text)
}

// IT-22: image auto-externalization to workspace
func TestMessageStoreRoundtrip_ImageExternalized(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	// Create a small PNG-like base64 data
	rawImage := []byte("fake-png-data-for-testing")
	b64Data := base64.StdEncoding.EncodeToString(rawImage)

	msg := &agent.Message{
		ID:      "msg_img_1",
		AgentID: ag.ID,
		Role:    "user",
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
	messages, err := s.ListMessages(ag.ID)
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

	img1 := base64.StdEncoding.EncodeToString([]byte("image-one"))
	img2 := base64.StdEncoding.EncodeToString([]byte("image-two"))

	msg := &agent.Message{
		ID:      "msg_multi_img",
		AgentID: ag.ID,
		Role:    "tool",
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
	messages, err := s.ListMessages(ag.ID)
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

	msg := &agent.Message{
		ID:        "msg_text",
		AgentID:   ag.ID,
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

// IT-20c: delete messages
func TestMessageStore_DeleteMessages(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	msg := &agent.Message{
		ID:        "msg_del",
		AgentID:   ag.ID,
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
