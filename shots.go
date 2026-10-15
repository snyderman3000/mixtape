package main

// Desktop-only scene setup for rendering screenshots (-shot) without a device.

import (
	"fmt"
	"image"
	"os"
	"strings"
	"time"
)

func demoScene(u *UI, scene string) {
	u.env.Offline = true
	u.cat = LoadCatalog(u.env)
	u.cat.Source = "live"
	u.frame = 3
	find := func(name string) *Port {
		for _, p := range u.cat.Ports {
			if p.Name == name {
				return p
			}
		}
		return u.cat.Ports[0]
	}
	flex := find("PocketFlex")
	bal := find("Balatro")
	u.manifests[flex.Name] = &Manifest{Name: flex.Name, Version: "v0.4.2"}
	u.updates[flex.Name] = "v0.4.3"
	u.manifests[find("PocketFeeds").Name] = &Manifest{Name: "PocketFeeds", Version: "v0.1.1"}
	u.onCard[find("Mines of Moria").Name] = "App/MinesOfMoria"
	u.scr = scrList
	u.refilter()
	for i, p := range u.visible {
		if p == flex {
			u.sel[0] = i
		}
	}
	switch scene {
	case "boot":
		u.scr = scrBoot
		u.frame = 11
	case "list":
		u.seekUntil = time.Now().Add(time.Second)
	case "apps":
		u.tab = 1
		u.refilter()
		u.sel[1] = 4
	case "detail", "busy", "done", "error", "confirm":
		u.cur = bal
		if scene != "detail" {
			u.cur = flex
		}
		u.scr = scrDetail
		u.plans[bal.Name] = &Plan{Version: "v0.1.3", Date: time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC),
			Files: []Asset{{Name: "balatro-miyoo-mini-v0.1.3.zip", Size: 23 << 20}}}
		u.plans[flex.Name] = &Plan{Version: "v0.4.3", Date: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
			Files: []Asset{{Name: "PocketFlex-v0.4.3.zip", Size: 9 << 20}}}
		if path := os.Getenv("MIXTAPE_DEMO_IMG"); path != "" {
			if f, err := os.Open(path); err == nil {
				img, _, err := image.Decode(f)
				f.Close()
				if err == nil {
					w, h := u.shotBox()
					u.imgs[u.cur.Image] = fit(img, w, h)
				}
			}
		}
		switch scene {
		case "busy":
			u.modal = modalBusy
			u.busyName = flex.Name
			u.prog = Progress{Phase: "DOWNLOADING", Done: 6 << 20, Total: 9 << 20}
			u.frame = 9
		case "done":
			u.message("TRACK LOADED", colCyan, "Recorded to App/PocketFlex. Find it in Apps.\n\nKept 1 of your settings file(s).")
		case "error":
			u.message("TAPE JAM", colMagenta, "Install failed: no network — turn Wi-Fi on in Settings → Network")
		case "confirm":
			u.confirmQ = "ERASE POCKETFLEX FROM THE CARD?"
			u.confirmOK = "ERASE"
			u.modal = modalConfirm
		}
	case "self":
		u.selfNew = &Plan{Version: "v0.2.0", Files: []Asset{{Name: "Mixtape-v0.2.0-OnionOS.zip", Size: 3 << 20}}}
	case "self-confirm":
		u.selfNew = &Plan{Version: "v0.2.0", Files: []Asset{{Name: "Mixtape-v0.2.0-OnionOS.zip", Size: 3 << 20}}}
		u.confirmSelf()
	case "whatsnew", "newtab", "newdetail":
		now := time.Now()
		u.fresh = map[*Port]int64{}
		for i, name := range []string{"Spelunky Classic", "Mr. Rescue", "Flashback"} {
			p := find(name)
			u.fresh[p] = now.Add(-time.Duration(i) * 30 * time.Hour).Unix()
			u.arrivals = append(u.arrivals, p)
		}
		u.fresh[find("MiniAmp")] = now.Add(-6 * 24 * time.Hour).Unix()
		switch scene {
		case "whatsnew":
			u.wnList = u.arrivals
			u.modal = modalWhatsNew
			u.wnSel = 0
		case "newtab":
			u.tab = tabNew
			u.refilter()
		case "newdetail":
			u.tab = tabNew
			u.refilter()
			u.sel[tabNew] = 1
		}
	case "updates":
		var list []*Port
		for _, v := range []struct{ name, from, to string }{
			{"PocketFlex", "v0.4.2", "v0.4.3"}, {"Diddy Kong Racing", "1.0.0", "1.0.2"}, {"GoldenEye 007", "1.0.0", "1.0.2"}} {
			p := find(v.name)
			u.manifests[p.Name] = &Manifest{Name: p.Name, Version: v.from}
			u.updates[p.Name] = v.to
			list = append(list, p)
		}
		u.pendingUpd = list
		u.maybeShowUpdates()
	case "homebrew", "hbdetail", "hbsync", "hbdone", "hbfull":
		demoHomebrew(u, scene)
	case "offline-detail":
		u.cur = find("Moonlight")
		u.scr = scrDetail
		u.planErr[u.cur.Name] = "no network — turn Wi-Fi on in Settings → Network"
		u.imgErr[u.cur.Image] = true
	}
}

// demoHomebrew fills the HOMEBREW tab with a few Homebrew Hub entries.
func demoHomebrew(u *UI, scene string) {
	c := newHBCatalog()
	day := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for i, d := range []struct{ slug, title, dev, file, kind, desc string }{
		{"dreamnoid_starfall-advance", "Starfall Advance", "Dreamnoid", "files/StarfallAdvance_jam.gba", "game", "A monster-taming RPG made for GBA Jam 2026: explore, befriend creatures and battle your way across a fallen star."},
		{"pyro-pyro_antibody", "Antibody", "Pyro_Pyro, exelotl", "files/Antibody.gba", "game", ""},
		{"mtthgn_sun-rise", "Sun Rise", "mtthgn", "files/sun_rise.gba", "game", ""},
		{"thedarknessuser_spelunky-gba", "Spelunky GBA", "thedarknessuser", "files/SPELUNKYGBA_jam.gba", "game", ""},
		{"leanart_kuli", "Kuli", "leanart", "files/kuli_jam.gbc", "game", ""},
		{"messiahm_civ-gb", "CivGB Demo", "MessiahM", "files/CivGB Demo.gbc", "demo", ""},
		{"wrymouth_my-dvd-logo-daycare", "My DVD Logo Daycare!", "wrymouth", "files/dvd_logo.nes", "game", ""},
		{"johnybot_kart-racer", "Kart Racer", "johnybot", "files/kart_racer.nes", "game", ""},
		{"2048gb", "2048gb", "Sanqui", "2048.gb", "game", ""},
		{"1d-marathon", "1D Marathon", "VL2M STUDIO", "1D_Marathon.gb", "game", ""},
		{"tobu-tobu-girl", "Tobu Tobu Girl", "Tangram Games", "tobutobugirl.gb", "game", ""},
		{"gb-240p", "240p Test Suite", "PinoBatch", "gb240p.gb", "tool", ""},
		{"deadeus", "Deadeus", "IZMA", "deadeus.gb", "game", ""},
		{"ucity", "µCity", "AntonioND", "ucity.gbc", "game", ""},
	} {
		e := &HBEntry{Slug: d.slug, Title: d.title, Devs: strings.Split(d.dev, ", "), File: d.file, Type: d.kind,
			Desc: d.desc, Shots: []string{"cover.png", "1.png", "2.png"}, Added: day.Add(-time.Duration(i*i) * 24 * time.Hour).Unix(),
			Tags: []string{"pixel art", "2d"}, Site: "https://itch.io"}
		if d.kind == "tool" {
			e.Desc = "Test patterns for checking your screen and controls."
		}
		c.bySlug[e.Slug] = e
	}
	c.sortEntries()
	c.Synced = time.Now().Add(-2 * time.Minute)
	u.hb = c
	u.hbRecs["mtthgn_sun-rise"] = &HBRecord{Slug: "mtthgn_sun-rise", ROM: "Roms/GBA/Sun Rise.gba"}
	u.tab = tabHomebrew
	u.refilter()
	switch scene {
	case "homebrew":
		u.seekUntil = time.Now().Add(time.Second)
	case "hbfull": // a full list, with the NEW tab showing too
		for i := 0; i < 1607; i++ {
			e := &HBEntry{Slug: fmt.Sprint("x", i), Title: fmt.Sprint("Homebrew ", i), File: "x.gb", Added: day.Add(-time.Duration(30+i) * 24 * time.Hour).Unix()}
			c.bySlug[e.Slug] = e
		}
		c.sortEntries()
		u.refilter()
		u.fresh = map[*Port]int64{u.cat.Ports[0]: time.Now().Unix(), u.cat.Ports[1]: time.Now().Unix()}
		u.sel[tabHomebrew] = 1203
	case "hbsync":
		u.hb = newHBCatalog()
		u.refilter()
		u.hbSyncing = true
		u.hbProg = HBProgress{Full: true, Page: 58, Pages: 163}
	case "hbdetail", "hbdone":
		u.openHBDetail(u.hbVis[0])
		if path := os.Getenv("MIXTAPE_DEMO_IMG"); path != "" {
			if f, err := os.Open(path); err == nil {
				img, _, err := image.Decode(f)
				f.Close()
				if err == nil {
					w, h := u.shotBox()
					u.imgs[u.hbShotURL()] = fit(img, w, h)
				}
			}
		}
		u.imgBusy = map[string]bool{}
		if scene == "hbdone" {
			u.message("TRACK LOADED", colCyan, "Saved to Roms/GBA/Starfall Advance.gba. Find it in Games → Game Boy Advance.\n\nIts cover was added as box art.")
		}
	}
}
