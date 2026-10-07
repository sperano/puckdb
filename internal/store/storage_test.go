package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDefaultStorage_WritesUnderDataPath(t *testing.T) {
	t.Parallel()
	dataPath := t.TempDir()
	storage := NewDefaultStorage(dataPath)
	require.IsType(t, &InstrumentedStorage{}, storage)

	require.NoError(t, storage.Write(context.Background(), "games/boxscore.json", []byte("{}")))

	data, err := os.ReadFile(filepath.Join(dataPath, "games", "boxscore.json"))
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))
}
