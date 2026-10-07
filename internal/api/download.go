package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// downloadTimeout covers fetching a large batch's results file.
const downloadTimeout = 5 * time.Minute

// Download is a fetched batch results file.
type Download struct {
	URL         string
	ContentType string
	Body        []byte
}

// FetchDownload GETs a batch's download_file URL. It sends no Authorization
// header: the URL is a presigned storage link, and forwarding the API token to
// the storage host would leak it. hc nil uses a client with downloadTimeout.
func FetchDownload(ctx context.Context, rawURL string, hc *http.Client) (*Download, error) {
	if hc == nil {
		hc = &http.Client{Timeout: downloadTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download results: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download results: HTTP %d (the link expires; fetch the batch again for a fresh one)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download results: %w", err)
	}
	return &Download{URL: rawURL, ContentType: resp.Header.Get("Content-Type"), Body: body}, nil
}
