package main

import (
	"testing"
	"time"
)

func TestUpdatesAnnouncedOnce(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	a, b := &Port{Name: "Diddy Kong Racing"}, &Port{Name: "GoldenEye 007"}
	ports := []*Port{b, a}
	got := newUpdates(env, ports, map[string]string{a.Name: "1.0.2", b.Name: "1.0.2"})
	if len(got) != 2 || got[0] != a {
		t.Fatalf("first open: %v", names(got))
	}
	if got := newUpdates(env, ports, map[string]string{a.Name: "1.0.2", b.Name: "1.0.2"}); len(got) != 0 {
		t.Fatalf("announced twice: %v", names(got))
	}
	if got := newUpdates(env, ports, map[string]string{a.Name: "1.0.3"}); len(got) != 1 || got[0] != a {
		t.Fatalf("a newer version isn't announced: %v", names(got))
	}
}

// The window waits for NEW ARRIVALS to close, and opens items from ON CARD.
func TestUpdatesWindowWaitsForArrivals(t *testing.T) {
	u := NewUI(&Env{DataDir: t.TempDir()}, 640, 480)
	p, q := &Port{Name: "Diddy Kong Racing"}, &Port{Name: "New Thing"}
	u.cat = &Catalog{Ports: []*Port{p, q}, Source: "live", Fetched: time.Now()}
	u.scr = scrList
	u.manifests[p.Name] = &Manifest{Name: p.Name, Version: "1.0.0"}
	u.updates[p.Name] = "1.0.2"
	u.arrivals = []*Port{q}
	u.wnList, u.modal = u.arrivals, modalWhatsNew

	u.pendingUpd = []*Port{p}
	u.maybeShowUpdates()
	if u.wnUpdates {
		t.Fatal("updates window replaced NEW ARRIVALS")
	}
	u.Key(BtnB) // close NEW ARRIVALS
	if u.modal != modalWhatsNew || !u.wnUpdates || len(u.wnList) != 1 || u.wnList[0] != p {
		t.Fatalf("updates window not shown after arrivals closed: modal=%v updates=%v", u.modal, u.wnUpdates)
	}
	u.Key(BtnA)
	if u.scr != scrDetail || u.cur != p || u.tab != tabOnCard {
		t.Fatalf("A didn't open the update: scr=%v tab=%v", u.scr, u.tab)
	}
	if u.modal != modalNone {
		t.Fatal("window reopened")
	}
}
