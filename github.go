package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []Asset   `json:"assets"`
}

// Plan is what Mixtape intends to download for a port.
type Plan struct {
	Version  string
	Date     time.Time
	Files    []Asset
	Snapshot bool   // no releases: using a source archive of the default branch
	Problem  string // non-empty: can't install on-device, with the reason
}

func (p *Plan) Size() int64 {
	var n int64
	for _, a := range p.Files {
		n += a.Size
	}
	return n
}

func (env *Env) api() string {
	if env.APIBase != "" {
		return env.APIBase
	}
	return "https://api.github.com"
}

type RateLimitError struct{ Reset time.Time }

func (e *RateLimitError) Error() string {
	return "GitHub rate limit reached — try again after " + e.Reset.Local().Format("15:04")
}

func (env *Env) ghGet(url string, v any) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if env.Config.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+env.Config.GitHubToken)
	}
	cl := &http.Client{Timeout: 20 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			sec, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
			return &RateLimitError{Reset: time.Unix(sec, 0)}
		}
	}
	if resp.StatusCode == 404 {
		return fmt.Errorf("repository not found (moved or deleted?)")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// Releases fetches (and caches for an hour) a repo's release list.
func (env *Env) Releases(repo string, force bool) ([]Release, error) {
	h := sha1.Sum([]byte(strings.ToLower(repo)))
	cache := filepath.Join(env.DataDir, "cache", "rel-"+hex.EncodeToString(h[:8])+".json")
	if !force {
		if st, err := os.Stat(cache); err == nil && time.Since(st.ModTime()) < time.Hour {
			var rs []Release
			if b, err := os.ReadFile(cache); err == nil && json.Unmarshal(b, &rs) == nil {
				return rs, nil
			}
		}
	}
	var rs []Release
	err := env.ghGet(env.api()+"/repos/"+repo+"/releases?per_page=8", &rs)
	if err != nil {
		log.Printf("release lookup for %s failed: %v", repo, err)
		// Fall back to a stale cache rather than nothing, except when the
		// user asked for a fresh check: then an old list would wrongly say
		// "up to date", so report the problem instead.
		if !force {
			if b, e2 := os.ReadFile(cache); e2 == nil && json.Unmarshal(b, &rs) == nil {
				return rs, nil
			}
		}
		return nil, err
	}
	if b, err := json.Marshal(rs); err == nil {
		_ = writeFileAtomic(cache, b)
	}
	return rs, nil
}

var badAssetWords = []string{"portmaster", "muos", "muxapp", "allium", "spruce", "knulli", "rocknix", "trimui",
	"anbernic", "rg35", "rg40", "x86_64", "x86-64", "amd64", "aarch64", "arm64", "windows", "win64", "macos",
	"darwin", "source", "src", "debug", "symbols", "sha256", ".sig", ".asc", "arkos", "batocera", "garlic", "minui", "nextui"}

func assetScore(name string, want string) int {
	n := strings.ToLower(name)
	if !(strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz")) {
		return -1000
	}
	s := 0
	if want != "" {
		if strings.Contains(n, strings.ToLower(want)) {
			s += 200
		} else {
			s -= 300
		}
	}
	for _, w := range badAssetWords {
		if strings.Contains(n, w) {
			s -= 100
		}
	}
	if strings.Contains(n, "onion") {
		s += 50
	}
	for _, w := range []string{"miyoo", "mmp", "mini"} {
		if strings.Contains(n, w) {
			s += 20
			break
		}
	}
	if strings.Contains(n, "app") {
		s += 5
	}
	return s
}

// PickAssets chooses which release files to install.
func PickAssets(rel Release, r *Recipe) ([]Asset, string) {
	want := ""
	all := false
	if r != nil {
		want, all = r.Asset, r.All
	}
	best, bestScore := -1, -60
	var picks []Asset
	has7z := false
	for i, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".7z") {
			has7z = true
		}
		sc := assetScore(a.Name, want)
		if all && sc > 0 {
			picks = append(picks, a)
		}
		if sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if all && len(picks) > 0 {
		return picks, ""
	}
	if best >= 0 {
		return []Asset{rel.Assets[best]}, ""
	}
	if has7z {
		return nil, "This release is only published as a .7z archive, which Mixtape can't unpack yet. Extract it to the SD card root on a PC."
	}
	if len(rel.Assets) == 0 {
		return nil, ""
	}
	return nil, "None of this release's files look like an OnionOS build."
}

// PlanFor works out what to install for a port.
func (env *Env) PlanFor(p *Port, force bool) (*Plan, error) {
	if reason := p.ManualReason(); reason != "" {
		return &Plan{Problem: reason}, nil
	}
	if p.Recipe != nil && p.Recipe.URL != "" {
		base := p.Recipe.URL[strings.LastIndex(p.Recipe.URL, "/")+1:]
		return &Plan{Version: "latest", Files: []Asset{{Name: base, URL: p.Recipe.URL}}}, nil
	}
	rels, err := env.Releases(p.Repo, force)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.Draft {
			continue
		}
		files, problem := PickAssets(rel, p.Recipe)
		if len(files) == 0 && problem == "" {
			continue // release with no binaries; try an older one
		}
		v := rel.Tag
		if v == "" {
			v = rel.Name
		}
		return &Plan{Version: v, Date: rel.Published, Files: files, Problem: problem}, nil
	}
	// No usable releases: many small Onion apps are just an App/ folder in the repo.
	return &Plan{
		Version:  "main",
		Snapshot: true,
		Files:    []Asset{{Name: "source.zip", URL: "https://github.com/" + p.Repo + "/archive/HEAD.zip"}},
	}, nil
}
