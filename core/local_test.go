package core

// type MockLocalClient struct {
// 	Names           []string
// 	Code            int
// 	Error           error
// 	CreatePath      string
// 	CreateContent   []byte
// 	DownloadContent string
// }

// func (m *MockLocalClient) CreateFile(ctx context.Context, filepath string, content []byte) error {
// 	if m.Error != nil {
// 		return m.Error
// 	}
// 	m.CreatePath = filepath
// 	m.CreateContent = content
// 	return nil
// }

// func (m *MockLocalClient) DownloadContents(ctx context.Context, owner, repo, filepath string, opts *github.RepositoryContentGetOptions) (io.ReadCloser, *github.Response, error) {
// 	if m.Error != nil {
// 		return nil, nil, m.Error
// 	}
// 	r := ioutil.NopCloser(strings.NewReader(m.DownloadContent))
// 	return r, nil, nil
// }

// func (m *MockLocalClient) GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
// 	if m.Error != nil {
// 		return nil, nil, nil, m.Error
// 	}
// 	dirContent := []*github.RepositoryContent{}
// 	for i := range m.Names {
// 		dirContent = append(dirContent, &github.RepositoryContent{Name: &m.Names[i]})
// 	}
// 	resp := github.Response{
// 		Response: &http.Response{
// 			StatusCode: m.Code,
// 		},
// 	}
// 	return nil, dirContent, &resp, nil
// }

// func TestLocalReadDirSuccess(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "foo_20221130024225.xml", "ta_ta_ta"}
// 	mock.Code = http.StatusOK
// 	client := NewLocalClient("/mnt/data/test")
// 	files, err := client.ReadDir(context.Background(), "dir")
// 	assert.Nil(t, err)
// 	assert.Equal(t, 2, len(files))
// 	assert.Equal(t, "foo_20210930024225.xml", files[0].Filename())
// 	assert.Equal(t, "foo_20221130024225.xml", files[1].Filename())
// }

// func TestLocalReadDirNotFound(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "foo_20221130024225.xml", "ta_ta_ta"}
// 	mock.Code = http.StatusNotFound
// 	client := NewLocalClient("/mnt/data/test")
// 	files, err := client.ReadDir(context.Background(), "dir")
// 	assert.Nil(t, err)
// 	assert.Equal(t, 0, len(files))
// }

// func TestLocalReadDirError(t *testing.T) {
// 	t.Parallel()
// 	errOriginal := errors.New("dummy")
// 	mock := &MockLocalClient{}
// 	mock.Error = errOriginal
// 	mock.Code = http.StatusOK
// 	client := NewLocalClient("/mnt/data/test")
// 	files, err := client.ReadDir(context.Background(), "dir")
// 	assert.Nil(t, files)
// 	assert.Equal(t, errOriginal, err)
// }

// func TestLocalFindSuccess(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
// 	mock.Code = http.StatusOK
// 	client := NewLocalClient("/mnt/data/test")
// 	files, err := client.Find(context.Background(), "dir", "bar", "xml")
// 	assert.Nil(t, err)
// 	assert.Equal(t, 2, len(files))
// 	// they will be sorted in descending order
// 	assert.Equal(t, "bar_20221205024225.xml", files[0].Filename())
// 	assert.Equal(t, "bar_20221130024225.xml", files[1].Filename())
// }

// func TestLocalFindError(t *testing.T) {
// 	t.Parallel()
// 	errOriginal := errors.New("dummy")
// 	mock := &MockLocalClient{}
// 	mock.Error = errOriginal
// 	client := NewLocalClient("/mnt/data/test")
// 	files, err := client.Find(context.Background(), "dir", "foo", "xml")
// 	assert.Nil(t, files)
// 	assert.Equal(t, errOriginal, err)
// }

// func TestLocalHasSuccess(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
// 	mock.Code = http.StatusOK
// 	client := NewLocalClient("/tmp/yfh-test")
// 	has, err := client.Has(context.Background(), "dir", "bar", "xml")
// 	assert.Nil(t, err)
// 	assert.True(t, has)
// }

// func TestLocalHasNotSuccess(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.Names = []string{"foo_20210930024225.xml", "lol.txt", "bar_20221130024225.xml", "bar_20221130024225.txt", "bar_20221205024225.xml"}
// 	mock.Code = http.StatusOK
// 	client := NewLocalClient("/tmp/yfh-test")
// 	has, err := client.Has(context.Background(), "dir", "bof", "xml")
// 	assert.Nil(t, err)
// 	assert.False(t, has)
// }

// func TestLocalHasError(t *testing.T) {
// 	t.Parallel()
// 	errOriginal := errors.New("dummy")
// 	mock := &MockLocalClient{}
// 	mock.Error = errOriginal
// 	client := NewLocalClient("/tmp/yfh-test")
// 	has, err := client.Has(context.Background(), "dir", "foo", "xml")
// 	assert.False(t, has)
// 	assert.Equal(t, errOriginal, err)
// }

// func TestLocalCreateFileSuccess(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	client := NewLocalClient("/tmp/yfh-test")
// 	data := []byte("hello world!")
// 	err := client.CreateFile(context.Background(), "/foo", data)
// 	assert.Nil(t, err)
// 	assert.Equal(t, "ericsperano", mock.CreateOwner)
// 	assert.Equal(t, "yahoo-fantasy-hockey-data", mock.CreateRepo)
// 	assert.Equal(t, "/foo", mock.CreatePath)
// 	assert.Equal(t, msg, mock.CreateMessage)
// 	assert.Equal(t, data, mock.CreateContent)
// }

// func TestCreateFileErr(t *testing.T) {
// 	t.Parallel()
// 	errOriginal := errors.New("dummy")
// 	mock := &MockLocalClient{}
// 	mock.Error = errOriginal
// 	client := NewLocalClient("/tmp/yfh-test")
// 	data := []byte("hello world!")
// 	err := client.CreateFile(context.Background(), "/foo", data)
// 	assert.True(t, errors.Is(err, errOriginal))
// }

// func TestReadFile(t *testing.T) {
// 	t.Parallel()
// 	mock := &MockLocalClient{}
// 	mock.DownloadContent = "Hello World!"
// 	client := NewLocalClient("/tmp/yfh-test")
// 	data, err := client.ReadFile(context.Background(), "/foo")
// 	assert.Nil(t, err)
// 	assert.Equal(t, mock.DownloadContent, string(data))
// }

// func TestReadFileErr(t *testing.T) {
// 	t.Parallel()
// 	errOriginal := errors.New("dummy")
// 	client := NewLocalClient("/tmp/yfh-test")
// 	data, err := client.ReadFile(context.Background(), "/foo")
// 	assert.Nil(t, data)
// 	assert.Equal(t, errOriginal, err)
// }

// // //////////////////////////////////////////////////////////////////////////////
// // // URLs
// // //////////////////////////////////////////////////////////////////////////////

// func TestYahooFantasyGameURL(t *testing.T) {
// 	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/game/nhl", YahooFantasyGameURL())
// }

// func TestYahooLeagueURL(t *testing.T) {
// 	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/league/411.l.123", YahooLeagueURL(123))
// }
