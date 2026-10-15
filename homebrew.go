package main

// Homebrew: the HOMEBREW tab lists the Game Boy, Game Boy Color, Game Boy
// Advance and NES homebrew archived on Homebrew Hub (https://hh.gbdev.io),
// read from its public API (https://hh3.gbdev.io/api). Homebrew Hub collects
// the community databases behind gbdev.io, gbadev.org and nesdev.org.
//
// Nothing from Homebrew Hub is built into Mixtape. The first visit downloads
// the whole list (about 160 small pages, in the background) and keeps it in
// data/homebrew.json. Later visits only ask for what was added since. Once a
// month the list is fetched again in full, to pick up corrections and removals.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultHomebrewAPI = "https://hh3.gbdev.io/api"

// hbFullEvery is how often the whole list is fetched again.
const hbFullEvery = 30 * 24 * time.Hour

// hbMaxPages caps one sync pass (the full list is about 165 pages of 10).
const hbMaxPages = 400

// HBEntry is one Homebrew Hub entry, reduced to what Mixtape shows and installs.
type HBEntry struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Platform string   `json:"platform"` // GB, GBC, GBA, NES (as Homebrew Hub labels it)
	Type     string   `json:"type"`     // game, demo, tool, music
	Devs     []string `json:"devs,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	File     string   `json:"file"` // the playable ROM, relative to the entry
	Shots    []string `json:"shots,omitempty"`
	License  string   `json:"license,omitempty"`
	Site     string   `json:"site,omitempty"`
	Repo     string   `json:"repo,omitempty"`
	Desc     string   `json:"desc,omitempty"`
	Added    int64    `json:"added,omitempty"` // unix time it joined Homebrew Hub
	Base     string   `json:"base,omitempty"`  // the database it's in, e.g. database-gba (part of its file addresses)
}

// hbSystem is where a ROM goes in OnionOS.
type hbSystem struct {
	Folder string // Roms/<Folder> and Emu/<Folder>
	Label  string
	Ext    string // extension the ROM is saved with
	Menu   string // what OnionOS calls it in Games
}

// hbSystemFor picks the OnionOS folder from the ROM's file extension (some
// Game Boy Color entries are dual-mode .gb carts, which belong in GB).
func hbSystemFor(file string) (hbSystem, bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".gba":
		return hbSystem{"GBA", "GAME BOY ADVANCE", ".gba", "Game Boy Advance"}, true
	case ".gbc", ".cgb":
		return hbSystem{"GBC", "GAME BOY COLOR", ".gbc", "Game Boy Color"}, true
	case ".gb":
		return hbSystem{"GB", "GAME BOY", ".gb", "Game Boy"}, true
	case ".nes":
		return hbSystem{"FC", "NES", ".nes", "NES (FC)"}, true
	}
	return hbSystem{}, false
}

// System filters for the HOMEBREW tab (X cycles through them).
var hbFilters = []string{"ALL", "GBA", "GBC", "GB", "NES"}

func (e *HBEntry) System() hbSystem {
	s, _ := hbSystemFor(e.File)
	return s
}

// SysLabel is the short system name shown in lists: GBA, GBC, GB or NES.
func (e *HBEntry) SysLabel() string {
	switch e.System().Folder {
	case "FC":
		return "NES"
	case "":
		return strings.ToUpper(e.Platform)
	}
	return e.System().Folder
}

func (e *HBEntry) DevLine() string {
	if len(e.Devs) == 0 {
		return "UNKNOWN DEVELOPER"
	}
	return strings.ToUpper(strings.Join(e.Devs, ", "))
}

// Kind is the entry's type for display.
func (e *HBEntry) Kind() string {
	if e.Type == "" {
		return "GAME"
	}
	return strings.ToUpper(e.Type)
}

// ---------- parsing the API's manifests ----------

// parseHHEntry turns one Homebrew Hub manifest into an HBEntry. ok is false for
// entries Mixtape doesn't list: no playable ROM for a supported system,
// hacks of commercial games (typetag "hackrom"), or entries flagged nsfw.
func parseHHEntry(raw json.RawMessage) (e *HBEntry, ok bool) {
	var d struct {
		Slug        string          `json:"slug"`
		Title       string          `json:"title"`
		Platform    string          `json:"platform"`
		Typetag     string          `json:"typetag"`
		Developer   json.RawMessage `json:"developer"`
		Tags        json.RawMessage `json:"tags"`
		Files       json.RawMessage `json:"files"`
		Screenshots json.RawMessage `json:"screenshots"`
		License     json.RawMessage `json:"license"`
		GameLicense json.RawMessage `json:"gameLicense"`
		Website     json.RawMessage `json:"website"`
		Repository  json.RawMessage `json:"repository"`
		Description json.RawMessage `json:"description"`
		FirstAdded  string          `json:"firstadded_date"`
		Basepath    string          `json:"basepath"`
		NSFW        json.RawMessage `json:"nsfw"`
	}
	if json.Unmarshal(raw, &d) != nil || d.Slug == "" {
		return nil, false
	}
	if strings.EqualFold(d.Typetag, "hackrom") || truthy(d.NSFW) {
		return nil, false
	}
	var files []struct {
		Default  bool   `json:"default"`
		Playable *bool  `json:"playable"`
		Filename string `json:"filename"`
	}
	_ = json.Unmarshal(d.Files, &files)
	// Like Homebrew Hub's own site: playable files, or every file when none
	// is marked playable; the default one first.
	anyPlayable := false
	for _, f := range files {
		if f.Playable != nil && *f.Playable {
			anyPlayable = true
		}
	}
	file := ""
	for _, pass := range []bool{true, false} {
		for _, f := range files {
			playable := !anyPlayable || (f.Playable != nil && *f.Playable)
			if !playable || (pass && !f.Default) || !safeEntryPath(f.Filename) {
				continue
			}
			if _, ok := hbSystemFor(f.Filename); ok {
				file = f.Filename
				break
			}
		}
		if file != "" {
			break
		}
	}
	if file == "" {
		return nil, false
	}
	e = &HBEntry{
		Slug:     d.Slug,
		Title:    strings.TrimSpace(d.Title),
		Platform: strings.ToUpper(strings.TrimSpace(d.Platform)),
		Type:     strings.ToLower(strings.TrimSpace(d.Typetag)),
		Devs:     jsonNames(d.Developer),
		Tags:     jsonNames(d.Tags),
		File:     file,
		License:  firstString(d.License, d.GameLicense),
		Site:     firstString(d.Website),
		Repo:     firstString(d.Repository),
		Desc:     clip(firstString(d.Description), 600),
	}
	if e.Title == "" {
		e.Title = d.Slug
	}
	if e.Platform == "" {
		e.Platform = e.SysLabel()
	}
	if baseRE.MatchString(d.Basepath) {
		e.Base = d.Basepath
	} else {
		e.Base = guessBase(e)
	}
	for _, s := range jsonNames(d.Screenshots) {
		if safeEntryPath(s) && len(e.Shots) < 8 {
			e.Shots = append(e.Shots, s)
		}
	}
	if t, err := time.Parse(time.RFC3339, d.FirstAdded); err == nil {
		e.Added = t.Unix()
	}
	return e, true
}

var baseRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// guessBase is the database an entry most likely comes from, for entries
// saved before Mixtape kept it.
func guessBase(e *HBEntry) string {
	switch e.SysLabel() {
	case "GBA":
		return "database-gba"
	case "NES":
		return "database-nes"
	}
	return "database-gb"
}

// safeEntryPath accepts a relative file path inside an entry ("files/game.gba").
func safeEntryPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "://") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "" {
			return false
		}
	}
	return true
}

func truthy(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.ToLower(strings.TrimSpace(s))
		return s == "true" || s == "yes" || s == "1"
	}
	return false
}

// names reads a field that may be a string, a list of strings, an object with
// a "name", or a list of such objects.
func jsonNames(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		add(s)
		return out
	}
	var obj struct {
		Name string `json:"name"`
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, item := range list {
			if json.Unmarshal(item, &s) == nil {
				add(s)
			} else if json.Unmarshal(item, &obj) == nil {
				add(obj.Name)
			}
		}
		return out
	}
	if json.Unmarshal(raw, &obj) == nil {
		add(obj.Name)
	}
	return out
}

func firstString(raws ...json.RawMessage) string {
	for _, r := range raws {
		if n := jsonNames(r); len(n) > 0 {
			return n[0]
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---------- the catalog and its on-card copy ----------

// hbState is how far syncing with Homebrew Hub has got.
type hbState struct {
	// Full pass: the whole list, in slug order (a stable order, so paging
	// can't skip anything). Resumes at NextPage if Mixtape was closed midway.
	Complete bool     `json:"complete"`
	NextPage int      `json:"next_page,omitempty"`
	Pass     []string `json:"pass,omitempty"` // slugs seen so far in the unfinished full pass
	FullAt   int64    `json:"full_at,omitempty"`
	// Offset is how many more entries the API reports than Mixtape counted at
	// the last full pass (duplicates and the like), so a mismatch that is
	// already explained doesn't trigger a full pass on every visit.
	Offset int `json:"offset,omitempty"`
}

type hbCacheFile struct {
	State   hbState    `json:"state"`
	Entries []*HBEntry `json:"entries"`
	Skipped []string   `json:"skipped,omitempty"` // slugs Mixtape doesn't list (counted for the check above)
	Synced  int64      `json:"synced,omitempty"`
}

type HBCatalog struct {
	Entries []*HBEntry // newest first
	bySlug  map[string]*HBEntry
	skipped map[string]bool
	state   hbState
	Synced  time.Time
}

func newHBCatalog() *HBCatalog {
	return &HBCatalog{bySlug: map[string]*HBEntry{}, skipped: map[string]bool{}}
}

func hbCachePath(env *Env) string { return filepath.Join(env.DataDir, "homebrew.json") }

// LoadHomebrew reads the on-card copy of the list (empty on the first visit).
func LoadHomebrew(env *Env) *HBCatalog {
	c := newHBCatalog()
	b, err := os.ReadFile(hbCachePath(env))
	if err != nil {
		return c
	}
	var f hbCacheFile
	if json.Unmarshal(b, &f) != nil {
		return c
	}
	c.state = f.State
	for _, e := range f.Entries {
		if e != nil && e.Slug != "" {
			if e.Base == "" {
				// Saved by the first test build, which didn't keep the
				// database name: guess it now and fetch the list again.
				e.Base = guessBase(e)
				c.state.Complete, c.state.NextPage, c.state.Pass = false, 1, nil
			}
			c.bySlug[e.Slug] = e
		}
	}
	for _, s := range f.Skipped {
		c.skipped[s] = true
	}
	if f.Synced > 0 {
		c.Synced = time.Unix(f.Synced, 0)
	}
	c.sortEntries()
	return c
}

func (c *HBCatalog) save(env *Env) error {
	f := hbCacheFile{State: c.state, Entries: c.Entries, Synced: c.Synced.Unix()}
	if c.Synced.IsZero() {
		f.Synced = 0
	}
	for s := range c.skipped {
		f.Skipped = append(f.Skipped, s)
	}
	sort.Strings(f.Skipped)
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return writeFileAtomic(hbCachePath(env), b)
}

func (c *HBCatalog) sortEntries() {
	c.Entries = c.Entries[:0]
	for _, e := range c.bySlug {
		c.Entries = append(c.Entries, e)
	}
	sort.SliceStable(c.Entries, func(i, j int) bool {
		a, b := c.Entries[i], c.Entries[j]
		if a.Added != b.Added {
			return a.Added > b.Added
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}

// known is every slug Homebrew Hub has listed, including the ones Mixtape skips.
func (c *HBCatalog) known() int { return len(c.bySlug) + len(c.skipped) }

func (c *HBCatalog) has(slug string) bool { return c.bySlug[slug] != nil || c.skipped[slug] }

// add records one raw entry; it reports whether the slug was new.
func (c *HBCatalog) add(raw json.RawMessage) (slug string, isNew bool) {
	var s struct {
		Slug string `json:"slug"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Slug == "" {
		return "", false
	}
	isNew = !c.has(s.Slug)
	if e, ok := parseHHEntry(raw); ok {
		c.bySlug[e.Slug] = e
		delete(c.skipped, e.Slug)
	} else {
		c.skipped[s.Slug] = true
		delete(c.bySlug, s.Slug)
	}
	return s.Slug, isNew
}

// Copy returns a snapshot the UI can hold while a sync keeps changing c.
func (c *HBCatalog) Copy() *HBCatalog {
	n := newHBCatalog()
	n.state = c.state
	n.Synced = c.Synced
	for k, v := range c.bySlug {
		n.bySlug[k] = v
	}
	for k := range c.skipped {
		n.skipped[k] = true
	}
	n.sortEntries()
	return n
}

// ---------- talking to Homebrew Hub ----------

func (env *Env) hbAPI() string {
	if env.Config.HomebrewAPI != "" {
		return strings.TrimRight(env.Config.HomebrewAPI, "/")
	}
	return defaultHomebrewAPI
}

// hbFileURL is where Homebrew Hub serves one of an entry's files (ROMs and
// screenshots): <host>/static/<database>/entries/<slug>/<file>, the same
// address Homebrew Hub's own site uses.
func (env *Env) hbFileURL(e *HBEntry, file string) string {
	host := strings.TrimSuffix(env.hbAPI(), "/api")
	base := e.Base
	if base == "" {
		base = guessBase(e)
	}
	parts := strings.Split(file, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return host + "/static/" + url.PathEscape(base) + "/entries/" + url.PathEscape(e.Slug) + "/" + strings.Join(parts, "/")
}

type hbPage struct {
	Results   int               `json:"results"`
	PageTotal int               `json:"page_total"`
	Page      int               `json:"page_current"`
	Entries   []json.RawMessage `json:"entries"`
}

func (env *Env) hbSearch(query url.Values) (*hbPage, error) {
	u := env.hbAPI() + "/search?" + query.Encode()
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	cl := &http.Client{Timeout: 20 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, netHint(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Homebrew Hub answered %s", resp.Status)
	}
	var p hbPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&p); err != nil {
		return nil, fmt.Errorf("Homebrew Hub sent something unexpected: %v", err)
	}
	return &p, nil
}

// HBProgress reports a sync in progress.
type HBProgress struct {
	Full  bool // fetching the whole list
	Page  int
	Pages int
	Added int // new entries so far
}

// SyncHomebrew brings c up to date with Homebrew Hub. It calls prog after each
// page with a copy-safe progress report, and saves the on-card copy as it goes,
// so a sync that's cut off resumes where it stopped. It returns how many new
// entries were found.
func (env *Env) SyncHomebrew(c *HBCatalog, now time.Time, prog func(HBProgress)) (int, error) {
	if prog == nil {
		prog = func(HBProgress) {}
	}
	added := 0
	// 1. Newest first, until we reach entries we already have.
	if c.state.Complete {
		first, err := env.hbSearch(url.Values{"page": {"1"}})
		if err != nil {
			return 0, err
		}
		var newest int64
		for _, e := range c.bySlug {
			newest = max(newest, e.Added)
		}
		page := first
		for n := 1; ; n++ {
			// Stop at a page with nothing new, or one that's entirely older
			// than anything already known.
			fresh, older := false, true
			for _, raw := range page.Entries {
				if _, isNew := c.add(raw); isNew {
					added++
					fresh = true
				}
				if e, ok := parseHHEntry(raw); !ok || e.Added == 0 || e.Added >= newest {
					older = false
				}
			}
			prog(HBProgress{Page: n, Pages: page.PageTotal, Added: added})
			if !fresh || older || n >= page.PageTotal || n >= 30 || len(page.Entries) == 0 {
				break
			}
			if page, err = env.hbSearch(url.Values{"page": {fmt.Sprint(n + 1)}}); err != nil {
				c.Synced = now
				c.sortEntries()
				_ = c.save(env)
				return added, err
			}
		}
		// Anything still missing (entries added out of order, or a batch that
		// shared one timestamp) shows up as a count mismatch: fetch everything.
		if first.Results > c.known()+c.state.Offset || now.Sub(time.Unix(c.state.FullAt, 0)) > hbFullEvery {
			c.state.Complete = false
			c.state.NextPage = 1
			c.state.Pass = nil
		}
	}
	// 2. The full list, in slug order.
	if !c.state.Complete {
		if c.state.NextPage < 1 {
			c.state.NextPage = 1
		}
		pass := map[string]bool{}
		for _, s := range c.state.Pass {
			pass[s] = true
		}
		for c.state.NextPage <= hbMaxPages {
			n := c.state.NextPage
			page, err := env.hbSearch(url.Values{"order_by": {"slug"}, "sort": {"asc"}, "page": {fmt.Sprint(n)}})
			if err != nil {
				c.state.Pass = keys(pass)
				c.sortEntries()
				_ = c.save(env)
				return added, err
			}
			if page.Page != 0 && page.Page != n {
				break // past the last page: the API answers with the last one again
			}
			for _, raw := range page.Entries {
				if slug, isNew := c.add(raw); slug != "" {
					pass[slug] = true
					if isNew {
						added++
					}
				}
			}
			c.state.NextPage = n + 1
			prog(HBProgress{Full: true, Page: n, Pages: page.PageTotal, Added: added})
			if n%10 == 0 {
				c.state.Pass = keys(pass)
				c.sortEntries()
				_ = c.save(env)
			}
			if n >= page.PageTotal || len(page.Entries) == 0 {
				// Done: drop what Homebrew Hub no longer lists.
				for s := range c.bySlug {
					if !pass[s] {
						delete(c.bySlug, s)
					}
				}
				for s := range c.skipped {
					if !pass[s] {
						delete(c.skipped, s)
					}
				}
				c.state = hbState{Complete: true, FullAt: now.Unix(), Offset: max(0, page.Results-c.known())}
				break
			}
		}
	}
	c.Synced = now
	c.sortEntries()
	return added, c.save(env)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
