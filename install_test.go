package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mkzip(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	zw.Close()
	return buf.Bytes()
}

func mktgz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type fixture struct {
	env   *Env
	srv   *httptest.Server
	files map[string][]byte
}

func newFixture(t *testing.T) *fixture {
	root := t.TempDir()
	sd := filepath.Join(root, "SDCARD")
	os.MkdirAll(filepath.Join(sd, "App"), 0o755)
	os.MkdirAll(filepath.Join(sd, "Roms", "PORTS", "Games"), 0o755)
	os.MkdirAll(filepath.Join(sd, "Emu", "PORTS"), 0o755)
	fx := &fixture{files: map[string][]byte{}}
	fx.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := fx.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(fx.srv.Close)
	fx.env = &Env{SDRoot: sd, DataDir: filepath.Join(root, "data"), HTTP: &http.Client{Transport: http.DefaultTransport}, dirCache: map[string][]string{}}
	return fx
}

func (fx *fixture) install(t *testing.T, p *Port, name string, data []byte, version string, snapshot bool) (*Result, error) {
	fx.files["/"+name] = data
	plan := &Plan{Version: version, Snapshot: snapshot, Files: []Asset{{Name: name, URL: fx.srv.URL + "/" + name, Size: int64(len(data))}}}
	return fx.env.Install(context.Background(), p, plan, func(Progress) {})
}

func (fx *fixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(fx.env.SDRoot, rel))
	return err == nil
}

func (fx *fixture) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(fx.env.SDRoot, rel))
	return string(b)
}

func TestLayouts(t *testing.T) {
	cases := []struct {
		name     string
		port     *Port
		files    map[string]string
		snapshot bool
		want     []string // paths that must exist afterwards
		notWant  []string
		where    string
	}{
		{"sdroot (PocketFeeds)", &Port{Name: "PocketFeeds"},
			map[string]string{"App/PocketFeeds/launch.sh": "x", "App/PocketFeeds/config.json": "{}", "App/PocketFeeds/pocketfeeds": "bin", "README.md": "r"},
			false, []string{"App/PocketFeeds/pocketfeeds"}, []string{"README.md"}, "App/PocketFeeds"},
		{"folder (PocketFlex)", &Port{Name: "PocketFlex"},
			map[string]string{"PocketFlex/config.json": "{}", "PocketFlex/launch.sh": "x", "PocketFlex/lib/libfoo.so": "so"},
			false, []string{"App/PocketFlex/lib/libfoo.so", "App/PocketFlex/config.json"}, nil, "App/PocketFlex"},
		{"flat repo snapshot (CPU Overclock)", &Port{Name: "CPU Overclock", Recipe: &Recipe{AppDir: "CpuOverclock"}},
			map[string]string{"Cpu_Overclock_Onion_OS-main/config.json": "{}", "Cpu_Overclock_Onion_OS-main/launch.sh": "x", "Cpu_Overclock_Onion_OS-main/app.py": "py", "Cpu_Overclock_Onion_OS-main/README.md": "r"},
			true, []string{"App/CpuOverclock/app.py"}, nil, "App/CpuOverclock"},
		{"flat snapshot without recipe uses port name", &Port{Name: "Time Quick Fix!"},
			map[string]string{"tqf-main/config.json": "{}", "tqf-main/launch.sh": "x"},
			true, []string{"App/TimeQuickFix/launch.sh"}, nil, "App/TimeQuickFix"},
		{"repo snapshot with App/ (Cloud Saves)", &Port{Name: "Cloud Saves"},
			map[string]string{"cloud-saves-main/App/CloudUpload/launch.sh": "x", "cloud-saves-main/App/CloudDownload/launch.sh": "y", "cloud-saves-main/README.md": "r"},
			true, []string{"App/CloudUpload/launch.sh", "App/CloudDownload/launch.sh"}, []string{"README.md"}, "App/CloudDownload"},
		{"ports-relative (Insaniquarium)", &Port{Name: "Insaniquarium"},
			map[string]string{"Insaniquarium.port": "p", "Games/Insaniquarium/game": "bin", "README.txt": "r"},
			false, []string{"Roms/PORTS/Insaniquarium.port", "Roms/PORTS/Games/Insaniquarium/game"}, []string{"Roms/PORTS/README.txt"}, "Roms/PORTS"},
		{"wrapped sdroot (Captain Claw)", &Port{Name: "Captain Claw"},
			map[string]string{"MiyooMiniPackage/Roms/PORTS/Games/Captain Claw/claw": "bin", "MiyooMiniPackage/Roms/PORTS/Shortcuts/Action/Captain Claw.port": "p"},
			false, []string{"Roms/PORTS/Games/Captain Claw/claw", "Roms/PORTS/Shortcuts/Action/Captain Claw.port"}, nil, "Roms/PORTS/Games/Captain Claw"},
		{"two app folders (moonlight style repo)", &Port{Name: "Moonlight"},
			map[string]string{"moonlight-app-miyoo-main/moonlight/config.json": "{}", "moonlight-app-miyoo-main/moonlight/bin/moonlight": "b", "moonlight-app-miyoo-main/README.md": "r", "moonlight-app-miyoo-main/.gitattributes": "g"},
			true, []string{"App/moonlight/bin/moonlight"}, nil, "App/moonlight"},
		{"guide-watch startup hook", &Port{Name: "guide-watch"},
			map[string]string{"App/GuideWatch/launch.sh": "x", ".tmp_update/startup/guidewatch.sh": "s"},
			false, []string{"App/GuideWatch/launch.sh", ".tmp_update/startup/guidewatch.sh"}, nil, "App/GuideWatch"},
		{"lowercase roots + macOS junk", &Port{Name: "Petals"},
			map[string]string{"app/Petals/launch.sh": "x", "__MACOSX/app/._launch.sh": "junk", "app/Petals/.DS_Store": "j"},
			false, []string{"App/Petals/launch.sh"}, []string{"__MACOSX", "App/Petals/.DS_Store"}, "App/Petals"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			res, err := fx.install(t, tc.port, "a.zip", mkzip(t, tc.files), "v1", tc.snapshot)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			for _, w := range tc.want {
				if !fx.exists(w) {
					t.Errorf("missing %s", w)
				}
			}
			for _, w := range tc.notWant {
				if fx.exists(w) {
					t.Errorf("unexpected %s", w)
				}
			}
			if !contains(res.Where, tc.where) {
				t.Errorf("where = %v, want to include %s", res.Where, tc.where)
			}
		})
	}
}

func TestSystemFilesProtected(t *testing.T) {
	fx := newFixture(t)
	os.MkdirAll(filepath.Join(fx.env.SDRoot, ".tmp_update", "bin"), 0o755)
	os.WriteFile(filepath.Join(fx.env.SDRoot, ".tmp_update", "bin", "prompt"), []byte("onion"), 0o755)
	res, err := fx.install(t, &Port{Name: "Sneaky"}, "s.zip", mkzip(t, map[string]string{
		"App/Sneaky/launch.sh":      "x",
		".tmp_update/bin/prompt":    "evil",
		".tmp_update/lib/newlib.so": "new",
	}), "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if fx.read(".tmp_update/bin/prompt") != "onion" {
		t.Fatal("overwrote an Onion system file")
	}
	if !fx.exists(".tmp_update/lib/newlib.so") {
		t.Error("new (non-conflicting) file should be allowed")
	}
	if len(res.Skipped) != 1 {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestZipSlip(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.install(t, &Port{Name: "Evil"}, "e.zip", mkzip(t, map[string]string{
		"App/Evil/launch.sh":      "x",
		"App/../../../escape.txt": "boom",
	}), "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(fx.env.SDRoot), "escape.txt")); err == nil {
		t.Fatal("wrote outside the SD card")
	}
}

func TestUpdateKeepsUserEditsAndRemovesStaleFiles(t *testing.T) {
	fx := newFixture(t)
	p := &Port{Name: "PocketFlex", Repo: "jackharvest/PocketFlex", Recipe: &Recipe{Keep: []string{"server.cfg"}}}
	v1 := map[string]string{"PocketFlex/config.json": "{}", "PocketFlex/launch.sh": "v1", "PocketFlex/settings.json": "default", "PocketFlex/old.bin": "old", "PocketFlex/server.cfg": "blank"}
	if _, err := fx.install(t, p, "v1.zip", mkzip(t, v1), "v1", false); err != nil {
		t.Fatal(err)
	}
	// user edits settings (later mtime) and server.cfg
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(filepath.Join(fx.env.SDRoot, "App/PocketFlex/settings.json"), []byte("my settings!"), 0o644)
	os.WriteFile(filepath.Join(fx.env.SDRoot, "App/PocketFlex/server.cfg"), []byte("my server"), 0o644)
	v2 := map[string]string{"PocketFlex/config.json": "{}", "PocketFlex/launch.sh": "v2", "PocketFlex/settings.json": "new default", "PocketFlex/server.cfg": "blank"}
	res, err := fx.install(t, p, "v2.zip", mkzip(t, v2), "v2", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fx.read("App/PocketFlex/launch.sh"); got != "v2" {
		t.Errorf("launch.sh = %q", got)
	}
	if got := fx.read("App/PocketFlex/settings.json"); got != "my settings!" {
		t.Errorf("user-edited settings overwritten: %q", got)
	}
	if got := fx.read("App/PocketFlex/server.cfg"); got != "my server" {
		t.Errorf("keep-glob file overwritten: %q", got)
	}
	if fx.exists("App/PocketFlex/old.bin") {
		t.Error("stale file from v1 not removed")
	}
	if len(res.Kept) != 2 {
		t.Errorf("kept = %v", res.Kept)
	}
	if m := fx.env.LoadManifest(p); m == nil || m.Version != "v2" {
		t.Fatalf("manifest = %+v", m)
	}
	// A third update must still recognise settings.json as user-edited.
	res, err = fx.install(t, p, "v3.zip", mkzip(t, v2), "v3", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fx.read("App/PocketFlex/settings.json"); got != "my settings!" {
		t.Errorf("third update clobbered settings: %q", got)
	}
}

func TestUninstall(t *testing.T) {
	fx := newFixture(t)
	p := &Port{Name: "Tennis vs Zombies"}
	_, err := fx.install(t, p, "t.zip", mkzip(t, map[string]string{
		"Roms/PORTS/Games/TvZ/game":            "bin",
		"Roms/PORTS/Shortcuts/Action/TvZ.port": "p",
		"Roms/PORTS/Games/TvZ/data/level1.dat": "d",
	}), "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(fx.env.SDRoot, "Roms/PORTS/PORTS_cache6.db"), []byte("cache"), 0o644)
	n, err := fx.env.Uninstall(p)
	if err != nil || n != 3 {
		t.Fatalf("uninstall n=%d err=%v", n, err)
	}
	if fx.exists("Roms/PORTS/Games/TvZ") {
		t.Error("game folder not pruned")
	}
	if !fx.exists("Roms/PORTS/Games") || !fx.exists("Roms/PORTS") {
		t.Error("pruned a protected folder")
	}
	if fx.exists("Roms/PORTS/PORTS_cache6.db") {
		t.Error("ports cache not refreshed")
	}
	if _, err := fx.env.Uninstall(p); err == nil {
		t.Error("second uninstall should refuse (no manifest)")
	}
}

func TestTarGz(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.install(t, &Port{Name: "wthr"}, "wthr.tar.gz", mktgz(map[string]string{"Wthr/config.json": "{}", "Wthr/launch.sh": "x", "Wthr/wthr": "bin"}), "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if fx.read("App/Wthr/wthr") != "bin" {
		t.Error("tar.gz not extracted")
	}
}

func TestUnknownLayoutRefused(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.install(t, &Port{Name: "Mystery"}, "m.zip", mkzip(t, map[string]string{"a/x": "1", "b/y": "2", "notes.bin": "3"}), "v1", false)
	if err == nil || !strings.Contains(err.Error(), "layout") {
		t.Fatalf("err = %v", err)
	}
}

func TestPickAssets(t *testing.T) {
	names := func(as []Asset) string {
		var s []string
		for _, a := range as {
			s = append(s, a.Name)
		}
		return strings.Join(s, ",")
	}
	rel := func(ns ...string) Release {
		r := Release{Tag: "v1"}
		for _, n := range ns {
			r.Assets = append(r.Assets, Asset{Name: n})
		}
		return r
	}
	cases := []struct {
		rel    Release
		recipe *Recipe
		want   string
	}{
		{rel("retsurf-portmaster.zip", "retsurf-onionos.zip", "retsurf-allium.zip", "retsurf-linux-x86_64.zip", "retsurf-windows-x86_64.zip"), nil, "retsurf-onionos.zip"},
		{rel("minesofmoria-portmaster.zip", "minesofmoria-onionos-port.zip", "minesofmoria-onionos-app.zip"), &Recipe{Asset: "onionos-app"}, "minesofmoria-onionos-app.zip"},
		{rel("PocketStream-OnionOS-0.3.zip", "PocketStream-OnionOS-0.3.zip.sha256"), nil, "PocketStream-OnionOS-0.3.zip"},
		{rel("PocketFlex-v0.4.3.zip"), nil, "PocketFlex-v0.4.3.zip"},
		{rel("Blockdude.-.Miyoo_Mini_Onion_Os.zip", "Blips.-.Miyoo_Mini_Onion_Os.zip", "Blockdude.-.Funkey.zip"), &Recipe{Asset: "onion", All: true}, "Blockdude.-.Miyoo_Mini_Onion_Os.zip,Blips.-.Miyoo_Mini_Onion_Os.zip"},
		{rel("Source code.zip", "game-trimui.zip"), nil, ""},
	}
	for _, c := range cases {
		got, _ := PickAssets(c.rel, c.recipe)
		if names(got) != c.want {
			t.Errorf("%v → %q, want %q", c.rel.Assets, names(got), c.want)
		}
	}
	if _, problem := PickAssets(rel("OpenRCT2mini-1.0.7z"), nil); !strings.Contains(problem, ".7z") {
		t.Errorf("7z problem = %q", problem)
	}
}

func TestEmbeddedCatalogParses(t *testing.T) {
	ports, err := parseCatalog(embeddedPorts, loadRecipes(t.TempDir()))
	if err != nil || len(ports) < 40 {
		t.Fatalf("ports=%d err=%v", len(ports), err)
	}
	var flex *Port
	for _, p := range ports {
		if p.Name == "PocketFlex" {
			flex = p
		}
		if p.Repo == "" {
			t.Errorf("%s has no GitHub repo", p.Name)
		}
	}
	if flex == nil || flex.Repo != "jackharvest/PocketFlex" || flex.Recipe == nil {
		t.Fatalf("PocketFlex entry: %+v", flex)
	}
}

func TestPlanAndInstallViaFakeGitHub(t *testing.T) {
	fx := newFixture(t)
	zipData := mkzip(t, map[string]string{"PocketFlex/config.json": "{}", "PocketFlex/launch.sh": "x"})
	fx.files["/dl/PocketFlex-v0.4.3.zip"] = zipData
	fx.files["/dl/PocketFlex-trimui.zip"] = zipData
	var rateLimited bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1900000000")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/repos/jackharvest/PocketFlex/releases":
			w.Write([]byte(`[{"tag_name":"v0.5.0-draft","draft":true,"assets":[]},
			{"tag_name":"v0.4.3","published_at":"2026-09-12T00:00:00Z","assets":[
			{"name":"PocketFlex-trimui.zip","browser_download_url":"` + fx.srv.URL + `/dl/PocketFlex-trimui.zip","size":10},
			{"name":"PocketFlex-v0.4.3.zip","browser_download_url":"` + fx.srv.URL + `/dl/PocketFlex-v0.4.3.zip","size":` + strconv.Itoa(len(zipData)) + `}]}]`))
		case "/repos/hotcereal/time-quick-fix/releases":
			w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	fx.env.APIBase = api.URL
	p := &Port{Name: "PocketFlex", Repo: "jackharvest/PocketFlex"}
	plan, err := fx.env.PlanFor(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v0.4.3" || len(plan.Files) != 1 || plan.Files[0].Name != "PocketFlex-v0.4.3.zip" {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := fx.env.Install(context.Background(), p, plan, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	if !fx.exists("App/PocketFlex/launch.sh") {
		t.Fatal("not installed")
	}
	// No releases → source snapshot plan.
	snap, err := fx.env.PlanFor(&Port{Name: "Time Quick Fix", Repo: "hotcereal/time-quick-fix"}, false)
	if err != nil || !snap.Snapshot || !strings.HasSuffix(snap.Files[0].URL, "/archive/HEAD.zip") {
		t.Fatalf("snapshot plan = %+v err=%v", snap, err)
	}
	// Rate limiting: cached result is still served; forced refresh falls back to stale cache.
	rateLimited = true
	if _, err := fx.env.PlanFor(p, true); err != nil {
		t.Fatalf("stale cache fallback failed: %v", err)
	}
	_, err = fx.env.PlanFor(&Port{Name: "Other", Repo: "someone/else"}, true)
	if _, ok := err.(*RateLimitError); !ok {
		t.Fatalf("want RateLimitError, got %v", err)
	}
	// Manual recipe short-circuits the network.
	pl, _ := fx.env.PlanFor(&Port{Name: "OpenRCT2", Repo: "x/y", Recipe: &Recipe{Manual: "7z"}}, false)
	if pl.Problem != "7z" {
		t.Fatalf("manual plan = %+v", pl)
	}
}

func TestWindowsZipPaths(t *testing.T) {
	fx := newFixture(t)
	os.WriteFile(filepath.Join(fx.env.SDRoot, "Roms/PORTS/keep.txt"), []byte("mine"), 0o644)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{`licenses\GPL.txt`, `Roms\PORTS\`, `Roms\PORTS\Games\`, `Roms\PORTS\Games\Half-Life\logs\`,
		`Roms\PORTS\Games\Half-Life\launch.sh`, `Roms\PORTS\Shortcuts\Ports\Half-Life.notfound`, `README.md`} {
		w, _ := zw.Create(n)
		if !strings.HasSuffix(n, `\`) {
			w.Write([]byte("x"))
		}
	}
	zw.Close()
	res, err := fx.install(t, &Port{Name: "Half-Life"}, "hl.zip", buf.Bytes(), "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Roms/PORTS/Games/Half-Life/launch.sh", "Roms/PORTS/Shortcuts/Ports/Half-Life.notfound", "Roms/PORTS/keep.txt"} {
		if !fx.exists(p) {
			t.Errorf("missing %s", p)
		}
	}
	if fx.exists("licenses") || fx.exists("README.md") {
		t.Error("copied non-SD files")
	}
	if !contains(res.Where, "Roms/PORTS/Games/Half-Life") {
		t.Errorf("where = %v", res.Where)
	}
}

func TestFileNeverReplacesFolder(t *testing.T) {
	fx := newFixture(t)
	os.MkdirAll(filepath.Join(fx.env.SDRoot, "App/Thing/data"), 0o755)
	os.WriteFile(filepath.Join(fx.env.SDRoot, "App/Thing/data/save"), []byte("s"), 0o644)
	_, err := fx.install(t, &Port{Name: "Thing"}, "t.zip", mkzip(t, map[string]string{"App/Thing/launch.sh": "x", "App/Thing/data": "not a folder"}), "v1", false)
	if err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("err = %v", err)
	}
	if fx.read("App/Thing/data/save") != "s" {
		t.Fatal("folder contents damaged")
	}
}
