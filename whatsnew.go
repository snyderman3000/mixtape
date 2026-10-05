package main

// "What's new": remembers which catalog entries this card has already seen
// (data/seen.json), so later visits can point out apps and ports added since.
//
// - First run: everything in the catalog is recorded as already known (time 0)
//   and nothing is announced.
// - Later runs: entries not in the file are "fresh". They are announced once in
//   the NEW ARRIVALS window, and stay in the NEW tab for freshDays.
// - Entries are never forgotten, so a project that briefly disappears from the
//   catalog (or an older offline catalog) isn't announced again later.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const freshDays = 14

type seenStore struct {
	FirstRun int64            `json:"first_run"`
	Ports    map[string]int64 `json:"ports"` // key -> unix time first seen; 0 = known since the first run
}

// portKey identifies a catalog entry across catalog updates: its GitHub repo,
// or its name when it has none.
func portKey(p *Port) string {
	if p.Repo != "" {
		return "gh:" + strings.ToLower(p.Repo)
	}
	return "name:" + norm(p.Name)
}

func seenPath(env *Env) string { return filepath.Join(env.DataDir, "seen.json") }

func loadSeen(env *Env) (*seenStore, bool) {
	b, err := os.ReadFile(seenPath(env))
	if err != nil {
		return &seenStore{Ports: map[string]int64{}}, false
	}
	var s seenStore
	if json.Unmarshal(b, &s) != nil || s.Ports == nil {
		return &seenStore{Ports: map[string]int64{}}, false
	}
	return &s, true
}

// news is what the catalog holds that's new to this card.
type news struct {
	sinceLast []*Port         // added since the last visit (announced in the window)
	firstSeen map[*Port]int64 // fresh entries (within freshDays) -> when first seen
}

// previousCatalog reads the catalog copies Mixtape saved on the last visit
// (data/ports.json and data/extra.json). Called before the catalog is loaded
// (loading overwrites them), it lets the first run of a Mixtape that has this
// feature still show what arrived since the previous visit. nil if there are none.
func previousCatalog(env *Env) map[string]bool {
	keys := map[string]bool{}
	none := map[string]*Recipe{}
	if b, err := os.ReadFile(filepath.Join(env.DataDir, "ports.json")); err == nil {
		if ports, err := parseCatalog(b, none); err == nil {
			for _, p := range ports {
				keys[portKey(p)] = true
			}
		}
	}
	if len(keys) == 0 {
		return nil // no main catalog copy: treat as a first run
	}
	if b, err := os.ReadFile(filepath.Join(env.DataDir, "extra.json")); err == nil {
		if ports, err := parseExtra(b, none); err == nil {
			for _, p := range ports {
				keys[portKey(p)] = true
			}
		}
	}
	return keys
}

// noteCatalog records the catalog's entries in seen.json and works out what's
// new. 'previous' (from previousCatalog) stands in for seen.json when that
// doesn't exist yet. The file is only rewritten when something was added.
func noteCatalog(env *Env, ports []*Port, now time.Time, previous map[string]bool) news {
	store, existed := loadSeen(env)
	n := news{firstSeen: map[*Port]int64{}}
	changed := !existed
	if !existed {
		store.FirstRun = now.Unix()
		if len(previous) > 0 {
			for k := range previous {
				store.Ports[k] = 0
			}
			existed = true // compare against the last visit's catalog
		}
	}
	cutoff := now.Add(-freshDays * 24 * time.Hour).Unix()
	for _, p := range ports {
		k := portKey(p)
		t, known := store.Ports[k]
		if !known {
			changed = true
			if !existed {
				t = 0 // first run: the whole catalog is the baseline
			} else {
				t = now.Unix()
				n.sinceLast = append(n.sinceLast, p)
			}
			store.Ports[k] = t
		}
		if t > 0 && t >= cutoff {
			n.firstSeen[p] = t
		}
	}
	if changed {
		if b, err := json.MarshalIndent(store, "", " "); err == nil {
			_ = writeFileAtomic(seenPath(env), b)
		}
	}
	sort.SliceStable(n.sinceLast, func(i, j int) bool {
		return strings.ToLower(n.sinceLast[i].Name) < strings.ToLower(n.sinceLast[j].Name)
	})
	return n
}

// freshOrder sorts NEW-tab entries newest first, then by name.
func freshOrder(ports []*Port, firstSeen map[*Port]int64) {
	sort.SliceStable(ports, func(i, j int) bool {
		ti, tj := firstSeen[ports[i]], firstSeen[ports[j]]
		if ti != tj {
			return ti > tj
		}
		return strings.ToLower(ports[i].Name) < strings.ToLower(ports[j].Name)
	})
}

// ageLabel says when an entry arrived, for the NEW tab ("TODAY", "3 DAYS AGO").
func ageLabel(t int64, now time.Time) string {
	d := int(now.Sub(time.Unix(t, 0)).Hours() / 24)
	switch {
	case d <= 0:
		return "TODAY"
	case d == 1:
		return "YESTERDAY"
	default:
		return strconv.Itoa(d) + " DAYS AGO"
	}
}
