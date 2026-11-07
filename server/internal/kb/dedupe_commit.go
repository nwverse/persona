package kb

import "encoding/json"

// commitDedupe checks the model's result against the user's latest split while holding the same mutation lock as
// Unmerge. The check, verdict and merge must stay together: a queued result cannot reverse a completed unmerge.
func (e *Engine) commitDedupe(key string, verdict dedupeVerdict, keepSlug, dropSlug string) (bool, error) {
	e.Store.applyMu.Lock()
	defer e.Store.applyMu.Unlock()
	if e.Store.ReadOnly() {
		return false, ErrReadOnly
	}
	if e.Store.dedupeVerdicts()[key].Split {
		return false, nil
	}
	if err := e.Store.putDedupeVerdict(key, verdict); err != nil {
		return false, err
	}
	if !verdict.Same {
		return false, nil
	}
	if err := e.mergeProjects(keepSlug, dropSlug, "去重 · "+verdict.Reason, false); err != nil {
		return false, err
	}
	return true, nil
}

// putDedupeVerdict records one pair's verdict, reading the file again so a dedupe run and an unmerge do not
// overwrite each other's.
func (s *Store) putDedupeVerdict(key string, v dedupeVerdict) error {
	if s.ReadOnly() {
		return ErrReadOnly
	}
	s.dedupeMu.Lock()
	defer s.dedupeMu.Unlock()
	all := s.dedupeVerdictsLocked()
	all[key] = v
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.dedupePath(), append(raw, '\n'))
}
