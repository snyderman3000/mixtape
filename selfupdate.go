package main

// Self-update: Mixtape checks its own GitHub releases and can install a newer
// version over itself, then restarts (launch.sh loops while a restart flag exists).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const selfRepo = "snyderman3000/mixtape"

// selfPort describes Mixtape itself as an installable port.
func (env *Env) selfPort() *Port {
	r := loadRecipes(env.DataDir)[strings.ToLower(selfRepo)]
	if r == nil {
		r = &Recipe{Asset: "onionos", Keep: []string{"data/*"}}
	}
	return &Port{Name: "Mixtape", Repo: selfRepo, Recipe: r, Categories: []string{"app"}, Status: "playable", Assets: "free"}
}

func isSelf(p *Port) bool { return p != nil && strings.EqualFold(p.Repo, selfRepo) }

// parseVersion turns "v1.2.3" / "1.2.3-beta" into comparable numbers.
func parseVersion(s string) [3]int {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	for i, part := range strings.SplitN(s, ".", 3) {
		n, _ := strconv.Atoi(part)
		v[i] = n
	}
	return v
}

func newerVersion(latest, current string) bool {
	a, b := parseVersion(latest), parseVersion(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// SelfUpdatePlan returns a plan when a newer stable release exists, nil when up to date.
func (env *Env) SelfUpdatePlan(force bool) (*Plan, error) {
	rels, err := env.releases(selfRepo, force, force)
	if err != nil {
		return nil, err
	}
	p := env.selfPort()
	for _, rel := range rels {
		if rel.Draft || rel.Prerelease {
			continue
		}
		if !newerVersion(rel.Tag, version) {
			return nil, nil // newest stable release is not newer than us
		}
		files, problem := PickAssets(rel, p.Recipe)
		if len(files) == 0 {
			if problem == "" {
				problem = "the release has no OnionOS zip attached yet"
			}
			return nil, fmt.Errorf("%s", problem)
		}
		return &Plan{Version: rel.Tag, Date: rel.Published, Files: files}, nil
	}
	return nil, nil
}

// SelfInstall installs the plan over the running copy and verifies it landed in our own folder.
func (env *Env) SelfInstall(ctx context.Context, plan *Plan, prog func(Progress)) error {
	res, err := env.Install(ctx, env.selfPort(), plan, prog)
	if err != nil {
		return err
	}
	if !contains(res.Where, "App/"+filepath.Base(env.AppDir)) {
		return fmt.Errorf("the update installed to %v instead of App/%s — reinstall Mixtape by hand", res.Where, filepath.Base(env.AppDir))
	}
	return nil
}

// RequestRestart asks launch.sh to start Mixtape again after it exits.
func (env *Env) RequestRestart() {
	os.WriteFile(filepath.Join(env.DataDir, ".restart"), []byte("1"), 0o644)
}
