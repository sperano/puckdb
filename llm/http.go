package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	maxResponseBodyBytes = 16 << 20
	maxErrorBodyBytes    = 500
)

func postJSON(
	ctx context.Context,
	client *http.Client,
	url string,
	headers http.Header,
	body []byte,
	destination any,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Content-Type", "application/json")

	log.Trace().Str("url", url).RawJSON("request_body", body).Msg("LLM HTTP request body")

	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := readBoundedResponse(response.Body)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}

	log.Debug().Int("status", response.StatusCode).Dur("elapsed", elapsed).Int("body_bytes", len(responseBody)).Msg("LLM HTTP response")
	log.Trace().RawJSON("response_body", responseBody).Msg("LLM HTTP response body")

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("LLM API error (status %d): %s", response.StatusCode, truncate(responseBody, maxErrorBodyBytes))
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return fmt.Errorf("unmarshal response: %w\nresponse: %s", err, truncate(responseBody, maxErrorBodyBytes))
	}
	return nil
}

func readBoundedResponse(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, maxResponseBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("read response: body exceeds %d-byte limit", maxResponseBodyBytes)
	}
	return body, nil
}
