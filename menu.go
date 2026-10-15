package main

// The MIXTAPE menu (SELECT): updating Mixtape itself, and the "Mixtape in
// Games" option (launcher.go).

import (
	"fmt"
	"strings"
)

type menuItem struct {
	label string
	value string // shown on the right ("ON", "OFF", a version)
	note  string
	act   func()
}

func (u *UI) openMenu() {
	u.modal = modalMenu
	u.gamesOn = u.env.GamesEntryOn()
	u.themeSt = u.env.ThemeState()
	if u.menuSel < 0 || u.menuSel >= len(u.menuItems()) {
		u.menuSel = 0
	}
}

func (u *UI) menuItems() []menuItem {
	upd := menuItem{label: "CHECK FOR A NEW MIXTAPE", value: "v" + version,
		note: "Mixtape also checks when it opens.",
		act: func() {
			u.modal = modalNone
			if u.selfNew != nil {
				u.confirmSelf()
			} else {
				u.checkSelf(true)
			}
		}}
	if u.selfNew != nil {
		upd.label = "UPDATE MIXTAPE"
		upd.value = u.selfNew.Version
		upd.note = "Your settings are kept and Mixtape restarts."
	}
	on := u.gamesOn
	val, note := "OFF", "Adds Mixtape to Games as its own system, so you can add it to Favorites and open it from the home screen."
	if on {
		val, note = "ON", "Mixtape is in Games → Mixtape. Add it to Favorites from there to keep it on the home screen."
	}
	games := menuItem{label: "MIXTAPE IN GAMES LIST", value: val, note: note,
		act: func() {
			if err := u.env.SetGamesEntry(!on); err != nil {
				u.message("TAPE JAM", colMagenta, "Couldn't change it: "+err.Error())
				return
			}
			u.gamesOn = u.env.GamesEntryOn()
			if !on {
				u.showToast("ADDED: GAMES → MIXTAPE")
			} else {
				u.showToast("REMOVED FROM GAMES")
			}
		}}
	items := []menuItem{upd}
	items = append(items, u.themeItems()...)
	items = append(items, games, u.syncItem())
	return items
}

func (u *UI) themeItems() []menuItem {
	st := u.themeSt
	if !st.Bundled && !st.Installed() {
		return nil
	}
	refresh := func() { u.themeSt = u.env.ThemeState() }
	install := menuItem{label: "INSTALL MIXTAPE THEME", value: "",
		note: "Adds the Mixtape theme to OnionOS. Then pick it in Apps → Theme Switcher.",
		act: func() {
			n, err := u.env.InstallTheme()
			refresh()
			if err != nil {
				u.message("TAPE JAM", colMagenta, "Couldn't install the theme: "+err.Error())
				return
			}
			body := fmt.Sprintf("Copied %d files to Themes/Mixtape.\n\nTo use it, open Apps → Theme Switcher and pick Mixtape.", n)
			if st.Active {
				body = "The theme you're using is up to date. The changes show up the next time OnionOS's menu opens."
			}
			u.message("THEME LOADED", colCyan, body)
		}}
	switch {
	case st.Outdated():
		install.label, install.value = "UPDATE MIXTAPE THEME", "v"+version
		install.note = "This Mixtape carries a newer version of the theme."
	case st.Installed():
		install.label, install.value = "MIXTAPE THEME", "INSTALLED"
		install.note = "It's in Apps → Theme Switcher."
		if st.Active {
			install.note = "OnionOS is using it now."
		}
		install.act = func() { u.showToast("THE THEME IS UP TO DATE") }
	}
	items := []menuItem{}
	if st.Bundled {
		items = append(items, install)
	}
	if st.Version != "" { // only what Mixtape installed itself
		items = append(items, menuItem{label: "REMOVE MIXTAPE THEME", value: "",
			note: "Deletes Themes/Mixtape. If you're using it, pick another theme first.",
			act: func() {
				err := u.env.RemoveTheme()
				refresh()
				if err != nil {
					u.message("TAPE JAM", colMagenta, "Couldn't remove the theme: "+err.Error())
					return
				}
				u.showToast("THEME REMOVED")
			}})
	}
	return items
}

func (u *UI) syncItem() menuItem {
	val := "NEVER"
	if !u.hb.Synced.IsZero() {
		val = u.hb.Synced.Local().Format("Jan 02 15:04")
	}
	if u.hbSyncing {
		val = "SYNCING"
	}
	return menuItem{label: "SYNC HOMEBREW HUB", value: val,
		note: "Mixtape checks Homebrew Hub for new games each time it opens; this checks again now.",
		act: func() {
			u.modal = modalNone
			u.startHBSync()
		}}
}

func (u *UI) keyMenu(k int) {
	items := u.menuItems()
	switch k {
	case BtnUp:
		u.menuSel = (u.menuSel + len(items) - 1) % len(items)
	case BtnDown:
		u.menuSel = (u.menuSel + 1) % len(items)
	case BtnA, BtnStart:
		items[u.menuSel].act()
	case BtnB, BtnSelect:
		u.modal = modalNone
	}
}

func (u *UI) drawMenu(c *Canvas) {
	items := u.menuItems()
	rowH := u.fSmall.height + u.px(16)
	w := c.W * 80 / 100
	noteLines := u.fTiny.Wrap(items[u.menuSel].note, w-u.px(40))
	h := u.px(112) + len(items)*rowH + len(noteLines)*(u.fTiny.height+u.px(2))
	x, y := u.panel(c, w, h, colAmber)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(14), "MIXTAPE", colAmber)
	c.TextRight(u.fTiny, x+w-u.px(20), y+u.px(22), "v"+version, colPaperDim)
	t := u.px(2)
	sy := y + u.px(48)
	c.Fill(x+u.px(20), sy, w-u.px(40), t, colAmber)
	c.Fill(x+u.px(20), sy+t, w-u.px(40), t, colMagenta)
	c.Fill(x+u.px(20), sy+2*t, w-u.px(40), t, colCyan)
	ly := sy + u.px(14)
	lx, lw := x+u.px(16), w-u.px(32)
	for i, it := range items {
		ry := ly + i*rowH
		fg, vc := colPaper, colAmber
		switch it.value {
		case "ON":
			vc = colGreen
		case "OFF":
			vc = colPaperDim
		}
		if strings.HasPrefix(it.label, "UPDATE") {
			vc = colMagenta
		}
		if it.value == "INSTALLED" {
			vc = colCyan
		}
		if i == u.menuSel {
			c.Fill(lx, ry, lw, rowH-u.px(4), colAmber)
			c.Fill(lx, ry, u.px(4), rowH-u.px(4), colCyan)
			fg, vc = colBG, colBG
		}
		c.Text(u.fSmall, lx+u.px(14), ry+u.px(5), it.label, fg)
		c.TextRight(u.fSmall, lx+lw-u.px(12), ry+u.px(5), it.value, vc)
	}
	ny := ly + len(items)*rowH + u.px(4)
	for _, l := range noteLines {
		c.Text(u.fTiny, x+u.px(20), ny, l, colPaperDim)
		ny += u.fTiny.height + u.px(2)
	}
	by := y + h - u.px(30)
	label := "OK"
	if strings.Contains(items[u.menuSel].label, "GAMES LIST") {
		label = "CHANGE"
	}
	bx := c.Button(u.fTiny, x+u.px(20), by, "A", label, colAmber)
	c.Button(u.fTiny, bx, by, "B", "CLOSE", colPaperDim)
}
