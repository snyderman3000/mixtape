package main

// Desktop-only scene setup for rendering screenshots (-shot) without a device.

import (
	"image"
	"os"
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
	case "hacks", "hack-detail", "hack-busy", "hack-done":
		hs, _ := parseHacks(embeddedHacks)
		u.hk.cat = &HackCatalog{Hacks: hs, Source: "live"}
		u.hk.scanned = true
		u.hk.match = map[*Hack]*Match{}
		u.hk.made = map[string]string{}
		for _, x := range hs {
			switch x.Title {
			case "Pokémon Throwback":
				u.hk.match[x] = &Match{ROM: &ROMFile{Path: "Roms/GBA/Pokemon - FireRed Version (USA).gba"}, Variant: 0}
			case "Pokemon Perfect Crystal - Original Version", "Super Mario Land DX", "Pokémon Crystal Legacy":
				u.hk.match[x] = &Match{ROM: &ROMFile{Path: "Roms/GBC/" + x.Game + ".gbc"}, Variant: 1}
			case "Super Metroid: Redesign", "Kaizo Mario World":
				u.hk.match[x] = &Match{ROM: &ROMFile{Path: "Roms/SFC/Super Mario World (USA).sfc"}, Variant: -1}
			}
			if x.Title == "Super Mario Land DX" {
				u.hk.made[hackKey(x)] = "Roms/GB/Super Mario Land DX.gb"
			}
		}
		u.tab = tabHacks
		u.refilterHacks()
		u.sel[tabHacks] = 1
		if scene != "hacks" {
			for _, x := range hs {
				if x.Title == "Pokémon Crystal Legacy" {
					u.hk.cur = x
					u.hk.variant = 1
				}
			}
			u.scr = scrHackDetail
			if path := os.Getenv("MIXTAPE_DEMO_IMG"); path != "" {
				if f, err := os.Open(path); err == nil {
					img, _, err := image.Decode(f)
					f.Close()
					if err == nil {
						w, h := u.shotBox()
						u.imgs[archiveURL(u.hk.cur.Screenshot)] = fit(img, w, h)
					}
				}
			}
		}
		switch scene {
		case "hack-busy":
			u.modal = modalBusy
			u.busyName = "Pokémon Crystal Legacy"
			u.prog = Progress{Phase: "PATCHING"}
		case "hack-done":
			u.message("TRACK LOADED", colCyan, "Saved as Roms/GBC/Pokémon Crystal Legacy v1.3.1 (Crystal (USA, Europe) Rev 1).gbc. Find it in Games → Game Boy Color.\n\n✓ Checksum verified: the patched game is exactly what the author released.\n\nYour original ROM was not changed.")
		}
	case "offline-detail":
		u.cur = find("Moonlight")
		u.scr = scrDetail
		u.planErr[u.cur.Name] = "no network — turn Wi-Fi on in Settings → Network"
		u.imgErr[u.cur.Image] = true
	}
}
