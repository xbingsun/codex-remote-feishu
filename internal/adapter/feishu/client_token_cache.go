package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

const maxLarkAuthErrorBodyBytes = 64 << 10

var invalidLarkAccessTokenCodes = map[int]struct{}{
	99991663: {}, // tenant_access_token invalid
	99991664: {}, // app_access_token invalid
	99991671: {}, // access token invalid
}

type larkTokenCacheEntry struct {
	value     string
	expiresAt time.Time
}

// larkTokenCache keeps the SDK's process-wide token cache observable by the
// HTTP client so a token rejected by Feishu can be evicted before the SDK's
// built-in second attempt.
type larkTokenCache struct {
	mu      sync.RWMutex
	entries map[string]larkTokenCacheEntry
}

func newLarkTokenCache() *larkTokenCache {
	return &larkTokenCache{entries: make(map[string]larkTokenCacheEntry)}
}

func (c *larkTokenCache) Get(_ context.Context, key string) (string, error) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || entry.value == "" || !entry.expiresAt.After(time.Now()) {
		return "", nil
	}
	return entry.value, nil
}

func (c *larkTokenCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	c.mu.Lock()
	c.entries[key] = larkTokenCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()
	return nil
}

func (c *larkTokenCache) invalidateValue(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if entry.value == value {
			delete(c.entries, key)
		}
	}
}

type larkTokenInvalidatingHTTPClient struct {
	delegate larkcore.HttpClient
	cache    *larkTokenCache
}

func newLarkTokenInvalidatingHTTPClient(
	delegate larkcore.HttpClient,
	cache *larkTokenCache,
) *larkTokenInvalidatingHTTPClient {
	return &larkTokenInvalidatingHTTPClient{delegate: delegate, cache: cache}
}

func (c *larkTokenInvalidatingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.delegate.Do(req)
	if err != nil || resp == nil || resp.Body == nil || c.cache == nil {
		return resp, err
	}
	token := bearerToken(req.Header.Get("Authorization"))
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if token == "" || !strings.Contains(contentType, "json") {
		return resp, nil
	}

	prefix, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLarkAuthErrorBodyBytes))
	resp.Body = &replayReadCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body),
		Closer: resp.Body,
	}
	if readErr != nil {
		return resp, nil
	}
	var apiError struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(prefix, &apiError) == nil {
		if _, invalid := invalidLarkAccessTokenCodes[apiError.Code]; invalid {
			c.cache.invalidateValue(token)
		}
	}
	return resp, nil
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

type replayReadCloser struct {
	io.Reader
	io.Closer
}
