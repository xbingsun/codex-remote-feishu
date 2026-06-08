package feishu

import (
	"strings"
	"testing"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

func TestParseDriveCommentNoticeEvent(t *testing.T) {
	req := &larkevent.EventReq{Body: []byte(`{
		"schema": "2.0",
		"header": {
			"event_id": "evt-1",
			"event_type": "drive.notice.comment_add_v1",
			"create_time": "1710000000000"
		},
		"event": {
			"operator_id": {"open_id": "ou_actor"},
			"file_token": "docx-token",
			"file_type": "docx",
			"file_name": "需求文档",
			"file_url": "https://example.feishu.cn/docx/docx-token",
			"comment_id": "comment-1",
			"reply_id": "reply-1",
			"reply_content": {
				"elements": [
					{"type": "text_run", "text_run": {"text": "帮我总结这一段"}}
				]
			}
		}
	}`)}

	notice, ok, err := parseDriveCommentNoticeEvent(req)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !ok {
		t.Fatal("expected drive comment event")
	}
	if notice.ActorUserID != "ou_actor" || notice.FileToken != "docx-token" || notice.FileType != "docx" || notice.CommentID != "comment-1" || notice.ReplyID != "reply-1" {
		t.Fatalf("unexpected notice: %#v", notice)
	}
	if notice.Content != "帮我总结这一段" {
		t.Fatalf("content = %q", notice.Content)
	}
}

func TestParseDriveCommentNoticeEventNoticeMetaShape(t *testing.T) {
	req := &larkevent.EventReq{Body: []byte(`{
		"event_id": "evt-meta-1",
		"type": "drive.notice.comment_add_v1",
		"timestamp": "1774951528000",
		"is_mentioned": true,
		"comment_id": "comment-1",
		"reply_id": "reply-1",
		"notice_meta": {
			"file_token": "docx-token",
			"file_type": "docx",
			"from_user_id": {
				"open_id": "ou_actor",
				"user_id": "on_actor"
			},
			"notice_type": "add_comment",
			"to_user_id": {
				"open_id": "ou_bot"
			}
		}
	}`)}

	notice, ok, err := parseDriveCommentNoticeEvent(req)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !ok {
		t.Fatal("expected drive comment event")
	}
	if notice.ActorUserID != "ou_actor" || notice.FileToken != "docx-token" || notice.FileType != "docx" || notice.CommentID != "comment-1" || notice.ReplyID != "reply-1" {
		t.Fatalf("unexpected notice: %#v", notice)
	}
	if notice.EventID != "evt-meta-1" || notice.CreateTime != "1774951528000" {
		t.Fatalf("unexpected event metadata: %#v", notice)
	}
	if notice.IsMentioned == nil || !*notice.IsMentioned {
		t.Fatalf("expected mentioned flag, got %#v", notice.IsMentioned)
	}
}

func TestParseDriveCommentNoticeEventSkipsUnmentionedNoticeMeta(t *testing.T) {
	req := &larkevent.EventReq{Body: []byte(`{
		"type": "drive.notice.comment_add_v1",
		"is_mentioned": false,
		"comment_id": "comment-1",
		"notice_meta": {
			"file_token": "docx-token",
			"file_type": "docx",
			"from_user_id": {"open_id": "ou_actor"}
		}
	}`)}

	_, ok, err := parseDriveCommentNoticeEvent(req)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ok {
		t.Fatal("expected unmentioned comment notice to be skipped")
	}
}

func TestDriveCommentSourceMessageIDRoundTrip(t *testing.T) {
	source := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  "docx",
		FileToken: "docx:token with space",
		CommentID: "comment-1",
		ReplyID:   "reply-1",
	})
	target, ok := parseDriveCommentSourceMessageID(source)
	if !ok {
		t.Fatalf("expected source id to parse: %s", source)
	}
	if target.FileType != "docx" || target.FileToken != "docx:token with space" || target.CommentID != "comment-1" || target.ReplyID != "reply-1" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestBuildDriveCommentPromptIncludesThreadContext(t *testing.T) {
	prompt := buildDriveCommentPrompt(
		driveCommentNotice{
			FileToken: "docx-token",
			FileType:  "docx",
			FileName:  "需求文档",
			FileURL:   "https://example.feishu.cn/docx/docx-token",
			CommentID: "comment-1",
			ReplyID:   "reply-2",
			Content:   "请处理这条评论",
		},
		&DriveFileCommentEntry{
			Quote: "被评论的原文",
			Replies: []DriveFileCommentReplyItem{
				{ReplyID: "reply-1", UserID: "ou_user", Text: "前一条评论"},
				{ReplyID: "reply-2", UserID: "ou_actor", Text: "请处理这条评论"},
			},
		},
		nil,
	)
	for _, want := range []string{"需求文档", "被评论的原文", "前一条评论", "当前触发内容", "请处理这条评论"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestDriveCommentActorUserIDFallsBackToReplyAuthor(t *testing.T) {
	actor := driveCommentActorUserID(
		driveCommentNotice{
			ReplyID: "reply-2",
		},
		&DriveFileCommentEntry{
			UserID: "ou_commenter",
			Replies: []DriveFileCommentReplyItem{
				{ReplyID: "reply-1", UserID: "ou_first", Text: "前一条评论"},
				{ReplyID: "reply-2", UserID: "ou_actor", Text: "请处理这条评论"},
			},
		},
	)
	if actor != "ou_actor" {
		t.Fatalf("actor = %q, want ou_actor", actor)
	}
}

func TestDriveCommentActorUserIDFallsBackToLatestMatchingText(t *testing.T) {
	actor := driveCommentActorUserID(
		driveCommentNotice{
			Content: "请处理这条评论",
		},
		&DriveFileCommentEntry{
			UserID: "ou_commenter",
			Replies: []DriveFileCommentReplyItem{
				{ReplyID: "reply-1", UserID: "ou_first", Text: "前一条评论"},
				{ReplyID: "reply-2", UserID: "ou_actor", Text: "请处理这条评论"},
			},
		},
	)
	if actor != "ou_actor" {
		t.Fatalf("actor = %q, want ou_actor", actor)
	}
}

func TestDriveCommentReplySuppression(t *testing.T) {
	gateway := NewLiveGateway(LiveGatewayConfig{GatewayID: "app-1"})
	gateway.rememberDriveCommentReply("docx", "docx-token", "comment-1", "处理好了")

	if !gateway.shouldSuppressDriveCommentNotice(driveCommentNotice{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
	}, "处理好了") {
		t.Fatal("expected matching reply echo to be suppressed")
	}
	if gateway.shouldSuppressDriveCommentNotice(driveCommentNotice{
		FileType:  "docx",
		FileToken: "docx-token",
		CommentID: "comment-1",
	}, "新的用户评论") {
		t.Fatal("did not expect different text to be suppressed")
	}
}
