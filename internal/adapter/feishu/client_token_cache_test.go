package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

func TestLarkTokenInvalidatingHTTPClientEvictsRejectedTokenAndPreservesBody(t *testing.T) {
	cache := newLarkTokenCache()
	if err := cache.Set(context.Background(), "tenant-token", "stale-token", time.Hour); err != nil {
		t.Fatalf("seed token cache: %v", err)
	}
	body := `{"code":99991663,"msg":"invalid token"}`
	client := newLarkTokenInvalidatingHTTPClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}), cache)
	req := httptest.NewRequest(http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer stale-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if string(gotBody) != body {
		t.Fatalf("replayed body = %q, want %q", gotBody, body)
	}
	if got, err := cache.Get(context.Background(), "tenant-token"); err != nil || got != "" {
		t.Fatalf("cached token after invalidation = %q, err=%v", got, err)
	}
}

func TestLarkTokenCacheLateStaleInvalidationDoesNotRemoveFreshToken(t *testing.T) {
	cache := newLarkTokenCache()
	ctx := context.Background()
	if err := cache.Set(ctx, "tenant-token", "stale-token", time.Hour); err != nil {
		t.Fatalf("seed stale token: %v", err)
	}
	cache.invalidateValue("stale-token")
	if err := cache.Set(ctx, "tenant-token", "fresh-token", time.Hour); err != nil {
		t.Fatalf("seed fresh token: %v", err)
	}

	cache.invalidateValue("stale-token")
	if got, err := cache.Get(ctx, "tenant-token"); err != nil || got != "fresh-token" {
		t.Fatalf("cached token after late invalidation = %q, err=%v", got, err)
	}
}

func TestNewLarkClientRefreshesRejectedCachedTokenOnSDKRetry(t *testing.T) {
	const appID = "cli_token_refresh_test"
	cache := newLarkTokenCache()
	t.Cleanup(func() {
		larkcore.NewCache(&larkcore.Config{TokenCache: sharedLarkTokenCache})
	})
	if err := cache.Set(
		context.Background(),
		"tenant_access_token-"+appID+"-",
		"stale-token",
		time.Hour,
	); err != nil {
		t.Fatalf("seed stale token: %v", err)
	}

	var tokenRequests atomic.Int32
	var businessRequests atomic.Int32
	httpClient := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenRequests.Add(1)
			return jsonHTTPResponse(http.StatusOK, map[string]any{
				"code":                0,
				"expire":              7200,
				"tenant_access_token": "fresh-token",
			}), nil
		case "/business":
			businessRequests.Add(1)
			if r.Header.Get("Authorization") == "Bearer stale-token" {
				return jsonHTTPResponse(http.StatusBadRequest, map[string]any{
					"code": 99991663,
					"msg":  "invalid token",
				}), nil
			}
			if r.Header.Get("Authorization") != "Bearer fresh-token" {
				return jsonHTTPResponse(http.StatusUnauthorized, map[string]any{
					"code": 99991663,
					"msg":  "unexpected authorization header",
				}), nil
			}
			return jsonHTTPResponse(http.StatusOK, map[string]any{"code": 0, "msg": "ok"}), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, map[string]any{"code": 404}), nil
		}
	})

	client := newLarkClient(appID, "secret", "https://feishu.test", cache, httpClient)
	resp, err := client.Post(
		context.Background(),
		"/business",
		map[string]string{"message": "hello"},
		larkcore.AccessTokenTypeTenant,
	)
	if err != nil {
		t.Fatalf("business request: %v", err)
	}
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(resp.RawBody, &result); err != nil {
		t.Fatalf("decode business response: %v", err)
	}
	if result.Code != 0 {
		t.Fatalf("business response code = %d, body=%s", result.Code, resp.RawBody)
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("token request count = %d, want 1", got)
	}
	if got := businessRequests.Load(); got != 2 {
		t.Fatalf("business request count = %d, want 2", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(status int, body any) *http.Response {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
	}
}
