package yahoo

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockHTTPDownloader implements HTTPDownloader for testing.
type MockHTTPDownloader struct {
	mock.Mock
}

func (m *MockHTTPDownloader) Download(ctx context.Context, url string) ([]byte, error) {
	args := m.Called(ctx, url)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}
