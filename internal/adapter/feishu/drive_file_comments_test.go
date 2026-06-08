package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

func TestReplyDriveFileCommentCreatesCommentReply(t *testing.T) {
	type replyRequest struct {
		Content struct {
			Elements []struct {
				Type    string `json:"type"`
				TextRun struct {
					Text string `json:"text"`
				} `json:"text_run"`
			} `json:"elements"`
		} `json:"content"`
	}

	var (
		tokenHits   int
		replyHits   int
		authHeader  string
		fileType    string
		userIDType  string
		requestBody replyRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case larkcore.TenantAccessTokenInternalUrlPath:
			tokenHits++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/drive/v1/files/docx-token/comments/comment-1/replies":
			replyHits++
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			authHeader = r.Header.Get("Authorization")
			fileType = r.URL.Query().Get("file_type")
			userIDType = r.URL.Query().Get("user_id_type")
			if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{"reply_id": "reply-created"},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := lark.NewClient(
		"cli_test_reply_app",
		"test_secret",
		lark.WithOpenBaseUrl(server.URL),
		lark.WithHttpClient(server.Client()),
		lark.WithReqTimeout(5*time.Second),
	)
	gateway := NewLiveGateway(LiveGatewayConfig{GatewayID: "app-1"})
	gateway.client = client
	gateway.broker = NewFeishuCallBroker("app-1", client)

	result, err := gateway.ReplyDriveFileComment(context.Background(), DriveFileCommentReplyRequest{
		GatewayID: "app-1",
		FileToken: "docx-token",
		FileType:  "docx",
		CommentID: "comment-1",
		Text:      "处理好了",
	})
	if err != nil {
		t.Fatalf("ReplyDriveFileComment returned error: %v", err)
	}
	if tokenHits != 1 {
		t.Fatalf("expected one tenant token fetch, got %d", tokenHits)
	}
	if replyHits != 1 {
		t.Fatalf("expected one reply create request, got %d", replyHits)
	}
	if authHeader != "Bearer tenant-token" {
		t.Fatalf("unexpected authorization header: %q", authHeader)
	}
	if fileType != "docx" || userIDType != "open_id" {
		t.Fatalf("unexpected query params file_type=%q user_id_type=%q", fileType, userIDType)
	}
	if len(requestBody.Content.Elements) != 1 || requestBody.Content.Elements[0].Type != "text_run" || requestBody.Content.Elements[0].TextRun.Text != "处理好了" {
		t.Fatalf("unexpected request body: %#v", requestBody)
	}
	if result.ReplyCount != 1 {
		t.Fatalf("reply count = %d, want 1", result.ReplyCount)
	}
}
