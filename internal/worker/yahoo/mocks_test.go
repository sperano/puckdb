package yahoo

import (
	"context"

	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/mock"
)

// MockHTTPDownloader returns a shared.Downloader backed by testify.Mock.
type MockHTTPDownloader struct {
	mock.Mock
}

func (m *MockHTTPDownloader) Downloader() shared.Downloader {
	return func(ctx context.Context, url string) ([]byte, error) {
		args := m.MethodCalled("Download", ctx, url)
		if args.Get(0) == nil {
			return nil, args.Error(1)
		}
		return args.Get(0).([]byte), args.Error(1)
	}
}
