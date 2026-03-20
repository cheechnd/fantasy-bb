package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Fetcher interface {
	Fetch(ctx context.Context, identifier string) ([]byte, error)
}

type FileSourceFetcher struct{}

func (f FileSourceFetcher) Fetch(_ context.Context, path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source file %q: %w", path, err)
	}
	return b, nil
}

type URLSourceFetcher struct {
	Client    *http.Client
	UserAgent string
}

func NewURLSourceFetcher() URLSourceFetcher {
	return URLSourceFetcher{
		Client:    &http.Client{Timeout: 15 * time.Second},
		UserAgent: "fantasy-baseball/fb phase1",
	}
}

func (f URLSourceFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %q: %w", rawURL, err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %q: unexpected status %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body from %q: %w", rawURL, err)
	}
	return body, nil
}
