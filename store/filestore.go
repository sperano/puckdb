package store

import (
	"fmt"
	"os"
	"path"
)

// File type name constants for metrics and logging
const (
	FileTypeDailySchedule   = "DailySchedule"
	FileTypeBoxscore        = "Boxscore"
	FileTypePlayByPlay      = "PlayByPlay"
	FileTypeShiftChart      = "ShiftChart"
	FileTypeGameStory       = "GameStory"
	FileTypeFranchises      = "Franchises"
	FileTypeSeasonsManifest = "SeasonsManifest"
	FileTypeSeasonStandings = "SeasonStandings"
	FileTypeLeague          = "League"
	FileTypeTeam            = "Team"
	FileTypeRoster          = "Roster"
	FileTypeTeamSummary     = "TeamSummary"
	FileTypePlayerLanding   = "PlayerLanding"
	FileTypeYahooPlayer     = "YahooPlayer"
	FileTypeGameKey         = "GameKey"
	FileTypeUnknown         = "Unknown"
)

// File interface defines methods that stored files must implement
type File interface {
	Dir() string
	Name() string
	Ext() string
}

// Store interface defines methods for reading/writing files to persistent storage.
// FileStore implements this interface; tests can provide mock implementations.
type Store interface {
	Read(file File) ([]byte, error)
	Write(file File, content []byte) error
	Exists(file File) bool
	MkdirAll(dir string, perm os.FileMode) error
	Remove(file File) error
	FullPath(file File) string
	ListFiles(sample File, parser FilenameParser) ([]File, error)
}

// Path returns the full path for a file (dir/name.ext)
func Path(file File) string {
	return path.Join(file.Dir(), fmt.Sprintf("%s.%s", file.Name(), file.Ext()))
}

// FilenameParser parses a filename (without extension) into a File.
// Returns nil if the filename doesn't match the expected pattern.
type FilenameParser func(name string) File

// FileStore is a filesystem-based store for persisting downloaded API data
type FileStore struct {
	RootPath string
}

// NewFileStore creates a new FileStore with the given root path
func NewFileStore(rootPath string) *FileStore {
	return &FileStore{
		RootPath: rootPath,
	}
}

// FullPath returns the absolute path for a file
func (fs *FileStore) FullPath(file File) string {
	return path.Join(fs.RootPath, Path(file))
}

// Read reads the contents of a file
func (fs *FileStore) Read(file File) ([]byte, error) {
	return os.ReadFile(fs.FullPath(file))
}

// Write writes content to a file, creating directories as needed
func (fs *FileStore) Write(file File, content []byte) error {
	fullPath := fs.FullPath(file)
	dir := path.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, content, 0644)
}

// Exists checks if a file exists
func (fs *FileStore) Exists(file File) bool {
	_, err := os.Stat(fs.FullPath(file))
	return err == nil
}

// MkdirAll creates a directory and all parent directories
func (fs *FileStore) MkdirAll(dir string, perm os.FileMode) error {
	return os.MkdirAll(path.Join(fs.RootPath, dir), perm)
}

// Remove deletes a file
func (fs *FileStore) Remove(file File) error {
	return os.Remove(fs.FullPath(file))
}

// ListFiles returns all files in a directory matching the sample file's extension.
// The sample file is used to determine the directory and extension to scan.
// Returns file metadata only; caller is responsible for reading/processing contents.
func (fs *FileStore) ListFiles(sample File, parser FilenameParser) ([]File, error) {
	return listAll(fs, sample, parser)
}

// listAll returns all files in a directory matching the sample file's extension.
// The sample file is used to determine the directory and extension to scan.
// Returns file metadata only; caller is responsible for reading/processing contents.
func listAll(
	fs *FileStore,
	sample File,
	filenameParser FilenameParser,
) ([]File, error) {
	dir := sample.Dir()
	ext := sample.Ext()
	dirPath := path.Join(fs.RootPath, dir)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var files []File
	expectedSuffix := "." + ext

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if len(filename) <= len(expectedSuffix) || filename[len(filename)-len(expectedSuffix):] != expectedSuffix {
			continue
		}

		// Strip extension to get the name
		name := filename[:len(filename)-len(expectedSuffix)]

		file := filenameParser(name)
		if file == nil {
			continue
		}

		files = append(files, file)
	}

	return files, nil
}
