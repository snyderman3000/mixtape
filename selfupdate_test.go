package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.2", "0.1.1", true}, {"v0.1.1", "0.1.1", false}, {"v0.1.0", "0.1.1", false},
		{"v0.2.0", "0.1.9", true}, {"v1.0.0-beta", "0.9.9", true}, {"0.10.0", "0.9.0", true}, {"garbage", "0.1.1", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

// fakeSelf serves a Mixtape release list and the zip.
func fakeSelf(t *testing.T, fx *fixture, releasesJSON func(dl string) string) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+selfRepo+"/releases" {
			w.Write([]byte(releasesJSON(fx.srv.URL)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)
	fx.env.APIBase = api.URL
}

func TestSelfUpdatePlan(t *testing.T) {
	fx := newFixture(t)
	fakeSelf(t, fx, func(dl string) string {
		return `[{"tag_name":"v9.5.0","prerelease":true,"assets":[{"name":"Mixtape-v9.5.0-OnionOS.zip","browser_download_url":"x"}]},
		{"tag_name":"v9.1.0","assets":[{"name":"Mixtape-v9.1.0-OnionOS.zip","browser_download_url":"` + dl + `/m.zip","size":5}]}]`
	})
	plan, err := fx.env.SelfUpdatePlan(true)
	if err != nil || plan == nil || plan.Version != "v9.1.0" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	fx2 := newFixture(t)
	fakeSelf(t, fx2, func(string) string { return `[{"tag_name":"v` + version + `","assets":[]}]` })
	if plan, err := fx2.env.SelfUpdatePlan(true); plan != nil || err != nil {
		t.Fatalf("up-to-date: plan=%+v err=%v", plan, err)
	}
	fx3 := newFixture(t)
	fakeSelf(t, fx3, func(string) string { return `[{"tag_name":"v99.0.0","assets":[]}]` })
	if _, err := fx3.env.SelfUpdatePlan(true); err == nil {
		t.Fatal("expected error for release without zip")
	}
}

func TestSelfUpdateFlowThroughUI(t *testing.T) {
	fx := newFixture(t)
	appDir := filepath.Join(fx.env.SDRoot, "App", "Mixtape")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "mixtape"), []byte("old binary"), 0o755)
	os.WriteFile(filepath.Join(appDir, "data", "config.json"), []byte(`{"github_token":"mine"}`), 0o644)
	fx.env.AppDir = appDir
	fx.env.DataDir = filepath.Join(appDir, "data")
	fx.files["/m.zip"] = mkzip(t, map[string]string{
		"App/Mixtape/mixtape": "new binary", "App/Mixtape/launch.sh": "new launch", "App/Mixtape/config.json": "{}",
	})
	fakeSelf(t, fx, func(dl string) string {
		return `[{"tag_name":"v0.9.0","assets":[{"name":"Mixtape-v0.9.0-OnionOS.zip","browser_download_url":"` + dl + `/m.zip","size":10}]}]`
	})
	fx.env.Offline = true
	u := NewUI(fx.env, 640, 480)
	c := NewCanvas(640, 480)
	u.Start()
	pump := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			select {
			case f := <-u.post:
				f()
			case <-time.After(15 * time.Millisecond):
			}
			u.frame++
			u.Draw(c)
		}
	}
	pump(200 * time.Millisecond)
	u.Key(BtnSelect)
	pump(300 * time.Millisecond)
	if u.modal != modalConfirm {
		t.Fatalf("expected confirm, modal=%v toast=%q msg=%v", u.modal, u.toast, u.msgBody)
	}
	u.Key(BtnA)
	pump(600 * time.Millisecond)
	if u.msgTitle != "NEW TAPE LOADED" {
		t.Fatalf("title=%q body=%v", u.msgTitle, u.msgBody)
	}
	if b, _ := os.ReadFile(filepath.Join(appDir, "mixtape")); string(b) != "new binary" {
		t.Fatalf("binary = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(appDir, "data", "config.json")); string(b) != `{"github_token":"mine"}` {
		t.Fatalf("settings clobbered: %q", b)
	}
	u.Key(BtnA)
	if !u.quit {
		t.Fatal("should quit to restart")
	}
	if _, err := os.Stat(filepath.Join(appDir, "data", ".restart")); err != nil {
		t.Fatal("restart flag missing")
	}
}
