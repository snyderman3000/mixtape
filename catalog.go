package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed assets/ports.json
var embeddedPorts []byte

//go:embed assets/recipes.json
var embeddedRecipes []byte

//go:embed catalog/extra.json
var embeddedExtra []byte

const defaultCatalogURL = "https://raw.githubusercontent.com/Producdevity/MiyooMini-Ports/master/ports.json"

// Extra ports maintained in the Mixtape repo itself (catalog/extra.json), for
// projects that aren't in the MiyooMini-Ports catalog yet. Same entry format,
// plus an optional inline "recipe".
const defaultExtraURL = "https://raw.githubusercontent.com/snyderman3000/mixtape/main/catalog/extra.json"

type Port struct {
	Name       string   `json:"name"`
	Categories []string `json:"categories"`
	Status     string   `json:"status"`
	Assets     string   `json:"assets"`
	Porter     []string `json:"porter"`
	Upstream   string   `json:"upstream"`
	Image      string   `json:"image"`
	Notes      string   `json:"notes"`

	Repo   string  `json:"-"` // owner/repo
	Recipe *Recipe `json:"-"`
	Index  int     `json:"-"`

	inlineRecipe *Recipe // install hints given in catalog/extra.json

}

type Recipe struct {
	Asset  string   `json:"asset"`
	All    bool     `json:"all"`
	URL    string   `json:"url"`
	AppDir string   `json:"appDir"`
	Manual string   `json:"manual"`
	Note   string   `json:"note"`
	Keep   []string `json:"keep"`
}

func (p *Port) IsApp() bool {
	return len(p.Categories) > 0 && p.Categories[0] == "app"
}

func (p *Port) Kind() string {
	if len(p.Categories) == 0 {
		return "MISC"
	}
	return strings.ToUpper(p.Categories[0])
}

func (p *Port) OwnFiles() bool { return p.Assets == "owned" }

func (p *Port) ManualReason() string {
	if p.Recipe != nil && p.Recipe.Manual != "" {
		return p.Recipe.Manual
	}
	if p.Status == "source-only" {
		return "Source only: there is no prebuilt binary to install."
	}
	if p.Repo == "" {
		return "This project isn't hosted on GitHub, so Mixtape can't fetch it."
	}
	return ""
}

var repoRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/#?]+)`)

type Catalog struct {
	Ports   []*Port
	Source  string // "live", "cached", "built-in"
	Fetched time.Time
}

func parseCatalog(data []byte, recipes map[string]*Recipe) ([]*Port, error) {
	var doc struct {
		Ports []*Port `json:"ports"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Ports) == 0 {
		return nil, fmt.Errorf("catalog is empty")
	}
	var out []*Port
	for _, p := range doc.Ports {
		if p == nil || p.Name == "" {
			continue
		}
		if m := repoRE.FindStringSubmatch(p.Upstream); m != nil {
			p.Repo = m[1] + "/" + strings.TrimSuffix(m[2], ".git")
			p.Recipe = recipes[strings.ToLower(p.Repo)]
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	for i, p := range out {
		p.Index = i
	}
	return out, nil
}

func loadRecipes(dataDir string) map[string]*Recipe {
	m := map[string]*Recipe{}
	raw := map[string]json.RawMessage{}
	_ = json.Unmarshal(embeddedRecipes, &raw)
	// A user-supplied recipes.json in the data folder overrides built-ins.
	if b, err := os.ReadFile(filepath.Join(dataDir, "recipes.json")); err == nil {
		extra := map[string]json.RawMessage{}
		if json.Unmarshal(b, &extra) == nil {
			for k, v := range extra {
				raw[k] = v
			}
		}
	}
	for k, v := range raw {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var r Recipe
		if json.Unmarshal(v, &r) == nil {
			m[strings.ToLower(k)] = &r
		}
	}
	return m
}

// LoadCatalog tries the live catalog, then the on-card cache, then the built-in snapshot,
// and adds Mixtape's own extra ports on top.
func LoadCatalog(env *Env) *Catalog {
	recipes := loadRecipes(env.DataDir)
	cat := loadMainCatalog(env, recipes)
	extra := loadExtra(env, recipes)
	cat.Ports = mergeExtra(cat.Ports, extra)
	return cat
}

func loadMainCatalog(env *Env, recipes map[string]*Recipe) *Catalog {
	cachePath := filepath.Join(env.DataDir, "ports.json")
	if !env.Offline {
		if b, err := fetchJSON(env, env.Config.CatalogURL, 4<<20); err == nil {
			if ports, err := parseCatalog(b, recipes); err == nil {
				_ = writeFileAtomic(cachePath, b)
				return &Catalog{Ports: ports, Source: "live", Fetched: time.Now()}
			}
		} else {
			env.LastNetErr = err
		}
	}
	if b, err := os.ReadFile(cachePath); err == nil {
		if ports, err := parseCatalog(b, recipes); err == nil {
			st, _ := os.Stat(cachePath)
			return &Catalog{Ports: ports, Source: "cached", Fetched: st.ModTime()}
		}
	}
	ports, _ := parseCatalog(embeddedPorts, recipes)
	return &Catalog{Ports: ports, Source: "built-in"}
}

func fetchJSON(env *Env, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	cl := &http.Client{Timeout: 12 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return b, nil
}

// parseExtra reads catalog/extra.json. Entries may carry an inline recipe;
// otherwise the usual recipes apply.
func parseExtra(data []byte, recipes map[string]*Recipe) ([]*Port, error) {
	var doc struct {
		Ports []json.RawMessage `json:"ports"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []*Port
	for _, raw := range doc.Ports {
		var p Port
		var r struct {
			Recipe *Recipe `json:"recipe"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Name == "" {
			continue
		}
		_ = json.Unmarshal(raw, &r)
		if m := repoRE.FindStringSubmatch(p.Upstream); m != nil {
			p.Repo = m[1] + "/" + strings.TrimSuffix(m[2], ".git")
			p.Recipe = recipes[strings.ToLower(p.Repo)]
		}
		if r.Recipe != nil {
			p.Recipe = r.Recipe
			p.inlineRecipe = r.Recipe
		}
		out = append(out, &p)
	}
	return out, nil
}

func loadExtra(env *Env, recipes map[string]*Recipe) []*Port {
	url := env.Config.ExtraURL
	if url == "" {
		url = defaultExtraURL
	}
	cachePath := filepath.Join(env.DataDir, "extra.json")
	if !env.Offline {
		if b, err := fetchJSON(env, url, 1<<20); err == nil {
			if ports, err := parseExtra(b, recipes); err == nil {
				_ = writeFileAtomic(cachePath, b)
				return ports
			}
		}
	}
	if b, err := os.ReadFile(cachePath); err == nil {
		if ports, err := parseExtra(b, recipes); err == nil {
			return ports
		}
	}
	ports, _ := parseExtra(embeddedExtra, recipes)
	return ports
}

// mergeExtra adds extra ports that the main catalog doesn't already list
// (matched by GitHub repo or name). When both list a project, the main
// catalog's entry is shown, but install hints from extra.json are kept, so
// how our own projects install stays under our control.
func mergeExtra(ports, extra []*Port) []*Port {
	byRepo := map[string]*Port{}
	byName := map[string]*Port{}
	for _, p := range ports {
		if p.Repo != "" {
			byRepo[strings.ToLower(p.Repo)] = p
		}
		byName[norm(p.Name)] = p
	}
	for _, p := range extra {
		var dup *Port
		if p.Repo != "" {
			dup = byRepo[strings.ToLower(p.Repo)]
		}
		if dup == nil {
			dup = byName[norm(p.Name)]
		}
		if dup != nil {
			if p.inlineRecipe != nil {
				dup.Recipe = p.inlineRecipe
			}
			continue
		}
		ports = append(ports, p)
	}
	sort.SliceStable(ports, func(i, j int) bool {
		return strings.ToLower(ports[i].Name) < strings.ToLower(ports[j].Name)
	})
	for i, p := range ports {
		p.Index = i
	}
	return ports
}

func writeFileAtomic(path string, b []byte) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
