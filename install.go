package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ---------- manifest ----------

type FileRec struct {
	P string `json:"p"` // path relative to SD root
	S int64  `json:"s"`
	M int64  `json:"m"` // mtime unix seconds
}

type Manifest struct {
	Name      string    `json:"name"`
	Repo      string    `json:"repo"`
	Version   string    `json:"version"`
	Installed time.Time `json:"installed"`
	Where     []string  `json:"where"` // top-level install locations, e.g. App/PocketFlex
	Files     []FileRec `json:"files"`
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func norm(s string) string { return slugRE.ReplaceAllString(strings.ToLower(s), "") }

func (env *Env) manifestPath(p *Port) string {
	return filepath.Join(env.DataDir, "installed", slug(p.Name)+".json")
}

func (env *Env) LoadManifest(p *Port) *Manifest {
	b, err := os.ReadFile(env.manifestPath(p))
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return &m
}

// FoundOnCard guesses whether a port was installed by hand (no manifest).
func (env *Env) FoundOnCard(p *Port) string {
	keys := []string{norm(p.Name)}
	if p.Recipe != nil && p.Recipe.AppDir != "" {
		keys = append(keys, norm(p.Recipe.AppDir))
	}
	for _, dir := range []string{"App", "Roms/PORTS/Games"} {
		for _, name := range env.listDir(dir) {
			n := norm(name)
			for _, k := range keys {
				if k != "" && n == k {
					return dir + "/" + name
				}
			}
		}
	}
	return ""
}

func (env *Env) listDir(rel string) []string {
	if v, ok := env.dirCache[rel]; ok {
		return v
	}
	ents, _ := os.ReadDir(filepath.Join(env.SDRoot, rel))
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	env.dirCache[rel] = out
	return out
}

// ---------- archive abstraction ----------

type entry struct {
	name  string // normalised, slash separated, no leading ./
	dir   bool
	skip  bool // symlink or special
	size  int64
	open  func() (io.ReadCloser, error)
	zfile *zip.File
}

type archive struct {
	entries []*entry
	closeFn func()
	tarPath string
}

func openArchive(p string) (*archive, error) {
	lp := strings.ToLower(p)
	if strings.HasSuffix(lp, ".tar.gz") || strings.HasSuffix(lp, ".tgz") {
		return openTarGz(p)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("can't open archive: %v", err)
	}
	a := &archive{closeFn: func() { zr.Close() }}
	for _, f := range zr.File {
		f := f
		e := &entry{name: cleanEntry(f.Name), dir: f.FileInfo().IsDir() || isDirName(f.Name), size: int64(f.UncompressedSize64), zfile: f}
		if f.Mode()&os.ModeSymlink != 0 {
			e.skip = true
		}
		e.open = func() (io.ReadCloser, error) { return f.Open() }
		a.entries = append(a.entries, e)
	}
	return a, nil
}

func openTarGz(p string) (*archive, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	a := &archive{closeFn: func() {}, tarPath: p}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		e := &entry{name: cleanEntry(h.Name), dir: h.Typeflag == tar.TypeDir || isDirName(h.Name), size: h.Size}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			e.skip = true
		}
		a.entries = append(a.entries, e)
	}
	return a, nil
}

// isDirName catches folder entries written by Windows zip tools ("Roms\\PORTS\\").
func isDirName(n string) bool {
	return strings.HasSuffix(n, "/") || strings.HasSuffix(n, "\\")
}

func cleanEntry(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = strings.TrimPrefix(n, "./")
	n = strings.TrimLeft(n, "/")
	return strings.TrimSuffix(n, "/")
}

func junk(n string) bool {
	return strings.HasPrefix(n, "__MACOSX") || strings.Contains(n, "/__MACOSX/") || path.Base(n) == ".DS_Store" || path.Base(n) == "Thumbs.db"
}

// ---------- layout detection ----------

var sdRootDirs = map[string]string{
	"app": "App", "roms": "Roms", "emu": "Emu", "rapp": "RApp", ".tmp_update": ".tmp_update", "media": "Media",
	"bios": "BIOS", "saves": "Saves", "themes": "Themes", "icons": "Icons",
}

var docExt = map[string]bool{".md": true, ".txt": true, ".pdf": true, ".sha256": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".html": true, ".htm": true, ".rtf": true, ".url": true}

func isDoc(name string) bool {
	b := strings.ToLower(path.Base(name))
	if strings.HasPrefix(b, "readme") || strings.HasPrefix(b, "license") || strings.HasPrefix(b, "licence") ||
		strings.HasPrefix(b, "changelog") || strings.HasPrefix(b, "copying") || strings.HasPrefix(b, ".git") {
		return true
	}
	return docExt[path.Ext(b)]
}

// Layout maps archive paths to SD-card paths.
type Layout struct {
	Mode  string // sdroot, app, apps, ports
	Where []string
	Map   func(name string) (string, bool)
}

type node struct {
	dirs  map[string]bool
	files map[string]bool
}

func children(names []string, prefix string) node {
	n := node{dirs: map[string]bool{}, files: map[string]bool{}}
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || name == strings.TrimSuffix(prefix, "/") {
			continue
		}
		rest := name[len(prefix):]
		if rest == "" {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			n.dirs[rest[:i]] = true
		} else {
			n.files[rest] = true
		}
	}
	return n
}

func DetectLayout(a *archive, p *Port, snapshot bool) (*Layout, error) {
	var names []string
	dirSet := map[string]bool{}
	for _, e := range a.entries {
		if junk(e.name) || e.name == "" {
			continue
		}
		if e.dir {
			dirSet[e.name] = true
			continue
		}
		names = append(names, e.name)
	}
	// make implicit directories explicit so children() sees them
	all := append([]string{}, names...)
	for d := range dirSet {
		all = append(all, d+"/.")
	}
	appName := func(prefix string, depth int) string {
		if p.Recipe != nil && p.Recipe.AppDir != "" {
			return p.Recipe.AppDir
		}
		if prefix != "" && !(snapshot && depth == 1) {
			return path.Base(strings.TrimSuffix(prefix, "/"))
		}
		n := regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(p.Name, "")
		if n == "" {
			n = "App" + fmt.Sprint(p.Index)
		}
		return n
	}
	prefix := ""
	for depth := 0; depth < 5; depth++ {
		ch := children(all, prefix)
		delete(ch.files, ".")
		// 1) SD-root layout: App/, Roms/, Emu/ ...
		var roots []string
		for d := range ch.dirs {
			if _, ok := sdRootDirs[strings.ToLower(d)]; ok {
				roots = append(roots, d)
			}
		}
		if len(roots) > 0 {
			sort.Strings(roots)
			pre := prefix
			l := &Layout{Mode: "sdroot"}
			rootSet := map[string]string{}
			for _, r := range roots {
				rootSet[r] = sdRootDirs[strings.ToLower(r)]
			}
			// report the second-level folders as "where"
			seen := map[string]bool{}
			for _, n := range names {
				if !strings.HasPrefix(n, pre) {
					continue
				}
				parts := strings.Split(n[len(pre):], "/")
				if canon, ok := rootSet[parts[0]]; ok && len(parts) >= 2 {
					w := canon
					if len(parts) >= 3 {
						w += "/" + parts[1]
						if canon == "Roms" && strings.EqualFold(parts[1], "PORTS") && len(parts) >= 5 && strings.EqualFold(parts[2], "Games") {
							w += "/" + parts[2] + "/" + parts[3]
						}
					}
					if !seen[w] {
						seen[w] = true
						l.Where = append(l.Where, w)
					}
				}
			}
			l.Map = func(n string) (string, bool) {
				if !strings.HasPrefix(n, pre) {
					return "", false
				}
				rest := n[len(pre):]
				i := strings.IndexByte(rest, '/')
				if i < 0 {
					return "", false
				}
				canon, ok := rootSet[rest[:i]]
				if !ok {
					return "", false
				}
				return canon + rest[i:], true
			}
			return l, nil
		}
		// 2) the archive root *is* an app folder
		if ch.files["config.json"] && (ch.files["launch.sh"] || hasLaunch(ch.files)) {
			dest := "App/" + appName(prefix, depth)
			pre := prefix
			return &Layout{Mode: "app", Where: []string{dest}, Map: func(n string) (string, bool) {
				if !strings.HasPrefix(n, pre) {
					return "", false
				}
				return dest + "/" + n[len(pre):], true
			}}, nil
		}
		// 3) a Roms/PORTS-relative layout (Games/ + Shortcuts/ or *.port)
		portsLike := ch.dirs["Games"] || ch.dirs["Shortcuts"]
		for f := range ch.files {
			if strings.HasSuffix(strings.ToLower(f), ".port") {
				portsLike = true
			}
		}
		if portsLike {
			pre := prefix
			l := &Layout{Mode: "ports", Where: []string{"Roms/PORTS"}}
			l.Map = func(n string) (string, bool) {
				if !strings.HasPrefix(n, pre) {
					return "", false
				}
				rest := n[len(pre):]
				if isDoc(rest) && !strings.Contains(rest, "/") {
					return "", false
				}
				return "Roms/PORTS/" + rest, true
			}
			return l, nil
		}
		// 4) one or more app folders side by side
		var realFiles []string
		for f := range ch.files {
			if !isDoc(f) {
				realFiles = append(realFiles, f)
			}
		}
		var appDirs []string
		for d := range ch.dirs {
			sub := children(all, prefix+d+"/")
			if sub.files["config.json"] {
				appDirs = append(appDirs, d)
			}
		}
		if len(appDirs) > 0 && len(appDirs) == len(ch.dirs) && !(snapshot && depth == 0) {
			sort.Strings(appDirs)
			pre := prefix
			set := map[string]bool{}
			l := &Layout{Mode: "apps"}
			for _, d := range appDirs {
				set[d] = true
				name := d
				if len(appDirs) == 1 && p.Recipe != nil && p.Recipe.AppDir != "" {
					name = p.Recipe.AppDir
				}
				l.Where = append(l.Where, "App/"+name)
			}
			l.Map = func(n string) (string, bool) {
				if !strings.HasPrefix(n, pre) {
					return "", false
				}
				rest := n[len(pre):]
				i := strings.IndexByte(rest, '/')
				if i < 0 || !set[rest[:i]] {
					return "", false
				}
				d := rest[:i]
				if len(appDirs) == 1 && p.Recipe != nil && p.Recipe.AppDir != "" {
					d = p.Recipe.AppDir
				}
				return "App/" + d + rest[i:], true
			}
			return l, nil
		}
		// 5) a single wrapper folder: descend
		if len(ch.dirs) == 1 && len(realFiles) == 0 {
			for d := range ch.dirs {
				prefix += d + "/"
			}
			continue
		}
		break
	}
	return nil, fmt.Errorf("unrecognised archive layout — install this one by hand (see the project page)")
}

func hasLaunch(files map[string]bool) bool {
	for f := range files {
		if strings.HasPrefix(strings.ToLower(f), "launch") {
			return true
		}
	}
	return false
}

// ---------- safety rules ----------

// safeDest validates a mapped destination; returns the absolute path or "".
func (env *Env) safeDest(rel string) string {
	rel = path.Clean("/" + rel)[1:]
	if rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return ""
		}
	}
	return filepath.Join(env.SDRoot, filepath.FromSlash(rel))
}

// allowWrite decides whether an existing/new file may be (over)written.
func allowSystemWrite(rel string, exists bool) bool {
	if !strings.HasPrefix(rel, ".tmp_update/") {
		return true
	}
	// Onion's own system folder: only startup hooks, and never replace existing system files.
	return strings.HasPrefix(rel, ".tmp_update/startup/") || !exists
}

var configExt = map[string]bool{".json": true, ".conf": true, ".cfg": true, ".ini": true, ".txt": true, ".xml": true,
	".yaml": true, ".yml": true, ".toml": true, ".db": true, ".sqlite": true}

func matchKeep(rel string, globs []string) bool {
	parts := strings.Split(rel, "/")
	for i := range parts {
		suffix := strings.Join(parts[i:], "/")
		for _, g := range globs {
			if ok, _ := path.Match(g, suffix); ok {
				return true
			}
		}
	}
	return false
}

// ---------- the install pipeline ----------

type Progress struct {
	Phase string
	Done  int64
	Total int64
	File  string
}

type Result struct {
	Where   []string
	Kept    []string
	Skipped []string
	Notes   []string
	Ports   bool
}

func diskFree(p string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func (env *Env) Install(ctx context.Context, p *Port, plan *Plan, prog func(Progress)) (*Result, error) {
	if plan.Problem != "" {
		return nil, fmt.Errorf("%s", plan.Problem)
	}
	need := plan.Size() * 3
	if need == 0 {
		need = 60 << 20
	}
	if free := diskFree(env.SDRoot); free >= 0 && free < need {
		return nil, fmt.Errorf("not enough space on the SD card (%s free, about %s needed)", human(free), human(need))
	}
	dlDir := filepath.Join(env.DataDir, "dl")
	os.RemoveAll(dlDir)
	os.MkdirAll(dlDir, 0o755)
	defer os.RemoveAll(dlDir)

	old := env.LoadManifest(p)
	oldRec := map[string]FileRec{}
	if old != nil {
		for _, f := range old.Files {
			oldRec[f.P] = f
		}
	}
	var keepGlobs []string
	if p.Recipe != nil {
		keepGlobs = p.Recipe.Keep
	}

	res := &Result{}
	man := &Manifest{Name: p.Name, Repo: p.Repo, Version: plan.Version, Installed: time.Now()}
	written := map[string]bool{}
	kept := map[string]bool{}
	var totalDL int64
	for _, a := range plan.Files {
		totalDL += a.Size
	}
	var doneDL int64
	for i, a := range plan.Files {
		local := filepath.Join(dlDir, fmt.Sprintf("%d-%s", i, safeName(a.Name)))
		err := env.download(ctx, a.URL, local, func(n, t int64) {
			tot := totalDL
			if tot == 0 {
				tot = t
			}
			prog(Progress{Phase: "DOWNLOADING", Done: doneDL + n, Total: tot, File: a.Name})
		})
		if err != nil {
			return nil, err
		}
		if st, err := os.Stat(local); err == nil {
			doneDL += st.Size()
		}
		prog(Progress{Phase: "UNPACKING", File: a.Name})
		arc, err := openArchive(local)
		if err != nil {
			return nil, err
		}
		lay, err := DetectLayout(arc, p, plan.Snapshot)
		if err != nil {
			arc.closeFn()
			return nil, err
		}
		for _, w := range lay.Where {
			if !contains(res.Where, w) {
				res.Where = append(res.Where, w)
			}
		}
		err = env.extract(ctx, arc, lay, func(rel string, e *entry) (bool, string) {
			abs := env.safeDest(rel)
			if abs == "" {
				return false, ""
			}
			st, statErr := os.Stat(abs)
			exists := statErr == nil
			if !allowSystemWrite(rel, exists) {
				res.Skipped = append(res.Skipped, rel)
				return false, ""
			}
			if exists && !st.IsDir() {
				rec, tracked := oldRec[rel]
				userChanged := tracked && (rec.S != st.Size() || rec.M != st.ModTime().Unix())
				if matchKeep(rel, keepGlobs) || (userChanged && configExt[strings.ToLower(path.Ext(rel))]) {
					res.Kept = append(res.Kept, rel)
					written[rel] = true // still belongs to the app
					kept[rel] = true
					return false, ""
				}
			}
			return true, abs
		}, func(done, total int64) {
			prog(Progress{Phase: "UNPACKING", Done: done, Total: total, File: a.Name})
		}, written)
		arc.closeFn()
		if err != nil {
			return nil, err
		}
	}
	if len(written) == 0 {
		return nil, fmt.Errorf("the archive didn't contain anything to install")
	}
	// Remove files the previous version had but the new one doesn't.
	if old != nil {
		for _, f := range old.Files {
			if !written[f.P] {
				if abs := env.safeDest(f.P); abs != "" {
					os.Remove(abs)
				}
			}
		}
	}
	for rel := range written {
		if rec, ok := oldRec[rel]; ok && kept[rel] {
			man.Files = append(man.Files, rec) // keep the original stamp so the edit stays detectable
			continue
		}
		abs := env.safeDest(rel)
		if st, err := os.Stat(abs); err == nil {
			man.Files = append(man.Files, FileRec{P: rel, S: st.Size(), M: st.ModTime().Unix()})
		}
	}
	sort.Slice(man.Files, func(i, j int) bool { return man.Files[i].P < man.Files[j].P })
	man.Where = res.Where
	b, _ := json.MarshalIndent(man, "", " ")
	if err := writeFileAtomic(env.manifestPath(p), b); err != nil {
		return nil, fmt.Errorf("installed, but couldn't save the record: %v", err)
	}
	for rel := range written {
		if strings.HasSuffix(strings.ToLower(rel), ".notfound") {
			res.Notes = append(res.Notes, "Its Ports entry stays hidden until the game files are in place. After copying them, open Games → Ports and run '~Import ports'.")
			break
		}
	}
	env.afterChange(res)
	if p.Recipe != nil && p.Recipe.Note != "" {
		res.Notes = append(res.Notes, p.Recipe.Note)
	}
	return res, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func safeName(s string) string {
	return regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(s, "_")
}

// afterChange refreshes Onion's cached game lists when Ports changed.
func (env *Env) afterChange(res *Result) {
	for _, w := range res.Where {
		if strings.HasPrefix(w, "Roms/") {
			res.Ports = true
		}
	}
	if !res.Ports {
		return
	}
	portsDir := filepath.Join(env.SDRoot, "Roms", "PORTS")
	for _, f := range []string{"PORTS_cache6.db", "PORTS_cache2.db"} {
		os.Remove(filepath.Join(portsDir, f))
	}
	if _, err := os.Stat(filepath.Join(env.SDRoot, "Emu", "PORTS")); err != nil {
		res.Notes = append(res.Notes, "Ports need Onion's 'Ports Collection'. Turn it on in Apps → Package Manager → Verified.")
	}
}

func (env *Env) extract(ctx context.Context, a *archive, lay *Layout,
	decide func(rel string, e *entry) (bool, string), prog func(done, total int64), written map[string]bool) error {
	var total, done int64
	for _, e := range a.entries {
		if !e.dir {
			total += e.size
		}
	}
	write := func(e *entry, r io.Reader) error {
		rel, ok := lay.Map(e.name)
		if !ok || junk(e.name) || e.skip {
			return nil
		}
		rel = path.Clean(rel)
		if e.dir {
			if abs := env.safeDest(rel); abs != "" && allowSystemWrite(rel+"/", false) {
				os.MkdirAll(abs, 0o755)
			}
			return nil
		}
		okw, abs := decide(rel, e)
		if !okw {
			return nil
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			if e.size == 0 {
				return nil // an empty "file" standing in for a folder that already exists
			}
			return fmt.Errorf("the archive has a file named %s, but that's a folder on your card — not replacing it", rel)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("can't create %s: %v", path.Dir(rel), err)
		}
		tmp := abs + ".mxt"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("can't write %s: %v", rel, err)
		}
		_, err = io.Copy(f, &ctxReader{ctx: ctx, r: r, n: &done, every: func() { prog(done, total) }})
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			return fmt.Errorf("writing %s: %v", rel, err)
		}
		if st, err := os.Lstat(abs); err == nil && st.Mode().IsRegular() {
			os.Remove(abs) // FAT: rename doesn't replace
		}
		if err := os.Rename(tmp, abs); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("replacing %s: %v", rel, err)
		}
		written[rel] = true
		return nil
	}
	if a.tarPath != "" {
		f, err := os.Open(a.tarPath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		tr := tar.NewReader(gz)
		for _, e := range a.entries {
			if _, err := tr.Next(); err != nil {
				return err
			}
			if err := write(e, tr); err != nil {
				return err
			}
		}
		return nil
	}
	for _, e := range a.entries {
		if e.dir {
			if err := write(e, nil); err != nil {
				return err
			}
			continue
		}
		rc, err := e.open()
		if err != nil {
			return err
		}
		err = write(e, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type ctxReader struct {
	ctx   context.Context
	r     io.Reader
	n     *int64
	last  time.Time
	every func()
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, fmt.Errorf("cancelled")
	}
	n, err := c.r.Read(p)
	*c.n += int64(n)
	if time.Since(c.last) > 80*time.Millisecond {
		c.last = time.Now()
		c.every()
	}
	return n, err
}

func (env *Env) download(ctx context.Context, url, dest string, prog func(n, total int64)) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	cl := &http.Client{Transport: env.HTTP.Transport} // no overall timeout: files can be big
	resp, err := cl.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled")
		}
		return netHint(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	var n int64
	cr := &ctxReader{ctx: ctx, r: resp.Body, n: &n, every: func() { prog(n, resp.ContentLength) }}
	_, err = io.Copy(f, cr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	prog(n, resp.ContentLength)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled")
		}
		return netHint(err)
	}
	return nil
}

// Uninstall removes exactly the files Mixtape recorded, then prunes empty folders.
func (env *Env) Uninstall(p *Port) (int, error) {
	m := env.LoadManifest(p)
	if m == nil {
		return 0, fmt.Errorf("Mixtape didn't install this one, so it won't guess which files to delete")
	}
	dirs := map[string]bool{}
	removed := 0
	for _, f := range m.Files {
		abs := env.safeDest(f.P)
		if abs == "" {
			continue
		}
		if os.Remove(abs) == nil {
			removed++
		}
		for d := path.Dir(f.P); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	var list []string
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return strings.Count(list[i], "/") > strings.Count(list[j], "/") })
	for _, d := range list {
		if protectedDir(d) {
			continue
		}
		os.Remove(filepath.Join(env.SDRoot, filepath.FromSlash(d))) // only succeeds when empty
	}
	os.Remove(env.manifestPath(p))
	res := &Result{Where: m.Where}
	env.afterChange(res)
	return removed, nil
}

func protectedDir(d string) bool {
	parts := strings.Split(d, "/")
	if len(parts) <= 1 {
		return true
	}
	if strings.EqualFold(parts[0], "Roms") && len(parts) <= 3 {
		return true // Roms/PORTS, Roms/PORTS/Games, Roms/PORTS/Shortcuts
	}
	return strings.HasPrefix(d, ".tmp_update") && len(parts) <= 2
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func netHint(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "certificate has expired") || strings.Contains(s, "not yet valid"):
		return fmt.Errorf("secure connection failed — the clock looks wrong. Sync the time in Tweaks → System → Date and time")
	case strings.Contains(s, "no such host") || strings.Contains(s, "network is unreachable") || strings.Contains(s, "dial"):
		return fmt.Errorf("no network — turn Wi-Fi on in Settings → Network")
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return fmt.Errorf("the connection timed out — check Wi-Fi and try again")
	}
	return err
}
