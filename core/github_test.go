package core

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v41/github"
	"github.com/stretchr/testify/assert"
)

type MockGithubCache struct {
}

func (m *MockGithubCache) GetDirectoryContents(ctx context.Context, dir string) ([]*GithubFile, error) {
	return nil, nil
}

func (m *MockGithubCache) SetDirectoryContents(ctx context.Context, dir string, contents []*GithubFile) error {
	return nil
}

func (m *MockGithubCache) InvalidateDirectoryContents(ctx context.Context, dir string) error {
	return nil
}

func (m *MockGithubCache) GetFile(ctx context.Context, path string) ([]byte, error) {
	return nil, nil
}

func (m *MockGithubCache) SetFile(ctx context.Context, path string, data []byte) error {
	return nil
}

type MockGithubClient struct {
	Names           []string
	Code            int
	Error           error
	CreateOwner     string
	CreateRepo      string
	CreatePath      string
	CreateContent   []byte
	CreateMessage   string
	DownloadContent string
}

func (m *MockGithubClient) CreateFile(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentFileOptions) (*github.RepositoryContentResponse, *github.Response, error) {
	if m.Error != nil {
		return nil, nil, m.Error
	}
	m.CreateOwner = owner
	m.CreateRepo = repo
	m.CreatePath = path
	m.CreateContent = opts.Content
	m.CreateMessage = *opts.Message
	return nil, nil, nil
}

func (m *MockGithubClient) DownloadContents(ctx context.Context, owner, repo, filepath string, opts *github.RepositoryContentGetOptions) (io.ReadCloser, *github.Response, error) {
	if m.Error != nil {
		return nil, nil, m.Error
	}
	r := ioutil.NopCloser(strings.NewReader(m.DownloadContent))
	return r, nil, nil
}

func (m *MockGithubClient) GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
	if m.Error != nil {
		return nil, nil, nil, m.Error
	}
	dirContent := []*github.RepositoryContent{}
	for i := range m.Names {
		dirContent = append(dirContent, &github.RepositoryContent{Name: &m.Names[i]})
	}
	resp := github.Response{
		Response: &http.Response{
			StatusCode: m.Code,
		},
	}
	return nil, dirContent, &resp, nil
}

func TestGithubReadDirSuccess(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "foo_20221130024225.xml", "ta_ta_ta"}
	mock.Code = http.StatusOK
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	files, err := client.ReadDir(context.Background(), "dir")
	assert.Nil(t, err)
	assert.Equal(t, 2, len(files))
	assert.Equal(t, "foo_20210930024225.xml", files[0].Filename())
	assert.Equal(t, "foo_20221130024225.xml", files[1].Filename())
}

func TestGithubReadDirNotFound(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "foo_20221130024225.xml", "ta_ta_ta"}
	mock.Code = http.StatusNotFound
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	files, err := client.ReadDir(context.Background(), "dir")
	assert.Nil(t, err)
	assert.Equal(t, 0, len(files))
}

func TestGithubReadDirError(t *testing.T) {
	t.Parallel()
	errOriginal := errors.New("dummy")
	mock := &MockGithubClient{}
	mock.Error = errOriginal
	mock.Code = http.StatusOK
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	files, err := client.ReadDir(context.Background(), "dir")
	assert.Nil(t, files)
	assert.Equal(t, errOriginal, err)
}

func TestGithubFindSuccess(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
	mock.Code = http.StatusOK
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	files, err := client.Find(context.Background(), "dir", "bar", "xml")
	assert.Nil(t, err)
	assert.Equal(t, 2, len(files))
	// they will be sorted in descending order
	assert.Equal(t, "bar_20221205024225.xml", files[0].Filename())
	assert.Equal(t, "bar_20221130024225.xml", files[1].Filename())
}

func TestGithubFindError(t *testing.T) {
	t.Parallel()
	errOriginal := errors.New("dummy")
	mock := &MockGithubClient{}
	mock.Error = errOriginal
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	files, err := client.Find(context.Background(), "dir", "foo", "xml")
	assert.Nil(t, files)
	assert.Equal(t, errOriginal, err)
}

func TestGithubHasSuccess(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
	mock.Code = http.StatusOK
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	has, err := client.Has(context.Background(), "dir", "bar", "xml")
	assert.Nil(t, err)
	assert.True(t, has)
}

func TestGithubHasNotSuccess(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
	mock.Code = http.StatusOK
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	has, err := client.Has(context.Background(), "dir", "bof", "xml")
	assert.Nil(t, err)
	assert.False(t, has)
}

func TestGithubHasError(t *testing.T) {
	t.Parallel()
	errOriginal := errors.New("dummy")
	mock := &MockGithubClient{}
	mock.Error = errOriginal
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	has, err := client.Has(context.Background(), "dir", "foo", "xml")
	assert.False(t, has)
	assert.Equal(t, errOriginal, err)
}

func TestCreateFileSuccess(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	data := []byte("hello world!")
	msg := "mah commit message"
	err := client.CreateFile(context.Background(), "/foo", msg, data)
	assert.Nil(t, err)
	assert.Equal(t, "ericsperano", mock.CreateOwner)
	assert.Equal(t, "yahoo-fantasy-hockey-data", mock.CreateRepo)
	assert.Equal(t, "data/foo", mock.CreatePath)
	assert.Equal(t, msg, mock.CreateMessage)
	assert.Equal(t, data, mock.CreateContent)
}

func TestCreateFileErr(t *testing.T) {
	t.Parallel()
	errOriginal := errors.New("dummy")
	mock := &MockGithubClient{}
	mock.Error = errOriginal
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	data := []byte("hello world!")
	msg := "mah commit message"
	err := client.CreateFile(context.Background(), "/foo", msg, data)
	assert.True(t, errors.Is(err, errOriginal))
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	mock := &MockGithubClient{}
	mock.DownloadContent = "Hello World!"
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	data, err := client.ReadFile(context.Background(), "/foo")
	assert.Nil(t, err)
	assert.Equal(t, mock.DownloadContent, string(data))
}

func TestReadFileErr(t *testing.T) {
	t.Parallel()
	errOriginal := errors.New("dummy")
	mock := &MockGithubClient{}
	mock.Error = errOriginal
	mockCache := &MockGithubCache{}
	client := NewGithubClient(mockCache, mock)
	data, err := client.ReadFile(context.Background(), "/foo")
	assert.Nil(t, data)
	assert.Equal(t, errOriginal, err)
}

//////////////////////////////////////////////////////////////////////////////
// URLs
//////////////////////////////////////////////////////////////////////////////

func TestYahooFantasyGameURL(t *testing.T) {
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/game/nhl", YahooFantasyGameURL())
}

func TestYahooLeagueURL(t *testing.T) {
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/league/411.l.123", YahooLeagueURL(123))
}
