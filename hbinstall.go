package main

// Installing Homebrew Hub entries: the ROM goes straight into the system's
// OnionOS folder (Roms/GBA, Roms/GBC, Roms/GB or Roms/FC), with the entry's
// cover as box art in that folder's Imgs/. Mixtape records what it wrote in
// data/homebrew/<slug>.json so Erase removes exactly that.

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// HBRecord is what Mixtape installed for one entry.
type HBRecord struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	ROM       string    `json:"rom"`           // relative to the SD root
	Art       string    `json:"art,omitempty"` // box art, if Mixtape added it
	Installed time.Time `json:"installed"`
}

func (env *Env) hbRecordPath(slug string) string {
	return filepath.Join(env.DataDir, "homebrew", safeName(slug)+".json")
}

// HBRecords lists what Mixtape has installed from Homebrew Hub, by slug.
// Records whose ROM was deleted by hand are left out.
func (env *Env) HBRecords() map[string]*HBRecord {
	out := map[string]*HBRecord{}
	ents, _ := os.ReadDir(filepath.Join(env.DataDir, "homebrew"))
	for _, de := range ents {
		if !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(env.DataDir, "homebrew", de.Name()))
		if err != nil {
			continue
		}
		var r HBRecord
		if json.Unmarshal(b, &r) != nil || r.Slug == "" || r.ROM == "" {
			continue
		}
		if abs := env.safeDest(r.ROM); abs == "" {
			continue
		} else if _, err := os.Stat(abs); err != nil {
			continue
		}
		out[r.Slug] = &r
	}
	return out
}

var badNameRE = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

// romBase is the file name (without extension) a ROM is saved under. OnionOS
// shows file names in its game lists, so it's the entry's title, made safe
// for the SD card's FAT file system.
func romBase(title string) string {
	n := badNameRE.ReplaceAllString(title, " ")
	n = strings.Join(strings.Fields(n), " ")
	n = strings.Trim(n, ". ")
	if r := []rune(n); len(r) > 80 {
		n = strings.TrimSpace(string(r[:80]))
	}
	if n == "" {
		n = "Homebrew"
	}
	return n
}

// HBResult describes a finished install.
type HBResult struct {
	ROM   string
	Art   bool
	Notes []string
}

// InstallHB downloads an entry's ROM into its OnionOS folder.
func (env *Env) InstallHB(ctx context.Context, e *HBEntry, prog func(Progress)) (*HBResult, error) {
	sys, ok := hbSystemFor(e.File)
	if !ok {
		return nil, fmt.Errorf("Mixtape doesn't know which system %s is for", path.Base(e.File))
	}
	if free := diskFree(env.SDRoot); free >= 0 && free < 64<<20 {
		return nil, fmt.Errorf("not enough space on the SD card (%s free)", human(free))
	}
	dir := "Roms/" + sys.Folder
	if err := os.MkdirAll(filepath.Join(env.SDRoot, dir), 0o755); err != nil {
		return nil, fmt.Errorf("can't create %s: %v", dir, err)
	}
	old := env.loadHBRecord(e.Slug)

	// Pick the file name: reuse the earlier one on a reinstall; otherwise the
	// title, numbered if that name is already taken by a file Mixtape didn't put there.
	rel := ""
	if old != nil && strings.HasPrefix(old.ROM, dir+"/") {
		rel = old.ROM
	} else {
		base := romBase(e.Title)
		for i := 1; i < 100; i++ {
			name := base
			if i > 1 {
				name = fmt.Sprintf("%s (%d)", base, i)
			}
			cand := dir + "/" + name + sys.Ext
			if _, err := os.Stat(env.safeDest(cand)); err != nil {
				rel = cand
				break
			}
		}
		if rel == "" {
			return nil, fmt.Errorf("too many files named %s in %s", base, dir)
		}
	}
	abs := env.safeDest(rel)
	if abs == "" {
		return nil, fmt.Errorf("unsafe file name")
	}

	dlDir := filepath.Join(env.DataDir, "dl")
	os.RemoveAll(dlDir)
	os.MkdirAll(dlDir, 0o755)
	defer os.RemoveAll(dlDir)
	tmp := filepath.Join(dlDir, "rom"+sys.Ext)
	name := path.Base(e.File)
	err := env.download(ctx, env.hbFileURL(e.Slug, e.File), tmp, func(n, t int64) {
		prog(Progress{Phase: "DOWNLOADING", Done: n, Total: t, File: name})
	})
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(tmp); err != nil || st.Size() < 32 {
		return nil, fmt.Errorf("%s didn't download correctly (it's empty). Nothing was changed; try again", name)
	} else if st.Size() > 64<<20 {
		return nil, fmt.Errorf("%s is larger than any %s cartridge, so Mixtape didn't install it", name, sys.Label)
	}
	if err := checkROM(tmp, sys); err != nil {
		return nil, err
	}
	prog(Progress{Phase: "RECORDING", File: name})
	if err := copyInto(tmp, abs); err != nil {
		return nil, fmt.Errorf("writing %s: %v", rel, err)
	}
	rec := &HBRecord{Slug: e.Slug, Title: e.Title, ROM: rel, Installed: time.Now()}
	res := &HBResult{ROM: rel}

	// Box art: Roms/<system>/Imgs/<rom name>.png, unless one is already there
	// that Mixtape didn't add.
	art := dir + "/Imgs/" + strings.TrimSuffix(path.Base(rel), sys.Ext) + ".png"
	_, artErr := os.Stat(env.safeDest(art))
	ours := old != nil && old.Art == art
	if len(e.Shots) > 0 && (artErr != nil || ours) {
		prog(Progress{Phase: "LABELLING", File: name})
		if err := env.saveBoxArt(ctx, env.hbFileURL(e.Slug, e.Shots[0]), env.safeDest(art)); err == nil {
			rec.Art = art
			res.Art = true
		}
	} else if ours {
		rec.Art = art
	}
	b, _ := json.MarshalIndent(rec, "", " ")
	if err := writeFileAtomic(env.hbRecordPath(e.Slug), b); err != nil {
		return nil, fmt.Errorf("installed, but couldn't save the record: %v", err)
	}
	env.refreshRomList(sys.Folder)
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Emu", sys.Folder)); err != nil {
		res.Notes = append(res.Notes, "OnionOS's "+sys.Label+" emulator isn't on your card. Turn it on in Apps → Package Manager.")
	}
	return res, nil
}

// checkROM catches a download that's obviously not a ROM (an error page, say).
func checkROM(p string, sys hbSystem) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	low := strings.ToLower(strings.TrimSpace(string(head[:min(n, 64)])))
	if strings.HasPrefix(low, "<!doctype") || strings.HasPrefix(low, "<html") || strings.HasPrefix(low, "{") {
		return fmt.Errorf("Homebrew Hub sent a web page instead of the game. Nothing was changed; try again later")
	}
	if sys.Ext == ".nes" && (n < 4 || string(head[:4]) != "NES\x1a") {
		return fmt.Errorf("the downloaded file isn't an NES ROM. Nothing was changed")
	}
	return nil
}

func copyInto(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".mxt"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Lstat(dst); err == nil && st.Mode().IsRegular() {
		os.Remove(dst) // FAT: rename doesn't replace
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// saveBoxArt downloads a cover image and saves it as a PNG sized for OnionOS.
func (env *Env) saveBoxArt(ctx context.Context, url, dest string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	cl := &http.Client{Timeout: 30 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, maxImageBytes)) // GIFs: first frame
	if err != nil {
		return err
	}
	// Small pixel-art covers are scaled up by whole steps so they stay crisp.
	b := img.Bounds()
	out := toRGBA(img)
	if k := min(250/max(1, b.Dx()), 250/max(1, b.Dy())); k >= 2 {
		out = scaleUp(out, k)
	} else {
		out = fit(img, 250, 250)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest + ".mxt")
	if err != nil {
		return err
	}
	err = png.Encode(f, out)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest + ".mxt")
		return err
	}
	os.Remove(dest)
	return os.Rename(dest+".mxt", dest)
}

func scaleUp(src *image.RGBA, k int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w*k, h*k))
	for y := 0; y < h*k; y++ {
		for x := 0; x < w*k; x++ {
			so := (y/k)*src.Stride + (x/k)*4
			do := y*dst.Stride + x*4
			copy(dst.Pix[do:do+4], src.Pix[so:so+4])
			dst.Pix[do+3] = 255
		}
	}
	return dst
}

func (env *Env) loadHBRecord(slug string) *HBRecord {
	b, err := os.ReadFile(env.hbRecordPath(slug))
	if err != nil {
		return nil
	}
	var r HBRecord
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// refreshRomList makes OnionOS rebuild a system's game list on its next visit.
func (env *Env) refreshRomList(folder string) {
	dir := filepath.Join(env.SDRoot, "Roms", folder)
	for _, f := range []string{folder + "_cache6.db", folder + "_cache2.db"} {
		os.Remove(filepath.Join(dir, f))
	}
}

// UninstallHB removes the ROM and box art Mixtape installed for an entry.
// Saves and save states (kept elsewhere by OnionOS) are left alone.
func (env *Env) UninstallHB(slug string) error {
	r := env.loadHBRecord(slug)
	if r == nil {
		return fmt.Errorf("Mixtape didn't install this one, so it won't guess which files to delete")
	}
	for _, rel := range []string{r.ROM, r.Art} {
		if rel == "" || !strings.HasPrefix(rel, "Roms/") {
			continue
		}
		if abs := env.safeDest(rel); abs != "" {
			os.Remove(abs)
		}
	}
	if r.Art != "" {
		os.Remove(filepath.Dir(env.safeDest(r.Art))) // Imgs/, only if now empty
	}
	if parts := strings.Split(r.ROM, "/"); len(parts) >= 2 {
		env.refreshRomList(parts[1])
	}
	return os.Remove(env.hbRecordPath(slug))
}
