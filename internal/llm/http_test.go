package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostJSON_Success(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		assert.Equal(t, "header-value", request.Header.Get("X-Test"))
		return responseWithBody(http.StatusOK, `{"result":"ok"}`), nil
	})
	client := &http.Client{Transport: transport}
	headers := make(http.Header)
	headers.Set("X-Test", "header-value")
	destination := struct {
		Result string `json:"result"`
	}{}

	err := postJSON(context.Background(), client, "http://example.com", headers, []byte(`{"request":true}`), &destination)

	require.NoError(t, err)
	assert.Equal(t, "ok", destination.Result)
}

func TestPostJSON_ErrorStatusTruncatesBody(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", maxErrorBodyBytes+1)
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return responseWithBody(http.StatusBadGateway, body), nil
	})}

	err := postJSON(context.Background(), client, "http://example.com", nil, nil, &struct{}{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 502")
	assert.Contains(t, err.Error(), strings.Repeat("x", maxErrorBodyBytes)+"...")
	assert.NotContains(t, err.Error(), body)
}

func TestPostJSON_TransportError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("transport failed")
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, sentinel
	})}

	err := postJSON(context.Background(), client, "http://example.com", nil, nil, &struct{}{})

	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "send request")
}

func TestReadBoundedResponse_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	body := io.LimitReader(zeroReader{}, maxResponseBodyBytes+1)
	_, err := readBoundedResponse(body)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "body exceeds")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d-byte limit", maxResponseBodyBytes))
}

func TestReadBoundedResponse_PropagatesReadError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("read failed")
	_, err := readBoundedResponse(errorReader{err: sentinel})

	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "read response")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func responseWithBody(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

type errorReader struct {
	err error
}

func (reader errorReader) Read(_ []byte) (int, error) {
	return 0, reader.err
}
