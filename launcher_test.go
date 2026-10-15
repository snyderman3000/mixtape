package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGamesEntry(t *testing.T) {
	root := t.TempDir()
	sd := filepath.Join(root, "SDCARD")
	app := filepath.Join(sd, "App", "Mixtape")
	os.MkdirAll(filepath.Join(app, "res"), 0o755)
	os.WriteFile(filepath.Join(app, "res", "mixtape.png"), []byte("png"), 0o644)
	os.WriteFile(filepath.Join(app, "res", "boxart.png"), []byte("box"), 0o644)
	os.MkdirAll(filepath.Join(sd, "Roms", "GBA"), 0o755) // something else on the card
	env := &Env{SDRoot: sd, AppDir: app}

	if env.GamesEntryOn() {
		t.Fatal("on by default")
	}
	if err := env.SetGamesEntry(true); err != nil {
		t.Fatal(err)
	}
	if !env.GamesEntryOn() {
		t.Fatal("not on after turning it on")
	}
	var cfg map[string]any
	b, _ := os.ReadFile(filepath.Join(sd, "Emu/MIXTAPE/config.json"))
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["icon"] != "/mnt/SDCARD/App/Mixtape/res/mixtape.png" || cfg["extlist"] != "mixtape" || cfg["rompath"] != "../../Roms/MIXTAPE" {
		t.Fatalf("config: %v", cfg)
	}
	l, _ := os.ReadFile(filepath.Join(sd, "Emu/MIXTAPE/launch.sh"))
	if !strings.Contains(string(l), "cd /mnt/SDCARD/App/Mixtape") || !strings.Contains(string(l), "./launch.sh") {
		t.Fatalf("launch.sh: %s", l)
	}
	if b, _ := os.ReadFile(filepath.Join(sd, "Roms/MIXTAPE/Imgs/Mixtape.png")); string(b) != "box" {
		t.Fatal("no box art")
	}
	// Turning it on again is harmless; off removes everything it added.
	if err := env.SetGamesEntry(true); err != nil {
		t.Fatal(err)
	}
	if err := env.SetGamesEntry(false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Emu/MIXTAPE", "Roms/MIXTAPE"} {
		if _, err := os.Stat(filepath.Join(sd, p)); err == nil {
			t.Fatalf("%s left behind", p)
		}
	}
	if _, err := os.Stat(filepath.Join(sd, "Roms/GBA")); err != nil {
		t.Fatal("removed something else")
	}

	// A different Emu/MIXTAPE isn't touched.
	os.MkdirAll(filepath.Join(sd, "Emu/MIXTAPE"), 0o755)
	os.WriteFile(filepath.Join(sd, "Emu/MIXTAPE/launch.sh"), []byte("#!/bin/sh\nother"), 0o755)
	if err := env.SetGamesEntry(true); err == nil {
		t.Fatal("overwrote another Emu/MIXTAPE")
	}
	if err := env.SetGamesEntry(false); err == nil {
		t.Fatal("removed another Emu/MIXTAPE")
	}
}

func TestMenu(t *testing.T) {
	fx := newFixture(t)
	fx.env.Offline = true
	fx.env.AppDir = filepath.Join(fx.env.SDRoot, "App", "Mixtape")
	u := NewUI(fx.env, 640, 480)
	c := NewCanvas(640, 480)
	u.cat = LoadCatalog(fx.env)
	u.scr = scrList
	u.refilter()
	u.Key(BtnSelect)
	if u.modal != modalMenu {
		t.Fatal("SELECT should open the menu")
	}
	u.Draw(c)
	u.Key(BtnDown)
	u.Key(BtnA) // turn on
	if !fx.env.GamesEntryOn() || !u.gamesOn {
		t.Fatal("not turned on")
	}
	u.Draw(c)
	u.Key(BtnA) // and off
	if fx.env.GamesEntryOn() {
		t.Fatal("not turned off")
	}
	u.Key(BtnB)
	if u.modal != modalNone {
		t.Fatal("B should close the menu")
	}
}
