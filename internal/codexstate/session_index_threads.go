package codexstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

const defaultCodexSessionIndexFilename = "session_index.jsonl"

var errStopSessionRolloutMeta = errors.New("stop reading codex session rollout metadata")

type SessionIndexThreadCatalogOptions struct {
	CodexHome        string
	SessionIndexPath string
	SessionsRoot     string
	Logf             func(string, ...any)
}

type SessionIndexThreadCatalog struct {
	sessionIndexPath string
	sessionsRoot     string
	logf             func(string, ...any)
}

type sessionIndexEntry struct {
	ID        string
	Title     string
	UpdatedAt time.Time
}

type sessionRolloutMeta struct {
	ThreadID        string
	CWD             string
	Source          string
	Model           string
	ReasoningEffort string
	UpdatedAt       time.Time
}

func NewDefaultSessionIndexThreadCatalog(opts SessionIndexThreadCatalogOptions) (*SessionIndexThreadCatalog, error) {
	codexHome := strings.TrimSpace(opts.CodexHome)
	if codexHome == "" {
		var err error
		codexHome, err = defaultCodexHomeDir()
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(opts.SessionIndexPath) == "" {
		opts.SessionIndexPath = filepath.Join(codexHome, defaultCodexSessionIndexFilename)
	}
	if strings.TrimSpace(opts.SessionsRoot) == "" {
		opts.SessionsRoot = filepath.Join(codexHome, defaultCodexSessionsDirName)
	}
	info, err := os.Stat(opts.SessionIndexPath)
	switch {
	case err == nil && !info.IsDir():
		return NewSessionIndexThreadCatalog(opts), nil
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	default:
		return nil, errors.New("codex session index path is a directory: " + opts.SessionIndexPath)
	}
}

func NewSessionIndexThreadCatalog(opts SessionIndexThreadCatalogOptions) *SessionIndexThreadCatalog {
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &SessionIndexThreadCatalog{
		sessionIndexPath: strings.TrimSpace(opts.SessionIndexPath),
		sessionsRoot:     strings.TrimSpace(opts.SessionsRoot),
		logf:             logf,
	}
}

func (c *SessionIndexThreadCatalog) RecentThreads(limit int) ([]state.ThreadRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	entries, err := c.readLatestIndexEntries()
	if err != nil {
		c.logError("read session index", err)
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	metas, err := c.readRolloutMetas(wantedSessionIDs(entries))
	if err != nil {
		c.logError("read rollout metadata", err)
		return nil, err
	}
	threads := make([]state.ThreadRecord, 0, min(limit, len(entries)))
	for _, entry := range entries {
		meta := metas[entry.ID]
		thread := sessionIndexEntryToThreadRecord(entry, meta)
		if thread == nil {
			continue
		}
		threads = append(threads, *thread)
		if len(threads) >= limit {
			break
		}
	}
	return threads, nil
}

func (c *SessionIndexThreadCatalog) RecentWorkspaces(limit int) (map[string]time.Time, error) {
	if limit <= 0 {
		limit = 200
	}
	threads, err := c.RecentThreads(limit * 4)
	if err != nil {
		return nil, err
	}
	workspaces := map[string]time.Time{}
	for _, thread := range threads {
		workspaceKey := state.ResolveWorkspaceKey(thread.WorkspaceKey, thread.CWD)
		if workspaceKey == "" {
			continue
		}
		if current, ok := workspaces[workspaceKey]; !ok || thread.LastUsedAt.After(current) {
			workspaces[workspaceKey] = thread.LastUsedAt
		}
		if len(workspaces) >= limit {
			break
		}
	}
	if len(workspaces) == 0 {
		return nil, nil
	}
	return workspaces, nil
}

func (c *SessionIndexThreadCatalog) ThreadByID(threadID string) (*state.ThreadRecord, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}
	entry, err := c.indexEntryByID(threadID)
	if err != nil {
		c.logError("read session index thread", err)
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	metas, err := c.readRolloutMetas(map[string]struct{}{threadID: {}})
	if err != nil {
		c.logError("read rollout metadata", err)
		return nil, err
	}
	return sessionIndexEntryToThreadRecord(*entry, metas[threadID]), nil
}

func (c *SessionIndexThreadCatalog) readLatestIndexEntries() ([]sessionIndexEntry, error) {
	if c == nil || strings.TrimSpace(c.sessionIndexPath) == "" {
		return nil, nil
	}
	latest := map[string]sessionIndexEntry{}
	err := readLargeJSONLines(c.sessionIndexPath, func(line []byte) error {
		var raw struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
			UpdatedAt  string `json:"updated_at"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			return err
		}
		entry := sessionIndexEntry{
			ID:        strings.TrimSpace(raw.ID),
			Title:     strings.TrimSpace(raw.ThreadName),
			UpdatedAt: parseCodexTimestamp(raw.UpdatedAt),
		}
		if entry.ID == "" {
			return nil
		}
		current, ok := latest[entry.ID]
		if !ok || entry.UpdatedAt.After(current.UpdatedAt) || entry.UpdatedAt.Equal(current.UpdatedAt) {
			latest[entry.ID] = entry
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries := make([]sessionIndexEntry, 0, len(latest))
	for _, entry := range latest {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func (c *SessionIndexThreadCatalog) indexEntryByID(threadID string) (*sessionIndexEntry, error) {
	entries, err := c.readLatestIndexEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ID == threadID {
			entryCopy := entry
			return &entryCopy, nil
		}
	}
	return nil, nil
}

func (c *SessionIndexThreadCatalog) readRolloutMetas(want map[string]struct{}) (map[string]sessionRolloutMeta, error) {
	root := filepath.Clean(strings.TrimSpace(c.sessionsRoot))
	if root == "" || len(want) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	metas := map[string]sessionRolloutMeta{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if !rolloutFilenameMaybeMatchesAnyThreadID(filepath.Base(path), want) {
			return nil
		}
		meta, err := readSessionRolloutMeta(path)
		if err != nil {
			if c != nil && c.logf != nil {
				c.logf("codex session index catalog skipped rollout %s: %v", path, err)
			}
			return nil
		}
		if !sessionRolloutMetaVisible(meta) {
			return nil
		}
		if _, ok := want[meta.ThreadID]; !ok {
			return nil
		}
		if info, err := d.Info(); err == nil && meta.UpdatedAt.IsZero() {
			meta.UpdatedAt = info.ModTime().UTC()
		}
		current, ok := metas[meta.ThreadID]
		if !ok || meta.UpdatedAt.After(current.UpdatedAt) {
			metas[meta.ThreadID] = meta
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return metas, nil
}

func readSessionRolloutMeta(path string) (sessionRolloutMeta, error) {
	var meta sessionRolloutMeta
	err := readLargeJSONLines(path, func(line []byte) error {
		var decoded map[string]any
		if err := json.Unmarshal(line, &decoded); err != nil {
			return err
		}
		payload := payloadMap(decoded)
		switch topLevelType(decoded) {
		case "session_meta":
			if meta.ThreadID == "" {
				meta.ThreadID = strings.TrimSpace(stringField(payload, "id"))
			}
			if meta.CWD == "" {
				meta.CWD = strings.TrimSpace(stringField(payload, "cwd"))
			}
			if meta.Source == "" {
				meta.Source = strings.TrimSpace(stringField(payload, "source"))
			}
			if meta.Model == "" {
				meta.Model = strings.TrimSpace(stringField(payload, "model"))
			}
			if meta.UpdatedAt.IsZero() {
				meta.UpdatedAt = parseCodexTimestamp(stringField(payload, "timestamp"))
			}
		case "turn_context":
			if meta.CWD == "" {
				meta.CWD = strings.TrimSpace(stringField(payload, "cwd"))
			}
			if meta.Model == "" {
				meta.Model = strings.TrimSpace(stringField(payload, "model"))
			}
			if meta.ReasoningEffort == "" {
				meta.ReasoningEffort = strings.TrimSpace(stringField(payload, "effort"))
			}
		}
		if meta.ThreadID != "" && meta.CWD != "" {
			return errStopSessionRolloutMeta
		}
		return nil
	})
	if errors.Is(err, errStopSessionRolloutMeta) {
		return meta, nil
	}
	return meta, err
}

func sessionIndexEntryToThreadRecord(entry sessionIndexEntry, meta sessionRolloutMeta) *state.ThreadRecord {
	threadID := strings.TrimSpace(entry.ID)
	cwd := state.ResolveWorkspaceKey(meta.CWD)
	if threadID == "" || cwd == "" || internalProbeWorkspace(cwd) || cronRepoWorkspace(cwd) {
		return nil
	}
	updatedAt := entry.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = meta.UpdatedAt
	}
	title := strings.TrimSpace(entry.Title)
	return &state.ThreadRecord{
		ThreadID:                threadID,
		Name:                    title,
		Preview:                 title,
		WorkspaceKey:            cwd,
		CWD:                     cwd,
		State:                   string(agentproto.ThreadRuntimeStatusTypeNotLoaded),
		RuntimeStatus:           &agentproto.ThreadRuntimeStatus{Type: agentproto.ThreadRuntimeStatusTypeNotLoaded},
		ExplicitModel:           strings.TrimSpace(meta.Model),
		ExplicitReasoningEffort: strings.TrimSpace(meta.ReasoningEffort),
		Loaded:                  false,
		LastUsedAt:              updatedAt.UTC(),
	}
}

func sessionRolloutMetaVisible(meta sessionRolloutMeta) bool {
	if strings.TrimSpace(meta.ThreadID) == "" {
		return false
	}
	source := strings.TrimSpace(meta.Source)
	if source != "" && source != "cli" && source != "vscode" {
		return false
	}
	cwd := state.ResolveWorkspaceKey(meta.CWD)
	return cwd != "" && !internalProbeWorkspace(cwd) && !cronRepoWorkspace(cwd)
}

func wantedSessionIDs(entries []sessionIndexEntry) map[string]struct{} {
	want := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if id := strings.TrimSpace(entry.ID); id != "" {
			want[id] = struct{}{}
		}
	}
	return want
}

func rolloutFilenameMaybeMatchesAnyThreadID(name string, want map[string]struct{}) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(want) == 0 {
		return false
	}
	withoutExt := strings.TrimSuffix(name, filepath.Ext(name))
	if len(withoutExt) >= 36 {
		if _, ok := want[withoutExt[len(withoutExt)-36:]]; ok {
			return true
		}
	}
	for id := range want {
		if strings.Contains(name, id) {
			return true
		}
	}
	return false
}

func readLargeJSONLines(path string, fn func([]byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if runErr := fn(line); runErr != nil {
				return runErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func parseCodexTimestamp(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func cronRepoWorkspace(cwd string) bool {
	return strings.Contains(filepath.ToSlash(filepath.Clean(strings.TrimSpace(cwd))), "/cron-repos/runs/")
}

func (c *SessionIndexThreadCatalog) logError(scope string, err error) {
	if c == nil || c.logf == nil || err == nil {
		return
	}
	c.logf("codex session index thread catalog %s failed: %v", strings.TrimSpace(scope), err)
}
