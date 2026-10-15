package main

// The Mixtape OnionOS theme ships inside Mixtape (App/Mixtape/theme/Mixtape,
// drawn by theme/generate.py), so it updates along with Mixtape. The SELECT
// menu copies it to Themes/Mixtape, where Apps → Theme Switcher lists it.
// Mixtape never switches themes itself; it only installs, updates and removes.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const themeDir = "Themes/Mixtape"

// themePort stands for the theme in Mixtape's install records
// (data/installed/mixtape-theme.json), so Erase works the usual way.
var themePort = &Port{Name: "Mixtape Theme"}

func (env *Env) bundledTheme() string { return filepath.Join(env.AppDir, "theme", "Mixtape") }

// ThemeState is what the SELECT menu shows for the theme.
type ThemeState struct {
	Bundled bool   // this Mixtape carries the theme
	Version string // installed by Mixtape, at this Mixtape version ("" if not)
	Manual  bool   // Themes/Mixtape is there, but Mixtape didn't put it there
	Active  bool   // it's the theme OnionOS is using
}

func (t ThemeState) Installed() bool { return t.Version != "" || t.Manual }

// Outdated: the copy on the card is older than the one this Mixtape carries.
func (t ThemeState) Outdated() bool {
	return t.Bundled && (t.Manual || (t.Version != "" && t.Version != version))
}

func (env *Env) ThemeState() ThemeState {
	var st ThemeState
	if _, err := os.Stat(filepath.Join(env.bundledTheme(), "config.json")); err == nil {
		st.Bundled = true
	}
	if m := env.LoadManifest(themePort); m != nil {
		st.Version = m.Version
	} else if _, err := os.Stat(filepath.Join(env.SDRoot, filepath.FromSlash(themeDir), "config.json")); err == nil {
		st.Manual = true
	}
	st.Active = env.themeActive()
	return st
}

// themeActive reports whether OnionOS is currently using Themes/Mixtape. The
// current theme is in /appconfigs/system.json ("theme": "/mnt/SDCARD/Themes/X/").
func (env *Env) themeActive() bool {
	path := env.SystemConfig
	if path == "" {
		path = "/appconfigs/system.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		Theme string `json:"theme"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	return strings.TrimRight(filepath.ToSlash(cfg.Theme), "/") == strings.TrimRight("/mnt/SDCARD/"+themeDir, "/") ||
		strings.HasSuffix(strings.TrimRight(filepath.ToSlash(cfg.Theme), "/"), "/"+themeDir)
}

// InstallTheme copies the bundled theme to Themes/Mixtape (or updates it),
// recording the files so RemoveTheme deletes exactly those.
func (env *Env) InstallTheme() (int, error) {
	src := env.bundledTheme()
	if _, err := os.Stat(filepath.Join(src, "config.json")); err != nil {
		return 0, fmt.Errorf("this copy of Mixtape doesn't include the theme")
	}
	old := env.LoadManifest(themePort)
	man := &Manifest{Name: themePort.Name, Version: version, Installed: time.Now(), Where: []string{themeDir}}
	written := map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dest := themeDir + "/" + filepath.ToSlash(rel)
		abs := env.safeDest(dest)
		if abs == "" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := copyInto(p, abs); err != nil {
			return fmt.Errorf("writing %s: %v", dest, err)
		}
		written[dest] = true
		if st, err := os.Stat(abs); err == nil {
			man.Files = append(man.Files, FileRec{P: dest, S: st.Size(), M: st.ModTime().Unix()})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if old != nil { // files an older theme had that this one doesn't
		for _, f := range old.Files {
			if !written[f.P] && strings.HasPrefix(f.P, themeDir+"/") {
				if abs := env.safeDest(f.P); abs != "" {
					os.Remove(abs)
				}
			}
		}
	}
	sort.Slice(man.Files, func(i, j int) bool { return man.Files[i].P < man.Files[j].P })
	b, _ := json.MarshalIndent(man, "", " ")
	if err := writeFileAtomic(env.manifestPath(themePort), b); err != nil {
		return 0, fmt.Errorf("installed, but couldn't save the record: %v", err)
	}
	return len(man.Files), nil
}

// RemoveTheme deletes the theme Mixtape installed, unless it's in use.
func (env *Env) RemoveTheme() error {
	if env.themeActive() {
		return fmt.Errorf("it's the theme OnionOS is using right now. Pick another one in Apps → Theme Switcher first")
	}
	_, _, err := env.Uninstall(themePort)
	return err
}
