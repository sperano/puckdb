package asset

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/httpx"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ─── PNG fixture ──────────────────────────────────────────────────────────────

// minimalPNG is a 68-byte valid PNG (1×1 transparent pixel). The bytes were
// generated once and are reproduced here as a literal so tests have no I/O
// dependency.
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, // 8-bit RGBA
	0x89, 0x00, 0x00, 0x00, 0x0b, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x02,
	0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, // IEND chunk
	0x60, 0x82,
}

// validPNG returns a PNG body large enough to satisfy minimumAssetBytes.
func validPNG() []byte {
	// Pad minimalPNG to well above the minimum so size validation passes.
	padded := make([]byte, minimumAssetBytes+64)
	copy(padded, minimalPNG)
	return padded
}

// ─── Mock downloader ──────────────────────────────────────────────────────────

// urlDownloader returns a shared.Downloader backed by a map of URL → response.
// If a URL is not in the map the downloader returns an error.
type urlDownloader map[string]downloadResponse

type downloadResponse struct {
	body []byte
	err  error
}

func (m urlDownloader) download(_ context.Context, url string) ([]byte, error) {
	if r, ok := m[url]; ok {
		return r.body, r.err
	}
	return nil, fmt.Errorf("unexpected download URL: %s", url)
}

func (m urlDownloader) downloader() shared.Downloader {
	return m.download
}

// recordingDownloader is a shared.Downloader that records, in order, every URL
// it was asked to download. Tests use it to assert that rows after a batch-abort
// are not processed.
type recordingDownloader struct {
	responses map[string]downloadResponse
	requested []string
}

func (r *recordingDownloader) download(_ context.Context, url string) ([]byte, error) {
	r.requested = append(r.requested, url)
	if resp, ok := r.responses[url]; ok {
		return resp.body, resp.err
	}
	return nil, fmt.Errorf("unexpected download URL: %s", url)
}

// noopHeartbeat is a heartbeatFunc that does nothing, for direct fetchAssetBatch
// calls that bypass the Temporal test environment.
func noopHeartbeat(_ context.Context, _ string) {}

func httpErr(code int) *httpx.HTTPError {
	return &httpx.HTTPError{
		StatusCode: code,
		Status:     fmt.Sprintf("%d Status", code),
		URL:        "https://example.com/image.png",
	}
}

// ─── Test suite ───────────────────────────────────────────────────────────────

type FetchAssetBatchSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchAssetBatchSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchAssetBatchSuite(t *testing.T) {
	suite.Run(t, new(FetchAssetBatchSuite))
}

// executeActivity is a convenience wrapper that calls FetchAssetBatch through
// the TestActivityEnvironment (so activity.RecordHeartbeat is handled).
func (s *FetchAssetBatchSuite) executeActivity(act *Activities, in FetchAssetBatchInput) (FetchAssetBatchResult, error) {
	s.env.RegisterActivity(act.FetchAssetBatch)
	val, err := s.env.ExecuteActivity(act.FetchAssetBatch, in)
	if err != nil {
		return FetchAssetBatchResult{}, err
	}
	var out FetchAssetBatchResult
	require.NoError(s.T(), val.Get(&out))
	return out, nil
}

// ─── Per-row outcome tests ────────────────────────────────────────────────────

// TestPerRowOutcomes exercises hit, miss-success, miss-404, miss-other-4xx,
// miss-5xx, HTML validation failure, and size validation failure all within a
// single batch so we also confirm batch-level atomicity implicitly.
func (s *FetchAssetBatchSuite) TestPerRowOutcomes() {
	const (
		urlHit      = "https://assets.nhle.com/mugs/headshot/1.png"
		urlMissOK   = "https://assets.nhle.com/mugs/headshot/2.png"
		urlMiss404  = "https://assets.nhle.com/mugs/headshot/3.png"
		urlMiss401  = "https://assets.nhle.com/mugs/headshot/4.png"
		urlMiss5xx  = "https://assets.nhle.com/mugs/headshot/5.png"
		urlHTMLBody = "https://assets.nhle.com/mugs/headshot/6.png"
		urlTinyBody = "https://assets.nhle.com/mugs/headshot/7.png"
	)

	mem := store.NewMemStorage()

	// Pre-populate the cache path for the hit asset.
	hitAsset := Asset{FileType: core.PlayerHeadshot, URL: urlHit, IDs: []int64{1}}
	hitPath, err := hitAsset.Path()
	require.NoError(s.T(), err)
	mem.SetFile(hitPath, validPNG())

	dl := urlDownloader{
		urlMissOK:   {body: validPNG()},
		urlMiss404:  {err: httpErr(http.StatusNotFound)},
		urlMiss401:  {err: httpErr(http.StatusUnauthorized)},
		urlMiss5xx:  {err: httpErr(http.StatusInternalServerError)},
		urlHTMLBody: {body: []byte("<html><body>Error page</body></html>")},
		urlTinyBody: {body: []byte("x")}, // below minimumAssetBytes
	}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: urlHit, IDs: []int64{1}},      // 0: hit
		{FileType: core.PlayerHeadshot, URL: urlMissOK, IDs: []int64{2}},   // 1: miss success
		{FileType: core.PlayerHeadshot, URL: urlMiss404, IDs: []int64{3}},  // 2: 404
		{FileType: core.PlayerHeadshot, URL: urlMiss401, IDs: []int64{4}},  // 3: 401
		{FileType: core.PlayerHeadshot, URL: urlMiss5xx, IDs: []int64{5}},  // 4: 5xx
		{FileType: core.PlayerHeadshot, URL: urlHTMLBody, IDs: []int64{6}}, // 5: HTML body
		{FileType: core.PlayerHeadshot, URL: urlTinyBody, IDs: []int64{7}}, // 6: tiny body
	}

	act := &Activities{Storage: mem, Download: dl.downloader()}
	out, err := s.executeActivity(act, FetchAssetBatchInput{Assets: assets})
	require.NoError(s.T(), err)
	require.Len(s.T(), out.Results, len(assets))

	// Row 0: cache hit
	r := out.Results[0]
	require.Equal(s.T(), core.OriginFileSystem, r.Origin)
	require.Empty(s.T(), r.Err)

	// Row 1: miss → downloaded and written
	r = out.Results[1]
	require.Equal(s.T(), core.OriginRemoteNHLCDN, r.Origin)
	require.Empty(s.T(), r.Err)
	missPath, _ := assets[1].Path()
	require.True(s.T(), mem.Has(missPath), "downloaded file should be written to storage")

	// Row 2: 404 → OriginUnknown, no error string
	r = out.Results[2]
	require.Equal(s.T(), core.OriginUnknown, r.Origin)
	require.Empty(s.T(), r.Err, "404 should not set Err")

	// Row 3: 401 → Err set
	r = out.Results[3]
	require.NotEmpty(s.T(), r.Err)
	p401, _ := assets[3].Path()
	require.False(s.T(), mem.Has(p401), "no file should be written for 401")

	// Row 4: 5xx → Err set
	r = out.Results[4]
	require.NotEmpty(s.T(), r.Err)

	// Row 5: HTML body → validation error
	r = out.Results[5]
	require.NotEmpty(s.T(), r.Err)
	require.True(s.T(), strings.Contains(r.Err, "validation"),
		"error %q should contain 'validation'", r.Err)
	pHTML, _ := assets[5].Path()
	require.False(s.T(), mem.Has(pHTML), "HTML body must not be written to storage")

	// Row 6: tiny body → validation error
	r = out.Results[6]
	require.NotEmpty(s.T(), r.Err)
	require.True(s.T(), strings.Contains(r.Err, "validation"),
		"error %q should contain 'validation'", r.Err)
	pTiny, _ := assets[6].Path()
	require.False(s.T(), mem.Has(pTiny), "tiny body must not be written to storage")
}

// TestBatchAtomicity verifies that a single 5xx row does not abort the batch;
// surrounding rows continue processing.
func (s *FetchAssetBatchSuite) TestBatchAtomicity() {
	const (
		urlOK  = "https://assets.nhle.com/mugs/headshot/10.png"
		urlOK2 = "https://assets.nhle.com/mugs/headshot/11.png"
		url5xx = "https://assets.nhle.com/mugs/headshot/12.png"
		urlOK3 = "https://assets.nhle.com/mugs/headshot/13.png"
		urlOK4 = "https://assets.nhle.com/mugs/headshot/14.png"
	)

	mem := store.NewMemStorage()
	dl := urlDownloader{
		urlOK:  {body: validPNG()},
		urlOK2: {body: validPNG()},
		url5xx: {err: httpErr(http.StatusInternalServerError)},
		urlOK3: {body: validPNG()},
		urlOK4: {body: validPNG()},
	}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: urlOK, IDs: []int64{10}},  // 0
		{FileType: core.PlayerHeadshot, URL: urlOK2, IDs: []int64{11}}, // 1
		{FileType: core.PlayerHeadshot, URL: url5xx, IDs: []int64{12}}, // 2 — 5xx
		{FileType: core.PlayerHeadshot, URL: urlOK3, IDs: []int64{13}}, // 3
		{FileType: core.PlayerHeadshot, URL: urlOK4, IDs: []int64{14}}, // 4
	}

	act := &Activities{Storage: mem, Download: dl.downloader()}
	out, err := s.executeActivity(act, FetchAssetBatchInput{Assets: assets})

	// The activity itself must succeed (no catastrophic failure).
	require.NoError(s.T(), err)
	require.Len(s.T(), out.Results, len(assets))

	// Rows 0, 1, 3, 4 should succeed.
	for _, i := range []int{0, 1, 3, 4} {
		require.Empty(s.T(), out.Results[i].Err,
			"row %d should succeed but got Err: %s", i, out.Results[i].Err)
		require.Equal(s.T(), core.OriginRemoteNHLCDN, out.Results[i].Origin)
	}

	// Row 2 should have an error.
	require.NotEmpty(s.T(), out.Results[2].Err, "row 2 (5xx) must have Err set")
}

// TestRefreshCurrent verifies that an already-cached asset is re-downloaded
// when RefreshCurrent is true.
func (s *FetchAssetBatchSuite) TestRefreshCurrent() {
	const assetURL = "https://assets.nhle.com/mugs/headshot/20.png"

	mem := store.NewMemStorage()
	ast := Asset{FileType: core.PlayerHeadshot, URL: assetURL, IDs: []int64{20}}
	p, err := ast.Path()
	require.NoError(s.T(), err)

	// Pre-populate storage with stale content.
	stale := make([]byte, minimumAssetBytes+10)
	stale[0] = 0x89 // not a valid PNG signature but size is fine — we're testing bypass, not content
	mem.SetFile(p, stale)

	freshBytes := validPNG()
	dl := urlDownloader{assetURL: {body: freshBytes}}

	act := &Activities{Storage: mem, Download: dl.downloader()}
	out, err := s.executeActivity(act, FetchAssetBatchInput{
		Assets:         []Asset{ast},
		RefreshCurrent: true,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), out.Results, 1)

	// Should have been re-downloaded: origin is CDN, not FileSystem.
	require.Equal(s.T(), core.OriginRemoteNHLCDN, out.Results[0].Origin)
	require.Empty(s.T(), out.Results[0].Err)

	// Storage now holds the fresh bytes.
	stored := mem.Get(p)
	require.NotNil(s.T(), stored)
	require.Equal(s.T(), freshBytes, stored)
}

// TestPathError verifies that an asset whose URL has no valid extension surfaces
// a path-computation error in Results[i].Err without aborting the batch.
func (s *FetchAssetBatchSuite) TestPathError() {
	const badURL = "https://assets.nhle.com/mugs/headshot/30.php"
	const okURL = "https://assets.nhle.com/mugs/headshot/31.png"

	mem := store.NewMemStorage()
	dl := urlDownloader{okURL: {body: validPNG()}}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: badURL, IDs: []int64{30}}, // 0: bad extension
		{FileType: core.PlayerHeadshot, URL: okURL, IDs: []int64{31}},  // 1: valid
	}

	act := &Activities{Storage: mem, Download: dl.downloader()}
	out, err := s.executeActivity(act, FetchAssetBatchInput{Assets: assets})
	require.NoError(s.T(), err)

	// Row 0: path error
	require.NotEmpty(s.T(), out.Results[0].Err, "bad extension should yield a path error")

	// Row 1: unaffected
	require.Empty(s.T(), out.Results[1].Err)
	require.Equal(s.T(), core.OriginRemoteNHLCDN, out.Results[1].Origin)
}

// TestEmptyURL verifies that an asset with no URL records OriginUnknown and
// does not call the downloader.
func (s *FetchAssetBatchSuite) TestEmptyURL() {
	called := false
	dl := func(_ context.Context, _ string) ([]byte, error) {
		called = true
		return nil, nil
	}

	mem := store.NewMemStorage()
	act := &Activities{Storage: mem, Download: dl}
	out, err := s.executeActivity(act, FetchAssetBatchInput{
		Assets: []Asset{
			{FileType: core.PlayerHeadshot, URL: "", IDs: []int64{99}},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), core.OriginUnknown, out.Results[0].Origin)
	require.Empty(s.T(), out.Results[0].Err)
	require.False(s.T(), called, "downloader must not be called for empty URL")
}

// TestHeartbeat_Cadence verifies heartbeat cadence via the injectable heartbeat
// function. The Temporal SDK coalesces heartbeats in the test environment and
// does not fire the listener for every call, so we bypass it here and call the
// internal implementation directly with a counting stub.
func TestHeartbeat_Cadence(t *testing.T) {
	t.Parallel()

	const numAssets = 25 // expect heartbeats at rows 10 and 20

	mem := store.NewMemStorage()
	dl := urlDownloader{}
	for i := 1; i <= numAssets; i++ {
		url := fmt.Sprintf("https://assets.nhle.com/mugs/headshot/%d.png", i)
		dl[url] = downloadResponse{body: validPNG()}
	}

	assets := make([]Asset, numAssets)
	for i := range numAssets {
		assets[i] = Asset{
			FileType: core.PlayerHeadshot,
			URL:      fmt.Sprintf("https://assets.nhle.com/mugs/headshot/%d.png", i+1),
			IDs:      []int64{int64(i + 1)},
		}
	}

	var heartbeats []string
	stubHeartbeat := func(_ context.Context, msg string) {
		heartbeats = append(heartbeats, msg)
	}

	act := &Activities{Storage: mem, Download: dl.downloader()}
	_, err := act.fetchAssetBatch(context.Background(), FetchAssetBatchInput{Assets: assets}, stubHeartbeat)
	require.NoError(t, err)

	// With 25 assets and heartbeatEveryNRows=10, we expect heartbeats at rows
	// 10 and 20 (i=9 and i=19), i.e. 2 heartbeats.
	expectedHeartbeats := numAssets / heartbeatEveryNRows
	require.Len(t, heartbeats, expectedHeartbeats,
		"expected %d heartbeats for %d assets", expectedHeartbeats, numAssets)
	require.Equal(t, "10/25", heartbeats[0])
	require.Equal(t, "20/25", heartbeats[1])
}

// TestYahooFileType verifies that Yahoo-typed assets record OriginRemoteYahooCDN.
func (s *FetchAssetBatchSuite) TestYahooFileType() {
	const assetURL = "https://s.yimg.com/fantasy/teams/logo/42_7.png"

	mem := store.NewMemStorage()
	dl := urlDownloader{assetURL: {body: validPNG()}}

	ast := Asset{FileType: core.YahooTeamLogo, URL: assetURL, IDs: []int64{42, 7}}
	act := &Activities{Storage: mem, Download: dl.downloader()}
	out, err := s.executeActivity(act, FetchAssetBatchInput{Assets: []Asset{ast}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), core.OriginRemoteYahooCDN, out.Results[0].Origin)
}

// ─── Direct unit tests (no testsuite overhead) ─────────────────────────────

// TestProcessAsset_StorageWriteError verifies that a storage write failure
// records an error in the result but does not return a hard error from the batch.
func TestProcessAsset_StorageWriteError(t *testing.T) {
	t.Parallel()

	const assetURL = "https://assets.nhle.com/mugs/headshot/999.png"
	dl := func(_ context.Context, _ string) ([]byte, error) {
		return validPNG(), nil
	}

	// errStorage always fails writes.
	act := &Activities{
		Storage:  &errStorage{},
		Download: dl,
	}

	ctx := context.Background()
	ast := Asset{FileType: core.PlayerHeadshot, URL: assetURL, IDs: []int64{999}}
	result, err := act.processAsset(ctx, ast, false)

	require.NoError(t, err, "a non-context write failure must not abort the batch")
	require.NotEmpty(t, result.Err, "write failure should produce a non-empty Err")
}

// errStorage is a test double whose Write always returns an error.
type errStorage struct {
	store.MemStorage
}

func (e *errStorage) Write(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("disk full")
}

func (e *errStorage) Exists(_ context.Context, _ string) bool {
	return false
}

// ─── Context cancellation aborts the batch ─────────────────────────────────

// TestContextCanceled_DownloadAbortsBatch verifies that when the downloader
// returns a context-cancellation error mid-batch, fetchAssetBatch aborts with
// that error instead of stringifying it into a per-row Err and continuing.
// The row after the cancelled one must not be downloaded.
func TestContextCanceled_DownloadAbortsBatch(t *testing.T) {
	t.Parallel()

	const (
		url0 = "https://assets.nhle.com/mugs/headshot/1.png" // succeeds
		url1 = "https://assets.nhle.com/mugs/headshot/2.png" // context.Canceled
		url2 = "https://assets.nhle.com/mugs/headshot/3.png" // must not be reached
	)

	rec := &recordingDownloader{responses: map[string]downloadResponse{
		url0: {body: validPNG()},
		url1: {err: context.Canceled},
		url2: {body: validPNG()},
	}}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: url0, IDs: []int64{1}},
		{FileType: core.PlayerHeadshot, URL: url1, IDs: []int64{2}},
		{FileType: core.PlayerHeadshot, URL: url2, IDs: []int64{3}},
	}

	act := &Activities{Storage: store.NewMemStorage(), Download: rec.download}
	_, err := act.fetchAssetBatch(context.Background(), FetchAssetBatchInput{Assets: assets}, noopHeartbeat)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{url0, url1}, rec.requested,
		"batch must abort at the cancelled row and not download later rows")
}

// TestContextCanceled_DeadlineExceededAbortsBatch verifies the deadline-exceeded
// variant is treated identically to cancellation.
func TestContextCanceled_DeadlineExceededAbortsBatch(t *testing.T) {
	t.Parallel()

	const (
		url0 = "https://assets.nhle.com/mugs/headshot/1.png" // deadline exceeded
		url1 = "https://assets.nhle.com/mugs/headshot/2.png" // must not be reached
	)

	rec := &recordingDownloader{responses: map[string]downloadResponse{
		url0: {err: context.DeadlineExceeded},
		url1: {body: validPNG()},
	}}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: url0, IDs: []int64{1}},
		{FileType: core.PlayerHeadshot, URL: url1, IDs: []int64{2}},
	}

	act := &Activities{Storage: store.NewMemStorage(), Download: rec.download}
	_, err := act.fetchAssetBatch(context.Background(), FetchAssetBatchInput{Assets: assets}, noopHeartbeat)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, []string{url0}, rec.requested,
		"batch must abort at the first row and not download later rows")
}

// TestContextCanceled_PreCancelledContextAbortsBatch verifies that a context
// already cancelled before the batch starts aborts immediately without
// downloading any rows.
func TestContextCanceled_PreCancelledContextAbortsBatch(t *testing.T) {
	t.Parallel()

	const (
		url0 = "https://assets.nhle.com/mugs/headshot/1.png"
		url1 = "https://assets.nhle.com/mugs/headshot/2.png"
	)

	rec := &recordingDownloader{responses: map[string]downloadResponse{
		url0: {body: validPNG()},
		url1: {body: validPNG()},
	}}

	assets := []Asset{
		{FileType: core.PlayerHeadshot, URL: url0, IDs: []int64{1}},
		{FileType: core.PlayerHeadshot, URL: url1, IDs: []int64{2}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	act := &Activities{Storage: store.NewMemStorage(), Download: rec.download}
	_, err := act.fetchAssetBatch(ctx, FetchAssetBatchInput{Assets: assets}, noopHeartbeat)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, rec.requested,
		"no rows should be downloaded when the context is pre-cancelled")
}

// ─── validateAssetBytes / looksLikeSVG ────────────────────────────────────────

func TestValidateAssetBytes(t *testing.T) {
	t.Parallel()

	pad := func(prefix []byte) []byte {
		out := make([]byte, minimumAssetBytes+32)
		copy(out, prefix)
		return out
	}

	cases := []struct {
		name      string
		data      []byte
		localPath string
		wantErr   string // substring; empty means expect no error
	}{
		{name: "png_path_extension_irrelevant", data: validPNG(), localPath: "assets/players/1/headshot.png"},
		{name: "png_with_jpg_path_still_ok", data: validPNG(), localPath: "assets/players/1/headshot.jpg"},
		{name: "svg_with_xml_prologue", data: pad([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)), localPath: "assets/teams/logos/8.svg"},
		{name: "svg_bare_root", data: pad([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>`)), localPath: "assets/teams/logos/8.svg"},
		{name: "svg_with_leading_whitespace_and_bom", data: pad(append([]byte("\xef\xbb\xbf  \n"), []byte(`<svg></svg>`)...)), localPath: "assets/teams/logos/8.svg"},
		{name: "svg_bytes_at_non_svg_path_rejected", data: pad([]byte(`<?xml version="1.0"?><svg></svg>`)), localPath: "assets/players/1/headshot.png", wantErr: "unexpected MIME type"},
		{name: "html_error_page_rejected", data: pad([]byte(`<!DOCTYPE html><html><body>Not Found</body></html>`)), localPath: "assets/players/1/headshot.png", wantErr: "unexpected MIME type"},
		{name: "below_minimum_size", data: []byte{0x89, 0x50, 0x4e, 0x47}, localPath: "assets/players/1/headshot.png", wantErr: "too small"},
		{name: "plain_text_at_svg_path_rejected", data: pad([]byte(`hello world`)), localPath: "assets/teams/logos/8.svg", wantErr: "unexpected MIME type"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateAssetBytes(tc.data, tc.localPath)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLooksLikeSVG(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{name: "bare_svg_root", in: []byte(`<svg></svg>`), want: true},
		{name: "xml_prologue_then_svg", in: []byte(`<?xml version="1.0"?><svg></svg>`), want: true},
		{name: "leading_whitespace_then_svg", in: []byte("  \n\t<svg></svg>"), want: true},
		{name: "utf8_bom_then_svg", in: append([]byte("\xef\xbb\xbf"), []byte(`<svg></svg>`)...), want: true},
		{name: "html_root_rejected", in: []byte(`<!DOCTYPE html><html></html>`), want: false},
		{name: "xml_prologue_no_svg", in: []byte(`<?xml version="1.0"?><other/>`), want: false},
		{name: "empty", in: nil, want: false},
		{name: "binary_png_signature", in: []byte{0x89, 0x50, 0x4e, 0x47}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, looksLikeSVG(tc.in))
		})
	}
}
