package patch

// ActionType defines the type of patch action
type ActionType string

const (
	// ActionAdd represents adding a new file
	ActionAdd ActionType = "add"
	// ActionDelete represents deleting an existing file
	ActionDelete ActionType = "delete"
	// ActionUpdate represents updating an existing file
	ActionUpdate ActionType = "update"
)

// Chunk represents a change in a specific part of a file
type Chunk struct {
	OrigIndex int      // Line index in the original file
	DelLines  []string // Lines to be deleted
	InsLines  []string // Lines to be inserted
}

// PatchAction represents an action to be performed on a file
type PatchAction struct {
	Type     ActionType
	FilePath string
	NewFile  string  // Content for new files (only used for ActionAdd)
	Chunks   []Chunk // Chunks for updates
	MovePath string  // Path to move the file to (optional)
}

// Patch represents a collection of actions to be applied
type Patch struct {
	Actions map[string]PatchAction // Map of filepath to action
}

// FileChange represents the change to be made to a file
type FileChange struct {
	Type       ActionType
	OldContent string
	NewContent string
	MovePath   string
}

// Commit represents a set of changes to be applied to files
type Commit struct {
	Changes map[string]FileChange // Map of filepath to change
}

// PatchResult represents the result of a patch operation
type PatchResult struct {
	FilePath      string
	OperationType string
	Success       bool
	Error         error
	Message       string
	LineStats     struct {
		Added    int
		Deleted  int
		Original int
		New      int
	}
}

// DiffError represents an error that occurred during patch processing
type DiffError struct {
	Message string
}

func (e DiffError) Error() string {
	return e.Message
}
