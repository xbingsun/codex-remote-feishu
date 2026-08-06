package codexstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionIndexThreadCatalogRecentThreadsUsesRolloutCWD(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, "sessions")
	indexPath := filepath.Join(root, defaultCodexSessionIndexFilename)
	workspace := filepath.Join(root, "260605 eu mixer")
	threadID := "019ea756-9ec0-7e13-b946-3eb02090f596"

	writeSessionIndexFixture(t, indexPath,
		`{"id":"`+threadID+`","thread_name":"旧标题","updated_at":"2026-06-08T13:05:27.000000Z"}`,
		`{"id":"`+threadID+`","thread_name":"审查结构改进","updated_at":"2026-06-08T13:06:27.617581Z"}`,
	)
	writeSessionRolloutFixture(t, sessionsRoot, "2026/06/08/rollout-2026-06-08T21-05-32-"+threadID+".jsonl",
		`{"timestamp":"2026-06-08T13:06:21.516Z","type":"session_meta","payload":{"id":"`+threadID+`","timestamp":"2026-06-08T13:05:32.352Z","cwd":"`+workspace+`","source":"vscode"}}`,
		`{"timestamp":"2026-06-08T13:06:21.523Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"`+workspace+`","model":"gpt-5.5","effort":"xhigh"}}`,
	)

	catalog := NewSessionIndexThreadCatalog(SessionIndexThreadCatalogOptions{
		SessionIndexPath: indexPath,
		SessionsRoot:     sessionsRoot,
		Logf:             func(string, ...any) {},
	})
	threads, err := catalog.RecentThreads(10)
	if err != nil {
		t.Fatalf("recent threads: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("expected one thread, got %#v", threads)
	}
	got := threads[0]
	if got.ThreadID != threadID || got.Name != "审查结构改进" || got.CWD != filepath.ToSlash(workspace) {
		t.Fatalf("unexpected thread record: %#v", got)
	}
	if got.Loaded {
		t.Fatalf("expected session-index thread to be marked not loaded: %#v", got)
	}
	if want := time.Date(2026, 6, 8, 13, 6, 27, 617581000, time.UTC); !got.LastUsedAt.Equal(want) {
		t.Fatalf("unexpected LastUsedAt: got %s want %s", got.LastUsedAt, want)
	}

	thread, err := catalog.ThreadByID(threadID)
	if err != nil {
		t.Fatalf("thread by id: %v", err)
	}
	if thread == nil || thread.CWD != filepath.ToSlash(workspace) || thread.Name != "审查结构改进" {
		t.Fatalf("unexpected thread by id: %#v", thread)
	}

	workspaces, err := catalog.RecentWorkspaces(10)
	if err != nil {
		t.Fatalf("recent workspaces: %v", err)
	}
	if got := workspaces[filepath.ToSlash(workspace)]; got.IsZero() {
		t.Fatalf("expected workspace recency for %s, got %#v", workspace, workspaces)
	}
}

func TestSessionIndexThreadCatalogSkipsThreadsWithoutCWD(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, "sessions")
	indexPath := filepath.Join(root, defaultCodexSessionIndexFilename)
	threadID := "019ea756-9ec0-7e13-b946-3eb02090f596"

	writeSessionIndexFixture(t, indexPath,
		`{"id":"`+threadID+`","thread_name":"缺少 cwd","updated_at":"2026-06-08T13:06:27.617581Z"}`,
	)
	writeSessionRolloutFixture(t, sessionsRoot, "2026/06/08/rollout-2026-06-08T21-05-32-"+threadID+".jsonl",
		`{"timestamp":"2026-06-08T13:06:21.516Z","type":"session_meta","payload":{"id":"`+threadID+`","source":"vscode"}}`,
	)

	catalog := NewSessionIndexThreadCatalog(SessionIndexThreadCatalogOptions{
		SessionIndexPath: indexPath,
		SessionsRoot:     sessionsRoot,
		Logf:             func(string, ...any) {},
	})
	threads, err := catalog.RecentThreads(10)
	if err != nil {
		t.Fatalf("recent threads: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("expected no restorable threads without cwd, got %#v", threads)
	}
}

func writeSessionIndexFixture(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir session index: %v", err)
	}
	raw := ""
	for _, line := range lines {
		raw += line + "\n"
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write session index: %v", err)
	}
}

func writeSessionRolloutFixture(t *testing.T, root, relativePath string, lines ...string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir rollout: %v", err)
	}
	raw := ""
	for _, line := range lines {
		raw += line + "\n"
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}
