package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHH imitates Homebrew Hub's /search route: pages of 10, newest first by
// default (entries sharing a timestamp come back in a different order on every
// request, as the real API does), or by slug with order_by=slug.
type fakeHH struct {
	mu      sync.Mutex
	entries []map[string]any
	files   map[string][]byte // "/static/<database>/entries/<slug>/<file>"
	fail    int               // fail requests after this many (0 = never)
	calls   int
	rng     *rand.Rand
}

func newFakeHH() *fakeHH {
	return &fakeHH{files: map[string][]byte{}, rng: rand.New(rand.NewSource(1))}
}

func (f *fakeHH) add(slug, title, platform, file string, added time.Time, extra map[string]any) {
	e := map[string]any{"slug": slug, "title": title, "platform": platform, "typetag": "game",
		"developer": "Dev " + slug, "files": []map[string]any{{"default": true, "playable": true, "filename": file}},
		"screenshots": []string{"cover.png"}, "firstadded_date": added.UTC().Format(time.RFC3339), "basepath": "database-test"}
	for k, v := range extra {
		e[k] = v
	}
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
}

func (f *fakeHH) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail > 0 && f.calls > f.fail {
		http.Error(w, "boom", 500)
		return
	}
	if b, ok := f.files[r.URL.Path]; ok {
		w.Write(b)
		return
	}
	if r.URL.Path != "/search" {
		http.NotFound(w, r)
		return
	}
	list := append([]map[string]any{}, f.entries...)
	if r.URL.Query().Get("order_by") == "slug" {
		sort.Slice(list, func(i, j int) bool { return list[i]["slug"].(string) < list[j]["slug"].(string) })
	} else {
		f.rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		sort.SliceStable(list, func(i, j int) bool {
			return list[i]["firstadded_date"].(string) > list[j]["firstadded_date"].(string)
		})
	}
	total := (len(list) + 9) / 10
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > total {
		page = total
	}
	lo, hi := (page-1)*10, min(page*10, len(list))
	if lo > hi {
		lo = hi
	}
	json.NewEncoder(w).Encode(map[string]any{"results": len(list), "page_total": total, "page_current": page,
		"page_elements": 10, "sort": "firstadded_date", "order": "desc", "entries": list[lo:hi]})
}

func hhEnv(t *testing.T, f *fakeHH) (*Env, *httptest.Server) {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	root := t.TempDir()
	sd := filepath.Join(root, "SDCARD")
	for _, d := range []string{"Roms/GBA", "Roms/GB", "Emu/GBA", "Emu/GB"} {
		os.MkdirAll(filepath.Join(sd, d), 0o755)
	}
	env := &Env{SDRoot: sd, DataDir: filepath.Join(root, "data"), HTTP: &http.Client{Transport: http.DefaultTransport}, dirCache: map[string][]string{}}
	env.Config.HomebrewAPI = srv.URL
	return env, srv
}

func TestParseHHEntry(t *testing.T) {
	cases := []struct {
		json string
		ok   bool
		want func(*HBEntry) string
	}{
		{`{"slug":"a","title":"A","platform":"GBA","typetag":"game","developer":["X","Y"],"files":[{"default":true,"playable":true,"filename":"files/a.gba"}],"screenshots":["cover.png","s/1.png"]}`, true,
			func(e *HBEntry) string {
				return e.File + "|" + strings.Join(e.Devs, ",") + "|" + e.SysLabel() + "|" + strings.Join(e.Shots, ",")
			}},
		// no platform: by extension; .cgb is Game Boy Color
		{`{"slug":"b","title":"B","files":[{"default":true,"playable":true,"filename":"b.cgb"}],"developer":{"name":"Z"}}`, true,
			func(e *HBEntry) string {
				return e.SysLabel() + "|" + e.Platform + "|" + e.Devs[0] + "|" + e.System().Ext
			}},
		// default file not playable: use another playable one
		{`{"slug":"c","title":"C","files":[{"default":true,"playable":false,"filename":"c.zip"},{"playable":true,"filename":"c.nes"}]}`, true,
			func(e *HBEntry) string { return e.File + "|" + e.System().Folder }},
		{`{"slug":"d","typetag":"hackrom","files":[{"default":true,"playable":true,"filename":"d.gb"}]}`, false, nil},
		{`{"slug":"e","nsfw":true,"files":[{"default":true,"playable":true,"filename":"e.gb"}]}`, false, nil},
		{`{"slug":"f","files":[{"default":true,"playable":true,"filename":"f.sms"}]}`, false, nil},
		{`{"slug":"g","files":[{"default":true,"playable":true,"filename":"../../g.gb"}]}`, false, nil},
		{`{"slug":"h","developer":null,"tags":"Puzzle","license":["MIT"],"files":[{"default":true,"playable":true,"filename":"h.gb"}]}`, true,
			func(e *HBEntry) string {
				return e.Title + "|" + e.DevLine() + "|" + strings.Join(e.Tags, ",") + "|" + e.License
			}},
	}
	want := []string{"files/a.gba|X,Y|GBA|cover.png,s/1.png", "GBC|GBC|Z|.gbc", "c.nes|FC", "", "", "", "", "h|UNKNOWN DEVELOPER|Puzzle|MIT"}
	for i, c := range cases {
		e, ok := parseHHEntry(json.RawMessage(c.json))
		if ok != c.ok {
			t.Errorf("case %d: ok=%v", i, ok)
			continue
		}
		if ok && c.want(e) != want[i] {
			t.Errorf("case %d: got %q want %q", i, c.want(e), want[i])
		}
	}
}

func TestHomebrewSync(t *testing.T) {
	f := newFakeHH()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// 95 entries, most of them sharing one of three timestamps (game jam batches)
	for i := 0; i < 95; i++ {
		f.add(fmt.Sprintf("game-%03d", i), fmt.Sprintf("Game %d", i), "GB", fmt.Sprintf("files/g%d.gb", i), base.Add(time.Duration(i/40)*24*time.Hour), nil)
	}
	f.add("hack", "Hack", "GB", "h.gb", base, map[string]any{"typetag": "hackrom"})
	env, _ := hhEnv(t, f)
	now := base.Add(400 * 24 * time.Hour)

	// First visit: the whole list, in slug order.
	c := LoadHomebrew(env)
	var pages int
	added, err := env.SyncHomebrew(c, now, func(p HBProgress) { pages = p.Page })
	if err != nil || added != 96 || len(c.Entries) != 95 || pages != 10 || !c.state.Complete {
		t.Fatalf("full sync: added=%d entries=%d pages=%d complete=%v err=%v", added, len(c.Entries), pages, c.state.Complete, err)
	}
	if c.Entries[0].Added < c.Entries[len(c.Entries)-1].Added {
		t.Fatal("not newest first")
	}

	// It's saved on the card.
	c = LoadHomebrew(env)
	if len(c.Entries) != 95 || !c.state.Complete {
		t.Fatalf("reload: %d %v", len(c.Entries), c.state.Complete)
	}

	// Next visit: three new entries; only the newest pages are read.
	for i := 0; i < 3; i++ {
		f.add(fmt.Sprintf("new-%d", i), fmt.Sprintf("New %d", i), "GBA", "n.gba", base.Add(30*24*time.Hour), nil)
	}
	f.calls = 0
	added, err = env.SyncHomebrew(c, now.Add(time.Hour), nil)
	if err != nil || added != 3 || len(c.Entries) != 98 || f.calls > 3 {
		t.Fatalf("incremental: added=%d entries=%d calls=%d err=%v", added, len(c.Entries), f.calls, err)
	}
	if c.Entries[0].Title[:3] != "New" {
		t.Fatalf("newest first: %s", c.Entries[0].Title)
	}

	// A batch added with an old timestamp isn't on the newest pages; the count
	// check notices and fetches the whole list.
	f.add("late", "Late", "GB", "l.gb", base.Add(-time.Hour), nil)
	added, err = env.SyncHomebrew(c, now.Add(2*time.Hour), nil)
	if err != nil || c.bySlug["late"] == nil || added != 1 {
		t.Fatalf("count check: added=%d late=%v err=%v", added, c.bySlug["late"] != nil, err)
	}

	// Nothing new: one request.
	f.calls = 0
	if added, err = env.SyncHomebrew(c, now.Add(3*time.Hour), nil); err != nil || added != 0 || f.calls != 1 {
		t.Fatalf("idle: added=%d calls=%d err=%v", added, f.calls, err)
	}

	// After a month, a full pass drops what was removed.
	f.mu.Lock()
	f.entries = f.entries[1:] // game-000 removed
	f.mu.Unlock()
	if _, err = env.SyncHomebrew(c, now.Add(40*24*time.Hour), nil); err != nil || c.bySlug["game-000"] != nil {
		t.Fatalf("monthly refresh: err=%v still there=%v", err, c.bySlug["game-000"] != nil)
	}
}

func TestHomebrewSyncResumes(t *testing.T) {
	f := newFakeHH()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		f.add(fmt.Sprintf("g-%03d", i), fmt.Sprintf("G %d", i), "GB", "g.gb", base, nil)
	}
	env, _ := hhEnv(t, f)
	f.fail = 12 // the connection drops after 12 pages
	c := LoadHomebrew(env)
	if _, err := env.SyncHomebrew(c, base, nil); err == nil {
		t.Fatal("expected an error")
	}
	got := len(LoadHomebrew(env).Entries)
	if got != 120 {
		t.Fatalf("saved %d entries before the error, want 120", got)
	}
	// Mixtape reopens: it carries on from page 13.
	f.fail, f.calls = 0, 0
	c = LoadHomebrew(env)
	if _, err := env.SyncHomebrew(c, base, nil); err != nil || len(c.Entries) != 250 || f.calls != 13 {
		t.Fatalf("resume: entries=%d calls=%d err=%v", len(c.Entries), f.calls, err)
	}
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestInstallHB(t *testing.T) {
	f := newFakeHH()
	env, _ := hhEnv(t, f)
	rom := bytes.Repeat([]byte{0xAA}, 4096)
	f.files["/static/database-gba/entries/sun-rise/files/sun rise.gba"] = rom // the server sees the decoded path
	f.files["/static/database-gba/entries/sun-rise/cover.png"] = pngBytes(80, 60)
	e := &HBEntry{Slug: "sun-rise", Title: "Sun Rise: Deluxe?", Platform: "GBA", File: "files/sun rise.gba", Shots: []string{"cover.png"}}
	// Onion's cached game list must be dropped so the new game shows up.
	os.WriteFile(filepath.Join(env.SDRoot, "Roms/GBA/GBA_cache6.db"), []byte("x"), 0o644)

	res, err := env.InstallHB(context.Background(), e, func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.ROM != "Roms/GBA/Sun Rise Deluxe.gba" || !res.Art || len(res.Notes) != 0 {
		t.Fatalf("result: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(env.SDRoot, res.ROM)); !bytes.Equal(b, rom) {
		t.Fatal("rom not written")
	}
	artPath := filepath.Join(env.SDRoot, "Roms/GBA/Imgs/Sun Rise Deluxe.png")
	af, err := os.Open(artPath)
	if err != nil {
		t.Fatal("no box art")
	}
	art, err := png.Decode(af)
	af.Close()
	if err != nil || art.Bounds().Dx() != 240 { // 80px cover, scaled up 3x
		t.Fatalf("box art: %v %v", art.Bounds(), err)
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Roms/GBA/GBA_cache6.db")); err == nil {
		t.Fatal("game list cache not cleared")
	}
	if env.HBRecords()["sun-rise"] == nil {
		t.Fatal("no record")
	}
	// Reinstalling writes the same file.
	if res2, err := env.InstallHB(context.Background(), e, func(Progress) {}); err != nil || res2.ROM != res.ROM {
		t.Fatalf("reinstall: %v %v", res2, err)
	}

	// A different entry with the same title doesn't replace it.
	f.files["/static/database-gba/entries/sun-rise-2/files/x.gba"] = rom
	e2 := &HBEntry{Slug: "sun-rise-2", Title: "Sun Rise: Deluxe?", File: "files/x.gba"}
	if res3, err := env.InstallHB(context.Background(), e2, func(Progress) {}); err != nil || res3.ROM != "Roms/GBA/Sun Rise Deluxe (2).gba" {
		t.Fatalf("name clash: %v %v", res3, err)
	}

	// A web page instead of a ROM is refused.
	f.files["/static/database-gb/entries/bad/b.gb"] = []byte("<!DOCTYPE html><html>nope</html>" + strings.Repeat(" ", 64))
	if _, err := env.InstallHB(context.Background(), &HBEntry{Slug: "bad", Title: "Bad", File: "b.gb"}, func(Progress) {}); err == nil {
		t.Fatal("html accepted as a ROM")
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Roms/GB/Bad.gb")); err == nil {
		t.Fatal("bad download left on the card")
	}

	// NES: goes to Roms/FC; the emulator is missing, so there's a note.
	f.files["/static/database-nes/entries/n/n.nes"] = append([]byte("NES\x1a"), rom...)
	resN, err := env.InstallHB(context.Background(), &HBEntry{Slug: "n", Title: "Nessy", File: "n.nes"}, func(Progress) {})
	if err != nil || resN.ROM != "Roms/FC/Nessy.nes" || len(resN.Notes) != 1 {
		t.Fatalf("nes: %+v %v", resN, err)
	}

	// Erase removes the ROM and box art, nothing else.
	save := filepath.Join(env.SDRoot, "Saves/CurrentProfile/saves/gpSP/Sun Rise Deluxe.sav")
	os.MkdirAll(filepath.Dir(save), 0o755)
	os.WriteFile(save, []byte("s"), 0o644)
	if err := env.UninstallHB("sun-rise"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{res.ROM, "Roms/GBA/Imgs/Sun Rise Deluxe.png"} {
		if _, err := os.Stat(filepath.Join(env.SDRoot, p)); err == nil {
			t.Fatalf("%s not erased", p)
		}
	}
	if _, err := os.Stat(save); err != nil {
		t.Fatal("save was removed")
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Roms/GBA/Sun Rise Deluxe (2).gba")); err != nil {
		t.Fatal("the other entry's ROM was removed")
	}
	if env.HBRecords()["sun-rise"] != nil {
		t.Fatal("record left behind")
	}
	if err := env.UninstallHB("never-installed"); err == nil {
		t.Fatal("erased something Mixtape didn't install")
	}
}

func TestRomBase(t *testing.T) {
	for in, want := range map[string]string{
		"Cozy Arcade Redemption Game [GBA]": "Cozy Arcade Redemption Game [GBA]",
		"What?: The *Game*":                 "What The Game",
		"...":                               "Homebrew",
		"Ounfò Tan Sispann":                 "Ounfò Tan Sispann",
	} {
		if got := romBase(in); got != want {
			t.Errorf("romBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// Drives the HOMEBREW tab with a fake Homebrew Hub: sync, filter, details, install, erase.
func TestHomebrewTab(t *testing.T) {
	fx := newFixture(t)
	f := newFakeHH()
	srv := httptest.NewServer(f)
	defer srv.Close()
	base := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 23; i++ {
		plat, file := "GBA", "files/x.gba"
		if i%3 == 0 {
			plat, file = "GBC", "files/x.gbc"
		}
		slug := fmt.Sprintf("hb-%02d", i)
		f.add(slug, fmt.Sprintf("Homebrew %d", i), plat, file, base.Add(time.Duration(i)*time.Minute), nil)
		f.files["/static/database-test/entries/"+slug+"/files/"+strings.TrimPrefix(file, "files/")] = bytes.Repeat([]byte{1}, 256)
		f.files["/static/database-test/entries/"+slug+"/cover.png"] = pngBytes(160, 120)
	}
	env := fx.env
	env.Config.HomebrewAPI = srv.URL
	for _, d := range []string{"Emu/GBA", "Emu/GBC"} {
		os.MkdirAll(filepath.Join(env.SDRoot, d), 0o755)
	}
	env.Offline = true // the ports catalog comes from the built-in copy
	u := NewUI(env, 640, 480)
	c := NewCanvas(640, 480)
	u.Start()
	pump := func(d time.Duration, until func() bool) {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			select {
			case fn := <-u.post:
				fn()
			case <-time.After(10 * time.Millisecond):
			}
			u.frame++
			u.Draw(c)
			if until != nil && until() {
				return
			}
		}
	}
	pump(2*time.Second, func() bool { return u.scr == scrList })
	u.modal = modalNone
	// Start the sync as a start with a live catalog would.
	env.Offline = false
	u.syncHomebrew(LoadHomebrew(env))
	u.tab = tabHomebrew
	u.refilter()
	u.Draw(c)
	pump(3*time.Second, func() bool { return !u.hbSyncing })
	if len(u.hb.Entries) != 23 || len(u.hbVis) != 23 || u.hbErr != "" {
		t.Fatalf("synced %d, visible %d, err %q", len(u.hb.Entries), len(u.hbVis), u.hbErr)
	}
	if u.hbVis[0].Title != "Homebrew 22" {
		t.Fatalf("not newest first: %s", u.hbVis[0].Title)
	}
	u.Key(BtnX) // GBA only
	if hbFilters[u.hbFilter] != "GBA" || len(u.hbVis) != 15 {
		t.Fatalf("filter %s: %d", hbFilters[u.hbFilter], len(u.hbVis))
	}
	u.Key(BtnDown)
	u.Key(BtnA)
	if u.scr != scrHBDetail || u.hbCur == nil {
		t.Fatal("no detail")
	}
	pump(time.Second, func() bool { return u.imgs[u.hbShotURL()] != nil })
	u.Key(BtnA) // install
	pump(2*time.Second, func() bool { return u.modal == modalMsg })
	if u.msgTitle != "TRACK LOADED" {
		t.Fatalf("install: %q %v", u.msgTitle, u.msgBody)
	}
	u.Key(BtnA)
	slug := u.hbCur.Slug
	if u.hbRecs[slug] == nil {
		t.Fatal("not recorded")
	}
	u.Draw(c)
	u.Key(BtnX)
	if u.modal != modalConfirm {
		t.Fatal("no erase confirm")
	}
	u.Key(BtnA)
	if u.msgTitle != "ERASED" || u.hbRecs[slug] != nil {
		t.Fatalf("erase: %q %v", u.msgTitle, u.msgBody)
	}
	u.Key(BtnA)
	for _, k := range []int{BtnR1, BtnL1, BtnY, BtnDown, BtnB} {
		u.Key(k)
		u.Draw(c)
	}
	if u.scr != scrList {
		t.Fatal("B should go back to the list")
	}
	for i := 0; i < 4; i++ {
		u.Key(BtnX)
		u.Draw(c)
	}
	if hbFilters[u.hbFilter] != "ALL" {
		t.Fatal("filter should wrap to ALL")
	}
	u.Key(BtnR1)
	if u.tab == tabHomebrew {
		t.Fatal("R1 should leave the tab")
	}
}

func TestHBFileURL(t *testing.T) {
	env := &Env{}
	e, ok := parseHHEntry(json.RawMessage(`{"slug":"messiahm_civ-gb","title":"CivGB","platform":"GBC","basepath":"database-gb","files":[{"default":true,"playable":true,"filename":"files/CivGB Demo.gbc"}],"screenshots":["cover.png"]}`))
	if !ok {
		t.Fatal("not parsed")
	}
	want := "https://hh3.gbdev.io/static/database-gb/entries/messiahm_civ-gb/files/CivGB%20Demo.gbc"
	if got := env.hbFileURL(e, e.File); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// Entries saved without a database name: guessed from the system.
	old := &HBEntry{Slug: "antibody", File: "files/Antibody.gba"}
	if got := env.hbFileURL(old, "cover.png"); got != "https://hh3.gbdev.io/static/database-gba/entries/antibody/cover.png" {
		t.Fatal(got)
	}
	// No file marked playable: any file will do, like on Homebrew Hub's site.
	if e, ok := parseHHEntry(json.RawMessage(`{"slug":"x","files":[{"filename":"x.gb"}]}`)); !ok || e.File != "x.gb" {
		t.Fatal("files without 'playable' skipped")
	}
}

func TestHBOldCacheResyncs(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	writeFileAtomic(hbCachePath(env), []byte(`{"state":{"complete":true,"full_at":1},"entries":[{"slug":"a","title":"A","file":"a.gba"}]}`))
	c := LoadHomebrew(env)
	if c.state.Complete || c.bySlug["a"].Base != "database-gba" {
		t.Fatalf("old cache: complete=%v base=%q", c.state.Complete, c.bySlug["a"].Base)
	}
}
