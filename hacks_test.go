package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func patchFixture(t *testing.T, name string) []byte {
	b, err := os.ReadFile(filepath.Join("patch", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type hackFix struct {
	*fixture
	src, dst       []byte
	srcCRC         string
	hacks          []*Hack
	zipIPS, zipBPS []byte
	snes           *Hack
}

// newHackFix builds a fake SD card and a small catalog whose "ROM" is the patch fixture source.
func newHackFix(t *testing.T) *hackFix {
	fx := newFixture(t)
	h := &hackFix{fixture: fx, src: patchFixture(t, "src.bin"), dst: patchFixture(t, "dst_same.bin")}
	h.srcCRC = hexCRC(crc32.ChecksumIEEE(h.src))
	h.zipIPS = mkzipBytes(t, map[string][]byte{"My Hack/My Hack.ips": patchFixture(t, "same.ips"), "My Hack/readme.txt": []byte("hi")})
	h.zipBPS = mkzipBytes(t, map[string][]byte{"v/My BPS Hack.bps": patchFixture(t, "same.delta.bps")})
	fx.files["/ips.zip"] = h.zipIPS
	fx.files["/bps.zip"] = h.zipBPS
	h.hacks = []*Hack{
		{Title: "GBA IPS Hack", System: "GBA", Game: "Base", Version: "1.0", Downloads: 10, Base: HackBase{ROMCRC: h.srcCRC},
			Archive: fx.srv.URL + "/ips.zip", Variants: []HackVariant{{Label: "My Hack", Member: "My Hack/My Hack.ips", Format: "ips"}}},
		{Title: "GB BPS Hack", System: "GB", Game: "Base", Downloads: 20,
			Archive: fx.srv.URL + "/bps.zip", Variants: []HackVariant{{Label: "BPS", Member: "v/My BPS Hack.bps", Format: "bps",
				SourceCRC: h.srcCRC, TargetCRC: hexCRC(crc32.ChecksumIEEE(h.dst))}}},
		{Title: "Needs Other ROM", System: "NES", Game: "Other", Base: HackBase{ROMCRC: "DEADBEEF"},
			Archive: fx.srv.URL + "/ips.zip", Variants: []HackVariant{{Label: "x", Member: "My Hack/My Hack.ips", Format: "ips"}}},
	}
	h.snes = &Hack{Title: "SNES Headerless Hack", System: "SNES", Game: "Base", Header: "none", Base: HackBase{ROMCRC: h.srcCRC},
		Archive: fx.srv.URL + "/ips.zip", Variants: []HackVariant{{Label: "x", Member: "My Hack/My Hack.ips", Format: "ips"}}}
	h.hacks = append(h.hacks, h.snes)
	for _, d := range []string{"GBA", "GB", "GBC", "SFC", "FC"} {
		os.MkdirAll(filepath.Join(fx.env.SDRoot, "Roms", d), 0o755)
	}
	return h
}

func mkzipBytes(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, _ := zw.Create(n)
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

func (h *hackFix) put(rel string, b []byte) {
	p := filepath.Join(h.env.SDRoot, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, b, 0o644)
}

func TestScanAndMatch(t *testing.T) {
	h := newHackFix(t)
	h.put("Roms/GBA/Base (USA).gba", h.src)
	h.put("Roms/GBC/zipped.zip", mkzipBytes(t, map[string][]byte{"Base.gbc": h.src}))
	h.put("Roms/SFC/Base (headered).smc", append(make([]byte, 512), h.src...))
	h.put("Roms/GBA/unrelated.gba", []byte("nope"))
	h.put("Roms/GBA/notes.txt", h.src) // not a ROM extension
	roms, err := h.env.ScanROMs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roms) != 4 {
		t.Fatalf("scanned %d roms: %+v", len(roms), roms)
	}
	m := MatchHacks(h.hacks, roms)
	if m[h.hacks[0]] == nil || m[h.hacks[0]].ROM.Path != "Roms/GBA/Base (USA).gba" {
		t.Errorf("GBA IPS match = %+v", m[h.hacks[0]])
	}
	if m[h.hacks[1]] == nil || m[h.hacks[1]].ROM.Member != "Base.gbc" || m[h.hacks[1]].Variant != 0 {
		t.Errorf("GB BPS match (zipped, in GBC folder) = %+v", m[h.hacks[1]])
	}
	if m[h.hacks[2]] != nil {
		t.Error("matched a hack whose ROM isn't on the card")
	}
	if m[h.snes] == nil || !strings.HasSuffix(m[h.snes].ROM.Path, ".smc") {
		t.Errorf("SNES headered copy should match via its headerless checksum: %+v", m[h.snes])
	}
	// cached rescan returns the same without re-hashing
	os.Chtimes(filepath.Join(h.env.SDRoot, "Roms/GBA/unrelated.gba"), time.Now(), time.Now().Add(time.Hour))
	roms2, _ := h.env.ScanROMs(nil)
	if len(roms2) != 4 {
		t.Fatalf("rescan: %d", len(roms2))
	}
}

func TestApplyHacks(t *testing.T) {
	h := newHackFix(t)
	h.put("Roms/GBA/Base (USA).gba", h.src)
	h.put("Roms/GBC/zipped.zip", mkzipBytes(t, map[string][]byte{"Base.gbc": h.src}))
	h.put("Roms/SFC/Base (headered).smc", append(make([]byte, 512), h.src...))
	os.WriteFile(filepath.Join(h.env.SDRoot, "Roms/GBA/GBA_cache6.db"), []byte("c"), 0o644)
	roms, _ := h.env.ScanROMs(nil)
	m := MatchHacks(h.hacks, roms)
	nop := func(Progress) {}

	res, err := h.env.ApplyHack(h.hacks[0], 0, m[h.hacks[0]], nop)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Roms/GBA/GBA IPS Hack v1.0.gba" || res.Verified {
		t.Errorf("ips result %+v", res)
	}
	if got := h.read(res.Output); got != string(h.dst) {
		t.Error("IPS output wrong")
	}
	if h.read("Roms/GBA/Base (USA).gba") != string(h.src) {
		t.Error("original ROM modified")
	}
	if h.exists("Roms/GBA/GBA_cache6.db") {
		t.Error("game list cache not refreshed")
	}

	res, err = h.env.ApplyHack(h.hacks[1], 0, m[h.hacks[1]], nop)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Roms/GBC/GB BPS Hack.gbc" || !res.Verified || h.read(res.Output) != string(h.dst) {
		t.Errorf("bps from zip: %+v", res)
	}

	res, err = h.env.ApplyHack(h.snes, 0, m[h.snes], nop)
	if err != nil {
		t.Fatal(err)
	}
	if h.read(res.Output) != string(h.dst) { // header stripped before patching, output headerless
		t.Errorf("snes header handling: len %d", len(h.read(res.Output)))
	}

	made := h.env.MadeHacks()
	if len(made) != 3 {
		t.Fatalf("made = %v", made)
	}
	if err := h.env.EraseHack(h.hacks[0]); err != nil || h.exists("Roms/GBA/GBA IPS Hack v1.0.gba") {
		t.Fatalf("erase: %v", err)
	}
	if !h.exists("Roms/GBA/Base (USA).gba") {
		t.Fatal("erase removed the original")
	}
}

func TestWrongROMRejected(t *testing.T) {
	h := newHackFix(t)
	bad := append([]byte(nil), h.src...)
	bad[5] ^= 1
	h.put("Roms/GB/bad.gb", bad)
	m := &Match{ROM: &ROMFile{Path: "Roms/GB/bad.gb", CRCs: []string{h.srcCRC}}, Variant: 0} // lie about the checksum
	if _, err := h.env.ApplyHack(h.hacks[1], 0, m, func(Progress) {}); err == nil {
		t.Fatal("patched a ROM that doesn't match")
	}
	if h.exists("Roms/GB/GB BPS Hack.gb") {
		t.Fatal("wrote output for a failed patch")
	}
}

func TestSNESHeaderAddedWhenRequired(t *testing.T) {
	h := &Hack{System: "SNES", Header: "required", Base: HackBase{ROMCRC: hexCRC(crc32.ChecksumIEEE([]byte("0123456789")))}}
	out, err := prepareSource(h, HackVariant{Format: "ips"}, []byte("0123456789"), "x.sfc")
	if err != nil || len(out) != 522 {
		t.Fatalf("len=%d err=%v", len(out), err)
	}
	h.Variants = []HackVariant{{Header: "none"}, {Header: "required"}}
	if d := DefaultVariant(h, &Match{ROM: &ROMFile{CRCs: []string{"A", "B"}}, Variant: -1}); d != 1 {
		t.Fatalf("headered ROM should default to the headered option, got %d", d)
	}
}

func TestEmbeddedHackCatalog(t *testing.T) {
	hs, err := parseHacks(embeddedHacks)
	if err != nil || len(hs) < 300 {
		t.Fatalf("n=%d err=%v", len(hs), err)
	}
	pk := 0
	for _, x := range hs {
		if x.Pokemon {
			pk++
		}
		for _, v := range x.Variants {
			if v.Member == "" || v.Format == "" {
				t.Errorf("%s: bad variant %+v", x.Title, v)
			}
		}
		if !strings.HasPrefix(archiveURL(x.Archive), "https://") {
			t.Errorf("%s: bad archive %q", x.Title, x.Archive)
		}
	}
	if pk < 30 {
		t.Errorf("only %d Pokémon entries", pk)
	}
	u := archiveURL("hacks/GB/Pokémon_ Blue Version/Pokemon Blue - 151___1_2_1/Pokemon Blue - 151 Patch.zip")
	if !strings.Contains(u, "Pok%C3%A9mon_%20Blue%20Version") {
		t.Errorf("url escaping: %s", u)
	}
}

func TestHacksTabFlow(t *testing.T) {
	h := newHackFix(t)
	h.put("Roms/GBA/Base (USA).gba", h.src)
	cat, _ := json.Marshal(map[string]any{"hacks": h.hacks})
	h.files["/hacks.json"] = cat
	h.env.Config.HacksURL = h.srv.URL + "/hacks.json"
	h.env.APIBase = h.srv.URL // no releases -> no self-update noise
	u := NewUI(h.env, 640, 480)
	c := NewCanvas(640, 480)
	u.Start()
	pump := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			select {
			case f := <-u.post:
				f()
			case <-time.After(10 * time.Millisecond):
			}
			u.frame++
			u.Draw(c)
		}
	}
	pump(300 * time.Millisecond)
	for u.tab != tabHacks {
		u.Key(BtnR1)
	}
	pump(400 * time.Millisecond)
	if !u.hk.scanned || len(u.hk.vis) != 1 || u.hk.vis[0].Title != "GBA IPS Hack" {
		t.Fatalf("ready list = %d entries, scanned=%v", len(u.hk.vis), u.hk.scanned)
	}
	for i := 0; i < len(hackFilters); i++ { // every filter renders
		u.Key(BtnY)
		u.Draw(c)
	}
	if hackFilters[u.hk.filter] != "READY" {
		t.Fatalf("filter cycle ended on %s", hackFilters[u.hk.filter])
	}
	u.Key(BtnA)
	if u.scr != scrHackDetail {
		t.Fatal("detail not opened")
	}
	u.Draw(c)
	u.Key(BtnA)
	pump(500 * time.Millisecond)
	if u.msgTitle != "TRACK LOADED" {
		t.Fatalf("title=%q body=%v", u.msgTitle, u.msgBody)
	}
	u.Key(BtnA)
	u.Draw(c)
	u.Key(BtnX) // erase
	if u.modal != modalConfirm {
		t.Fatal("no erase confirm")
	}
	u.Key(BtnA)
	if h.exists("Roms/GBA/GBA IPS Hack v1.0.gba") {
		t.Fatal("not erased")
	}
	u.Key(BtnB)
	u.Key(BtnX) // rescan
	pump(300 * time.Millisecond)
	for _, w := range [][2]int{{752, 560}} {
		u2 := NewUI(h.env, w[0], w[1])
		u2.hk = u.hk
		u2.tab = tabHacks
		u2.scr = scrList
		u2.Draw(NewCanvas(w[0], w[1]))
		u2.hk.cur = h.hacks[2]
		u2.scr = scrHackDetail
		u2.Draw(NewCanvas(w[0], w[1]))
	}
}

func TestArchiveURLEscapesPlusAndAmpersand(t *testing.T) {
	u := archiveURL("hacks/SNES/Chrono Trigger/Chrono Trigger+___1/CT+ & more.zip")
	if !strings.Contains(u, "Chrono%20Trigger%2B___1/CT%2B%20%26%20more.zip") {
		t.Fatal(u)
	}
}
