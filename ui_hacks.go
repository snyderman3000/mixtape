package main

// The HACKS tab: romhacks and translations patched onto ROMs already on the card.

import (
	"fmt"
	"image/color"
	"math"
	"path"
	"strings"
	"time"
)

const tabHacks = 4

var hackFilters = []string{"READY", "POKÉMON", "NES", "SNES", "GB", "GBA", "ALL"}

type hackState struct {
	cat      *HackCatalog
	loading  bool
	match    map[*Hack]*Match
	made     map[string]string
	scanned  bool
	scanning string // progress text while scanning
	filter   int
	vis      []*Hack
	cur      *Hack
	variant  int
}

// ---------- loading & scanning ----------

func (u *UI) loadHacks() {
	h := &u.hk
	if h.cat != nil || h.loading {
		return
	}
	h.loading = true
	go func() {
		cat := LoadHacks(u.env)
		u.post <- func() {
			h.loading = false
			h.cat = cat
			u.refilterHacks()
		}
	}()
}

func (u *UI) scanROMs() {
	h := &u.hk
	if h.scanning != "" {
		return
	}
	h.scanning = "SCANNING ROMS"
	go func() {
		roms, _ := u.env.ScanROMs(func(done, total int) {
			select {
			case u.post <- func() { h.scanning = fmt.Sprintf("SCANNING ROMS %d/%d", done, total) }:
			default:
			}
		})
		made := u.env.MadeHacks()
		u.post <- func() {
			h.scanning = ""
			h.scanned = true
			h.made = made
			if h.cat != nil {
				h.match = MatchHacks(h.cat.Hacks, roms)
			}
			u.hackROMs = roms
			u.refilterHacks()
			n := len(h.match)
			if n == 0 {
				u.showToast(fmt.Sprintf("%d ROMS CHECKED — NO MATCHES YET. Y: SHOW ALL", len(roms)))
			} else {
				u.showToast(fmt.Sprintf("%d ROMS CHECKED — %d HACKS READY", len(roms), n))
			}
		}
	}()
}

func (u *UI) enterHacksTab() {
	u.loadHacks()
	if !u.hk.scanned {
		u.scanROMs()
	}
	u.refilterHacks()
}

func (u *UI) refilterHacks() {
	h := &u.hk
	h.vis = h.vis[:0]
	if h.cat == nil {
		return
	}
	if h.match == nil && h.scanned {
		h.match = MatchHacks(h.cat.Hacks, u.hackROMs)
	}
	var ready, rest []*Hack
	for _, x := range h.cat.Hacks {
		ok := true
		switch hackFilters[h.filter] {
		case "READY":
			ok = h.match[x] != nil
		case "POKÉMON":
			ok = x.Pokemon
		case "NES", "SNES", "GB", "GBA":
			ok = x.System == hackFilters[h.filter]
		}
		if !ok {
			continue
		}
		if h.match[x] != nil {
			ready = append(ready, x)
		} else {
			rest = append(rest, x)
		}
	}
	h.vis = append(append(h.vis, ready...), rest...) // playable first, then by popularity
	if u.sel[tabHacks] >= len(h.vis) {
		u.sel[tabHacks] = max(0, len(h.vis)-1)
	}
}

func (u *UI) selectedHack() *Hack {
	if len(u.hk.vis) == 0 {
		return nil
	}
	return u.hk.vis[u.sel[tabHacks]]
}

// ---------- input ----------

func (u *UI) keyHackList(k int) bool {
	h := &u.hk
	switch k {
	case BtnY:
		h.filter = (h.filter + 1) % len(hackFilters)
		u.sel[tabHacks] = 0
		u.refilterHacks()
		return true
	case BtnX:
		if h.scanning == "" {
			h.match = nil
			h.scanned = false
			u.scanROMs()
		}
		return true
	case BtnA, BtnStart:
		if x := u.selectedHack(); x != nil {
			u.openHack(x)
		}
		return true
	}
	return false // let the normal list handle movement, tabs and B
}

func (u *UI) openHack(x *Hack) {
	u.hk.cur = x
	u.hk.variant = DefaultVariant(x, u.hk.match[x])
	u.scr = scrHackDetail
	u.detailScrl = 0
	if x.Screenshot != "" {
		u.fetchImageURL(archiveURL(x.Screenshot))
	}
}

func (u *UI) keyHackDetail(k int) {
	h := &u.hk
	x := h.cur
	switch k {
	case BtnB:
		u.scr = scrList
	case BtnLeft:
		if len(x.Variants) > 1 {
			h.variant = (h.variant + len(x.Variants) - 1) % len(x.Variants)
		}
	case BtnRight:
		if len(x.Variants) > 1 {
			h.variant = (h.variant + 1) % len(x.Variants)
		}
	case BtnL1, BtnR1:
		n := len(h.vis)
		if n == 0 {
			return
		}
		d := 1
		if k == BtnL1 {
			d = -1
		}
		u.sel[tabHacks] = (u.sel[tabHacks] + d + n) % n
		u.openHack(h.vis[u.sel[tabHacks]])
	case BtnUp:
		if u.detailScrl > 0 {
			u.detailScrl--
		}
	case BtnDown:
		u.detailScrl++
	case BtnA, BtnStart:
		if m := h.match[x]; m != nil {
			u.startPatch(x, h.variant, m)
		}
	case BtnX:
		if h.made[hackKey(x)] != "" {
			u.confirmQ = "ERASE THE PATCHED GAME " + strings.ToUpper(path.Base(h.made[hackKey(x)])) + "? YOUR ORIGINAL ROM IS NOT TOUCHED."
			u.confirmOK = "ERASE"
			u.confirmFn = func() {
				if err := u.env.EraseHack(x); err != nil {
					u.message("TAPE JAM", colMagenta, err.Error())
					return
				}
				h.made = u.env.MadeHacks()
				u.showToast("ERASED")
			}
			u.modal = modalConfirm
		}
	}
}

func (u *UI) startPatch(x *Hack, vi int, m *Match) {
	u.modal = modalBusy
	u.cancel = nil
	u.busyName = x.Title
	u.prog = Progress{Phase: "CONNECTING"}
	go func() {
		res, err := u.env.ApplyHack(x, vi, m, func(pr Progress) {
			select {
			case u.post <- func() { u.prog = pr }:
			default:
			}
		})
		u.post <- func() {
			if err != nil {
				u.message("TAPE JAM", colMagenta, "Patching failed: "+err.Error())
				return
			}
			u.hk.made = u.env.MadeHacks()
			folder := map[string]string{"NES": "Nintendo (NES)", "SNES": "Super Nintendo", "GB": "Game Boy", "GBA": "Game Boy Advance"}[x.System]
			if strings.Contains(res.Output, "/GBC/") {
				folder = "Game Boy Color"
			}
			lines := []string{"Saved as " + res.Output + ". Find it in Games → " + folder + ".", ""}
			if res.Verified {
				lines = append(lines, "✓ Checksum verified: the patched game is exactly what the author released.")
			} else {
				lines = append(lines, "Your ROM matched the checksum the hack was made for. (IPS patches can't confirm the result themselves.)")
			}
			lines = append(lines, "", "Your original ROM was not changed.")
			u.message("TRACK LOADED", colCyan, strings.Join(lines, "\n"))
		}
	}()
}

// fetchImageURL loads a preview image by URL into the shared image cache.
func (u *UI) fetchImageURL(url string) {
	if url == "" || u.imgs[url] != nil || u.imgBusy[url] {
		return
	}
	u.imgBusy[url] = true
	delete(u.imgErr, url)
	w, h := u.shotBox()
	go func() {
		img, err := u.env.Preview(url, w, h)
		u.post <- func() {
			u.imgBusy[url] = false
			if err != nil {
				u.imgErr[url] = true
				return
			}
			u.imgs[url] = img
		}
	}()
}

// ---------- drawing ----------

func sysColor(s string) color.RGBA {
	switch s {
	case "NES":
		return colRed
	case "SNES":
		return rgb(0xb4, 0x8c, 0xff)
	case "GB":
		return colGreen
	}
	return colCyan
}

func (u *UI) drawHackList(c *Canvas) {
	h := &u.hk
	u.drawTabs(c)
	top := u.listTop()
	lw := c.W * 58 / 100
	rows := u.rowsPerPage()
	rh := u.rowH()
	n := len(h.vis)
	sel := u.sel[tabHacks]
	sc := u.scroll[tabHacks]
	if sel < sc {
		sc = sel
	}
	if sel >= sc+rows {
		sc = sel - rows + 1
	}
	u.scroll[tabHacks] = sc
	c.Fill(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), colPanel)
	c.Corners(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), u.px(10), colAmberDim)
	cx := (lw + u.px(8)) / 2
	switch {
	case h.cat == nil:
		c.TextCenter(u.fBody, cx, top+u.px(60), "LOADING HACK CATALOG"+strings.Repeat(".", u.frame/4%4), colAmber)
	case n == 0 && hackFilters[h.filter] == "READY":
		if h.scanning != "" {
			c.TextCenter(u.fBody, cx, top+u.px(60), h.scanning, colAmber)
		} else {
			c.TextCenter(u.fBody, cx, top+u.px(50), "NO MATCHING ROMS YET", colPaperDim)
			for i, l := range u.fTiny.Wrap("Hacks show up here when a ROM they need is in Roms/FC, SFC, GB, GBC or GBA. Press Y to browse everything and see which ROM each hack needs.", lw-u.px(60)) {
				c.TextCenter(u.fTiny, cx, top+u.px(86)+i*(u.fTiny.height+u.px(2)), l, colAmberDim)
			}
		}
	case n == 0:
		c.TextCenter(u.fBody, cx, top+u.px(60), "NOTHING HERE", colPaperDim)
	}
	for i := 0; i < rows && sc+i < n; i++ {
		x := h.vis[sc+i]
		y := top + i*rh
		isSel := sc+i == sel
		fg, dim := colPaper, colAmberDim
		ready := h.match[x] != nil
		if !ready && !isSel {
			fg = colPaperDim
		}
		if isSel {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colAmber)
			c.Fill(u.px(10), y, u.px(4), rh-u.px(2), colCyan)
			fg, dim = colBG, colAmberDk
		} else if i%2 == 1 {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colPanel2)
		}
		// system tag instead of a track number
		sc2 := sysColor(x.System)
		if isSel {
			sc2 = dim
		}
		c.Text(u.fTiny, u.px(18), y+u.px(7), x.System, sc2)
		nx := u.px(62)
		rx := lw - u.px(18)
		mark := func(s string, col color.RGBA) {
			if isSel {
				col = colBG
			}
			w := u.fSmall.Width(s)
			rx -= w
			c.Text(u.fSmall, rx, y+u.px(5), s, col)
			rx -= u.px(8)
		}
		if h.made[hackKey(x)] != "" {
			mark("●", colCyan)
		} else if ready {
			mark("✓", colGreen)
		}
		if x.Kind == "translation" {
			mark("EN", colAmber)
		}
		c.Text(u.fBody, nx, y+u.px(3), u.fBody.Ellipsize(x.Title, rx-nx-u.px(4)), fg)
	}
	if n > rows {
		tx := lw - u.px(6)
		th := rows * rh
		c.Fill(tx, top, u.px(3), th, colGrid)
		bh := max(u.px(12), th*rows/n)
		by := top + (th-bh)*sc/max(1, n-rows)
		c.Fill(tx, by, u.px(3), bh, colAmber)
	}
	// right panel
	px := lw + u.px(10)
	pw := c.W - px - u.px(12)
	if x := u.selectedHack(); x != nil {
		a := 0.0
		if time.Now().Before(u.seekUntil) {
			a = u.angle()
		}
		prog := float64(sel) / math.Max(1, float64(n-1))
		c.Cassette(px, top-u.px(4), pw, u.px(150), x.Title, "HACK OF "+strings.ToUpper(x.Game), a, prog, u.fBody, u.fTiny)
		y := top + u.px(158)
		kv := func(k, v string, col color.RGBA) {
			c.Text(u.fTiny, px+u.px(4), y+u.px(2), k, colPaperDim)
			c.Text(u.fSmall, px+u.px(70), y, u.fSmall.Ellipsize(v, pw-u.px(74)), col)
			y += u.fSmall.height + u.px(4)
		}
		kv("SYSTEM", x.System, sysColor(x.System))
		kv("TYPE", x.KindLabel(), colPaper)
		switch m := h.match[x]; {
		case h.made[hackKey(x)] != "":
			kv("STATE", "PATCHED · ON CARD", colCyan)
		case m != nil:
			kv("YOUR ROM", "✓ "+path.Base(m.ROM.Path), colGreen)
		case h.scanning != "":
			kv("YOUR ROM", "CHECKING…", colAmber)
		default:
			kv("YOUR ROM", "NOT FOUND", colPaperDim)
		}
		if x.Downloads > 0 {
			kv("PLAYS", fmt.Sprintf("%d DOWNLOADS", x.Downloads), colPaper)
		}
		y += u.px(4)
		c.HLineDashed(px, y, pw, u.px(4), u.px(3), colGrid)
		y += u.px(8)
		for _, line := range u.fTiny.Wrap(x.Description, pw-u.px(4)) {
			if y+u.fTiny.height > c.H-u.px(40) {
				break
			}
			c.Text(u.fTiny, px+u.px(2), y, line, colPaperDim)
			y += u.fTiny.height + u.px(1)
		}
	}
	right := ""
	if h.scanning != "" {
		right = h.scanning
	} else if h.cat != nil {
		right = fmt.Sprintf("%d IN CATALOG", len(h.cat.Hacks))
	}
	u.footer(c, [][2]string{{"A", "OPEN"}, {"Y", "SHOW: " + hackFilters[h.filter]}, {"X", "RESCAN"}, {"B", "EXIT"}}, right)
}

func (u *UI) drawHackDetail(c *Canvas) {
	h := &u.hk
	x := h.cur
	top := u.headerH() + u.px(16)
	sw, sh := u.shotBox()
	fx, fy := u.px(14), top
	fw, fh := sw+u.px(16), sh+u.px(16)
	c.Fill(fx, fy, fw, fh, colPanel2)
	c.Border(fx, fy, fw, fh, 1, colGrid)
	c.Corners(fx-u.px(3), fy-u.px(3), fw+u.px(6), fh+u.px(6), u.px(12), sysColor(x.System))
	ix, iy := fx+u.px(8), fy+u.px(8)
	c.Fill(ix, iy, sw, sh, colBlack)
	shot := archiveURL(x.Screenshot)
	if img := u.imgs[shot]; img != nil {
		b := img.Bounds()
		// scale small retro screenshots up to fill the frame
		scale := math.Min(float64(sw)/float64(b.Dx()), float64(sh)/float64(b.Dy()))
		dw, dh := int(float64(b.Dx())*scale), int(float64(b.Dy())*scale)
		c.DrawImage(img, ix+(sw-dw)/2, iy+(sh-dh)/2, dw, dh)
		c.Scanlines(ix, iy, sw, sh, 0.28)
	} else {
		label, seed := "TUNING", uint32(u.frame)
		if x.Screenshot == "" || u.imgErr[shot] {
			label, seed = "NO SIGNAL", 7
		}
		c.Noise(ix, iy, sw, sh, seed)
		lw := u.fLCDBig.Width(label) + u.px(20)
		c.Fill(ix+(sw-lw)/2, iy+sh/2-u.px(22), lw, u.px(40), colBG)
		c.TextCenter(u.fLCDBig, ix+sw/2, iy+sh/2-u.px(18), label, colAmber)
	}
	c.Text(u.fTiny, fx, fy+fh+u.px(6), x.System+" · "+strings.ToUpper(x.KindLabel()), sysColor(x.System))
	c.TextRight(u.fTiny, fx+fw, fy+fh+u.px(6), "◀ L1  R1 ▶", colAmberDim)

	// info column
	ix2 := fx + fw + u.px(18)
	w := c.W - ix2 - u.px(14)
	y := top - u.px(2)
	for i, line := range u.fBig.Wrap(x.Title, w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBig, ix2, y, line, colAmber)
		y += u.fBig.height
	}
	by := "BY " + strings.ToUpper(x.Author)
	if x.Author == "" {
		by = "HACK OF " + strings.ToUpper(x.Game)
	}
	c.Text(u.fTiny, ix2, y+u.px(2), u.fTiny.Ellipsize(by, w), colCyan)
	y += u.fTiny.height + u.px(10)
	m := h.match[x]
	cx := c.Chip(u.fTiny, ix2, y, x.System, sysColor(x.System), false)
	if x.Version != "" {
		cx = c.Chip(u.fTiny, cx, y, "V"+strings.ToUpper(x.Version), colPaperDim, false)
	}
	if m != nil {
		c.Chip(u.fTiny, cx, y, "ROM FOUND", colGreen, true)
	} else {
		c.Chip(u.fTiny, cx, y, "NEEDS ROM", colMagenta, false)
	}
	y += u.fTiny.height + u.px(16)
	bh := u.px(100)
	c.Fill(ix2, y, w, bh, colPanel)
	c.Corners(ix2, y, w, bh, u.px(8), colAmberDim)
	ry := y + u.px(8)
	kv := func(k, v string, col color.RGBA) {
		c.Text(u.fTiny, ix2+u.px(10), ry+u.px(2), k, colPaperDim)
		c.Text(u.fSmall, ix2+u.px(84), ry, u.fSmall.Ellipsize(v, w-u.px(92)), col)
		ry += u.fSmall.height + u.px(3)
	}
	if m != nil {
		kv("YOUR ROM", path.Base(m.ROM.Path), colGreen)
	} else {
		kv("YOUR ROM", "NOT ON CARD", colPaperDim)
	}
	v := x.Variants[h.variant]
	if len(x.Variants) > 1 {
		kv("OPTION", fmt.Sprintf("%d/%d · %s", h.variant+1, len(x.Variants), v.Label), colAmber)
	}
	check := "IPS · ROM CHECKED"
	if v.TargetCRC != "" {
		check = strings.ToUpper(v.Format) + " · FULLY VERIFIED"
	}
	kv("PATCH", check, colPaper)
	if out := h.made[hackKey(x)]; out != "" {
		kv("MADE", path.Base(out), colCyan)
	}

	// liner notes
	ny := max(y+bh+u.px(8), fy+fh+u.px(28))
	nx, nw := u.px(14), c.W-u.px(28)
	bottom := c.H - u.px(38)
	var lines []string
	var cols []color.RGBA
	add := func(s string, col color.RGBA) {
		for _, l := range u.fSmall.Wrap(s, nw-u.px(20)) {
			lines = append(lines, l)
			cols = append(cols, col)
		}
	}
	if m == nil {
		need := x.Base.Name
		if need == "" || strings.EqualFold(need, "not found") {
			need = x.Game
		}
		add("▲ Needs your own copy of: "+need+". Put it in Roms/"+systemDirs[x.System][0]+" and press X on the list to rescan.", colMagenta)
	}
	add(x.Description, colPaper)
	if x.Patching != "" && !strings.EqualFold(x.Patching, "No Special Requirements") {
		add("Patching: "+x.Patching+" (Mixtape handles this.)", colPaperDim)
	}
	src := "Source: RomHacking.net archive"
	if x.ID > 0 {
		src += fmt.Sprintf(" · romhacking.net/hacks/%d", x.ID)
	}
	if x.Home != "" {
		src = "Source: " + strings.TrimPrefix(x.Home, "https://")
	}
	add(src, colCyanDim)
	c.Fill(nx, ny, nw, bottom-ny, colPanel)
	c.Text(u.fTiny, nx+u.px(10), ny+u.px(4), "LINER NOTES", colAmberDim)
	ly := ny + u.fTiny.height + u.px(8)
	maxLines := max(1, (bottom-ly)/(u.fSmall.height+u.px(1)))
	if u.detailScrl > max(0, len(lines)-maxLines) {
		u.detailScrl = max(0, len(lines)-maxLines)
	}
	for i := u.detailScrl; i < len(lines) && i-u.detailScrl < maxLines; i++ {
		c.Text(u.fSmall, nx+u.px(10), ly, lines[i], cols[i])
		ly += u.fSmall.height + u.px(1)
	}
	if len(lines) > maxLines {
		c.TextRight(u.fTiny, nx+nw-u.px(8), ny+u.px(4), "↕ MORE", colAmberDim)
	}
	items := [][2]string{}
	if m != nil {
		label := "PATCH"
		if h.made[hackKey(x)] != "" {
			label = "RE-PATCH"
		}
		items = append(items, [2]string{"A", label})
	}
	if len(x.Variants) > 1 {
		items = append(items, [2]string{"◀▶", "OPTION"})
	}
	if h.made[hackKey(x)] != "" {
		items = append(items, [2]string{"X", "ERASE"})
	}
	items = append(items, [2]string{"B", "BACK"})
	u.footer(c, items, "")
}
