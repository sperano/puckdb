package yahoo

import (
	"context"

	"github.com/sperano/puckdb/worker/shared"
)

// mockDownloader returns a shared.Downloader that returns the given content and error.
func mockDownloader(content []byte, err error) shared.Downloader {
	return func(_ context.Context, _ string) ([]byte, error) {
		return content, err
	}
}
