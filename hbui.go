package main

// The HOMEBREW tab: Homebrew Hub's Game Boy, Game Boy Color, Game Boy Advance
// and NES homebrew (see homebrew.go), with a details screen to install or erase.

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"
)

type hbUI struct {
	hb       *HBCatalog // what the UI shows (a copy; the sync works on hbWork)
	hbWork   *HBCatalog
	hbVis    []*HBEntry
	hbFilter int
	hbRecs   map[string]*HBRecord
	hbCur    *HBEntry
	hbShot   int

	hbSyncing bool
	hbProg    HBProgress
	hbErr     string
}

func newHBUI() hbUI {
	return hbUI{hb: newHBCatalog(), hbRecs: map[string]*HBRecord{}}
}

// setHomebrew swaps in a new copy of the list, keeping the selection.
func (u *UI) setHomebrew(c *HBCatalog) {
	var keep *HBEntry
	if u.tab == tabHomebrew {
		keep = u.hbSelected()
	}
	u.hb = c
	if u.tab != tabHomebrew {
		return
	}
	u.refilterHB()
	if keep != nil {
		for i, e := range u.hbVis {
			if e.Slug == keep.Slug {
				u.sel[tabHomebrew] = i
			}
		}
	}
}

func (u *UI) refilterHB() {
	u.hbVis = u.hbVis[:0]
	want := hbFilters[u.hbFilter]
	for _, e := range u.hb.Entries {
		if want == "ALL" || e.SysLabel() == want {
			u.hbVis = append(u.hbVis, e)
		}
	}
	t := tabHomebrew
	if u.sel[t] >= len(u.hbVis) {
		u.sel[t] = len(u.hbVis) - 1
	}
	if u.sel[t] < 0 {
		u.sel[t] = 0
	}
}

func (u *UI) hbSelected() *HBEntry {
	if len(u.hbVis) == 0 || u.sel[tabHomebrew] >= len(u.hbVis) {
		return nil
	}
	return u.hbVis[u.sel[tabHomebrew]]
}

func (u *UI) hbAnimating() bool {
	if u.hbSyncing && u.tab == tabHomebrew {
		return true
	}
	if u.scr == scrHBDetail && u.hbCur != nil && len(u.hbCur.Shots) > 0 {
		return u.imgBusy[u.hbShotURL()]
	}
	return false
}

// syncHomebrew updates the list from Homebrew Hub in the background.
func (u *UI) syncHomebrew(work *HBCatalog) {
	if u.hbSyncing || work == nil || u.env.Offline {
		return
	}
	u.hbWork = work
	u.hbSyncing = true
	u.hbErr = ""
	u.hbProg = HBProgress{}
	go func() {
		lastCopy := time.Time{}
		added, err := u.env.SyncHomebrew(work, time.Now(), func(p HBProgress) {
			// Show new entries as they arrive, without copying the list on every page.
			var snap *HBCatalog
			if p.Added > 0 && time.Since(lastCopy) > 1500*time.Millisecond {
				snap = work.Copy()
				lastCopy = time.Now()
			}
			select {
			case u.post <- func() {
				u.hbProg = p
				if snap != nil {
					u.setHomebrew(snap)
				}
			}:
			default:
			}
		})
		snap := work.Copy()
		u.post <- func() {
			u.hbSyncing = false
			u.setHomebrew(snap)
			if err != nil {
				u.hbErr = strings.ToUpper(netHint(err).Error())
				if u.tab == tabHomebrew {
					u.showToast("HOMEBREW HUB: " + u.hbErr)
				}
				return
			}
			if added > 0 && len(snap.Entries) > added && u.tab == tabHomebrew {
				u.showToast(fmt.Sprintf("HOMEBREW HUB: %d NEW TITLE(S)", added))
			}
		}
	}()
}

// ---------- input ----------

func (u *UI) keyHBList(k int) {
	t := tabHomebrew
	n := len(u.hbVis)
	seek := func() { u.seekUntil = time.Now().Add(450 * time.Millisecond) }
	switch k {
	case BtnUp, BtnDown:
		if n > 0 {
			d := 1
			if k == BtnUp {
				d = -1
			}
			u.sel[t] = (u.sel[t] + d + n) % n
			seek()
		}
	case BtnLeft:
		if n > 0 {
			u.sel[t] = max(0, u.sel[t]-u.rowsPerPage())
			seek()
		}
	case BtnRight:
		if n > 0 {
			u.sel[t] = min(n-1, u.sel[t]+u.rowsPerPage())
			seek()
		}
	case BtnL1, BtnL2:
		u.tab = (u.tab + u.tabsShown() - 1) % u.tabsShown()
		u.refilter()
	case BtnR1, BtnR2:
		u.tab = (u.tab + 1) % u.tabsShown()
		u.refilter()
	case BtnX:
		u.hbFilter = (u.hbFilter + 1) % len(hbFilters)
		u.sel[t], u.scroll[t] = 0, 0
		u.refilterHB()
	case BtnA, BtnStart:
		if e := u.hbSelected(); e != nil {
			u.openHBDetail(e)
		}
	case BtnY:
		switch {
		case u.hbSyncing:
			u.showToast("ALREADY SYNCING WITH HOMEBREW HUB")
		case u.env.Offline || u.hbWork == nil:
			u.showToast("OFFLINE — TURN ON WI-FI AND REOPEN MIXTAPE")
		case !wifiUp() && u.env.SDRoot == "/mnt/SDCARD":
			u.showToast("WI-FI IS OFF — TURN IT ON IN SETTINGS → NETWORK")
		default:
			u.showToast("SYNCING WITH HOMEBREW HUB" + "…")
			u.syncHomebrew(u.hbWork)
		}
	case BtnB:
		u.quit = true
	}
}

func (u *UI) openHBDetail(e *HBEntry) {
	u.hbCur = e
	u.hbShot = 0
	u.scr = scrHBDetail
	u.detailScrl = 0
	u.fetchImageURL(u.hbShotURL())
}

func (u *UI) hbShotURL() string {
	e := u.hbCur
	if e == nil || len(e.Shots) == 0 {
		return ""
	}
	return u.env.hbFileURL(e.Slug, e.Shots[u.hbShot%len(e.Shots)])
}

func (u *UI) keyHBDetail(k int) {
	e := u.hbCur
	switch k {
	case BtnB:
		u.scr = scrList
	case BtnA, BtnStart:
		u.startHBInstall(e)
	case BtnX:
		if u.hbRecs[e.Slug] != nil {
			u.confirmQ = "ERASE " + strings.ToUpper(e.Title) + " FROM THE CARD? SAVES ARE KEPT."
			u.confirmOK = "ERASE"
			u.confirmFn = func() { u.doHBUninstall(e) }
			u.modal = modalConfirm
		}
	case BtnY:
		if len(e.Shots) > 1 {
			u.hbShot = (u.hbShot + 1) % len(e.Shots)
			u.fetchImageURL(u.hbShotURL())
		}
	case BtnUp:
		if u.detailScrl > 0 {
			u.detailScrl--
		}
	case BtnDown:
		u.detailScrl++
	case BtnL1, BtnLeft, BtnR1, BtnRight:
		n := len(u.hbVis)
		if n == 0 {
			return
		}
		d := 1
		if k == BtnL1 || k == BtnLeft {
			d = -1
		}
		u.sel[tabHomebrew] = (u.sel[tabHomebrew] + d + n) % n
		u.openHBDetail(u.hbVis[u.sel[tabHomebrew]])
	}
}

func (u *UI) startHBInstall(e *HBEntry) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.modal = modalBusy
	u.busyName = e.Title
	u.prog = Progress{Phase: "CONNECTING"}
	go func() {
		res, err := u.env.InstallHB(ctx, e, func(pr Progress) {
			select {
			case u.post <- func() {
				if u.prog.Phase != "ABORTING" {
					u.prog = pr
				}
			}:
			default:
			}
		})
		u.post <- func() {
			cancel()
			u.cancel = nil
			u.hbRecs = u.env.HBRecords()
			if err != nil {
				u.message("TAPE JAM", colMagenta, "Install failed: "+err.Error())
				return
			}
			sys := e.System()
			lines := []string{"Saved to " + res.ROM + ". Find it in Games → " + sys.Menu + "."}
			if res.Art {
				lines = append(lines, "", "Its cover was added as box art.")
			}
			for _, n := range res.Notes {
				lines = append(lines, "", n)
			}
			u.message("TRACK LOADED", colCyan, strings.Join(lines, "\n"))
		}
	}()
}

func (u *UI) doHBUninstall(e *HBEntry) {
	rec := u.hbRecs[e.Slug]
	err := u.env.UninstallHB(e.Slug)
	u.hbRecs = u.env.HBRecords()
	if err != nil {
		u.message("TAPE JAM", colMagenta, err.Error())
		return
	}
	body := "Removed " + e.Title + "."
	if rec != nil {
		body = "Removed " + rec.ROM + "."
	}
	u.message("ERASED", colAmber, body+" Saves and save states were left in place.")
}

// ---------- drawing ----------

func sysColor(label string) color.RGBA {
	switch label {
	case "GBA":
		return colMagenta
	case "GBC":
		return colCyan
	case "NES":
		return colRed
	}
	return colGreen
}

func (u *UI) hbStatusLine() string {
	switch {
	case u.hbSyncing && u.hbProg.Full && u.hbProg.Pages > 0:
		return fmt.Sprintf("SYNCING %d/%d", u.hbProg.Page, u.hbProg.Pages) + strings.Repeat("·", u.frame/4%4)
	case u.hbSyncing:
		return "SYNCING" + strings.Repeat("·", u.frame/4%4)
	case u.hbErr != "" && len(u.hb.Entries) > 0:
		return "HOMEBREW HUB · SAVED COPY"
	case !u.hb.Synced.IsZero():
		return "HOMEBREW HUB · " + u.hb.Synced.Local().Format("Jan 02 15:04")
	}
	return "HOMEBREW HUB"
}

func (u *UI) drawHBList(c *Canvas) {
	u.drawTabs(c)
	t := tabHomebrew
	top := u.listTop()
	lw := c.W * 58 / 100
	rows := u.rowsPerPage()
	rh := u.rowH()
	n := len(u.hbVis)
	sel := u.sel[t]
	sc := u.scroll[t]
	if sel < sc {
		sc = sel
	}
	if sel >= sc+rows {
		sc = sel - rows + 1
	}
	u.scroll[t] = sc
	c.Fill(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), colPanel)
	c.Corners(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), u.px(10), colAmberDim)
	if n == 0 {
		msg, sub := "NO TAPES HERE", "X TO CHANGE SYSTEM"
		switch {
		case len(u.hb.Entries) == 0 && u.hbSyncing:
			msg, sub = "TUNING IN TO HOMEBREW HUB", "THE FIRST VISIT TAKES ABOUT A MINUTE"
		case len(u.hb.Entries) == 0 && (u.env.Offline || u.hbErr != ""):
			msg, sub = "HOMEBREW HUB IS OUT OF RANGE", "TURN ON WI-FI AND REOPEN MIXTAPE"
		case len(u.hb.Entries) == 0:
			msg, sub = "NOTHING LOADED YET", "Y TO SYNC WITH HOMEBREW HUB"
		}
		c.TextCenter(u.fBody, (lw+u.px(8))/2, top+u.px(60), msg, colPaperDim)
		c.TextCenter(u.fTiny, (lw+u.px(8))/2, top+u.px(90), sub, colAmberDim)
		if len(u.hb.Entries) == 0 && u.hbSyncing && u.hbProg.Pages > 0 {
			frac := float64(u.hbProg.Page) / float64(u.hbProg.Pages)
			c.VU(u.px(30), top+u.px(124), lw-u.px(52), u.px(14), 20, frac)
		}
	}
	numW := u.fLCD.Width(strings.Repeat("0", max(2, len(fmt.Sprint(n))))) + u.px(10)
	for i := 0; i < rows && sc+i < n; i++ {
		e := u.hbVis[sc+i]
		y := top + i*rh
		x := u.px(14)
		isSel := sc+i == sel
		fg, dim := colPaper, colAmberDim
		if isSel {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colAmber)
			c.Fill(u.px(10), y, u.px(4), rh-u.px(2), colCyan)
			fg, dim = colBG, colAmberDk
		} else if i%2 == 1 {
			c.Fill(u.px(10), y, lw-u.px(18), rh-u.px(2), colPanel2)
		}
		c.Text(u.fLCD, x+u.px(4), y+u.px(2), fmt.Sprintf("%02d", sc+i+1), dim)
		nx := x + numW
		rx := lw - u.px(18)
		mark := func(s string, col color.RGBA) {
			if isSel {
				col = colBG
			}
			rx -= u.fSmall.Width(s)
			c.Text(u.fSmall, rx, y+u.px(5), s, col)
			rx -= u.px(8)
		}
		mark(e.SysLabel(), sysColor(e.SysLabel()))
		if u.hbRecs[e.Slug] != nil {
			mark("●", colCyan)
		}
		c.Text(u.fBody, nx, y+u.px(3), u.fBody.Ellipsize(e.Title, rx-nx-u.px(4)), fg)
	}
	if n > rows {
		tx := lw - u.px(6)
		th := rows * rh
		c.Fill(tx, top, u.px(3), th, colGrid)
		bh := max(u.px(12), th*rows/n)
		c.Fill(tx, top+(th-bh)*sc/max(1, n-rows), u.px(3), bh, colAmber)
	}
	// right panel
	px := lw + u.px(10)
	pw := c.W - px - u.px(12)
	if e := u.hbSelected(); e != nil {
		a := 0.0
		if time.Now().Before(u.seekUntil) {
			a = u.angle()
		}
		prog := float64(sel) / math.Max(1, float64(n-1))
		c.Cassette(px, top-u.px(4), pw, u.px(150), e.Title, "BY "+e.DevLine(), a, prog, u.fBody, u.fTiny)
		y := top + u.px(158)
		kv := func(k, v string, col color.RGBA) {
			c.Text(u.fTiny, px+u.px(4), y+u.px(2), k, colPaperDim)
			c.Text(u.fSmall, px+u.px(70), y, u.fSmall.Ellipsize(v, pw-u.px(74)), col)
			y += u.fSmall.height + u.px(4)
		}
		kv("SYSTEM", e.System().Label, sysColor(e.SysLabel()))
		kv("TYPE", e.Kind(), colPaper)
		if e.Added > 0 {
			kv("ADDED", time.Unix(e.Added, 0).Format("2006-01-02"), colPaper)
		}
		if r := u.hbRecs[e.Slug]; r != nil {
			kv("STATE", "ON CARD", colCyan)
		} else {
			kv("STATE", "NOT INSTALLED", colPaperDim)
		}
		y += u.px(4)
		c.HLineDashed(px, y, pw, u.px(4), u.px(3), colGrid)
		y += u.px(8)
		notes := e.Desc
		if notes == "" && len(e.Tags) > 0 {
			notes = "Tags: " + strings.Join(e.Tags, ", ")
		}
		bottom := c.H - u.px(40)
		for _, line := range u.fTiny.Wrap(notes, pw-u.px(4)) {
			if y+u.fTiny.height > bottom {
				break
			}
			c.Text(u.fTiny, px+u.px(2), y, line, colPaperDim)
			y += u.fTiny.height + u.px(1)
		}
	}
	u.footer(c, [][2]string{{"A", "PLAY"}, {"B", "EXIT"}, {"X", "SYS: " + hbFilters[u.hbFilter]}, {"Y", "SYNC"}}, u.hbStatusLine())
}

func (u *UI) drawHBDetail(c *Canvas) {
	e := u.hbCur
	top := u.headerH() + u.px(16)
	sw, sh := u.shotBox()
	fx, fy := u.px(14), top
	fw, fh := sw+u.px(16), sh+u.px(16)
	c.Fill(fx, fy, fw, fh, colPanel2)
	c.Border(fx, fy, fw, fh, 1, colGrid)
	c.Corners(fx-u.px(3), fy-u.px(3), fw+u.px(6), fh+u.px(6), u.px(12), colCyan)
	ix, iy := fx+u.px(8), fy+u.px(8)
	c.Fill(ix, iy, sw, sh, colBlack)
	url := u.hbShotURL()
	if img := u.imgs[url]; img != nil {
		b := img.Bounds()
		c.DrawImage(img, ix+(sw-b.Dx())/2, iy+(sh-b.Dy())/2, b.Dx(), b.Dy())
		c.Scanlines(ix, iy, sw, sh, 0.28)
	} else {
		label := "TUNING"
		seed := uint32(u.frame)
		if url == "" || u.imgErr[url] {
			label = "NO SIGNAL"
			seed = 7
		}
		c.Noise(ix, iy, sw, sh, seed)
		lw := u.fLCDBig.Width(label) + u.px(20)
		c.Fill(ix+(sw-lw)/2, iy+sh/2-u.px(22), lw, u.px(40), colBG)
		c.TextCenter(u.fLCDBig, ix+sw/2, iy+sh/2-u.px(18), label, colAmber)
	}
	if n := len(e.Shots); n > 1 {
		c.Text(u.fTiny, fx, fy+fh+u.px(6), fmt.Sprintf("SHOT %d/%d · Y", u.hbShot+1, n), colAmberDim)
	} else {
		c.Text(u.fTiny, fx, fy+fh+u.px(6), fmt.Sprintf("TRK %02d/%02d", u.sel[tabHomebrew]+1, len(u.hbVis)), colAmberDim)
	}
	c.TextRight(u.fTiny, fx+fw, fy+fh+u.px(6), "◀ L1  R1 ▶", colAmberDim)

	x := fx + fw + u.px(18)
	w := c.W - x - u.px(14)
	y := top - u.px(2)
	for i, line := range u.fBig.Wrap(e.Title, w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBig, x, y, line, colAmber)
		y += u.fBig.height
	}
	c.Text(u.fTiny, x, y+u.px(2), u.fTiny.Ellipsize("BY "+e.DevLine(), w), colCyan)
	y += u.fTiny.height + u.px(10)
	cx := c.Chip(u.fTiny, x, y, e.SysLabel(), sysColor(e.SysLabel()), false)
	cx = c.Chip(u.fTiny, cx, y, e.Kind(), colPaperDim, false)
	c.Chip(u.fTiny, cx, y, "FREE", colCyan, true)
	y += u.fTiny.height + u.px(16)
	c.Fill(x, y, w, u.px(100), colPanel)
	c.Corners(x, y, w, u.px(100), u.px(8), colAmberDim)
	ry := y + u.px(8)
	kv := func(k, v string, col color.RGBA) {
		c.Text(u.fTiny, x+u.px(10), ry+u.px(2), k, colPaperDim)
		c.Text(u.fSmall, x+u.px(84), ry, u.fSmall.Ellipsize(v, w-u.px(92)), col)
		ry += u.fSmall.height + u.px(3)
	}
	kv("SOURCE", "HOMEBREW HUB", colPaper)
	if e.Added > 0 {
		kv("ADDED", time.Unix(e.Added, 0).Format("2006-01-02"), colPaper)
	}
	if e.License != "" {
		kv("LICENSE", strings.ToUpper(e.License), colPaper)
	}
	if r := u.hbRecs[e.Slug]; r != nil {
		kv("STATE", "ON CARD", colCyan)
	} else {
		kv("STATE", "→ ROMS/"+e.System().Folder, colPaperDim)
	}
	y += u.px(108)

	ny := max(y, fy+fh+u.px(28))
	nx, nw := u.px(14), c.W-u.px(28)
	bottom := c.H - u.px(38)
	var lines []string
	var colors []color.RGBA
	add := func(s string, col color.RGBA) {
		for _, l := range u.fSmall.Wrap(s, nw-u.px(20)) {
			lines = append(lines, l)
			colors = append(colors, col)
		}
	}
	if r := u.hbRecs[e.Slug]; r != nil {
		add("On your card as "+r.ROM+".", colCyan)
	}
	if e.Desc != "" {
		add(e.Desc, colPaper)
	}
	if len(e.Tags) > 0 {
		add("Tags: "+strings.Join(e.Tags, ", "), colPaperDim)
	}
	if e.Site != "" {
		add("WEB "+e.Site, colCyanDim)
	}
	if e.Repo != "" && e.Repo != e.Site {
		add("SRC "+e.Repo, colCyanDim)
	}
	add("Listed on Homebrew Hub (hh.gbdev.io), where it can also be played in a browser.", colPaperDim)
	c.Fill(nx, ny, nw, bottom-ny, colPanel)
	c.Text(u.fTiny, nx+u.px(10), ny+u.px(4), "LINER NOTES", colAmberDim)
	c.HLineDashed(nx+u.px(10)+u.fTiny.Width("LINER NOTES ")+u.px(4), ny+u.px(4)+u.fTiny.height/2, nw-u.px(120), u.px(4), u.px(3), colGrid)
	ly := ny + u.fTiny.height + u.px(8)
	maxLines := max(1, (bottom-ly)/(u.fSmall.height+u.px(1)))
	if u.detailScrl > max(0, len(lines)-maxLines) {
		u.detailScrl = max(0, len(lines)-maxLines)
	}
	for i := u.detailScrl; i < len(lines) && i-u.detailScrl < maxLines; i++ {
		c.Text(u.fSmall, nx+u.px(10), ly, lines[i], colors[i])
		ly += u.fSmall.height + u.px(1)
	}
	if len(lines) > maxLines {
		c.TextRight(u.fTiny, nx+nw-u.px(8), ny+u.px(4), "↕ MORE", colAmberDim)
	}
	label := "INSTALL"
	if u.hbRecs[e.Slug] != nil {
		label = "REINSTALL"
	}
	items := [][2]string{{"A", label}}
	if u.hbRecs[e.Slug] != nil {
		items = append(items, [2]string{"X", "ERASE"})
	}
	if len(e.Shots) > 1 {
		items = append(items, [2]string{"Y", "NEXT SHOT"})
	}
	items = append(items, [2]string{"B", "BACK"})
	u.footer(c, items, "")
}
