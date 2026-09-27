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

const defaultCatalogURL = "https://raw.githubusercontent.com/Producdevity/MiyooMini-Ports/master/ports.json"

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

// LoadCatalog tries the live catalog, then the on-card cache, then the built-in snapshot.
func LoadCatalog(env *Env) *Catalog {
	recipes := loadRecipes(env.DataDir)
	cachePath := filepath.Join(env.DataDir, "ports.json")
	if !env.Offline {
		req, _ := http.NewRequest("GET", env.Config.CatalogURL, nil)
		req.Header.Set("User-Agent", userAgent)
		cl := &http.Client{Timeout: 12 * time.Second, Transport: env.HTTP.Transport}
		if resp, err := cl.Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				if ports, err := parseCatalog(b, recipes); err == nil {
					_ = writeFileAtomic(cachePath, b)
					return &Catalog{Ports: ports, Source: "live", Fetched: time.Now()}
				}
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

func writeFileAtomic(path string, b []byte) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
