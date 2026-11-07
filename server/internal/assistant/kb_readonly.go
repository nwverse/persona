package assistant

import (
	"context"
	"path/filepath"
	"strings"
)

func (s *Service) kbReadOnly(ctx context.Context) bool {
	set := s.kbSettings(ctx)
	return set != nil && !set.Off && set.ReadOnly
}

func (s *Service) kbWritable(ctx context.Context) bool {
	set := s.kbSettings(ctx)
	return set != nil && !set.Off && !set.ReadOnly
}

// kbReadDirs preserves the configured, read-only Markdown search roots in both modes.
func kbReadDirs(dirs []string) []string {
	var out []string
	for _, dir := range dirs {
		if dir = expandHome(strings.TrimSpace(dir)); filepath.IsAbs(dir) {
			out = append(out, dir)
		}
	}
	return out
}
