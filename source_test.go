package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSource = `{
  "name": "Miyoo64",
  "homepage": "https://miyoo-n64.pages.dev",
  "ports": [
    {"name": "Mario Kart 64", "version": "1.0.0", "url": "https://miyoo-n64.pages.dev/dl/MarioKart64-miyoo-1.0.0.zip",
     "sha256": "C2A7", "size": 1367535, "categories": ["racing"], "status": "playable", "assets": "owned",
     "porter": ["mr-finster"], "upstream": "https://github.com/mr-finster/miyoo64/tree/main/mario-kart-64"},
    {"name": "Paper Mario", "version": "1.0.0", "url": "https://miyoo-n64.pages.dev/dl/PaperMario-miyoo-1.0.0.zip",
     "categories": ["rpg"], "upstream": "https://github.com/mr-finster/miyoo64/tree/main/paper-mario",
     "keep": ["App/PaperMario/*.cfg"]},
    {"name": "Plain HTTP", "version": "1", "url": "http://example.com/x.zip"}
  ]
}`

func TestSourceEntries(t *testing.T) {
	ports, name, err := parseSource([]byte(testSource), map[string]*Recipe{})
	if err != nil || name != "Miyoo64" {
		t.Fatalf("parse: %v %q", err, name)
	}
	if len(ports) != 2 {
		t.Fatalf("want 2 ports (plain-HTTP download dropped), got %d", len(ports))
	}
	for _, p := range ports {
		// Every game shares one source repo, so they must not be tied to it.
		if !p.Direct() || p.Repo != "" || p.Source != "Miyoo64" || p.ManualReason() != "" {
			t.Errorf("%s: direct=%v repo=%q source=%q manual=%q", p.Name, p.Direct(), p.Repo, p.Source, p.ManualReason())
		}
	}
	if ports[1].Recipe == nil || len(ports[1].Recipe.Keep) != 1 {
		t.Errorf("keep list not applied: %+v", ports[1].Recipe)
	}
	env := &Env{DataDir: t.TempDir()}
	plan, err := env.PlanFor(ports[0], false)
	if err != nil || plan.Problem != "" || plan.Version != "1.0.0" || len(plan.Files) != 1 {
		t.Fatalf("plan: %v %+v", err, plan)
	}
	if f := plan.Files[0]; f.Name != "MarioKart64-miyoo-1.0.0.zip" || f.Size != 1367535 || f.SHA256 != "c2a7" {
		t.Fatalf("plan file: %+v", f)
	}
}

func TestSourceMergeRenamesOtherPortsOfTheSameGame(t *testing.T) {
	load := func() []*Port {
		recipes := map[string]*Recipe{}
		main, _ := parseCatalog([]byte(`{"ports":[{"name":"Zulu","upstream":"https://github.com/z/zulu"}]}`), recipes)
		extra, _ := parseExtra([]byte(`{"ports":[{"name":"Mario Kart 64","upstream":"https://github.com/cacuracaptors/mk64-miyoomini/releases"}]}`), recipes)
		src, _, _ := parseSource([]byte(testSource), recipes)
		return mergeExtra(mergeExtra(main, extra), src)
	}
	got := load()
	names := map[string]*Port{}
	for _, p := range got {
		names[p.Name] = p
	}
	if len(got) != 4 || names["Mario Kart 64"] == nil || names["Mario Kart 64 (Miyoo64)"] == nil || names["Paper Mario"] == nil {
		var n []string
		for _, p := range got {
			n = append(n, p.Name)
		}
		t.Fatalf("merged names: %v", n)
	}
	if names["Mario Kart 64"].Repo != "cacuracaptors/mk64-miyoomini" || !names["Mario Kart 64 (Miyoo64)"].Direct() {
		t.Fatal("the two Mario Kart 64 ports got mixed up")
	}
	// Names are what install records are keyed by, so they must be stable.
	again := load()
	for i := range got {
		if got[i].Name != again[i].Name {
			t.Fatalf("names changed between loads: %q vs %q", got[i].Name, again[i].Name)
		}
	}
	if portKey(names["Paper Mario"]) == portKey(names["Mario Kart 64 (Miyoo64)"]) {
		t.Fatal("two games from one source share a what's-new key")
	}
}

func TestSourcesFetchedAndCached(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(testSource))
	}))
	defer srv.Close()
	env := &Env{DataDir: t.TempDir(), HTTP: srv.Client(), Config: Config{
		SourcesURL: srv.URL + "/sources.json", // answers with no published catalogs
		Sources:    []string{srv.URL + "/manifest.json", "http://insecure.example/m.json"}}}
	got := loadSources(env, map[string]*Recipe{})
	if len(got) != 1 || len(got[0]) != 2 || hits != 2 {
		t.Fatalf("sources: %d lists, hits %d", len(got), hits)
	}
	env.Offline = true
	got = loadSources(env, map[string]*Recipe{})
	if len(got) != 1 || len(got[0]) != 2 || got[0][0].Source != "Miyoo64" {
		t.Fatalf("offline: cached copy not used: %+v", got)
	}
}

func TestSourceListIsPublishedAndAdditive(t *testing.T) {
	list := `{"sources":[{"name":"A","url":"https://a.example/m.json"},{"name":"Bad","url":"http://b.example/m.json"}]}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(list))
	}))
	defer srv.Close()
	env := &Env{DataDir: t.TempDir(), HTTP: srv.Client(), Config: Config{
		SourcesURL: srv.URL + "/sources.json",
		Sources:    []string{"https://mine.example/m.json", "https://a.example/m.json"},
	}}
	want := "https://a.example/m.json https://mine.example/m.json"
	if got := strings.Join(env.sourceURLs(), " "); got != want {
		t.Fatalf("live list: %q, want %q", got, want)
	}
	// Offline: the copy saved on the card is used.
	env.Offline = true
	if got := strings.Join(env.sourceURLs(), " "); got != want {
		t.Fatalf("cached list: %q", got)
	}
	// Nothing saved and offline: the built-in list (Miyoo64) plus the card's own.
	fresh := &Env{DataDir: t.TempDir(), Offline: true, Config: Config{Sources: []string{"https://mine.example/m.json"}}}
	got := fresh.sourceURLs()
	if len(got) != 2 || !strings.Contains(got[0], "miyoo-n64.pages.dev") || got[1] != "https://mine.example/m.json" {
		t.Fatalf("built-in list: %v", got)
	}
}

func TestPublishedSourceListParses(t *testing.T) {
	urls, err := parseSourceList(embeddedSources)
	if err != nil || len(urls) == 0 {
		t.Fatalf("catalog/sources.json: %v %v", err, urls)
	}
}

// Updating extracts over the game's folder: saves, settings and built game
// data that aren't in the zip stay, and a bad download changes nothing.
func TestDirectInstallUpdateAndChecksum(t *testing.T) {
	fx := newFixture(t)
	v1 := mkzip(t, map[string]string{
		"App/MarioKart64/mk64":                    "exe v1",
		"App/MarioKart64/launch.sh":               "#!/bin/sh",
		"App/MarioKart64/config.json":             `{"label":"Mario Kart 64"}`,
		"Roms/PORTS/Shortcuts/Mario Kart 64.port": "port",
		"Emus/miyoomini/MK64.pak/launch.sh":       "minui only",
	})
	v2 := mkzip(t, map[string]string{
		"App/MarioKart64/mk64":                    "exe v2",
		"App/MarioKart64/launch.sh":               "#!/bin/sh",
		"App/MarioKart64/config.json":             `{"label":"Mario Kart 64"}`,
		"Roms/PORTS/Shortcuts/Mario Kart 64.port": "port",
	})
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	fx.files["/MK64-1.0.0.zip"] = v1
	fx.files["/MK64-1.1.0.zip"] = v2
	p := &Port{Name: "Mario Kart 64 (Miyoo64)", URL: fx.srv.URL + "/MK64-1.0.0.zip", Version: "1.0.0", SHA256: sum(v1)}
	install := func() error {
		plan, err := fx.env.PlanFor(p, false)
		if err != nil {
			return err
		}
		_, err = fx.env.Install(context.Background(), p, plan, func(Progress) {})
		return err
	}
	if err := install(); err != nil {
		t.Fatal(err)
	}
	if fx.read("App/MarioKart64/mk64") != "exe v1" || fx.exists("Emus") {
		t.Fatal("v1 not installed as expected")
	}
	// What the player and the game add to the folder.
	user := map[string]string{
		"App/MarioKart64/rom.z64":          "rom",
		"App/MarioKart64/saves/eeprom.sav": "save",
		"App/MarioKart64/states/1.st":      "state",
		"App/MarioKart64/data.bin":         "built from the ROM",
	}
	for rel, c := range user {
		abs := filepath.Join(fx.env.SDRoot, rel)
		os.MkdirAll(filepath.Dir(abs), 0o755)
		os.WriteFile(abs, []byte(c), 0o644)
	}
	// A corrupted download is refused before anything is touched.
	p.URL, p.Version, p.SHA256 = fx.srv.URL+"/MK64-1.1.0.zip", "1.1.0", sum(v1)
	if err := install(); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum mismatch not caught: %v", err)
	}
	if fx.read("App/MarioKart64/mk64") != "exe v1" {
		t.Fatal("a failed download changed files")
	}
	p.SHA256 = strings.ToUpper(sum(v2)) // case doesn't matter
	if err := install(); err != nil {
		t.Fatal(err)
	}
	if fx.read("App/MarioKart64/mk64") != "exe v2" {
		t.Fatal("not updated")
	}
	for rel, c := range user {
		if fx.read(rel) != c {
			t.Errorf("update touched %s", rel)
		}
	}
	if m := fx.env.LoadManifest(p); m == nil || m.Version != "1.1.0" {
		t.Fatalf("record: %+v", m)
	}
	// Erase removes only what Mixtape installed.
	if _, _, err := fx.env.Uninstall(p); err != nil {
		t.Fatal(err)
	}
	if fx.exists("App/MarioKart64/mk64") || fx.exists("Roms/PORTS/Shortcuts/Mario Kart 64.port") {
		t.Fatal("erase left installed files")
	}
	for rel, c := range user {
		if fx.read(rel) != c {
			t.Errorf("erase removed %s", rel)
		}
	}
}
