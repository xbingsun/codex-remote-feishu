package feishu

import (
	"context"
	"net/http"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

const (
	defaultLarkRequestTimeout            = 2 * time.Minute
	sendIMFileTimeout                    = 2 * time.Minute
	inboundMessageParseTimeout           = 30 * time.Second
	asyncInboundFailureNoticeTimeout     = 10 * time.Second
	previewDriveSummaryTimeout           = 20 * time.Second
	previewDriveCleanupTimeout           = 45 * time.Second
	previewDriveBackgroundCleanupTimeout = 45 * time.Second
)

var sharedLarkTokenCache = newLarkTokenCache()

func NewLarkClient(appID, appSecret string) *lark.Client {
	return newLarkClient(appID, appSecret, "", sharedLarkTokenCache, &http.Client{
		Timeout: defaultLarkRequestTimeout,
	})
}

func newLarkClient(
	appID string,
	appSecret string,
	openBaseURL string,
	tokenCache *larkTokenCache,
	httpClient larkcore.HttpClient,
) *lark.Client {
	if tokenCache == nil {
		tokenCache = sharedLarkTokenCache
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultLarkRequestTimeout}
	}
	options := []lark.ClientOptionFunc{
		lark.WithTokenCache(tokenCache),
		lark.WithHttpClient(newLarkTokenInvalidatingHTTPClient(httpClient, tokenCache)),
	}
	if strings.TrimSpace(openBaseURL) != "" {
		options = append(options, lark.WithOpenBaseUrl(strings.TrimSpace(openBaseURL)))
	}
	return lark.NewClient(
		strings.TrimSpace(appID),
		strings.TrimSpace(appSecret),
		options...,
	)
}

func newFeishuTimeoutContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = parent
	}
	if timeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, timeout)
}
