package fileops

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// FileInfo represents information about a file
type FileInfo struct {
	Path      string
	Content   string
	Size      int64
	Mode      os.FileMode
	IsDir     bool
	ModTime   int64
	Exists    bool
	IsSymlink bool
}

// GetFile reads a file and returns its contents
func GetFile(path string) (*FileInfo, error) {
	// Check if the file exists
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &FileInfo{
				Path:   path,
				Exists: false,
			}, nil
		}
		return nil, fmt.Errorf("error getting file info: %w", err)
	}

	// Create FileInfo
	fileInfo := &FileInfo{
		Path:      path,
		Size:      info.Size(),
		Mode:      info.Mode(),
		IsDir:     info.IsDir(),
		ModTime:   info.ModTime().Unix(),
		Exists:    true,
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
	}

	// If it's a symlink, resolve it
	if fileInfo.IsSymlink {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("error reading symlink: %w", err)
		}
		// If the target is relative, make it absolute
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		// Get info about the target
		targetInfo, err := GetFile(target)
		if err != nil {
			return nil, fmt.Errorf("error getting symlink target info: %w", err)
		}
		// Update the FileInfo with target info
		fileInfo.IsDir = targetInfo.IsDir
		fileInfo.Size = targetInfo.Size
	}

	// If it's a directory, don't read the content
	if fileInfo.IsDir {
		return fileInfo, nil
	}

	// Read the file content
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}
	fileInfo.Content = string(content)

	return fileInfo, nil
}

// WriteFile writes content to a file
func WriteFile(path string, content string, mode os.FileMode) error {
	// Create parent directories if they don't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating directories: %w", err)
	}

	// Write the file
	if err := ioutil.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}

// ListDir lists the contents of a directory
func ListDir(path string) ([]FileInfo, error) {
	// Check if the directory exists
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("error getting directory info: %w", err)
	}

	// Check if it's a directory
	if !info.IsDir() {
		return nil, errors.New("path is not a directory")
	}

	// Read the directory
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("error reading directory: %w", err)
	}

	// Create FileInfo for each entry
	var result []FileInfo
	for _, entry := range entries {
		result = append(result, FileInfo{
			Path:      filepath.Join(path, entry.Name()),
			Size:      entry.Size(),
			Mode:      entry.Mode(),
			IsDir:     entry.IsDir(),
			ModTime:   entry.ModTime().Unix(),
			Exists:    true,
			IsSymlink: entry.Mode()&os.ModeSymlink != 0,
		})
	}

	return result, nil
}

// Exists checks if a file or directory exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir checks if a path is a directory
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// IsFile checks if a path is a file
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Diff represents a diff between two files
type Diff struct {
	Path    string
	OldText string
	NewText string
	Hunks   []DiffHunk
}

// DiffHunk represents a hunk in a diff
type DiffHunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Content  string
}

// CreateDiff creates a diff between two strings
func CreateDiff(path, oldText, newText string) (*Diff, error) {
	// This is a placeholder for a real diff implementation
	// In a real implementation, we would use a proper diff algorithm

	// For now, just return a simple diff
	return &Diff{
		Path:    path,
		OldText: oldText,
		NewText: newText,
		Hunks: []DiffHunk{
			{
				OldStart: 1,
				OldLines: len(strings.Split(oldText, "\n")),
				NewStart: 1,
				NewLines: len(strings.Split(newText, "\n")),
				Content:  newText,
			},
		},
	}, nil
}

// ApplyDiff applies a diff to a file
func ApplyDiff(diff *Diff) error {
	// Read the current file content
	fileInfo, err := GetFile(diff.Path)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	// Check if the file exists
	if !fileInfo.Exists {
		// If the file doesn't exist, create it
		return WriteFile(diff.Path, diff.NewText, 0644)
	}

	// Check if the current content matches the expected old content
	if fileInfo.Content != diff.OldText {
		return errors.New("file content has changed since diff was created")
	}

	// Apply the diff (for now, just replace the content)
	return WriteFile(diff.Path, diff.NewText, fileInfo.Mode)
}
