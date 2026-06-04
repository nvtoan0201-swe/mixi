package compact

import (
	"sort"

	"github.com/user/mixi-agent/internal/session"
)

// MergeFileLists combines a previous compaction's file lists with the
// working set's read and written paths into the cumulative lists stored on
// the next compaction entry. A file that was ever modified is reported only
// as modified: readFiles = read − modified.
func MergeFileLists(prev session.FileLists, read, written []string) session.FileLists {
	modified := map[string]struct{}{}
	for _, p := range prev.ModifiedFiles {
		modified[p] = struct{}{}
	}
	for _, p := range written {
		modified[p] = struct{}{}
	}
	readSet := map[string]struct{}{}
	for _, p := range prev.ReadFiles {
		readSet[p] = struct{}{}
	}
	for _, p := range read {
		readSet[p] = struct{}{}
	}
	for p := range modified {
		delete(readSet, p)
	}
	return session.FileLists{
		ReadFiles:     sortedKeys(readSet),
		ModifiedFiles: sortedKeys(modified),
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
