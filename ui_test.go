package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Drives the UI through a whole session with a fake GitHub, rendering every step.
func TestUISession(t *testing.T) {
	fx := newFixture(t)
	zipData := mkzip(t, map[string]string{"PocketFlex/config.json": "{}", "PocketFlex/launch.sh": "x"})
	fx.files["/dl/p.zip"] = zipData
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "PocketFlex") {
			w.Write([]byte(`[{"tag_name":"v0.4.3","assets":[{"name":"PocketFlex-v0.4.3.zip","browser_download_url":"` + fx.srv.URL + `/dl/p.zip","size":100}]}]`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer api.Close()
	env := fx.env
	env.APIBase = api.URL
	env.Offline = true
	for _, size := range [][2]int{{640, 480}, {752, 560}} {
		u := NewUI(env, size[0], size[1])
		c := NewCanvas(size[0], size[1])
		u.Start()
		pump := func(d time.Duration) {
			deadline := time.Now().Add(d)
			for time.Now().Before(deadline) {
				select {
				case f := <-u.post:
					f()
				case <-time.After(20 * time.Millisecond):
				}
				u.frame++
				u.Draw(c)
			}
		}
		pump(300 * time.Millisecond)
		if u.scr != scrList {
			t.Fatal("did not reach list")
		}
		for _, k := range []int{BtnUp, BtnUp, BtnDown, BtnLeft, BtnRight, BtnR1, BtnR1, BtnR1, BtnR1, BtnL1, BtnL1, BtnL2, BtnR2} {
			u.Key(k)
			u.Draw(c)
		}
		// jump to PocketFlex in ALL
		u.tab = 0
		u.refilter()
		for i, p := range u.visible {
			if p.Name == "PocketFlex" {
				u.sel[0] = i
			}
		}
		u.Key(BtnA)
		pump(300 * time.Millisecond)
		if u.plans["PocketFlex"] == nil {
			t.Fatalf("no plan: %v", u.planErr)
		}
		u.Key(BtnA) // install
		pump(800 * time.Millisecond)
		if u.modal != modalMsg || u.msgTitle != "TRACK LOADED" {
			t.Fatalf("modal=%v title=%q body=%v", u.modal, u.msgTitle, u.msgBody)
		}
		u.Key(BtnA)
		if u.manifests["PocketFlex"] == nil {
			t.Fatal("manifest not picked up")
		}
		for _, k := range []int{BtnDown, BtnDown, BtnUp, BtnR1, BtnL1} {
			u.Key(k)
			pump(30 * time.Millisecond)
		}
		// back to PocketFlex, erase it
		u.openDetail(u.cat.Ports[func() int {
			for i, p := range u.cat.Ports {
				if p.Name == "PocketFlex" {
					return i
				}
			}
			return 0
		}()])
		u.Key(BtnX)
		if u.modal != modalConfirm {
			t.Fatal("no confirm")
		}
		u.Draw(c)
		u.Key(BtnA)
		if u.msgTitle != "ERASED" || fx.exists("App/PocketFlex") {
			t.Fatalf("erase failed: %q %v", u.msgTitle, u.msgBody)
		}
		u.Key(BtnA)
		u.Key(BtnB)
		u.tab = 3
		u.refilter()
		u.Draw(c)
		u.Key(BtnY) // update check with nothing installed
		u.Draw(c)
		u.Key(BtnB)
		if !u.quit {
			t.Fatal("B on list should quit")
		}
	}
}
