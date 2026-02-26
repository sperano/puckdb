package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFranchiseRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewFranchiseRepo(storage)

	data := []byte(`[{"id": 1, "fullName": "New Jersey Devils"}]`)

	err := repo.Save(data)
	require.NoError(t, err)

	assert.True(t, repo.Exists())

	franchises, err := repo.Get()
	require.NoError(t, err)
	assert.Len(t, franchises, 1)
	assert.Equal(t, int64(1), franchises[0].ID)
	assert.Equal(t, "New Jersey Devils", franchises[0].FullName)
}

func TestFranchiseRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewFranchiseRepo(storage)

	data := []byte(`[{"id": 1}]`)

	repo.Save(data)

	got, err := repo.GetRaw()
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestFranchiseRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewFranchiseRepo(storage)

	storage.SetFile(FranchisesPath(), []byte(`not valid json`))

	_, err := repo.Get()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse franchises")
}

func TestFranchiseRepo_Exists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewFranchiseRepo(storage)

	assert.False(t, repo.Exists())

	repo.Save([]byte(`[]`))
	assert.True(t, repo.Exists())
}
