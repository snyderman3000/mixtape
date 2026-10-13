package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Updates on open: when Mixtape starts with a live catalog, it checks what it
// installed for updates in the background and opens an UPDATES READY window
// listing them. Direct downloads cost nothing to check (their version is in the
// catalog); GitHub projects use the hour-long release cache, so reopening
// Mixtape doesn't spend GitHub's lookup allowance. Each update is announced
// once (data/notified.json); after that it's still marked ▲UPD in the list.

func notifiedPath(env *Env) string { return filepath.Join(env.DataDir, "notified.json") }

func loadNotified(env *Env) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(notifiedPath(env)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// newUpdates returns the updates not announced before and records them.
func newUpdates(env *Env, ports []*Port, updates map[string]string) []*Port {
	seen := loadNotified(env)
	var out []*Port
	for _, p := range ports {
		v, ok := updates[p.Name]
		if !ok || seen[p.Name] == v {
			continue
		}
		seen[p.Name] = v
		out = append(out, p)
	}
	if len(out) > 0 {
		if b, err := json.MarshalIndent(seen, "", " "); err == nil {
			_ = writeFileAtomic(notifiedPath(env), b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// autoCheckUpdates runs once at startup.
func (u *UI) autoCheckUpdates() {
	if u.cat == nil {
		return
	}
	var todo []*Port
	for _, p := range u.cat.Ports {
		if u.manifests[p.Name] != nil && !isSelf(p) {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		return
	}
	go func() {
		plans := map[*Port]*Plan{}
		for _, p := range todo {
			plan, err := u.env.PlanFor(p, false)
			if _, limited := err.(*RateLimitError); limited {
				break
			}
			if err == nil && plan != nil {
				plans[p] = plan
			}
		}
		u.post <- func() {
			var ports []*Port
			for p, plan := range plans {
				if u.plans[p.Name] == nil {
					u.plans[p.Name] = plan
				}
				m := u.manifests[p.Name]
				if m != nil && plan.Problem == "" && !plan.Snapshot && plan.Version != "" && plan.Version != m.Version {
					u.updates[p.Name] = plan.Version
					ports = append(ports, p)
				}
			}
			if fresh := newUpdates(u.env, ports, u.updates); len(fresh) > 0 {
				u.pendingUpd = fresh
				u.maybeShowUpdates()
			}
		}
	}()
}

// maybeShowUpdates opens the UPDATES READY window once nothing else is on screen
// (for example after the NEW ARRIVALS window is closed).
func (u *UI) maybeShowUpdates() {
	if len(u.pendingUpd) == 0 || u.modal != modalNone || u.scr == scrBoot {
		return
	}
	var list []*Port
	for _, p := range u.pendingUpd {
		if u.updates[p.Name] != "" { // not installed in the meantime
			list = append(list, p)
		}
	}
	u.pendingUpd = nil
	if len(list) == 0 {
		return
	}
	u.wnList, u.wnUpdates = list, true
	u.wnSel, u.wnScroll = 0, 0
	u.modal = modalWhatsNew
}
