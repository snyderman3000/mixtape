package main

// Romhacks: catalog, ROM scanning/matching, and patching on the device.

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"mixtape/patch"
)

//go:embed assets/hacks.json
var embeddedHacks []byte

const (
	defaultHacksURL = "https://raw.githubusercontent.com/snyderman3000/mixtape/main/catalog/hacks.json"
	rhdnArchive     = "https://archive.org/download/rhdn-20210914/RHDN-20210914.zip"
)

type HackVariant struct {
	Label     string `json:"label"`
	Member    string `json:"member"`
	Format    string `json:"format"`
	Size      int    `json:"size"`
	SourceCRC string `json:"src_crc,omitempty"`
	TargetCRC string `json:"dst_crc,omitempty"`
	Header    string `json:"header,omitempty"` // SNES IPS option: "none" | "required"
}

type HackBase struct {
	Name     string `json:"name,omitempty"`
	FileCRC  string `json:"file_crc,omitempty"`
	FileSHA1 string `json:"file_sha1,omitempty"`
	ROMCRC   string `json:"rom_crc,omitempty"`
	ROMSHA1  string `json:"rom_sha1,omitempty"`
}

type Hack struct {
	ID          int           `json:"id"`
	Kind        string        `json:"kind"`
	Title       string        `json:"title"`
	System      string        `json:"system"`
	Game        string        `json:"game"`
	Category    string        `json:"category,omitempty"`
	Genre       string        `json:"genre,omitempty"`
	Author      string        `json:"author,omitempty"`
	Version     string        `json:"version,omitempty"`
	Released    string        `json:"released,omitempty"`
	Downloads   int           `json:"downloads"`
	Score       string        `json:"score,omitempty"`
	Pokemon     bool          `json:"pokemon,omitempty"`
	Description string        `json:"desc"`
	Patching    string        `json:"patching,omitempty"`
	Header      string        `json:"header,omitempty"`
	Base        HackBase      `json:"base"`
	Archive     string        `json:"archive"`
	Home        string        `json:"home,omitempty"`
	Screenshot  string        `json:"shot,omitempty"`
	Variants    []HackVariant `json:"variants"`

	Index int `json:"-"`
}

type HackCatalog struct {
	Hacks  []*Hack
	Source string
}

// archiveURL turns a catalog path into a download URL.
func archiveURL(p string) string {
	if p == "" || strings.HasPrefix(p, "https://") || strings.HasPrefix(p, "http://") {
		return p
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		// PathEscape leaves + and & alone, but archive.org reads + as a space
		parts[i] = strings.NewReplacer("+", "%2B", "&", "%26").Replace(url.PathEscape(s))
	}
	return rhdnArchive + "/" + strings.Join(parts, "/")
}

func (h *Hack) KindLabel() string {
	if h.Kind == "translation" {
		return "TRANSLATION"
	}
	if h.Category != "" {
		return "HACK · " + strings.ToUpper(h.Category)
	}
	return "HACK"
}

func parseHacks(b []byte) ([]*Hack, error) {
	var doc struct {
		Hacks []*Hack `json:"hacks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []*Hack
	for _, h := range doc.Hacks {
		if h == nil || h.Title == "" || len(h.Variants) == 0 || systemDirs[h.System] == nil {
			continue
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("hack catalog is empty")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Downloads > out[j].Downloads })
	for i, h := range out {
		h.Index = i
	}
	return out, nil
}

func LoadHacks(env *Env) *HackCatalog {
	cache := filepath.Join(env.DataDir, "hacks.json")
	u := env.Config.HacksURL
	if u == "" {
		u = defaultHacksURL
	}
	if !env.Offline {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", userAgent)
		cl := &http.Client{Timeout: 20 * time.Second, Transport: env.HTTP.Transport}
		if resp, err := cl.Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				if hs, err := parseHacks(b); err == nil {
					writeFileAtomic(cache, b)
					return &HackCatalog{Hacks: hs, Source: "live"}
				}
			}
		}
	}
	if b, err := os.ReadFile(cache); err == nil {
		if hs, err := parseHacks(b); err == nil {
			return &HackCatalog{Hacks: hs, Source: "cached"}
		}
	}
	hs, _ := parseHacks(embeddedHacks)
	return &HackCatalog{Hacks: hs, Source: "built-in"}
}

// ---------- ROM scanning ----------

// Onion's ROM folders for each catalog system.
var systemDirs = map[string][]string{
	"NES":  {"FC"},
	"SNES": {"SFC"},
	"GB":   {"GB", "GBC"},
	"GBA":  {"GBA"},
}

var romExt = map[string]bool{".nes": true, ".sfc": true, ".smc": true, ".fig": true, ".gb": true, ".gbc": true,
	".dmg": true, ".gba": true, ".bin": true, ".zip": true}

// ROMFile is one ROM on the card with its checksums.
type ROMFile struct {
	Path   string   `json:"p"` // relative to SD root
	Size   int64    `json:"s"`
	MTime  int64    `json:"m"`
	Member string   `json:"z,omitempty"` // ROM inside a zip
	CRCs   []string `json:"c"`           // whole file, and without a copier/iNES header when present
}

type romCache struct {
	Files map[string]*ROMFile `json:"files"`
}

// ScanROMs checksums ROMs in the systems' folders, reusing cached results.
func (env *Env) ScanROMs(progress func(done, total int)) ([]*ROMFile, error) {
	cachePath := filepath.Join(env.DataDir, "romhash.json")
	var cache romCache
	if b, err := os.ReadFile(cachePath); err == nil {
		json.Unmarshal(b, &cache)
	}
	if cache.Files == nil {
		cache.Files = map[string]*ROMFile{}
	}
	type job struct {
		rel string
		fi  os.FileInfo
	}
	var jobs []job
	seenDir := map[string]bool{}
	for _, dirs := range systemDirs {
		for _, d := range dirs {
			if seenDir[d] {
				continue
			}
			seenDir[d] = true
			root := filepath.Join(env.SDRoot, "Roms", d)
			filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if fi.IsDir() {
					if strings.HasPrefix(fi.Name(), ".") || strings.EqualFold(fi.Name(), "Imgs") {
						return filepath.SkipDir
					}
					return nil
				}
				if !romExt[strings.ToLower(filepath.Ext(p))] || strings.HasPrefix(fi.Name(), ".") {
					return nil
				}
				rel, _ := filepath.Rel(env.SDRoot, p)
				jobs = append(jobs, job{filepath.ToSlash(rel), fi})
				return nil
			})
		}
	}
	var out []*ROMFile
	fresh := map[string]*ROMFile{}
	for i, j := range jobs {
		if progress != nil {
			progress(i, len(jobs))
		}
		if c := cache.Files[j.rel]; c != nil && c.Size == j.fi.Size() && c.MTime == j.fi.ModTime().Unix() {
			out = append(out, c)
			fresh[j.rel] = c
			continue
		}
		r, err := hashROM(filepath.Join(env.SDRoot, filepath.FromSlash(j.rel)))
		if err != nil {
			continue
		}
		r.Path, r.Size, r.MTime = j.rel, j.fi.Size(), j.fi.ModTime().Unix()
		out = append(out, r)
		fresh[j.rel] = r
	}
	cache.Files = fresh
	if b, err := json.Marshal(cache); err == nil {
		writeFileAtomic(cachePath, b)
	}
	if progress != nil {
		progress(len(jobs), len(jobs))
	}
	return out, nil
}

func hexCRC(c uint32) string { return fmt.Sprintf("%08X", c) }

// hashROM computes the CRC32 of the ROM and of the ROM without a header.
func hashROM(p string) (*ROMFile, error) {
	if strings.EqualFold(filepath.Ext(p), ".zip") {
		zr, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || !romExt[strings.ToLower(filepath.Ext(f.Name))] || strings.HasSuffix(strings.ToLower(f.Name), ".zip") {
				continue
			}
			r := &ROMFile{Member: f.Name, CRCs: []string{hexCRC(f.CRC32)}} // the zip already knows the CRC
			if hl := headerLen(f.Name, int64(f.UncompressedSize64), nil); hl > 0 {
				rc, err := f.Open()
				if err == nil {
					c, err := crcSkipping(rc, hl)
					rc.Close()
					if err == nil {
						r.CRCs = append(r.CRCs, c)
					}
				}
			}
			return r, nil
		}
		return nil, fmt.Errorf("no ROM inside zip")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	head := make([]byte, 16)
	io.ReadFull(f, head)
	f.Seek(0, 0)
	hl := headerLen(p, st.Size(), head)
	whole := crc32.NewIEEE()
	body := crc32.NewIEEE()
	buf := make([]byte, 256<<10)
	var off int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			whole.Write(buf[:n])
			if hl > 0 {
				start := int64(0)
				if off < int64(hl) {
					start = int64(hl) - off
				}
				if start < int64(n) {
					body.Write(buf[start:n])
				}
			}
			off += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	r := &ROMFile{CRCs: []string{hexCRC(whole.Sum32())}}
	if hl > 0 {
		r.CRCs = append(r.CRCs, hexCRC(body.Sum32()))
	}
	return r, nil
}

// headerLen reports a removable header: 512-byte SNES copier headers and 16-byte iNES headers.
func headerLen(name string, size int64, head []byte) int {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".sfc", ".smc", ".fig":
		if size%1024 == 512 {
			return 512
		}
	case ".nes":
		if head == nil || bytes.HasPrefix(head, []byte("NES\x1a")) {
			return 16
		}
	}
	return 0
}

func crcSkipping(r io.Reader, n int) (string, error) {
	if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
		return "", err
	}
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hexCRC(h.Sum32()), nil
}

// ---------- matching ----------

// Match links a hack to a ROM on the card.
type Match struct {
	ROM     *ROMFile
	Variant int // preferred variant index (one whose checksum matches), or -1 = any
}

func hackCRCs(h *Hack) map[string]bool {
	m := map[string]bool{}
	for _, c := range []string{h.Base.FileCRC, h.Base.ROMCRC} {
		if c != "" {
			m[strings.ToUpper(c)] = true
		}
	}
	return m
}

// MatchHacks finds, for each hack, a ROM on the card it can be applied to.
func MatchHacks(hacks []*Hack, roms []*ROMFile) map[*Hack]*Match {
	byCRC := map[string][]*ROMFile{}
	for _, r := range roms {
		for _, c := range r.CRCs {
			byCRC[c] = append(byCRC[c], r)
		}
	}
	out := map[*Hack]*Match{}
	for _, h := range hacks {
		dirs := systemDirs[h.System]
		inSystem := func(r *ROMFile) bool {
			for _, d := range dirs {
				if strings.HasPrefix(r.Path, "Roms/"+d+"/") {
					return true
				}
			}
			return false
		}
		// a variant whose own checksum matches is the strongest match
		for vi, v := range h.Variants {
			if v.SourceCRC == "" {
				continue
			}
			for _, r := range byCRC[strings.ToUpper(v.SourceCRC)] {
				if inSystem(r) {
					out[h] = &Match{ROM: r, Variant: vi}
					break
				}
			}
			if out[h] != nil {
				break
			}
		}
		if out[h] != nil {
			continue
		}
		for c := range hackCRCs(h) {
			for _, r := range byCRC[c] {
				if inSystem(r) {
					out[h] = &Match{ROM: r, Variant: -1}
					break
				}
			}
			if out[h] != nil {
				break
			}
		}
	}
	return out
}

// DefaultVariant picks the option most likely to fit the user's ROM.
func DefaultVariant(h *Hack, m *Match) int {
	if m == nil {
		return 0
	}
	if m.Variant >= 0 {
		return m.Variant
	}
	if h.System == "SNES" && len(h.Variants) > 1 {
		state := "none"
		if len(m.ROM.CRCs) > 1 { // has a removable copier header
			state = "required"
		}
		for i, v := range h.Variants {
			if v.Header == state {
				return i
			}
		}
	}
	return 0
}

// ---------- applying ----------

type HackResult struct {
	Output   string // relative to SD root
	Verified bool   // output checksum confirmed by the patch
	Note     string
}

var badName = regexp.MustCompile(`[\\/:*?"<>|]+`)

func outputName(h *Hack, v HackVariant, srcPath string) string {
	ext := strings.ToLower(filepath.Ext(srcPath))
	if ext == ".zip" || ext == "" {
		ext = map[string]string{"NES": ".nes", "SNES": ".sfc", "GB": ".gbc", "GBA": ".gba"}[h.System]
	}
	name := h.Title
	if h.Version != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(h.Version)) {
		name += " v" + h.Version
	}
	if len(h.Variants) > 1 {
		name += " (" + v.Label + ")"
	}
	name = strings.TrimSpace(badName.ReplaceAllString(name, "-"))
	if len(name) > 110 {
		name = name[:110]
	}
	return name + ext
}

// readROM loads the ROM (from inside its zip when zipped).
func (env *Env) readROM(r *ROMFile) ([]byte, error) {
	abs := filepath.Join(env.SDRoot, filepath.FromSlash(r.Path))
	if r.Member == "" {
		return os.ReadFile(abs)
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == r.Member {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 64<<20))
		}
	}
	return nil, fmt.Errorf("%s is no longer inside %s", r.Member, r.Path)
}

// fetchPatch downloads the hack's archive and returns the chosen patch file.
func (env *Env) fetchPatch(h *Hack, v HackVariant, prog func(Progress)) ([]byte, error) {
	cache := filepath.Join(env.DataDir, "cache", "hack-"+slug(h.Title)+"-"+slug(h.Version)+".zip")
	var data []byte
	if b, err := os.ReadFile(cache); err == nil {
		data = b
	} else {
		req, _ := http.NewRequest("GET", archiveURL(h.Archive), nil)
		req.Header.Set("User-Agent", userAgent)
		cl := &http.Client{Timeout: 3 * time.Minute, Transport: env.HTTP.Transport}
		resp, err := cl.Do(req)
		if err != nil {
			return nil, netHint(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("the patch download failed (%s)", resp.Status)
		}
		var n int64
		cr := &ctxReader{ctx: noCancel{}, r: io.LimitReader(resp.Body, 40<<20), n: &n,
			every: func() { prog(Progress{Phase: "DOWNLOADING", Done: n, Total: resp.ContentLength}) }}
		data, err = io.ReadAll(cr)
		if err != nil {
			return nil, netHint(err)
		}
		writeFileAtomic(cache, data)
	}
	lower := strings.ToLower(h.Archive)
	if !strings.HasSuffix(lower, ".zip") { // a bare patch file
		return data, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		os.Remove(cache)
		return nil, fmt.Errorf("the download wasn't a readable zip — try again")
	}
	for _, f := range zr.File {
		if f.Name == v.Member {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 40<<20))
		}
	}
	return nil, fmt.Errorf("the patch %q wasn't in the download", v.Label)
}

type noCancel struct{}

func (noCancel) Deadline() (time.Time, bool) { return time.Time{}, false }
func (noCancel) Done() <-chan struct{}       { return nil }
func (noCancel) Err() error                  { return nil }
func (noCancel) Value(key any) any           { return nil }

// prepareSource picks the form of the ROM the patch expects (with/without header).
func prepareSource(h *Hack, v HackVariant, rom []byte, name string) ([]byte, error) {
	hl := headerLen(name, int64(len(rom)), rom)
	forms := [][]byte{rom}
	if hl > 0 && len(rom) > hl {
		forms = append(forms, rom[hl:])
	}
	if v.SourceCRC != "" { // BPS/UPS: the patch says exactly which bytes it wants
		want := strings.ToUpper(v.SourceCRC)
		for _, f := range forms {
			if hexCRC(crc32.ChecksumIEEE(f)) == want {
				return f, nil
			}
		}
		if h.System == "SNES" && hl == 0 { // patch made against a headered copy
			withHdr := append(make([]byte, 512, 512+len(rom)), rom...)
			if hexCRC(crc32.ChecksumIEEE(withHdr)) == want {
				return withHdr, nil
			}
		}
		return nil, patch.ErrSourceCRC
	}
	// IPS: check against the catalog's checksums, then fix the header if needed
	want := hackCRCs(h)
	var match []byte
	for _, f := range forms {
		if want[hexCRC(crc32.ChecksumIEEE(f))] {
			match = f
			break
		}
	}
	if match == nil {
		return nil, patch.ErrSourceCRC
	}
	if h.System == "SNES" {
		headered := len(match)%1024 == 512
		want := h.Header
		if v.Header != "" {
			want = v.Header
		}
		switch {
		case want == "none" && headered:
			match = match[512:]
		case want == "required" && !headered:
			match = append(make([]byte, 512, 512+len(match)), match...)
		}
	}
	return match, nil
}

var hackMu sync.Mutex

// ApplyHack patches the matched ROM and writes a new file beside it.
func (env *Env) ApplyHack(h *Hack, vi int, m *Match, prog func(Progress)) (*HackResult, error) {
	hackMu.Lock()
	defer hackMu.Unlock()
	defer debug.FreeOSMemory()
	v := h.Variants[vi]
	prog(Progress{Phase: "CONNECTING"})
	p, err := env.fetchPatch(h, v, prog)
	if err != nil {
		return nil, err
	}
	prog(Progress{Phase: "READING ROM"})
	rom, err := env.readROM(m.ROM)
	if err != nil {
		return nil, fmt.Errorf("couldn't read your ROM: %v", err)
	}
	name := m.ROM.Path
	if m.ROM.Member != "" {
		name = m.ROM.Member
	}
	src, err := prepareSource(h, v, rom, name)
	rom = nil
	if err != nil {
		if len(h.Variants) > 1 {
			return nil, fmt.Errorf("this option doesn't fit your ROM — try another option with ◀ ▶")
		}
		return nil, fmt.Errorf("your ROM doesn't match what this patch needs (%s)", h.Base.Name)
	}
	prog(Progress{Phase: "PATCHING"})
	out, err := patch.Apply(src, p)
	src = nil
	if err != nil {
		return nil, err
	}
	res := &HackResult{Verified: v.TargetCRC != ""}
	dir := filepath.Dir(m.ROM.Path)
	rel := filepath.ToSlash(filepath.Join(dir, outputName(h, v, name)))
	abs := env.safeDest(rel)
	if abs == "" {
		return nil, fmt.Errorf("bad output path")
	}
	prog(Progress{Phase: "WRITING"})
	if err := writeFileAtomic(abs, out); err != nil {
		os.Remove(abs + ".tmp")
		return nil, fmt.Errorf("couldn't save the patched game: %v", err)
	}
	res.Output = rel
	env.recordHack(h, rel)
	// refresh Onion's game list for that folder
	sysDir := filepath.Join(env.SDRoot, filepath.FromSlash(dir))
	for {
		base := filepath.Base(sysDir)
		os.Remove(filepath.Join(sysDir, base+"_cache6.db"))
		os.Remove(filepath.Join(sysDir, base+"_cache2.db"))
		parent := filepath.Dir(sysDir)
		if filepath.Base(parent) == "Roms" || parent == sysDir {
			break
		}
		sysDir = parent
	}
	return res, nil
}

// ---------- records of patched games ----------

func (env *Env) madePath() string { return filepath.Join(env.DataDir, "hacks-made.json") }

func (env *Env) MadeHacks() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(env.madePath()); err == nil {
		json.Unmarshal(b, &m)
	}
	for k, rel := range m { // forget files the user deleted
		if _, err := os.Stat(filepath.Join(env.SDRoot, filepath.FromSlash(rel))); err != nil {
			delete(m, k)
		}
	}
	return m
}

func hackKey(h *Hack) string { return h.System + "/" + h.Title }

func (env *Env) recordHack(h *Hack, rel string) {
	m := env.MadeHacks()
	m[hackKey(h)] = rel
	b, _ := json.MarshalIndent(m, "", " ")
	writeFileAtomic(env.madePath(), b)
}

func (env *Env) EraseHack(h *Hack) error {
	m := env.MadeHacks()
	rel, ok := m[hackKey(h)]
	if !ok {
		return fmt.Errorf("Mixtape didn't make this one")
	}
	if abs := env.safeDest(rel); abs != "" {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	delete(m, hackKey(h))
	b, _ := json.MarshalIndent(m, "", " ")
	writeFileAtomic(env.madePath(), b)
	dir := filepath.Join(env.SDRoot, filepath.FromSlash(filepath.Dir(rel)))
	base := filepath.Base(dir)
	os.Remove(filepath.Join(dir, base+"_cache6.db"))
	return nil
}
