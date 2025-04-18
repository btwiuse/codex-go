package patch

import (
	"strings"
)

// Constants for patch parsing
const (
	PatchBeginMarker = "*** Begin Patch"
	PatchEndMarker   = "*** End Patch"
	UpdateFilePrefix = "*** Update File: "
	AddFilePrefix    = "*** Add File: "
	DeleteFilePrefix = "*** Delete File: "
	MoveToPrefix     = "*** Move to: "
	EndOfFileMarker  = "*** End of File"
)

// Parser is a struct that handles parsing patch text into operations
type Parser struct {
	CurrentFiles map[string]string
	Lines        []string
	Index        int
	Patch        Patch
	Fuzz         int
}

// NewParser creates a new parser instance
func NewParser(currentFiles map[string]string, lines []string) *Parser {
	return &Parser{
		CurrentFiles: currentFiles,
		Lines:        lines,
		Index:        0,
		Patch: Patch{
			Actions: make(map[string]PatchAction),
		},
		Fuzz: 0,
	}
}

// isDone checks if parsing is complete
func (p *Parser) isDone(prefixes []string) bool {
	if p.Index >= len(p.Lines) {
		return true
	}

	if prefixes != nil {
		for _, prefix := range prefixes {
			if strings.HasPrefix(p.Lines[p.Index], prefix) {
				return true
			}
		}
	}

	return false
}

// startsWith checks if the current line starts with a prefix
func (p *Parser) startsWith(prefixes []string) bool {
	if p.Index >= len(p.Lines) {
		return false
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(p.Lines[p.Index], prefix) {
			return true
		}
	}

	return false
}

// readString reads a line with the given prefix and returns the rest
func (p *Parser) readString(prefix string, returnEverything bool) string {
	if p.Index >= len(p.Lines) {
		return ""
	}

	if strings.HasPrefix(p.Lines[p.Index], prefix) {
		text := p.Lines[p.Index]
		if !returnEverything {
			text = strings.TrimPrefix(text, prefix)
		}
		p.Index++
		return text
	}

	return ""
}

// Parse parses the patch text into patch actions
func (p *Parser) Parse() error {
	for !p.isDone([]string{PatchEndMarker}) {
		path := p.readString(UpdateFilePrefix, false)
		if path != "" {
			if _, exists := p.Patch.Actions[path]; exists {
				return &DiffError{Message: "Update File Error: Duplicate Path: " + path}
			}

			moveTo := p.readString(MoveToPrefix, false)

			if _, exists := p.CurrentFiles[path]; !exists {
				return &DiffError{Message: "Update File Error: Missing File: " + path}
			}

			text := p.CurrentFiles[path]
			action, err := p.parseUpdateFile(text)
			if err != nil {
				return err
			}

			if moveTo != "" {
				action.MovePath = moveTo
			}

			p.Patch.Actions[path] = action
			continue
		}

		path = p.readString(DeleteFilePrefix, false)
		if path != "" {
			if _, exists := p.Patch.Actions[path]; exists {
				return &DiffError{Message: "Delete File Error: Duplicate Path: " + path}
			}

			if _, exists := p.CurrentFiles[path]; !exists {
				return &DiffError{Message: "Delete File Error: Missing File: " + path}
			}

			p.Patch.Actions[path] = PatchAction{
				Type:     ActionDelete,
				FilePath: path,
				Chunks:   []Chunk{},
			}
			continue
		}

		path = p.readString(AddFilePrefix, false)
		if path != "" {
			if _, exists := p.Patch.Actions[path]; exists {
				return &DiffError{Message: "Add File Error: Duplicate Path: " + path}
			}

			if _, exists := p.CurrentFiles[path]; exists {
				return &DiffError{Message: "Add File Error: File already exists: " + path}
			}

			action, err := p.parseAddFile()
			if err != nil {
				return err
			}
			action.FilePath = path

			p.Patch.Actions[path] = action
			continue
		}

		if p.Index < len(p.Lines) {
			return &DiffError{Message: "Unknown Line: " + p.Lines[p.Index]}
		} else {
			return &DiffError{Message: "Unexpected end of patch"}
		}
	}

	if !p.startsWith([]string{PatchEndMarker}) {
		return &DiffError{Message: "Missing End Patch"}
	}

	p.Index++
	return nil
}

// parseUpdateFile parses an update file section
func (p *Parser) parseUpdateFile(text string) (PatchAction, error) {
	action := PatchAction{
		Type:   ActionUpdate,
		Chunks: []Chunk{},
	}

	fileLines := strings.Split(text, "\n")
	index := 0

	for !p.isDone([]string{
		PatchEndMarker,
		UpdateFilePrefix,
		DeleteFilePrefix,
		AddFilePrefix,
		EndOfFileMarker,
	}) {
		// Parse the modification markers in the current section
		oldContext, chunks, endIndex, eof, err := peekNextSection(p.Lines, p.Index)
		if err != nil {
			return action, err
		}

		// Try to find where in the file this change applies
		newIndex, fuzz := findContext(fileLines, oldContext, index, eof)
		if newIndex == -1 {
			return action, &DiffError{Message: "Could not find context in file"}
		}

		// Track the highest fuzziness score
		if fuzz > p.Fuzz {
			p.Fuzz = fuzz
		}

		// Adjust the chunks to point to the right line numbers
		for i := range chunks {
			chunks[i].OrigIndex = newIndex + chunks[i].OrigIndex
		}

		action.Chunks = append(action.Chunks, chunks...)
		index = newIndex + len(oldContext)
		p.Index = endIndex
	}

	// Skip the EndOfFileMarker if present
	if p.Index < len(p.Lines) && p.Lines[p.Index] == EndOfFileMarker {
		p.Index++
	}

	return action, nil
}

// parseAddFile parses an add file section
func (p *Parser) parseAddFile() (PatchAction, error) {
	var lines []string

	for !p.isDone([]string{
		PatchEndMarker,
		UpdateFilePrefix,
		DeleteFilePrefix,
		AddFilePrefix,
	}) {
		line := p.readString("", true)
		if !strings.HasPrefix(line, "+") {
			return PatchAction{}, &DiffError{Message: "Invalid Add File Line: " + line}
		}

		// Remove the "+" prefix
		lines = append(lines, line[1:])
	}

	return PatchAction{
		Type:    ActionAdd,
		NewFile: strings.Join(lines, "\n"),
		Chunks:  []Chunk{},
	}, nil
}

// TextToPatch converts patch text to a Patch object
func TextToPatch(text string, orig map[string]string) (Patch, int, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")

	if len(lines) < 2 || !strings.HasPrefix(lines[0], PatchBeginMarker) || lines[len(lines)-1] != PatchEndMarker {
		return Patch{}, 0, &DiffError{Message: "Invalid patch format: must begin with '*** Begin Patch' and end with '*** End Patch'"}
	}

	parser := NewParser(orig, lines)
	parser.Index = 1 // Skip the Begin Patch line

	err := parser.Parse()
	if err != nil {
		return Patch{}, 0, err
	}

	return parser.Patch, parser.Fuzz, nil
}

// peekNextSection analyzes the next section of the patch to extract context and chunks
func peekNextSection(lines []string, initialIndex int) ([]string, []Chunk, int, bool, error) {
	index := initialIndex
	var oldContext []string
	var delLines []string
	var insLines []string
	var chunks []Chunk
	mode := "keep"

	for index < len(lines) {
		s := lines[index]

		// End of section markers
		if strings.HasPrefix(s, "@@") ||
			strings.HasPrefix(s, PatchEndMarker) ||
			strings.HasPrefix(s, UpdateFilePrefix) ||
			strings.HasPrefix(s, DeleteFilePrefix) ||
			strings.HasPrefix(s, AddFilePrefix) ||
			strings.HasPrefix(s, EndOfFileMarker) {
			break
		}

		// Skip separator markers
		if s == "***" {
			index++
			continue
		}

		// Invalid section marker
		if strings.HasPrefix(s, "***") {
			return nil, nil, 0, false, &DiffError{Message: "Invalid Line: " + s}
		}

		index++
		lastMode := mode
		line := s

		// Determine line type based on prefix
		switch {
		case strings.HasPrefix(line, "+"):
			mode = "add"
			line = line[1:]
		case strings.HasPrefix(line, "-"):
			mode = "delete"
			line = line[1:]
		case strings.HasPrefix(line, " "):
			mode = "keep"
			line = line[1:]
		default:
			// Be tolerant of missing leading space in context lines
			mode = "keep"
		}

		// When we switch modes, finalize the current chunk if needed
		if mode == "keep" && lastMode != mode {
			if len(insLines) > 0 || len(delLines) > 0 {
				chunks = append(chunks, Chunk{
					OrigIndex: len(oldContext) - len(delLines),
					DelLines:  delLines,
					InsLines:  insLines,
				})

				delLines = []string{}
				insLines = []string{}
			}
		}

		// Add the line to the appropriate collection
		if mode == "delete" {
			delLines = append(delLines, line)
			oldContext = append(oldContext, line)
		} else if mode == "add" {
			insLines = append(insLines, line)
		} else {
			oldContext = append(oldContext, line)
		}
	}

	// Finalize the last chunk if there are pending lines
	if len(insLines) > 0 || len(delLines) > 0 {
		chunks = append(chunks, Chunk{
			OrigIndex: len(oldContext) - len(delLines),
			DelLines:  delLines,
			InsLines:  insLines,
		})
	}

	// Check if we reached end of file marker
	eof := false
	if index < len(lines) && lines[index] == EndOfFileMarker {
		index++
		eof = true
	}

	return oldContext, chunks, index, eof, nil
}

// findContext finds the best match for a set of context lines within a file
func findContext(lines []string, context []string, start int, eof bool) (int, int) {
	if len(context) == 0 {
		return start, 0
	}

	// If we're at EOF, try searching from the end of the file first
	if eof {
		if len(lines) >= len(context) {
			newIndex, fuzz := findContextCore(lines, context, len(lines)-len(context))
			if newIndex != -1 {
				return newIndex, fuzz
			}
		}

		// If that fails, try from the start
		newIndex, fuzz := findContextCore(lines, context, start)
		if newIndex != -1 {
			// Add fuzz penalty for not being at EOF
			return newIndex, fuzz + 10000
		}

		return -1, 0
	}

	// Normal search from the start position
	return findContextCore(lines, context, start)
}

// findContextCore implements the core context matching logic with different levels of fuzzy matching
func findContextCore(lines []string, context []string, start int) (int, int) {
	if len(context) == 0 {
		return start, 0
	}

	// Try exact match first
	for i := start; i <= len(lines)-len(context); i++ {
		match := true
		for j := 0; j < len(context); j++ {
			if lines[i+j] != context[j] {
				match = false
				break
			}
		}
		if match {
			return i, 0
		}
	}

	// Try trimming line endings
	for i := start; i <= len(lines)-len(context); i++ {
		match := true
		for j := 0; j < len(context); j++ {
			if strings.TrimRight(lines[i+j], " \t") != strings.TrimRight(context[j], " \t") {
				match = false
				break
			}
		}
		if match {
			return i, 1
		}
	}

	// Try fully trimmed lines
	for i := start; i <= len(lines)-len(context); i++ {
		match := true
		for j := 0; j < len(context); j++ {
			if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(context[j]) {
				match = false
				break
			}
		}
		if match {
			return i, 100
		}
	}

	// No match found
	return -1, 0
}
