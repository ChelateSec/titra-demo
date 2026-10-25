package server

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ChelateSec/titra/internal/audit"
)

// Action is one recorded agent action as the server stores it: the guard's
// record plus a server id, receive time, and the run key it belongs to.
type Action struct {
	ID       int64     `json:"id"`
	Key      string    `json:"key"` // run key, so the live stream can route it
	Received time.Time `json:"received"`
	audit.Record
}

// Run is a group of actions from one pipeline run: one CI job, one agent.
type Run struct {
	Key        string    `json:"key"`
	Project    string    `json:"project"`
	Repo       string    `json:"repo"`
	Workflow   string    `json:"workflow"`
	RunID      string    `json:"run_id"`
	Actor      string    `json:"actor"`
	Mode       string    `json:"mode"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Total      int       `json:"total"`
	Allowed    int       `json:"allowed"`
	Blocked    int       `json:"blocked"`
	WouldBlock int       `json:"would_block"`
}

// Store holds actions and runs, persisted to a JSONL file, with live
// subscriptions for the dashboard stream. Dependency-free and good for a pilot;
// the method set is the seam to swap in Postgres later.
type Store struct {
	mu      sync.RWMutex
	actions []Action
	runs    map[string]*Run
	nextID  int64
	path    string
	subs    map[int]chan Action
	subSeq  int
	cfg     *Config
	cfgPath string
}

// OpenStore loads (or creates) a store rooted at dir.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		runs:    map[string]*Run{},
		path:    filepath.Join(dir, "actions.jsonl"),
		subs:    map[int]chan Action{},
		cfgPath: filepath.Join(dir, "config.json"),
	}
	if err := s.loadActions(); err != nil {
		return nil, err
	}
	if err := s.loadConfig(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) loadActions() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var a Action
		if json.Unmarshal(sc.Bytes(), &a) != nil {
			continue
		}
		if a.Key == "" {
			a.Key = runKey(a.Record)
		}
		s.actions = append(s.actions, a)
		if a.ID > s.nextID {
			s.nextID = a.ID
		}
		s.index(a)
	}
	return sc.Err()
}

// Add records an action, persists it, updates its run, and notifies subscribers.
func (s *Store) Add(rec audit.Record) Action {
	s.mu.Lock()
	s.nextID++
	a := Action{ID: s.nextID, Key: runKey(rec), Received: time.Now().UTC(), Record: rec}
	s.actions = append(s.actions, a)
	s.index(a)
	s.persist(a)
	subs := make([]chan Action, 0, len(s.subs))
	for _, ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- a:
		default: // never block ingest on a slow subscriber
		}
	}
	return a
}

// index updates the run aggregate for an action. Caller holds the write lock.
func (s *Store) index(a Action) {
	key := runKey(a.Record)
	r := s.runs[key]
	if r == nil {
		r = &Run{
			Key: key, Project: orLocal(a.Project), Repo: a.Repo,
			Workflow: a.Workflow, RunID: a.RunID, Actor: a.Actor,
			Mode: a.Mode, FirstSeen: a.Received,
		}
		s.runs[key] = r
	}
	r.LastSeen = a.Received
	if a.Mode != "" {
		r.Mode = a.Mode
	}
	if a.Actor != "" {
		r.Actor = a.Actor
	}
	r.Total++
	switch a.Verdict {
	case "block":
		r.Blocked++
	case "would-block":
		r.WouldBlock++
	default:
		r.Allowed++
	}
}

func (s *Store) persist(a Action) {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if line, err := json.Marshal(a); err == nil {
		_, _ = f.Write(append(line, '\n'))
	}
}

// Runs returns all runs, most recently active first.
func (s *Store) Runs() []*Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Run, 0, len(s.runs))
	for _, r := range s.runs {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// Run returns a single run and its actions in order.
func (s *Store) Run(key string) (*Run, []Action) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.runs[key]
	if r == nil {
		return nil, nil
	}
	cp := *r
	var acts []Action
	for _, a := range s.actions {
		if runKey(a.Record) == key {
			acts = append(acts, a)
		}
	}
	return &cp, acts
}

// Totals is the all-runs rollup shown on the dashboard header.
type Totals struct {
	Runs       int `json:"runs"`
	Actions    int `json:"actions"`
	Blocked    int `json:"blocked"`
	WouldBlock int `json:"would_block"`
}

func (s *Store) Totals() Totals {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := Totals{Runs: len(s.runs), Actions: len(s.actions)}
	for _, a := range s.actions {
		switch a.Verdict {
		case "block":
			t.Blocked++
		case "would-block":
			t.WouldBlock++
		}
	}
	return t
}

// Subscribe returns a channel of new actions and an unsubscribe func (for SSE).
func (s *Store) Subscribe() (<-chan Action, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.subSeq
	s.subSeq++
	ch := make(chan Action, 64)
	s.subs[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
	}
}

func runKey(r audit.Record) string {
	proj := orLocal(r.Project)
	repo := r.Repo
	if repo == "" {
		repo = "local"
	}
	run := r.RunID
	if run == "" {
		run = "session"
	}
	wf := r.Workflow
	if wf == "" {
		wf = "job"
	}
	return proj + "|" + repo + "|" + run + "|" + wf
}

func orLocal(s string) string {
	if s == "" {
		return "local"
	}
	return s
}
