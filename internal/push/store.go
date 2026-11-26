package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// Store is the set of subscriptions, held in one JSON file and in memory.
//
// One writer at a time reaches the file: a mutex guards both the in-memory list
// and the write, and the write goes through a temp file and a rename at mode
// 0600. The in-memory list changes only after the file write succeeds, so a
// failed write never leaves the caller with a store that disagrees with disk.
type Store struct {
	path string
	mu   sync.Mutex
	subs []Subscription
}

// Load reads the subscription file at path. A missing file is an empty store,
// because a fresh install has no phones yet. A file that cannot be read or
// parsed is an error: an unreadable store must not look like an empty one, or
// the next Add would overwrite subscriptions that were only temporarily
// unreachable.
func Load(path string) (*Store, error) {
	store := &Store{path: path, subs: []Subscription{}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return store, nil
	case err != nil:
		return nil, fmt.Errorf("push: read subscriptions %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &store.subs); err != nil {
		return nil, fmt.Errorf("push: parse subscriptions %s: %w", path, err)
	}
	if store.subs == nil {
		store.subs = []Subscription{}
	}
	return store, nil
}

// Add adds sub, replacing the entry with the same endpoint. The endpoint is the
// subscription's identity: a phone that subscribes again reports the same one
// and expects to hear from dm once, not twice.
func (s *Store) Add(sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Subscription, 0, len(s.subs)+1)
	replaced := false
	for _, have := range s.subs {
		if have.Endpoint == sub.Endpoint {
			next = append(next, sub)
			replaced = true
			continue
		}
		next = append(next, have)
	}
	if !replaced {
		next = append(next, sub)
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.subs = next
	return nil
}

// Remove deletes the entry with endpoint and reports whether it was there. A
// missing endpoint is not an error: the caller prunes a dead phone it may
// already have pruned, and the answer says whether this call did the work.
func (s *Store) Remove(endpoint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Subscription, 0, len(s.subs))
	found := false
	for _, have := range s.subs {
		if have.Endpoint == endpoint {
			found = true
			continue
		}
		next = append(next, have)
	}
	if !found {
		return false, nil
	}
	if err := s.write(next); err != nil {
		return false, err
	}
	s.subs = next
	return true, nil
}

// List returns a copy of the subscriptions. The caller may read and sort it
// without holding the store's lock or racing the writer.
func (s *Store) List() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Subscription{}, s.subs...)
}

// write replaces the file with subs. It runs with the lock held.
func (s *Store) write(subs []Subscription) error {
	raw, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return fmt.Errorf("push: encode subscriptions: %w", err)
	}
	raw = append(raw, '\n')
	return writeFileAtomic(s.path, raw)
}
