package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func themeFixture(t *testing.T) *Env {
	root := t.TempDir()
	sd := filepath.Join(root, "SDCARD")
	app := filepath.Join(sd, "App", "Mixtape")
	bt := filepath.Join(app, "theme", "Mixtape")
	for rel, body := range map[string]string{"config.json": `{"name":"Mixtape"}`, "skin/background.png": "bg", "skin/extra/bootScreen.png": "boot", "preview.png": "pv"} {
		os.MkdirAll(filepath.Dir(filepath.Join(bt, rel)), 0o755)
		os.WriteFile(filepath.Join(bt, rel), []byte(body), 0o644)
	}
	os.MkdirAll(filepath.Join(sd, "Themes", "Other"), 0o755)
	return &Env{SDRoot: sd, AppDir: app, DataDir: filepath.Join(root, "data"), SystemConfig: filepath.Join(root, "system.json"), dirCache: map[string][]string{}}
}

func TestThemeInstallUpdateRemove(t *testing.T) {
	env := themeFixture(t)
	st := env.ThemeState()
	if !st.Bundled || st.Installed() {
		t.Fatalf("fresh: %+v", st)
	}
	n, err := env.InstallTheme()
	if err != nil || n != 4 {
		t.Fatalf("install: %d %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(env.SDRoot, "Themes/Mixtape/skin/extra/bootScreen.png")); string(b) != "boot" {
		t.Fatal("not copied")
	}
	if st = env.ThemeState(); st.Version != version || st.Outdated() {
		t.Fatalf("after install: %+v", st)
	}

	// An older Mixtape installed it, and this one drops a file: updating
	// replaces the rest and removes that file.
	m := env.LoadManifest(themePort)
	m.Version = "0.0.1"
	m.Files = append(m.Files, FileRec{P: "Themes/Mixtape/skin/old.png"})
	b, _ := json.Marshal(m)
	os.WriteFile(env.manifestPath(themePort), b, 0o644)
	os.WriteFile(filepath.Join(env.SDRoot, "Themes/Mixtape/skin/old.png"), []byte("x"), 0o644)
	if !env.ThemeState().Outdated() {
		t.Fatal("not outdated")
	}
	if _, err := env.InstallTheme(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Themes/Mixtape/skin/old.png")); err == nil {
		t.Fatal("old file kept")
	}

	// In use: not removed.
	os.WriteFile(env.SystemConfig, []byte(`{"vol":10,"theme":"/mnt/SDCARD/Themes/Mixtape/"}`), 0o644)
	if !env.ThemeState().Active {
		t.Fatal("not seen as active")
	}
	if err := env.RemoveTheme(); err == nil {
		t.Fatal("removed the theme in use")
	}
	os.WriteFile(env.SystemConfig, []byte(`{"theme":"/mnt/SDCARD/Themes/Other/"}`), 0o644)
	if err := env.RemoveTheme(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Themes/Mixtape")); err == nil {
		t.Fatal("Themes/Mixtape left behind")
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Themes/Other")); err != nil {
		t.Fatal("another theme was touched")
	}
	if env.ThemeState().Installed() {
		t.Fatal("still installed")
	}

	// Copied by hand: shown as installed, offered as an update, not removable.
	os.MkdirAll(filepath.Join(env.SDRoot, "Themes/Mixtape"), 0o755)
	os.WriteFile(filepath.Join(env.SDRoot, "Themes/Mixtape/config.json"), []byte("{}"), 0o644)
	if st = env.ThemeState(); !st.Manual || !st.Outdated() || st.Version != "" {
		t.Fatalf("manual: %+v", st)
	}
}

func TestSortHB(t *testing.T) {
	mk := func(slug, title string, added, rel int64) *HBEntry {
		return &HBEntry{Slug: slug, Title: title, Added: added, Released: rel}
	}
	newest := []*HBEntry{mk("c", "Charlie", 30, 0), mk("a", "alpha", 20, 100), mk("b", "\"Bravo\"", 10, 200)}
	order := func(l []*HBEntry) string {
		s := ""
		for _, e := range l {
			s += e.Slug
		}
		return s
	}
	l := append([]*HBEntry{}, newest...)
	sortHB(l, "A–Z", 0)
	if order(l) != "abc" {
		t.Fatalf("A–Z: %s", order(l))
	}
	l = append([]*HBEntry{}, newest...)
	sortHB(l, "RELEASED", 0)
	if order(l) != "bac" {
		t.Fatalf("released: %s", order(l))
	}
	l1, l2 := append([]*HBEntry{}, newest...), append([]*HBEntry{}, newest...)
	sortHB(l1, "SHUFFLE", 42)
	sortHB(l2, "SHUFFLE", 42)
	if order(l1) != order(l2) {
		t.Fatal("shuffle not stable for one seed")
	}
}
