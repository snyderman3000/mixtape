package main

// Mixtape in Games: an option (off by default, in the SELECT menu) that adds
// Mixtape to OnionOS's Games list as a one-entry "system". From there it can
// be added to Favorites like any game, so it sits on the home screen.
//
// It's three small things OnionOS already understands:
//   Emu/MIXTAPE/config.json + launch.sh   the "emulator", which starts Mixtape
//   Roms/MIXTAPE/Mixtape.mixtape          its one "game"
//   Roms/MIXTAPE/Imgs/Mixtape.png         box art
// Turning the option off removes them again.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const launcherSystem = "MIXTAPE"

func (env *Env) launcherPaths() (emu, roms string) {
	return filepath.Join(env.SDRoot, "Emu", launcherSystem), filepath.Join(env.SDRoot, "Roms", launcherSystem)
}

// GamesEntryOn reports whether Mixtape is in the Games list.
func (env *Env) GamesEntryOn() bool {
	emu, roms := env.launcherPaths()
	b, err := os.ReadFile(filepath.Join(emu, "launch.sh"))
	if err != nil || !strings.Contains(string(b), "App/Mixtape") {
		return false
	}
	_, err = os.Stat(filepath.Join(roms, "Mixtape.mixtape"))
	return err == nil
}

// SetGamesEntry adds Mixtape to the Games list, or removes it.
func (env *Env) SetGamesEntry(on bool) error {
	emu, roms := env.launcherPaths()
	if !on {
		// Only remove what Mixtape put there; a folder of the same name made
		// by something else is left alone.
		if b, err := os.ReadFile(filepath.Join(emu, "launch.sh")); err == nil && !strings.Contains(string(b), "App/Mixtape") {
			return fmt.Errorf("Emu/%s belongs to something else, so Mixtape left it alone", launcherSystem)
		}
		for _, p := range []string{
			filepath.Join(emu, "config.json"), filepath.Join(emu, "launch.sh"),
			filepath.Join(roms, "Mixtape.mixtape"), filepath.Join(roms, "Imgs", "Mixtape.png"),
			filepath.Join(roms, "miyoogamelist.xml"),
			filepath.Join(roms, launcherSystem+"_cache6.db"), filepath.Join(roms, launcherSystem+"_cache2.db"),
		} {
			os.Remove(p)
		}
		for _, d := range []string{filepath.Join(roms, "Imgs"), roms, emu} {
			os.Remove(d) // only if empty
		}
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(emu, "launch.sh")); err == nil && !strings.Contains(string(b), "App/Mixtape") {
		return fmt.Errorf("there's already a different Emu/%s folder on the card", launcherSystem)
	}
	icon := filepath.Join(env.AppDir, "res", "mixtape.png")
	if _, err := os.Stat(icon); err != nil {
		icon = filepath.Join(env.AppDir, "icon.png")
	}
	// Absolute paths, as OnionOS writes them itself when it applies icons.
	icon = filepath.Join("/mnt/SDCARD", relToSD(env, icon))
	cfg := map[string]any{
		"label":     "Mixtape",
		"icon":      icon,
		"iconsel":   icon,
		"launch":    "launch.sh",
		"rompath":   "../../Roms/" + launcherSystem,
		"imgpath":   "../../Roms/" + launcherSystem + "/Imgs",
		"gamelist":  "../../Roms/" + launcherSystem + "/miyoogamelist.xml",
		"useswap":   0,
		"shortname": 0,
		"hidebios":  1,
		"extlist":   "mixtape",
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	launch := "#!/bin/sh\n# Added by Mixtape (\"Mixtape in Games\"). Starts Mixtape from the Games list.\n" +
		"cd /mnt/SDCARD/App/Mixtape || exit 1\nexec sh ./launch.sh\n"
	if err := os.MkdirAll(emu, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(roms, "Imgs"), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(emu, "config.json"), b); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(emu, "launch.sh"), []byte(launch)); err != nil {
		return err
	}
	os.Chmod(filepath.Join(emu, "launch.sh"), 0o755)
	if err := writeFileAtomic(filepath.Join(roms, "Mixtape.mixtape"), []byte("Opens Mixtape. Added by Mixtape's \"Mixtape in Games\" option.\n")); err != nil {
		return err
	}
	if art, err := os.ReadFile(filepath.Join(env.AppDir, "res", "boxart.png")); err == nil {
		_ = writeFileAtomic(filepath.Join(roms, "Imgs", "Mixtape.png"), art)
	}
	env.refreshRomList(launcherSystem)
	return nil
}

// relToSD turns a path on the card into one relative to the card's root.
func relToSD(env *Env, p string) string {
	if r, err := filepath.Rel(env.SDRoot, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	if r, err := filepath.Rel("/mnt/SDCARD", p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return "App/Mixtape/icon.png"
}
