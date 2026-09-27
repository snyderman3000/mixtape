package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runs only when MIXTAPE_REAL points at a folder of real release zips.
func TestRealArchives(t *testing.T) {
	dir := os.Getenv("MIXTAPE_REAL")
	if dir == "" {
		t.Skip("set MIXTAPE_REAL")
	}
	zs, _ := filepath.Glob(filepath.Join(dir, "*.zip"))
	for _, z := range zs {
		name := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(z), ".zip"), "_", " ")
		a, err := openArchive(z)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		l, err := DetectLayout(a, &Port{Name: name}, false)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else {
			n := 0
			for _, e := range a.entries {
				if _, ok := l.Map(e.name); ok && !e.dir {
					n++
				}
			}
			t.Logf("%-16s %-7s files=%-4d where=%v", name, l.Mode, n, l.Where)
		}
		a.closeFn()
	}
}
