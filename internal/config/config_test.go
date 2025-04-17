package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	// Create a temporary HOME directory for this test
	tmpHome, err := os.MkdirTemp("", "codex-test-home")
	if err != nil {
		t.Fatalf("Failed to create temp home directory: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Save the original HOME and restore it after the test
	origHome := os.Getenv("HOME")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
	})
	os.Setenv("HOME", tmpHome)

	// Create a test config directory
	configDir := filepath.Join(tmpHome, DefaultConfigDir)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Load config with no existing files
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Verify defaults
	if cfg.Model != DefaultModel {
		t.Errorf("Expected Model=%s, got %s", DefaultModel, cfg.Model)
	}

	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("Expected BaseURL=%s, got %s", DefaultBaseURL, cfg.BaseURL)
	}

	if cfg.APITimeout != DefaultAPITimeout {
		t.Errorf("Expected APITimeout=%d, got %d", DefaultAPITimeout, cfg.APITimeout)
	}

	if cfg.ApprovalMode != Suggest {
		t.Errorf("Expected ApprovalMode=%s, got %s", Suggest, cfg.ApprovalMode)
	}
}

func TestLoadWithAPIKey(t *testing.T) {
	// Create a temporary HOME directory for this test
	tmpHome, err := os.MkdirTemp("", "codex-test-home")
	if err != nil {
		t.Fatalf("Failed to create temp home directory: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	// Save the original HOME and API key, restore them after the test
	origHome := os.Getenv("HOME")
	origAPIKey := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("OPENAI_API_KEY", origAPIKey)
	})
	os.Setenv("HOME", tmpHome)

	// Set a test API key
	testAPIKey := "test-api-key"
	os.Setenv("OPENAI_API_KEY", testAPIKey)

	// Load config
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Verify the API key was set
	if cfg.APIKey != testAPIKey {
		t.Errorf("Expected APIKey=%s, got %s", testAPIKey, cfg.APIKey)
	}
}

func TestLoadProjectDoc(t *testing.T) {
	// Create a temporary directory for this test
	tmpDir, err := os.MkdirTemp("", "codex-test-project")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test project doc
	projectDocPath := filepath.Join(tmpDir, "test-doc.md")
	testContent := "# Test Project\n\nThis is a test project doc."
	if err := os.WriteFile(projectDocPath, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write test project doc: %v", err)
	}

	// Create a config with the test project doc
	cfg := &Config{
		ProjectDocPath: projectDocPath,
	}

	// Load the project doc
	content, err := cfg.LoadProjectDoc()
	if err != nil {
		t.Fatalf("LoadProjectDoc() failed: %v", err)
	}

	// Verify the content
	if content != testContent {
		t.Errorf("Expected content=%q, got %q", testContent, content)
	}

	// Test with disabled project doc
	cfg.DisableProjectDoc = true
	content, err = cfg.LoadProjectDoc()
	if err != nil {
		t.Fatalf("LoadProjectDoc() failed: %v", err)
	}
	if content != "" {
		t.Errorf("Expected empty content with disabled project doc, got %q", content)
	}
}
