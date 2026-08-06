package daemon

import (
	"sort"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/claudestate"
	"github.com/kxn/codex-remote-feishu/internal/codexstate"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
	"github.com/kxn/codex-remote-feishu/internal/core/threadcatalogcontract"
)

type daemonPersistedThreadCatalog struct {
	codexSQLite       *codexstate.SQLiteThreadCatalog
	codexSessionIndex *codexstate.SessionIndexThreadCatalog
	claude            *claudestate.SessionCatalog
}

var _ threadcatalogcontract.BackendAwarePersistedThreadCatalog = (*daemonPersistedThreadCatalog)(nil)

func newDaemonPersistedThreadCatalog(logf func(string, ...any)) (*daemonPersistedThreadCatalog, error) {
	codexCatalog, err := codexstate.NewDefaultSQLiteThreadCatalog(codexstate.SQLiteThreadCatalogOptions{Logf: logf})
	if err != nil {
		return nil, err
	}
	sessionIndexCatalog, err := codexstate.NewDefaultSessionIndexThreadCatalog(codexstate.SessionIndexThreadCatalogOptions{Logf: logf})
	if err != nil {
		return nil, err
	}
	return &daemonPersistedThreadCatalog{
		codexSQLite:       codexCatalog,
		codexSessionIndex: sessionIndexCatalog,
		claude:            claudestate.NewSessionCatalog(claudestate.SessionCatalogOptions{Logf: logf}),
	}, nil
}

func (c *daemonPersistedThreadCatalog) RecentThreads(limit int) ([]state.ThreadRecord, error) {
	return c.RecentThreadsForBackend(agentproto.BackendCodex, limit)
}

func (c *daemonPersistedThreadCatalog) RecentWorkspaces(limit int) (map[string]time.Time, error) {
	return c.RecentWorkspacesForBackend(agentproto.BackendCodex, limit)
}

func (c *daemonPersistedThreadCatalog) ThreadByID(threadID string) (*state.ThreadRecord, error) {
	return c.ThreadByIDForBackend(agentproto.BackendCodex, threadID)
}

func (c *daemonPersistedThreadCatalog) RecentThreadsForBackend(backend agentproto.Backend, limit int) ([]state.ThreadRecord, error) {
	switch agentproto.NormalizeBackend(backend) {
	case agentproto.BackendClaude:
		if c == nil || c.claude == nil {
			return nil, nil
		}
		return c.claude.RecentThreads(limit)
	default:
		if c == nil {
			return nil, nil
		}
		return c.recentCodexThreads(limit)
	}
}

func (c *daemonPersistedThreadCatalog) RecentWorkspacesForBackend(backend agentproto.Backend, limit int) (map[string]time.Time, error) {
	switch agentproto.NormalizeBackend(backend) {
	case agentproto.BackendClaude:
		if c == nil || c.claude == nil {
			return nil, nil
		}
		return c.claude.RecentWorkspaces(limit)
	default:
		if c == nil {
			return nil, nil
		}
		return c.recentCodexWorkspaces(limit)
	}
}

func (c *daemonPersistedThreadCatalog) ThreadByIDForBackend(backend agentproto.Backend, threadID string) (*state.ThreadRecord, error) {
	switch agentproto.NormalizeBackend(backend) {
	case agentproto.BackendClaude:
		if c == nil || c.claude == nil {
			return nil, nil
		}
		return c.claude.ThreadByID(threadID)
	default:
		if c == nil {
			return nil, nil
		}
		return c.codexThreadByID(threadID)
	}
}

func (c *daemonPersistedThreadCatalog) recentCodexThreads(limit int) ([]state.ThreadRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		merged   []state.ThreadRecord
		firstErr error
	)
	if c.codexSQLite != nil {
		threads, err := c.codexSQLite.RecentThreads(limit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		merged = mergePersistedThreadRecords(merged, threads)
	}
	if c.codexSessionIndex != nil {
		threads, err := c.codexSessionIndex.RecentThreads(limit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		merged = mergePersistedThreadRecords(merged, threads)
	}
	sortPersistedThreadRecords(merged)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	if len(merged) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return merged, nil
}

func (c *daemonPersistedThreadCatalog) recentCodexWorkspaces(limit int) (map[string]time.Time, error) {
	if limit <= 0 {
		limit = 200
	}
	workspaces := map[string]time.Time{}
	var firstErr error
	if c.codexSQLite != nil {
		next, err := c.codexSQLite.RecentWorkspaces(limit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		mergeWorkspaceRecency(workspaces, next)
	}
	if c.codexSessionIndex != nil {
		next, err := c.codexSessionIndex.RecentWorkspaces(limit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		mergeWorkspaceRecency(workspaces, next)
	}
	if len(workspaces) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if len(workspaces) == 0 {
		return nil, nil
	}
	return workspaces, nil
}

func (c *daemonPersistedThreadCatalog) codexThreadByID(threadID string) (*state.ThreadRecord, error) {
	var firstErr error
	if c.codexSQLite != nil {
		thread, err := c.codexSQLite.ThreadByID(threadID)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if thread != nil {
			return thread, nil
		}
	}
	if c.codexSessionIndex != nil {
		thread, err := c.codexSessionIndex.ThreadByID(threadID)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if thread != nil {
			return thread, nil
		}
	}
	return nil, firstErr
}

func mergePersistedThreadRecords(existing, next []state.ThreadRecord) []state.ThreadRecord {
	if len(next) == 0 {
		return existing
	}
	index := make(map[string]int, len(existing)+len(next))
	for i := range existing {
		index[existing[i].ThreadID] = i
	}
	for _, thread := range next {
		if thread.ThreadID == "" {
			continue
		}
		if currentIndex, ok := index[thread.ThreadID]; ok {
			existing[currentIndex] = mergePersistedThreadRecord(existing[currentIndex], thread)
			continue
		}
		index[thread.ThreadID] = len(existing)
		existing = append(existing, thread)
	}
	return existing
}

func mergePersistedThreadRecord(current, next state.ThreadRecord) state.ThreadRecord {
	primary := current
	secondary := next
	if next.LastUsedAt.After(current.LastUsedAt) {
		primary = next
		secondary = current
	}
	if primary.Name == "" {
		primary.Name = secondary.Name
	}
	if primary.Preview == "" {
		primary.Preview = secondary.Preview
	}
	if primary.FirstUserMessage == "" {
		primary.FirstUserMessage = secondary.FirstUserMessage
	}
	if primary.WorkspaceKey == "" {
		primary.WorkspaceKey = secondary.WorkspaceKey
	}
	if primary.CWD == "" {
		primary.CWD = secondary.CWD
	}
	if primary.ExplicitModel == "" {
		primary.ExplicitModel = secondary.ExplicitModel
	}
	if primary.ExplicitReasoningEffort == "" {
		primary.ExplicitReasoningEffort = secondary.ExplicitReasoningEffort
	}
	if primary.RuntimeStatus == nil {
		primary.RuntimeStatus = secondary.RuntimeStatus
	}
	primary.Loaded = primary.Loaded || secondary.Loaded
	return primary
}

func sortPersistedThreadRecords(threads []state.ThreadRecord) {
	sort.SliceStable(threads, func(i, j int) bool {
		if !threads[i].LastUsedAt.Equal(threads[j].LastUsedAt) {
			return threads[i].LastUsedAt.After(threads[j].LastUsedAt)
		}
		return threads[i].ThreadID < threads[j].ThreadID
	})
}

func mergeWorkspaceRecency(target, next map[string]time.Time) {
	for workspaceKey, updatedAt := range next {
		if workspaceKey == "" {
			continue
		}
		if current, ok := target[workspaceKey]; !ok || updatedAt.After(current) {
			target[workspaceKey] = updatedAt
		}
	}
}
