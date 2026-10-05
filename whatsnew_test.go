package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func testPorts(names ...string) []*Port {
	var out []*Port
	for _, n := range names {
		out = append(out, &Port{Name: n, Repo: "someone/" + n, Categories: []string{"game"}})
	}
	return out
}

func names(ps []*Port) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestWhatsNewFirstRunAnnouncesNothing(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	n := noteCatalog(env, testPorts("a", "b", "c"), now, nil)
	if len(n.sinceLast) != 0 || len(n.firstSeen) != 0 {
		t.Fatalf("first run should be quiet: %v / %d fresh", names(n.sinceLast), len(n.firstSeen))
	}
	if _, err := os.Stat(seenPath(env)); err != nil {
		t.Fatal("seen.json not written on first run")
	}
}

func TestWhatsNewAnnouncesOnceThenKeepsInTab(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	day := 24 * time.Hour
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	noteCatalog(env, testPorts("a", "b"), t0, nil)

	// next visit: c and d were added
	n := noteCatalog(env, testPorts("a", "b", "c", "d"), t0.Add(day), nil)
	if got := names(n.sinceLast); len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("since last visit = %v, want [c d]", got)
	}
	if len(n.firstSeen) != 2 {
		t.Fatalf("fresh = %d, want 2", len(n.firstSeen))
	}

	// the visit after: no announcement, but still in the NEW tab
	n = noteCatalog(env, testPorts("a", "b", "c", "d"), t0.Add(2*day), nil)
	if len(n.sinceLast) != 0 {
		t.Fatalf("announced again: %v", names(n.sinceLast))
	}
	if len(n.firstSeen) != 2 {
		t.Fatalf("NEW tab lost entries: %d", len(n.firstSeen))
	}

	// a project that drops out of the catalog and comes back isn't new again
	noteCatalog(env, testPorts("a", "b", "d"), t0.Add(3*day), nil)
	n = noteCatalog(env, testPorts("a", "b", "c", "d"), t0.Add(4*day), nil)
	if len(n.sinceLast) != 0 {
		t.Fatalf("returning entry announced again: %v", names(n.sinceLast))
	}

	// after freshDays they leave the NEW tab
	n = noteCatalog(env, testPorts("a", "b", "c", "d"), t0.Add(day+(freshDays+1)*day), nil)
	if len(n.firstSeen) != 0 {
		t.Fatalf("still fresh after %d days: %d", freshDays, len(n.firstSeen))
	}
}

func TestWhatsNewKeysByRepoThenName(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	t0 := time.Now()
	noteCatalog(env, []*Port{{Name: "Old Name", Repo: "x/proj"}, {Name: "No Repo Thing"}}, t0, nil)
	// renamed in the catalog but same repo: not new; same name without repo: not new
	n := noteCatalog(env, []*Port{{Name: "New Name", Repo: "X/Proj"}, {Name: "no-repo thing"}}, t0.Add(time.Hour), nil)
	if len(n.sinceLast) != 0 {
		t.Fatalf("matched entries announced: %v", names(n.sinceLast))
	}
}

// Upgrading from a Mixtape without this feature: the catalog copy saved on the
// last visit stands in for seen.json, so the first visit already shows news.
func TestWhatsNewAfterUpgradeUsesLastCatalog(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	os.WriteFile(env.DataDir+"/ports.json", []byte(`{"ports":[{"name":"a","upstream":"https://github.com/someone/a"},{"name":"b","upstream":"https://github.com/someone/b"}]}`), 0o644)
	prev := previousCatalog(env)
	if len(prev) != 2 {
		t.Fatalf("previous catalog keys = %v", prev)
	}
	n := noteCatalog(env, testPorts("a", "b", "c"), time.Now(), prev)
	if got := names(n.sinceLast); len(got) != 1 || got[0] != "c" {
		t.Fatalf("since last visit = %v, want [c]", got)
	}
	// with no saved copy at all it's a quiet first run
	env2 := &Env{DataDir: t.TempDir()}
	if n := noteCatalog(env2, testPorts("a", "b", "c"), time.Now(), previousCatalog(env2)); len(n.sinceLast) != 0 {
		t.Fatalf("no history but announced %v", names(n.sinceLast))
	}
}

func TestAgeLabel(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{{time.Hour, "TODAY"}, {30 * time.Hour, "YESTERDAY"}, {5 * 24 * time.Hour, "5 DAYS AGO"}} {
		if got := ageLabel(now.Add(-c.ago).Unix(), now); got != c.want {
			t.Errorf("%v ago: %q, want %q", c.ago, got, c.want)
		}
	}
}

// The window opens on a later visit, A opens the selected tape from the NEW
// tab, and B closes it.
func TestWhatsNewWindow(t *testing.T) {
	fx := newFixture(t)
	env := fx.env
	env.Offline = true
	c := NewCanvas(640, 480)

	start := func() *UI {
		u := NewUI(env, 640, 480)
		u.Start()
		deadline := time.Now().Add(2 * time.Second)
		for u.scr != scrList && time.Now().Before(deadline) {
			select {
			case f := <-u.post:
				f()
			case <-time.After(10 * time.Millisecond):
			}
		}
		if u.scr != scrList {
			t.Fatal("did not reach list")
		}
		u.Draw(c)
		return u
	}

	u := start() // first visit: quiet
	if u.modal == modalWhatsNew || u.tabsShown() != tabNew {
		t.Fatalf("first visit: modal=%v tabs=%d", u.modal, u.tabsShown())
	}

	// pretend two catalog entries weren't there last time
	store, _ := loadSeen(env)
	var gone []string
	for i, p := range u.cat.Ports {
		if i == 3 || i == 7 {
			delete(store.Ports, portKey(p))
			gone = append(gone, p.Name)
		}
	}
	b, _ := json.Marshal(store)
	os.WriteFile(seenPath(env), b, 0o644)

	u = start()
	if u.modal != modalWhatsNew || len(u.arrivals) != 2 {
		t.Fatalf("second visit: modal=%v arrivals=%v (want %v)", u.modal, names(u.arrivals), gone)
	}
	if u.tabsShown() != len(tabNames) || u.tabCount(tabNew) != 2 {
		t.Fatalf("NEW tab: shown=%d count=%d", u.tabsShown(), u.tabCount(tabNew))
	}
	u.Key(BtnDown)
	u.Draw(c)
	want := u.arrivals[1]
	u.Key(BtnA)
	if u.modal != modalNone || u.scr != scrDetail || u.cur != want || u.tab != tabNew {
		t.Fatalf("A: modal=%v scr=%v cur=%v tab=%d", u.modal, u.scr, u.cur, u.tab)
	}
	u.Draw(c)
	u.Key(BtnB) // back to the list, on the NEW tab
	if u.scr != scrList || len(u.visible) != 2 {
		t.Fatalf("list after detail: scr=%v visible=%d", u.scr, len(u.visible))
	}
	u.Draw(c)

	u = start() // third visit: no window, NEW tab still there
	if u.modal == modalWhatsNew || u.tabCount(tabNew) != 2 {
		t.Fatalf("third visit: modal=%v new=%d", u.modal, u.tabCount(tabNew))
	}
	u.modal = modalWhatsNew // B closes
	u.Key(BtnB)
	if u.modal != modalNone {
		t.Fatal("B did not close the window")
	}
}
