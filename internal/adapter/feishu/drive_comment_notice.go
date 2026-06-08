package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"

	gatewaypkg "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/gateway"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
)

const (
	driveCommentNoticeEventType       = "drive.notice.comment_add_v1"
	driveCommentSourceMessagePrefix   = "drive-comment:"
	maxDriveCommentPromptRunes        = 12000
	maxDriveCommentThreadReplies      = 20
	driveCommentUnknownPlaceholder    = "未知"
	driveCommentFetchFailurePrefix    = "评论线程读取失败："
	driveCommentSourceMessagePartSize = 4
	driveCommentReplySuppressionTTL   = 5 * time.Minute
)

type driveCommentTarget struct {
	FileType  string
	FileToken string
	CommentID string
	ReplyID   string
}

type driveCommentNotice struct {
	EventID     string
	EventType   string
	CreateTime  string
	RequestID   string
	IsMentioned *bool
	FileToken   string
	FileType    string
	FileName    string
	FileURL     string
	CommentID   string
	ReplyID     string
	ActorUserID string
	Content     string
}

type driveCommentReplySuppression struct {
	Text      string
	ExpiresAt time.Time
}

func (g *LiveGateway) handleDriveCommentNoticeEvent(ctx context.Context, event *larkevent.EventReq, handler ActionHandler) error {
	action, ok, err := g.parseDriveCommentNoticeAction(ctx, event)
	if err != nil {
		log.Printf("feishu drive comment notice parse failed: err=%v", err)
		return err
	}
	if !ok {
		return nil
	}
	return handleGatewayEventAction(ctx, action, handler)
}

func (g *LiveGateway) parseDriveCommentNoticeAction(ctx context.Context, event *larkevent.EventReq) (control.Action, bool, error) {
	notice, ok, err := parseDriveCommentNoticeEvent(event)
	if err != nil || !ok {
		return control.Action{}, ok, err
	}
	if strings.TrimSpace(notice.FileToken) == "" {
		return control.Action{}, false, fmt.Errorf("drive comment notice missing file_token")
	}
	if strings.TrimSpace(notice.FileType) == "" {
		return control.Action{}, false, fmt.Errorf("drive comment notice missing file_type")
	}
	if strings.TrimSpace(notice.CommentID) == "" {
		return control.Action{}, false, fmt.Errorf("drive comment notice missing comment_id")
	}

	entry, fetchErr := g.getDriveFileCommentEntry(ctx, notice.FileToken, notice.FileType, notice.CommentID)
	if fetchErr != nil {
		log.Printf(
			"feishu drive comment thread fetch failed: gateway=%s file=%s type=%s comment=%s err=%v",
			g.config.GatewayID,
			notice.FileToken,
			notice.FileType,
			notice.CommentID,
			fetchErr,
		)
	}

	preview := driveCommentTriggerText(notice, entry)
	if preview == "" {
		preview = "飞书文档评论 @ bot"
	}
	if g.shouldSuppressDriveCommentNotice(notice, preview) {
		log.Printf(
			"feishu drive comment notice ignored: gateway=%s file=%s type=%s comment=%s reason=self_reply_echo",
			g.config.GatewayID,
			notice.FileToken,
			notice.FileType,
			notice.CommentID,
		)
		return control.Action{}, false, nil
	}
	actorUserID := driveCommentActorUserID(notice, entry)
	if strings.TrimSpace(actorUserID) == "" {
		return control.Action{}, false, fmt.Errorf("drive comment notice missing operator open_id/user_id")
	}
	prompt := buildDriveCommentPrompt(notice, entry, fetchErr)
	if prompt == "" {
		prompt = preview
	}
	input := agentproto.Input{Type: agentproto.InputText, Text: prompt}
	sourceMessageID := driveCommentSourceMessageID(driveCommentTarget{
		FileType:  notice.FileType,
		FileToken: notice.FileToken,
		CommentID: notice.CommentID,
		ReplyID:   notice.ReplyID,
	})
	surfaceSessionID := gatewaypkg.SurfaceIDForInbound(g.config.GatewayID, "", "p2p", actorUserID)
	return control.Action{
		Kind:             control.ActionTextMessage,
		GatewayID:        g.config.GatewayID,
		SurfaceSessionID: surfaceSessionID,
		ActorUserID:      actorUserID,
		MessageID:        sourceMessageID,
		Text:             preview,
		Inputs:           []agentproto.Input{input},
		SteerInputs:      []agentproto.Input{input},
		Inbound: &control.ActionInboundMeta{
			EventID:           strings.TrimSpace(notice.EventID),
			EventType:         strings.TrimSpace(notice.EventType),
			EventCreateTime:   parseDriveCommentEventTime(notice.CreateTime),
			RequestID:         strings.TrimSpace(notice.RequestID),
			OpenMessageID:     sourceMessageID,
			MessageCreateTime: parseDriveCommentEventTime(notice.CreateTime),
		},
	}, true, nil
}

func driveCommentActorUserID(notice driveCommentNotice, entry *DriveFileCommentEntry) string {
	if actor := strings.TrimSpace(notice.ActorUserID); actor != "" {
		return actor
	}
	if entry == nil {
		return ""
	}
	if replyID := strings.TrimSpace(notice.ReplyID); replyID != "" {
		for _, reply := range entry.Replies {
			if strings.TrimSpace(reply.ReplyID) == replyID {
				if userID := strings.TrimSpace(reply.UserID); userID != "" {
					return userID
				}
			}
		}
	}
	triggerText := normalizeDriveCommentSuppressionText(driveCommentTriggerText(notice, entry))
	if triggerText != "" {
		for i := len(entry.Replies) - 1; i >= 0; i-- {
			reply := entry.Replies[i]
			if normalizeDriveCommentSuppressionText(reply.Text) == triggerText {
				if userID := strings.TrimSpace(reply.UserID); userID != "" {
					return userID
				}
			}
		}
	}
	for i := len(entry.Replies) - 1; i >= 0; i-- {
		if userID := strings.TrimSpace(entry.Replies[i].UserID); userID != "" {
			return userID
		}
	}
	return strings.TrimSpace(entry.UserID)
}

func parseDriveCommentNoticeEvent(req *larkevent.EventReq) (driveCommentNotice, bool, error) {
	if req == nil || len(req.Body) == 0 {
		return driveCommentNotice{}, false, nil
	}
	var root map[string]any
	if err := json.Unmarshal(req.Body, &root); err != nil {
		return driveCommentNotice{}, false, err
	}
	header := mapAny(root["header"])
	event := mapAny(root["event"])
	if len(event) == 0 {
		event = root
	}
	eventType := firstNonEmpty(
		stringAt(header, "event_type"),
		stringAt(root, "event_type"),
		stringAt(event, "event_type"),
		stringAt(root, "type"),
		stringAt(event, "type"),
	)
	if eventType != "" && eventType != driveCommentNoticeEventType {
		return driveCommentNotice{}, false, nil
	}
	notice := driveCommentNotice{
		EventID: firstNonEmpty(
			stringAt(header, "event_id"),
			stringAt(root, "event_id"),
			stringAt(event, "event_id"),
		),
		EventType: firstNonEmpty(eventType, driveCommentNoticeEventType),
		CreateTime: firstNonEmpty(
			stringAt(header, "create_time"),
			stringAt(root, "create_time"),
			stringAt(event, "create_time"),
			stringAt(root, "timestamp"),
			stringAt(event, "timestamp"),
		),
		RequestID:   req.RequestId(),
		IsMentioned: boolPtrAt(event, "is_mentioned"),
		FileToken: firstNonEmpty(
			stringAt(event, "file_token"),
			stringAt(event, "doc_token"),
			stringAt(event, "document_token"),
			stringAt(event, "obj_token"),
			nestedString(event, "notice_meta", "file_token"),
			nestedString(event, "notice_meta", "doc_token"),
			nestedString(event, "notice_meta", "document_token"),
			nestedString(event, "notice_meta", "obj_token"),
			nestedString(event, "file", "file_token"),
			nestedString(event, "file", "token"),
			nestedString(event, "document", "token"),
		),
		FileType: normalizeDriveFileCommentFileType(firstNonEmpty(
			stringAt(event, "file_type"),
			stringAt(event, "doc_type"),
			stringAt(event, "document_type"),
			stringAt(event, "obj_type"),
			nestedString(event, "notice_meta", "file_type"),
			nestedString(event, "notice_meta", "doc_type"),
			nestedString(event, "notice_meta", "document_type"),
			nestedString(event, "notice_meta", "obj_type"),
			nestedString(event, "file", "file_type"),
			nestedString(event, "file", "type"),
			nestedString(event, "document", "type"),
		)),
		FileName: firstNonEmpty(
			stringAt(event, "file_name"),
			stringAt(event, "name"),
			stringAt(event, "title"),
			nestedString(event, "file", "name"),
			nestedString(event, "document", "title"),
		),
		FileURL: firstNonEmpty(
			stringAt(event, "file_url"),
			stringAt(event, "url"),
			stringAt(event, "link"),
			nestedString(event, "file", "url"),
			nestedString(event, "document", "url"),
		),
		CommentID: firstNonEmpty(
			stringAt(event, "comment_id"),
			stringAt(event, "commentId"),
			stringAt(event, "batch_comment_id"),
			stringAt(event, "batchCommentId"),
			nestedString(event, "notice_meta", "comment_id"),
			nestedString(event, "notice_meta", "commentId"),
			nestedString(event, "notice_meta", "batch_comment_id"),
			nestedString(event, "notice_meta", "batchCommentId"),
			nestedString(event, "comment", "comment_id"),
			nestedString(event, "comment", "id"),
		),
		ReplyID: firstNonEmpty(
			stringAt(event, "reply_id"),
			stringAt(event, "replyId"),
			nestedString(event, "reply", "reply_id"),
			nestedString(event, "reply", "id"),
		),
	}
	if notice.FileType == "" {
		notice.FileType = inferDriveCommentFileTypeFromURL(notice.FileURL)
	}
	if notice.IsMentioned != nil && !*notice.IsMentioned {
		return driveCommentNotice{}, false, nil
	}
	notice.ActorUserID = preferredDriveCommentUserID(
		nestedString(event, "notice_meta", "from_user_id", "open_id"),
		nestedString(event, "notice_meta", "from_user_id", "user_id"),
		nestedString(event, "notice_meta", "from_user_id", "union_id"),
		nestedString(event, "operator_id", "open_id"),
		nestedString(event, "operator", "open_id"),
		nestedString(event, "operator_id", "user_id"),
		nestedString(event, "operator", "user_id"),
		nestedString(event, "operator_id", "union_id"),
		nestedString(event, "operator", "union_id"),
		stringAt(event, "open_id"),
		stringAt(event, "user_id"),
		stringAt(event, "union_id"),
	)
	notice.Content = firstNonEmpty(
		extractDriveCommentNoticeText(event["reply_content"]),
		extractDriveCommentNoticeText(event["comment_content"]),
		extractDriveCommentNoticeText(event["content"]),
		extractDriveCommentNoticeText(event["text"]),
		extractDriveCommentNoticeText(nestedAny(event, "reply", "content")),
		extractDriveCommentNoticeText(nestedAny(event, "comment", "content")),
		extractDriveCommentNoticeText(nestedAny(event, "reply", "reply_content")),
		extractDriveCommentNoticeText(nestedAny(event, "comment", "comment_content")),
	)
	return notice, true, nil
}

func driveCommentSourceMessageID(target driveCommentTarget) string {
	parts := []string{
		url.QueryEscape(strings.TrimSpace(target.FileType)),
		url.QueryEscape(strings.TrimSpace(target.FileToken)),
		url.QueryEscape(strings.TrimSpace(target.CommentID)),
		url.QueryEscape(strings.TrimSpace(target.ReplyID)),
	}
	return driveCommentSourceMessagePrefix + strings.Join(parts, ":")
}

func parseDriveCommentSourceMessageID(value string) (driveCommentTarget, bool) {
	raw := strings.TrimSpace(value)
	if !strings.HasPrefix(raw, driveCommentSourceMessagePrefix) {
		return driveCommentTarget{}, false
	}
	parts := strings.Split(strings.TrimPrefix(raw, driveCommentSourceMessagePrefix), ":")
	if len(parts) != driveCommentSourceMessagePartSize {
		return driveCommentTarget{}, false
	}
	decoded := make([]string, 0, len(parts))
	for _, part := range parts {
		value, err := url.QueryUnescape(part)
		if err != nil {
			return driveCommentTarget{}, false
		}
		decoded = append(decoded, strings.TrimSpace(value))
	}
	target := driveCommentTarget{
		FileType:  normalizeDriveFileCommentFileType(decoded[0]),
		FileToken: decoded[1],
		CommentID: decoded[2],
		ReplyID:   decoded[3],
	}
	if target.FileType == "" || target.FileToken == "" || target.CommentID == "" {
		return driveCommentTarget{}, false
	}
	return target, true
}

func buildDriveCommentPrompt(notice driveCommentNotice, entry *DriveFileCommentEntry, fetchErr error) string {
	var b strings.Builder
	b.WriteString("你在飞书云文档评论里被 @ 了。请直接回答评论里的请求；最终答复会自动写回同一个评论线程。\n\n")
	b.WriteString("文档信息：\n")
	writeDriveCommentPromptLine(&b, "文件名", notice.FileName)
	writeDriveCommentPromptLine(&b, "文件类型", notice.FileType)
	writeDriveCommentPromptLine(&b, "文件 token", notice.FileToken)
	writeDriveCommentPromptLine(&b, "文档链接", notice.FileURL)
	writeDriveCommentPromptLine(&b, "评论 ID", notice.CommentID)
	if strings.TrimSpace(notice.ReplyID) != "" {
		writeDriveCommentPromptLine(&b, "触发回复 ID", notice.ReplyID)
	}
	if entry != nil && strings.TrimSpace(entry.Quote) != "" {
		b.WriteString("\n评论引用：\n")
		b.WriteString(strings.TrimSpace(entry.Quote))
		b.WriteString("\n")
	}
	if fetchErr != nil {
		b.WriteString("\n")
		b.WriteString(driveCommentFetchFailurePrefix)
		b.WriteString(strings.TrimSpace(fetchErr.Error()))
		b.WriteString("\n")
	}
	thread := formatDriveCommentThread(entry)
	if thread != "" {
		b.WriteString("\n评论线程：\n")
		b.WriteString(thread)
		b.WriteString("\n")
	}
	trigger := driveCommentTriggerText(notice, entry)
	if trigger != "" {
		b.WriteString("\n当前触发内容：\n")
		b.WriteString(trigger)
		b.WriteString("\n")
	}
	return truncateRunes(strings.TrimSpace(b.String()), maxDriveCommentPromptRunes)
}

func writeDriveCommentPromptLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = driveCommentUnknownPlaceholder
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString("：")
	b.WriteString(value)
	b.WriteString("\n")
}

func driveCommentTriggerText(notice driveCommentNotice, entry *DriveFileCommentEntry) string {
	if text := strings.TrimSpace(notice.Content); text != "" {
		return text
	}
	if entry == nil || len(entry.Replies) == 0 {
		return ""
	}
	if replyID := strings.TrimSpace(notice.ReplyID); replyID != "" {
		for _, reply := range entry.Replies {
			if strings.TrimSpace(reply.ReplyID) == replyID && strings.TrimSpace(reply.Text) != "" {
				return strings.TrimSpace(reply.Text)
			}
		}
	}
	for i := len(entry.Replies) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(entry.Replies[i].Text); text != "" {
			return text
		}
	}
	return ""
}

func formatDriveCommentThread(entry *DriveFileCommentEntry) string {
	if entry == nil || len(entry.Replies) == 0 {
		return ""
	}
	start := 0
	if len(entry.Replies) > maxDriveCommentThreadReplies {
		start = len(entry.Replies) - maxDriveCommentThreadReplies
	}
	lines := make([]string, 0, len(entry.Replies)-start)
	for _, reply := range entry.Replies[start:] {
		text := strings.TrimSpace(reply.Text)
		if text == "" {
			continue
		}
		userID := strings.TrimSpace(reply.UserID)
		if userID == "" {
			userID = driveCommentUnknownPlaceholder
		}
		replyID := strings.TrimSpace(reply.ReplyID)
		if replyID != "" {
			lines = append(lines, fmt.Sprintf("- %s（%s）：%s", userID, replyID, text))
		} else {
			lines = append(lines, fmt.Sprintf("- %s：%s", userID, text))
		}
	}
	return strings.Join(lines, "\n")
}

func extractDriveCommentNoticeText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := extractDriveCommentNoticeText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	case map[string]any:
		if text := stringAt(v, "text"); text != "" {
			return text
		}
		if text := stringAt(v, "plain_text"); text != "" {
			return text
		}
		if text := extractDriveCommentNoticeText(v["text_run"]); text != "" {
			return text
		}
		if text := extractDriveCommentNoticeText(nestedAny(v, "text_run", "text")); text != "" {
			return text
		}
		for _, key := range []string{"elements", "content", "reply_content", "comment_content"} {
			if text := extractDriveCommentNoticeText(v[key]); text != "" {
				return text
			}
		}
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func inferDriveCommentFileTypeFromURL(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	for _, part := range []string{"/docx/", "/docs/", "/doc/", "/sheets/", "/sheet/", "/base/", "/slides/", "/file/"} {
		if strings.Contains(raw, part) {
			switch part {
			case "/docx/", "/docs/":
				return "docx"
			case "/doc/":
				return "doc"
			case "/sheets/", "/sheet/":
				return "sheet"
			case "/slides/":
				return "slides"
			case "/file/":
				return "file"
			}
		}
	}
	return ""
}

func preferredDriveCommentUserID(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (g *LiveGateway) rememberDriveCommentReply(fileType, fileToken, commentID, text string) {
	if g == nil {
		return
	}
	key := driveCommentSuppressionKey(fileType, fileToken, commentID)
	text = normalizeDriveCommentSuppressionText(text)
	if key == "" || text == "" {
		return
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneDriveCommentReplySuppressionsLocked(now)
	g.driveCommentReplies[key] = append(g.driveCommentReplies[key], driveCommentReplySuppression{
		Text:      text,
		ExpiresAt: now.Add(driveCommentReplySuppressionTTL),
	})
}

func (g *LiveGateway) shouldSuppressDriveCommentNotice(notice driveCommentNotice, text string) bool {
	if g == nil {
		return false
	}
	key := driveCommentSuppressionKey(notice.FileType, notice.FileToken, notice.CommentID)
	text = normalizeDriveCommentSuppressionText(text)
	if key == "" || text == "" {
		return false
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneDriveCommentReplySuppressionsLocked(now)
	for _, item := range g.driveCommentReplies[key] {
		if item.Text == text {
			return true
		}
	}
	return false
}

func (g *LiveGateway) pruneDriveCommentReplySuppressionsLocked(now time.Time) {
	if g.driveCommentReplies == nil {
		g.driveCommentReplies = map[string][]driveCommentReplySuppression{}
		return
	}
	for key, items := range g.driveCommentReplies {
		kept := items[:0]
		for _, item := range items {
			if item.ExpiresAt.After(now) {
				kept = append(kept, item)
			}
		}
		if len(kept) == 0 {
			delete(g.driveCommentReplies, key)
			continue
		}
		g.driveCommentReplies[key] = kept
	}
}

func driveCommentSuppressionKey(fileType, fileToken, commentID string) string {
	fileType = normalizeDriveFileCommentFileType(fileType)
	fileToken = strings.TrimSpace(fileToken)
	commentID = strings.TrimSpace(commentID)
	if fileType == "" || fileToken == "" || commentID == "" {
		return ""
	}
	return strings.Join([]string{fileType, fileToken, commentID}, "\x00")
}

func normalizeDriveCommentSuppressionText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func mapAny(value any) map[string]any {
	values, _ := value.(map[string]any)
	return values
}

func stringAt(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func boolPtrAt(values map[string]any, key string) *bool {
	if len(values) == 0 {
		return nil
	}
	value, ok := values[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case bool:
		return &v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes":
			out := true
			return &out
		case "false", "0", "no":
			out := false
			return &out
		}
	case json.Number:
		switch strings.TrimSpace(v.String()) {
		case "1":
			out := true
			return &out
		case "0":
			out := false
			return &out
		}
	}
	return nil
}

func nestedAny(values map[string]any, path ...string) any {
	var current any = values
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}

func nestedString(values map[string]any, path ...string) string {
	return extractDriveCommentNoticeText(nestedAny(values, path...))
}

func parseDriveCommentEventTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}
	}
	switch {
	case value >= 1e17:
		return time.Unix(0, value).UTC()
	case value >= 1e14:
		return time.UnixMicro(value).UTC()
	case value >= 1e11:
		return time.UnixMilli(value).UTC()
	default:
		return time.Unix(value, 0).UTC()
	}
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "\n\n[内容已截断]"
}
