// Command hackdb builds Mixtape's romhack catalog from the public RomHacking.net
// archive on the Internet Archive (rhdn-20210914). It reads the 6.5 GB zip
// remotely with HTTP range requests, picks popular NES/SNES/GB/GBA hacks and
// English translations (plus every well-downloaded Pokémon hack), opens each
// patch zip, validates the patches, and writes hacks.json.
//
//	go run ./tools/hackdb -out hacks.json
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mixtape/patch"
)

const archiveURL = "https://archive.org/download/rhdn-20210914/RHDN-20210914.zip"

// ---------- remote zip over HTTP ranges ----------

type remote struct {
	url   string
	size  int64
	mu    sync.Mutex
	cache map[int64][]byte
	order []int64
	cl    *http.Client
}

const blockSize = 256 << 10

func openRemote(u string) (*remote, error) {
	cl := &http.Client{Timeout: 90 * time.Second}
	req, _ := http.NewRequest("HEAD", u, nil)
	req.Header.Set("User-Agent", "mixtape-hackdb (+https://github.com/snyderman3000/mixtape)")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	final := resp.Request.URL.String() // follow the redirect to the storage node once
	return &remote{url: final, size: resp.ContentLength, cache: map[int64][]byte{}, cl: cl}, nil
}

func (r *remote) fetch(off, n int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest("GET", r.url, nil)
		req.Header.Set("User-Agent", "mixtape-hackdb (+https://github.com/snyderman3000/mixtape)")
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
		resp, err := r.cl.Do(req)
		if err == nil {
			b, err2 := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 206 && err2 == nil && int64(len(b)) == n {
				return b, nil
			}
			err = fmt.Errorf("range %d+%d: status %d, got %d bytes (%v)", off, n, resp.StatusCode, len(b), err2)
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	return nil, lastErr
}

func (r *remote) block(i int64) ([]byte, error) {
	r.mu.Lock()
	if b, ok := r.cache[i]; ok {
		r.mu.Unlock()
		return b, nil
	}
	r.mu.Unlock()
	off := i * blockSize
	n := int64(blockSize)
	if off+n > r.size {
		n = r.size - off
	}
	b, err := r.fetch(off, n)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[i] = b
	r.order = append(r.order, i)
	if len(r.order) > 400 { // ~100 MB cache
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
	r.mu.Unlock()
	return b, nil
}

func (r *remote) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}
	// large reads go straight to the network
	if len(p) > 2*blockSize {
		n := int64(len(p))
		if off+n > r.size {
			n = r.size - off
		}
		b, err := r.fetch(off, n)
		if err != nil {
			return 0, err
		}
		copy(p, b)
		if n < int64(len(p)) {
			return int(n), io.EOF
		}
		return int(n), nil
	}
	done := 0
	for done < len(p) {
		cur := off + int64(done)
		if cur >= r.size {
			return done, io.EOF
		}
		b, err := r.block(cur / blockSize)
		if err != nil {
			return done, err
		}
		done += copy(p[done:], b[cur%blockSize:])
	}
	return done, nil
}

// ---------- scraped_info.txt ----------

type info struct {
	Title, Link, Author, Platform, Category, Genre, HackOf, Version, Released, Patching, Language, Status string
	Downloads                                                                                               int
	Score                                                                                                   string
	Description                                                                                             string
	FileSHA1, FileCRC, ROMSHA1, ROMCRC, DBMatch                                                             string
}

var fieldRE = regexp.MustCompile(`(?m)^## ([a-z_]+):\s*(.*)$`)
var hexRE = regexp.MustCompile(`[0-9A-Fa-f]{8,40}`)

func parseInfo(s string) *info {
	in := &info{}
	for _, m := range fieldRE.FindAllStringSubmatch(s, -1) {
		v := strings.TrimSpace(m[2])
		switch m[1] {
		case "title":
			in.Title = v
		case "rhdn_link":
			in.Link = v
		case "released_by":
			in.Author = v
		case "platform":
			in.Platform = v
		case "downloads":
			in.Downloads, _ = strconv.Atoi(strings.ReplaceAll(v, ",", ""))
		case "score":
			in.Score = v
		case "category":
			in.Category = v
		case "genre":
			in.Genre = v
		case "hack_of", "translation_of", "game":
			if in.HackOf == "" {
				in.HackOf = v
			}
		case "patch_ver":
			in.Version = v
		case "hack_release_date", "translation_release_date", "release_date":
			in.Released = v
		case "patching_info":
			in.Patching = v
		case "language":
			in.Language = v
		case "status":
			in.Status = v
		}
	}
	if i := strings.Index(s, "######## description ########"); i >= 0 {
		d := s[i+len("######## description ########"):]
		if j := strings.Index(d, "########"); j >= 0 {
			d = d[:j]
		}
		in.Description = strings.TrimSpace(d)
	}
	if i := strings.Index(s, "######## rom_info ########"); i >= 0 {
		for _, line := range strings.Split(s[i:], "\n") {
			l := strings.ToLower(line)
			h := hexRE.FindString(line[strings.Index(line, ":")+1:])
			switch {
			case strings.Contains(l, "database match:"):
				in.DBMatch = strings.TrimSpace(line[strings.Index(line, ":")+1:])
			case strings.Contains(l, "file/rom sha-1") && len(h) == 40:
				in.FileSHA1, in.ROMSHA1 = h, h
			case strings.Contains(l, "file/rom crc32") && len(h) == 8:
				in.FileCRC, in.ROMCRC = h, h
			case strings.Contains(l, "file sha-1") && len(h) == 40:
				in.FileSHA1 = h
			case strings.Contains(l, "file crc32") && len(h) == 8:
				in.FileCRC = h
			case strings.Contains(l, "rom sha-1") && len(h) == 40:
				in.ROMSHA1 = h
			case strings.Contains(l, "rom crc32") && len(h) == 8:
				in.ROMCRC = h
			}
		}
	}
	return in
}

// ---------- catalog output ----------

type Variant struct {
	Label     string `json:"label"`
	Member    string `json:"member"` // path of the patch inside the download zip
	Format    string `json:"format"`
	Size      int    `json:"size"`
	SourceCRC string `json:"src_crc,omitempty"` // from BPS/UPS header
	TargetCRC string `json:"dst_crc,omitempty"`
}

type Hack struct {
	ID          int       `json:"id"`
	Kind        string    `json:"kind"` // hack | translation
	Title       string    `json:"title"`
	System      string    `json:"system"` // NES | SNES | GB | GBA
	Game        string    `json:"game"`
	Category    string    `json:"category,omitempty"`
	Genre       string    `json:"genre,omitempty"`
	Author      string    `json:"author,omitempty"`
	Version     string    `json:"version,omitempty"`
	Released    string    `json:"released,omitempty"`
	Downloads   int       `json:"downloads"`
	Score       string    `json:"score,omitempty"`
	Pokemon     bool      `json:"pokemon,omitempty"`
	Description string    `json:"desc"`
	Patching    string    `json:"patching,omitempty"`
	Header      string    `json:"header,omitempty"` // SNES: "none" | "required"
	Base        Base      `json:"base"`
	Archive     string    `json:"archive"` // path of the download zip inside the RHDN archive
	Screenshot  string    `json:"shot,omitempty"`
	Variants    []Variant `json:"variants"`
}

type Base struct {
	Name     string `json:"name,omitempty"`
	FileCRC  string `json:"file_crc,omitempty"`
	FileSHA1 string `json:"file_sha1,omitempty"`
	ROMCRC   string `json:"rom_crc,omitempty"`
	ROMSHA1  string `json:"rom_sha1,omitempty"`
}

type Catalog struct {
	Version   int    `json:"version"`
	Generated string `json:"generated"`
	Source    string `json:"source"`
	Archive   string `json:"archive_url"`
	Hacks     []Hack `json:"hacks"`
}

// ---------- selection ----------

type group struct {
	dir     string
	kind    string
	system  string
	info    *zip.File
	patches []*zip.File // candidate download archives / loose patches
	shot    *zip.File
	parsed  *info
}

var systems = map[string]string{
	"hacks/NES": "NES", "hacks/SNES": "SNES", "hacks/GBA": "GBA", "hacks/GB": "GB", "hacks/GBC": "GB",
	"translations/Nintendo Entertainment System": "NES", "translations/Super Nintendo": "SNES",
	"translations/Game Boy Advance": "GBA", "translations/Game Boy": "GB", "translations/Game Boy Color": "GB",
}

var quota = map[string]int{ // top-N by downloads per system+kind
	"hack/NES": 90, "hack/SNES": 110, "hack/GBA": 70, "hack/GB": 50,
	"translation/NES": 35, "translation/SNES": 45, "translation/GBA": 30, "translation/GB": 30,
}

const pokemonMinDownloads = 1000

func isPokemon(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "pokemon") || strings.Contains(l, "pokémon") || strings.Contains(l, "pocket monsters")
}

func main() {
	out := flag.String("out", "hacks.json", "output file")
	report := flag.String("report", "report.txt", "human-readable build report")
	flag.Parse()
	log.SetFlags(log.Ltime)

	rem, err := openRemote(archiveURL)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("archive %s (%.1f GB)", rem.url, float64(rem.size)/(1<<30))
	zr, err := zip.NewReader(rem, rem.size)
	if err != nil {
		log.Fatalf("zip: %v", err)
	}
	log.Printf("%d entries", len(zr.File))

	groups := map[string]*group{}
	for _, f := range zr.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) < 5 || f.FileInfo().IsDir() {
			continue
		}
		sys, ok := systems[parts[0]+"/"+parts[1]]
		if !ok {
			continue
		}
		dir := strings.Join(parts[:4], "/")
		g := groups[dir]
		if g == nil {
			g = &group{dir: dir, system: sys, kind: strings.TrimSuffix(parts[0], "s")}
			if parts[0] == "translations" {
				g.kind = "translation"
			}
			groups[dir] = g
		}
		base := strings.ToLower(path.Base(f.Name))
		switch {
		case base == "scraped_info.txt":
			g.info = f
		case strings.Contains(base, "_screenshot0."):
			g.shot = f
		case strings.HasSuffix(base, ".zip") || strings.HasSuffix(base, ".ips") || strings.HasSuffix(base, ".bps") || strings.HasSuffix(base, ".ups"):
			g.patches = append(g.patches, f)
		}
	}
	var all []*group
	for _, g := range groups {
		if g.info != nil && len(g.patches) > 0 {
			all = append(all, g)
		}
	}
	log.Printf("%d candidate entries with info + files", len(all))

	// Phase 1: read every info file (parallel).
	parallel(all, 24, func(g *group) {
		rc, err := g.info.Open()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 256<<10))
		rc.Close()
		g.parsed = parseInfo(string(b))
	})

	// Phase 2: choose.
	byKey := map[string][]*group{}
	var chosen []*group
	seen := map[*group]bool{}
	for _, g := range all {
		p := g.parsed
		if p == nil || (p.FileCRC == "" && p.ROMCRC == "" && p.FileSHA1 == "" && p.ROMSHA1 == "") {
			continue
		}
		if g.kind == "translation" && !strings.EqualFold(strings.TrimSpace(p.Language), "English") {
			continue
		}
		if g.kind == "translation" && strings.Contains(strings.ToLower(p.Status), "unfinished") {
			continue
		}
		if isPokemon(p.Title+" "+p.HackOf) && p.Downloads >= pokemonMinDownloads && !seen[g] {
			chosen = append(chosen, g)
			seen[g] = true
		}
		k := g.kind + "/" + g.system
		byKey[k] = append(byKey[k], g)
	}
	for k, list := range byKey {
		sort.Slice(list, func(i, j int) bool { return list[i].parsed.Downloads > list[j].parsed.Downloads })
		for i := 0; i < len(list) && i < quota[k]; i++ {
			if !seen[list[i]] {
				chosen = append(chosen, list[i])
				seen[list[i]] = true
			}
		}
	}
	log.Printf("chose %d entries", len(chosen))

	// Phase 3: open the downloads and validate patches.
	var mu sync.Mutex
	var hacks []Hack
	var rep []string
	parallel(chosen, 12, func(g *group) {
		h, why := build(g)
		mu.Lock()
		defer mu.Unlock()
		if h == nil {
			rep = append(rep, fmt.Sprintf("SKIP %-6s %-4s %6d  %s — %s", g.kind, g.system, g.parsed.Downloads, g.parsed.Title, why))
			return
		}
		hacks = append(hacks, *h)
	})
	sort.Slice(hacks, func(i, j int) bool {
		if hacks[i].System != hacks[j].System {
			return hacks[i].System < hacks[j].System
		}
		return hacks[i].Downloads > hacks[j].Downloads
	})
	cat := Catalog{Version: 1, Generated: time.Now().UTC().Format(time.RFC3339),
		Source: "RomHacking.net archive (Internet Archive item rhdn-20210914)", Archive: archiveURL, Hacks: hacks}
	b, _ := json.MarshalIndent(cat, "", " ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	counts := map[string]int{}
	pk := 0
	for _, h := range hacks {
		counts[h.Kind+"/"+h.System]++
		if h.Pokemon {
			pk++
		}
	}
	sort.Strings(rep)
	head := []string{fmt.Sprintf("generated %s: %d entries (%d Pokémon), %d skipped", cat.Generated, len(hacks), pk, len(rep))}
	for k, v := range counts {
		head = append(head, fmt.Sprintf("  %-18s %d", k, v))
	}
	sort.Strings(head[1:])
	head = append(head, "")
	for _, h := range hacks {
		vs := []string{}
		for _, v := range h.Variants {
			vs = append(vs, v.Format+":"+path.Base(v.Member))
		}
		head = append(head, fmt.Sprintf("OK   %-11s %-4s %6d  %s  [%s]  base=%s", h.Kind, h.System, h.Downloads, h.Title, strings.Join(vs, ", "), h.Base.Name))
	}
	os.WriteFile(*report, []byte(strings.Join(append(head, rep...), "\n")+"\n"), 0o644)
	log.Printf("wrote %s: %d entries, %d skipped", *out, len(hacks), len(rep))
}

func parallel(gs []*group, n int, fn func(*group)) {
	ch := make(chan *group)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range ch {
				fn(g)
			}
		}()
	}
	for i, g := range gs {
		if i%500 == 0 && i > 0 {
			log.Printf("  %d/%d", i, len(gs))
		}
		ch <- g
	}
	close(ch)
	wg.Wait()
}

var idRE = regexp.MustCompile(`/(?:hacks|translations)/(\d+)`)

var skipWords = []string{"optional", "extra", "addon", "add-on", "bonus", "alternate", "alt ", "old", "legacy", "beta", "debug", "cheat", "unheadered", "headered"}

func build(g *group) (*Hack, string) {
	p := g.parsed
	// pick the download: prefer a .zip; else a loose patch
	var dl *zip.File
	for _, f := range g.patches {
		if strings.HasSuffix(strings.ToLower(f.Name), ".zip") && (dl == nil || f.UncompressedSize64 > dl.UncompressedSize64) {
			dl = f
		}
	}
	type cand struct {
		member string
		data   []byte
	}
	var cands []cand
	if dl != nil {
		if dl.UncompressedSize64 > 24<<20 {
			return nil, "download too large"
		}
		rc, err := dl.Open()
		if err != nil {
			return nil, "can't open download: " + err.Error()
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, "read: " + err.Error()
		}
		inner, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, "download isn't a readable zip"
		}
		sawXdelta := false
		for _, f := range inner.File {
			n := f.Name
			l := strings.ToLower(n)
			if strings.HasPrefix(l, "__macosx") || f.FileInfo().IsDir() {
				continue
			}
			if strings.HasSuffix(l, ".xdelta") || strings.HasSuffix(l, ".vcdiff") {
				sawXdelta = true
			}
			if _, ok := patch.FormatFromName(n); !ok || f.UncompressedSize64 > 32<<20 {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			cands = append(cands, cand{n, b})
		}
		if len(cands) == 0 {
			if sawXdelta {
				return nil, "xdelta only (not supported yet)"
			}
			return nil, "no IPS/BPS/UPS patch inside"
		}
	} else {
		return nil, "no zip download"
	}

	var vars []Variant
	for _, c := range cands {
		inf, err := patch.Inspect(c.data)
		if err != nil {
			continue
		}
		v := Variant{Member: c.member, Format: string(inf.Format), Size: len(c.data), Label: label(c.member)}
		if inf.HasCRC {
			v.SourceCRC = fmt.Sprintf("%08X", inf.SourceCRC)
			v.TargetCRC = fmt.Sprintf("%08X", inf.TargetCRC)
		}
		vars = append(vars, v)
	}
	if len(vars) == 0 {
		return nil, "patch files failed validation"
	}
	if len(vars) > 12 {
		return nil, fmt.Sprintf("%d patch files — too many to choose from automatically", len(vars))
	}
	// order: likely-main first
	sort.SliceStable(vars, func(i, j int) bool { return rank(vars[i], p.Title) > rank(vars[j], p.Title) })

	h := &Hack{
		Kind: g.kind, Title: clean(p.Title), System: g.system, Game: clean(p.HackOf), Category: p.Category, Genre: p.Genre,
		Author: p.Author, Version: p.Version, Released: p.Released, Downloads: p.Downloads, Score: p.Score,
		Description: short(p.Description, 700), Patching: p.Patching, Archive: dl.Name, Variants: vars,
		Base: Base{Name: p.DBMatch, FileCRC: strings.ToUpper(p.FileCRC), FileSHA1: strings.ToUpper(p.FileSHA1),
			ROMCRC: strings.ToUpper(p.ROMCRC), ROMSHA1: strings.ToUpper(p.ROMSHA1)},
	}
	h.Pokemon = isPokemon(p.Title + " " + p.HackOf)
	if m := idRE.FindStringSubmatch(p.Link); m != nil {
		h.ID, _ = strconv.Atoi(m[1])
	}
	if g.shot != nil {
		h.Screenshot = g.shot.Name
	}
	pl := strings.ToLower(p.Patching)
	if g.system == "SNES" {
		switch {
		case strings.Contains(pl, "no-header") || strings.Contains(pl, "no header") || strings.Contains(pl, "headerless"):
			h.Header = "none"
		case strings.Contains(pl, "header"):
			h.Header = "required"
		}
	}
	return h, ""
}

func rank(v Variant, title string) int {
	l := strings.ToLower(v.Member)
	s := 0
	for _, w := range skipWords {
		if strings.Contains(l, w) {
			s -= 10
		}
	}
	s -= 3 * strings.Count(v.Member, "/")
	for _, w := range strings.Fields(strings.ToLower(title)) {
		if len(w) > 2 && strings.Contains(l, w) {
			s += 2
		}
	}
	if v.Format == "bps" {
		s += 1 // carries its own checksums
	}
	return s
}

func label(member string) string {
	b := path.Base(member)
	return strings.TrimSuffix(b, path.Ext(b))
}

func clean(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, " ", " ")) }

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)[:n]
	if i := strings.LastIndexAny(string(r), ".!?"); i > n/2 {
		return string(r)[:i+1]
	}
	return string(r) + "…"
}

// URL builds the device download URL for a path inside the archive.
func URL(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return archiveURL + "/" + strings.Join(parts, "/")
}
