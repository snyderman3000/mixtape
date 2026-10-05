package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"time"
)

type screen int

const (
	scrBoot screen = iota
	scrList
	scrDetail
)

type modalKind int

const (
	modalNone modalKind = iota
	modalBusy
	modalMsg
	modalConfirm
	modalWhatsNew
)

var tabNames = []string{"ALL", "APPS", "GAMES", "ON CARD", "NEW"}

const tabNew = 4 // only shown while something is fresh (see whatsnew.go)

type UI struct {
	env  *Env
	W, H int
	s    float64 // scale vs 640x480

	fLogo, fBig, fBody, fSmall, fTiny, fLCD, fLCDBig *Font

	scr     screen
	cat     *Catalog
	tab     int
	sel     [5]int
	scroll  [5]int
	visible []*Port

	cur        *Port
	plans      map[string]*Plan
	planErr    map[string]string
	planBusy   map[string]bool
	imgs       map[string]*image.RGBA
	imgErr     map[string]bool
	imgBusy    map[string]bool
	updates    map[string]string // port name -> new version
	manifests  map[string]*Manifest
	onCard     map[string]string
	checking   string
	seekUntil  time.Time
	detailScrl int

	modal     modalKind
	msgTitle  string
	msgBody   []string
	msgCol    color.RGBA
	confirmFn func()
	confirmQ  string
	confirmOK string
	busyName  string
	prog      Progress
	cancel    context.CancelFunc

	toast      string
	toastUntil time.Time

	fresh    map[*Port]int64 // entries added in the last freshDays -> when first seen
	arrivals []*Port         // added since the last visit (NEW ARRIVALS window)
	wnSel    int
	wnScroll int

	selfNew      *Plan
	selfChecking bool
	msgDone      func()

	frame int
	quit  bool
	post  chan func()
}

func NewUI(env *Env, w, h int) *UI {
	s := float64(h) / 480
	if sw := float64(w) / 640; sw < s {
		s = sw
	}
	u := &UI{env: env, W: w, H: h, s: s, post: make(chan func(), 64),
		plans: map[string]*Plan{}, planErr: map[string]string{}, planBusy: map[string]bool{},
		imgs: map[string]*image.RGBA{}, imgErr: map[string]bool{}, imgBusy: map[string]bool{},
		updates: map[string]string{}, manifests: map[string]*Manifest{}, onCard: map[string]string{}}
	u.fLogo = loadFont(titleTTF, 23*s)
	u.fBig = loadFont(titleTTF, 21*s)
	u.fBody = loadFont(bodyTTF, 18*s)
	u.fSmall = loadFont(bodyTTF, 15*s)
	u.fTiny = loadFont(bodyTTF, 13*s)
	u.fLCD = loadFont(lcdTTF, 24*s)
	u.fLCDBig = loadFont(lcdTTF, 34*s)
	return u
}

func (u *UI) px(v int) int { return int(math.Round(float64(v) * u.s)) }

// Start loads the catalog in the background.
func (u *UI) Start() {
	u.scr = scrBoot
	go func() {
		prev := previousCatalog(u.env) // before LoadCatalog refreshes the saved copies
		cat := LoadCatalog(u.env)
		u.post <- func() {
			u.cat = cat
			u.scanCard()
			u.noteNews(time.Now(), prev)
			u.scr = scrList
			u.refilter()
			if cat.Source != "live" {
				u.showToast("OFFLINE — USING " + strings.ToUpper(cat.Source) + " CATALOG")
			} else {
				u.checkSelf(false)
			}
		}
	}()
}

// noteNews records the catalog in seen.json and opens the NEW ARRIVALS window
// when something was added since the last visit.
func (u *UI) noteNews(now time.Time, previous map[string]bool) {
	n := noteCatalog(u.env, u.cat.Ports, now, previous)
	u.fresh = n.firstSeen
	u.arrivals = n.sinceLast
	u.wnSel, u.wnScroll = 0, 0
	if len(u.arrivals) > 0 {
		u.modal = modalWhatsNew
	}
}

// tabsShown is how many tabs the tab bar has: NEW only while something is fresh.
func (u *UI) tabsShown() int {
	if len(u.fresh) > 0 {
		return len(tabNames)
	}
	return tabNew
}

// checkSelf looks for a newer Mixtape release. manual=true comes from the SELECT button.
func (u *UI) checkSelf(manual bool) {
	if u.selfChecking {
		return
	}
	u.selfChecking = true
	if manual {
		u.showToast("CHECKING FOR A NEW MIXTAPE" + "…")
	}
	go func() {
		plan, err := u.env.SelfUpdatePlan(manual)
		u.post <- func() {
			u.selfChecking = false
			switch {
			case err != nil:
				if manual {
					u.message("TAPE JAM", colMagenta, "Couldn't check for Mixtape updates: "+netHint(err).Error())
				}
			case plan == nil:
				u.selfNew = nil
				if manual {
					u.showToast("MIXTAPE IS UP TO DATE (v" + version + ")")
				}
			default:
				u.selfNew = plan
				if manual {
					u.confirmSelf()
				} else {
					u.showToast("MIXTAPE " + plan.Version + " IS OUT — PRESS SELECT")
				}
			}
		}
	}()
}

func (u *UI) confirmSelf() {
	plan := u.selfNew
	size := ""
	if n := plan.Size(); n > 0 {
		size = " (" + human(n) + ")"
	}
	u.confirmQ = "UPDATE MIXTAPE FROM v" + version + " TO " + plan.Version + size + "? YOUR SETTINGS ARE KEPT AND MIXTAPE WILL RESTART."
	u.confirmOK = "UPDATE"
	u.confirmFn = func() { u.doSelfUpdate(plan) }
	u.modal = modalConfirm
}

func (u *UI) doSelfUpdate(plan *Plan) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.modal = modalBusy
	u.busyName = "Mixtape " + plan.Version
	u.prog = Progress{Phase: "CONNECTING"}
	go func() {
		err := u.env.SelfInstall(ctx, plan, func(pr Progress) {
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
			if err != nil {
				u.message("TAPE JAM", colMagenta, "Mixtape update failed: "+err.Error()+"\n\nYour current version still works.")
				return
			}
			u.selfNew = nil
			u.message("NEW TAPE LOADED", colCyan, "Mixtape "+plan.Version+" is installed. Press A to restart it.\n\nIf you land back in Onion's menu instead, just open Mixtape again.")
			u.msgDone = func() {
				u.env.RequestRestart()
				u.quit = true
			}
		}
	}()
}

func (u *UI) scanCard() {
	u.env.dirCache = map[string][]string{}
	for _, p := range u.cat.Ports {
		if m := u.env.LoadManifest(p); m != nil {
			u.manifests[p.Name] = m
			delete(u.onCard, p.Name)
		} else {
			delete(u.manifests, p.Name)
			if where := u.env.FoundOnCard(p); where != "" {
				u.onCard[p.Name] = where
			} else {
				delete(u.onCard, p.Name)
			}
		}
	}
}

func (u *UI) installed(p *Port) bool {
	return u.manifests[p.Name] != nil || u.onCard[p.Name] != ""
}

func (u *UI) refilter() {
	u.visible = u.visible[:0]
	if u.cat == nil {
		return
	}
	for _, p := range u.cat.Ports {
		switch u.tab {
		case 1:
			if !p.IsApp() {
				continue
			}
		case 2:
			if p.IsApp() {
				continue
			}
		case 3:
			if !u.installed(p) {
				continue
			}
		case tabNew:
			if u.fresh[p] == 0 {
				continue
			}
		}
		u.visible = append(u.visible, p)
	}
	if u.tab == tabNew {
		freshOrder(u.visible, u.fresh)
	}
	if u.sel[u.tab] >= len(u.visible) {
		u.sel[u.tab] = len(u.visible) - 1
	}
	if u.sel[u.tab] < 0 {
		u.sel[u.tab] = 0
	}
}

func (u *UI) tabCount(t int) int {
	if u.cat == nil {
		return 0
	}
	n := 0
	for _, p := range u.cat.Ports {
		switch {
		case t == 0, t == 1 && p.IsApp(), t == 2 && !p.IsApp(), t == 3 && u.installed(p), t == tabNew && u.fresh[p] > 0:
			n++
		}
	}
	return n
}

func (u *UI) selected() *Port {
	if len(u.visible) == 0 {
		return nil
	}
	return u.visible[u.sel[u.tab]]
}

func (u *UI) showToast(s string) {
	u.toast = s
	u.toastUntil = time.Now().Add(3 * time.Second)
}

func (u *UI) Animating() bool {
	return u.scr == scrBoot || u.modal == modalBusy || time.Now().Before(u.seekUntil) ||
		(u.toast != "" && time.Now().Before(u.toastUntil.Add(100*time.Millisecond))) ||
		(u.cur != nil && u.scr == scrDetail && (u.planBusy[u.cur.Name] || u.imgBusy[u.cur.Image])) ||
		u.checking != ""
}

// ---------- input ----------

func (u *UI) Key(k int) {
	if k == BtnMenu && u.modal != modalBusy {
		u.quit = true
		return
	}
	switch u.modal {
	case modalBusy:
		if k == BtnB && u.cancel != nil && !strings.HasPrefix(u.prog.Phase, "UNPACK") {
			u.cancel()
			u.prog.Phase = "ABORTING"
		}
		return
	case modalMsg:
		if k == BtnA || k == BtnB || k == BtnStart {
			u.modal = modalNone
			if f := u.msgDone; f != nil {
				u.msgDone = nil
				f()
			}
		}
		return
	case modalConfirm:
		if k == BtnA {
			u.modal = modalNone
			u.confirmFn()
		} else if k == BtnB {
			u.modal = modalNone
		}
		return
	case modalWhatsNew:
		u.keyWhatsNew(k)
		return
	}
	if k == BtnSelect && u.scr != scrBoot {
		u.checkSelf(true)
		return
	}
	switch u.scr {
	case scrList:
		u.keyList(k)
	case scrDetail:
		u.keyDetail(k)
	}
}

func (u *UI) keyList(k int) {
	n := len(u.visible)
	move := func(d int) {
		if n == 0 {
			return
		}
		u.sel[u.tab] = (u.sel[u.tab] + d + n) % n
		u.seekUntil = time.Now().Add(450 * time.Millisecond)
	}
	page := u.rowsPerPage()
	switch k {
	case BtnUp:
		move(-1)
	case BtnDown:
		move(1)
	case BtnLeft:
		if n > 0 {
			u.sel[u.tab] = max(0, u.sel[u.tab]-page)
			u.seekUntil = time.Now().Add(450 * time.Millisecond)
		}
	case BtnRight:
		if n > 0 {
			u.sel[u.tab] = min(n-1, u.sel[u.tab]+page)
			u.seekUntil = time.Now().Add(450 * time.Millisecond)
		}
	case BtnL1, BtnL2:
		u.tab = (u.tab + u.tabsShown() - 1) % u.tabsShown()
		u.refilter()
	case BtnR1, BtnR2:
		u.tab = (u.tab + 1) % u.tabsShown()
		u.refilter()
	case BtnA, BtnStart:
		if p := u.selected(); p != nil {
			u.openDetail(p)
		}
	case BtnY:
		u.checkUpdates()
	case BtnB:
		u.quit = true
	}
}

func (u *UI) keyWhatsNew(k int) {
	n := len(u.arrivals)
	switch k {
	case BtnUp:
		if n > 0 {
			u.wnSel = (u.wnSel + n - 1) % n
		}
	case BtnDown:
		if n > 0 {
			u.wnSel = (u.wnSel + 1) % n
		}
	case BtnA, BtnStart:
		// open it from the NEW tab, so L1/R1 in the detail view steps through the new ones
		u.modal = modalNone
		if n == 0 {
			return
		}
		p := u.arrivals[u.wnSel]
		u.tab = tabNew
		u.refilter()
		for i, q := range u.visible {
			if q == p {
				u.sel[tabNew] = i
			}
		}
		u.openDetail(p)
	case BtnB, BtnSelect:
		u.modal = modalNone
	}
}

func (u *UI) openDetail(p *Port) {
	u.cur = p
	u.scr = scrDetail
	u.detailScrl = 0
	u.fetchPlan(p, false)
	u.fetchImage(p)
}

func (u *UI) keyDetail(k int) {
	p := u.cur
	switch k {
	case BtnB:
		u.scr = scrList
	case BtnA, BtnStart:
		if isSelf(p) {
			u.checkSelf(true)
			return
		}
		if plan := u.plans[p.Name]; plan != nil && plan.Problem == "" && !u.planBusy[p.Name] {
			u.startInstall(p, plan)
		}
	case BtnX:
		if u.manifests[p.Name] != nil && !isSelf(p) {
			u.confirmQ = "ERASE " + strings.ToUpper(p.Name) + " FROM THE CARD?"
			u.confirmOK = "ERASE"
			u.confirmFn = func() { u.doUninstall(p) }
			u.modal = modalConfirm
		}
	case BtnY:
		u.fetchPlan(p, true)
	case BtnUp:
		if u.detailScrl > 0 {
			u.detailScrl--
		}
	case BtnDown:
		u.detailScrl++
	case BtnL1, BtnLeft:
		u.stepDetail(-1)
	case BtnR1, BtnRight:
		u.stepDetail(1)
	}
}

func (u *UI) stepDetail(d int) {
	n := len(u.visible)
	if n == 0 {
		return
	}
	u.sel[u.tab] = (u.sel[u.tab] + d + n) % n
	u.openDetail(u.visible[u.sel[u.tab]])
}

// ---------- background jobs ----------

func (u *UI) fetchPlan(p *Port, force bool) {
	if u.planBusy[p.Name] || (!force && u.plans[p.Name] != nil) {
		return
	}
	u.planBusy[p.Name] = true
	delete(u.planErr, p.Name)
	go func() {
		plan, err := u.env.PlanFor(p, force)
		u.post <- func() {
			u.planBusy[p.Name] = false
			if err != nil {
				u.planErr[p.Name] = netHint(err).Error()
				return
			}
			u.plans[p.Name] = plan
			if m := u.manifests[p.Name]; m != nil && plan.Problem == "" && plan.Version != "" && plan.Version != m.Version && !plan.Snapshot {
				u.updates[p.Name] = plan.Version
			} else {
				delete(u.updates, p.Name)
			}
		}
	}()
}

func (u *UI) fetchImage(p *Port) {
	url := p.Image
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

func (u *UI) checkUpdates() {
	if u.checking != "" || u.cat == nil {
		return
	}
	var todo []*Port
	for _, p := range u.cat.Ports {
		if u.manifests[p.Name] != nil {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		u.showToast("NOTHING INSTALLED BY MIXTAPE YET")
		return
	}
	u.checking = fmt.Sprintf("CHECKING 0/%d", len(todo))
	go func() {
		found := 0
		var lastErr error
		for i, p := range todo {
			plan, err := u.env.PlanFor(p, true)
			i, p := i, p
			if err != nil {
				lastErr = err
			}
			u.post <- func() {
				u.checking = fmt.Sprintf("CHECKING %d/%d", i+1, len(todo))
				if plan != nil {
					u.plans[p.Name] = plan
					m := u.manifests[p.Name]
					if m != nil && plan.Problem == "" && !plan.Snapshot && plan.Version != m.Version {
						u.updates[p.Name] = plan.Version
						found++
					}
				}
			}
			if _, ok := err.(*RateLimitError); ok {
				break
			}
		}
		u.post <- func() {
			u.checking = ""
			switch {
			case lastErr != nil && found == 0:
				u.showToast(strings.ToUpper(netHint(lastErr).Error()))
			case found == 0:
				u.showToast("ALL TAPES UP TO DATE")
			default:
				u.showToast(fmt.Sprintf("%d UPDATE(S) READY — MARKED ▲", found))
			}
		}
	}()
}

func (u *UI) startInstall(p *Port, plan *Plan) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.modal = modalBusy
	u.busyName = p.Name
	u.prog = Progress{Phase: "CONNECTING"}
	go func() {
		res, err := u.env.Install(ctx, p, plan, func(pr Progress) {
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
			u.scanCard()
			u.refilter()
			if err != nil {
				u.message("TAPE JAM", colMagenta, "Install failed: "+err.Error())
				return
			}
			delete(u.updates, p.Name)
			var lines []string
			where := strings.Join(res.Where, ", ")
			if res.Ports {
				lines = append(lines, "Recorded to "+where+". Find it in Games → Ports.")
			} else {
				lines = append(lines, "Recorded to "+where+". Find it in Apps.")
			}
			if p.OwnFiles() {
				lines = append(lines, "", "▲ This one needs your own game files: "+p.Notes)
			}
			for _, n := range res.Notes {
				lines = append(lines, "", n)
			}
			if len(res.Kept) > 0 {
				lines = append(lines, "", fmt.Sprintf("Kept %d of your settings file(s).", len(res.Kept)))
			}
			if len(res.Skipped) > 0 {
				lines = append(lines, "", fmt.Sprintf("Skipped %d file(s) that would have replaced Onion's own system files.", len(res.Skipped)))
			}
			u.message("TRACK LOADED", colCyan, strings.Join(lines, "\n"))
		}
	}()
}

func (u *UI) doUninstall(p *Port) {
	n, note, err := u.env.Uninstall(p)
	u.scanCard()
	u.refilter()
	if err != nil {
		u.message("TAPE JAM", colMagenta, err.Error())
		return
	}
	body := fmt.Sprintf("Removed %d file(s) for %s. Anything the app saved itself (settings, logs) was left in place.", n, p.Name)
	if note != "" {
		body += "\n\n" + note
	}
	u.message("ERASED", colAmber, body)
}

func (u *UI) message(title string, col color.RGBA, body string) {
	u.modal = modalMsg
	u.msgTitle = title
	u.msgCol = col
	u.msgBody = strings.Split(body, "\n")
}

// ---------- drawing ----------

func (u *UI) Draw(c *Canvas) {
	c.Fill(0, 0, c.W, c.H, colBG)
	switch u.scr {
	case scrBoot:
		u.drawBoot(c)
	case scrList:
		u.drawHeader(c)
		u.drawList(c)
	case scrDetail:
		u.drawHeader(c)
		u.drawDetail(c)
	}
	switch u.modal {
	case modalBusy:
		u.drawBusy(c)
	case modalMsg:
		u.drawMsg(c)
	case modalConfirm:
		u.drawConfirm(c)
	case modalWhatsNew:
		u.drawWhatsNew(c)
	}
	if u.toast != "" && time.Now().Before(u.toastUntil) {
		w := u.fSmall.Width(u.toast) + u.px(24)
		h := u.fSmall.height + u.px(10)
		x, y := (c.W-w)/2, c.H-u.px(34)-h-u.px(8)
		c.Fill(x, y, w, h, colPanel2)
		c.Border(x, y, w, h, 1, colCyan)
		c.Text(u.fSmall, x+u.px(12), y+u.px(5), u.toast, colCyan)
	}
	// whole-screen CRT feel: faint scanlines
	c.Scanlines(0, 0, c.W, c.H, 0.12)
}

func (u *UI) angle() float64 { return float64(u.frame) * 0.35 }

func (u *UI) drawStripes(c *Canvas, y int) {
	t := u.px(2)
	c.Fill(0, y, c.W, t, colAmber)
	c.Fill(0, y+t, c.W, t, colMagenta)
	c.Fill(0, y+2*t, c.W, t, colCyan)
}

func (u *UI) headerH() int { return u.px(44) }

func (u *UI) drawHeader(c *Canvas) {
	h := u.headerH()
	c.Fill(0, 0, c.W, h, colPanel)
	x := c.TextGlow(u.fLogo, u.px(14), u.px(9), "MIXTAPE", colAmber)
	if u.selfNew != nil {
		c.Text(u.fTiny, x+u.px(10), u.px(19), "▲ "+strings.ToUpper(u.selfNew.Version)+" READY · SELECT", colMagenta)
	} else {
		c.Text(u.fTiny, x+u.px(10), u.px(19), "// COMMUNITY PORT DECK", colCyan)
	}
	// right: battery, wifi, clock
	right := c.W - u.px(14)
	clock := time.Now().Format("15:04")
	cw := u.fLCD.Width(clock)
	c.TextGlow(u.fLCD, right-cw, u.px(9), clock, colAmber)
	right -= cw + u.px(14)
	// wifi bars
	up := wifiUp()
	for i := 0; i < 3; i++ {
		bh := u.px(6 + i*5)
		col := colCyan
		if !up {
			col = colGrid
		}
		c.Fill(right-u.px(22)+i*u.px(8), u.px(30)-bh, u.px(5), bh, col)
	}
	right -= u.px(34)
	if b := battery(); b >= 0 {
		s := fmt.Sprintf("%d%%", b)
		bw := u.px(30)
		bx := right - bw
		col := colPaperDim
		if b <= 15 {
			col = colRed
		}
		c.Border(bx, u.px(15), bw, u.px(14), 1, col)
		c.Fill(bx+bw, u.px(19), u.px(3), u.px(6), col)
		c.Fill(bx+u.px(2), u.px(17), (bw-u.px(4))*b/100, u.px(10), mix(colBG, col, 0.7))
		c.TextRight(u.fTiny, bx-u.px(6), u.px(15), s, col)
	}
	u.drawStripes(c, h)
}

func (u *UI) footer(c *Canvas, items [][2]string, right string) {
	fh := u.px(32)
	y := c.H - fh
	c.Fill(0, y, c.W, fh, colPanel)
	c.Fill(0, y, c.W, 1, colGrid)
	x := u.px(12)
	for _, it := range items {
		col := colAmber
		switch it[0] {
		case "B":
			col = colPaperDim
		case "X":
			col = colMagenta
		case "Y":
			col = colCyan
		}
		x = c.Button(u.fTiny, x, y+u.px(8), it[0], it[1], col)
	}
	if right != "" {
		c.TextRight(u.fTiny, c.W-u.px(12), y+u.px(9), right, colPaperDim)
	}
}

func (u *UI) rowH() int { return u.px(30) }

func (u *UI) listTop() int { return u.headerH() + u.px(6) + u.px(38) }

func (u *UI) rowsPerPage() int {
	return max(1, (u.H-u.px(32)-u.listTop()-u.px(6))/u.rowH())
}

func (u *UI) drawTabs(c *Canvas) {
	y := u.headerH() + u.px(14)
	x := u.px(12)
	x = c.Chip(u.fTiny, x, y+u.px(2), "L1", colAmberDim, false) + u.px(2)
	for i, t := range tabNames[:u.tabsShown()] {
		label := fmt.Sprintf("%s %02d", t, u.tabCount(i))
		pad := u.px(10)
		if u.tabsShown() > tabNew {
			pad = u.px(7) // five tabs: a little tighter so they fit
		}
		w := u.fSmall.Width(label) + 2*pad
		h := u.fSmall.height + u.px(8)
		fill, edge := colAmber, colGrid
		if i == tabNew {
			fill, edge = colGreen, colGreen
		}
		if i == u.tab {
			c.Fill(x, y, w, h, fill)
			c.Text(u.fSmall, x+pad, y+u.px(4), label, colBG)
			c.Fill(x, y+h, w, u.px(2), colCyan)
		} else {
			fg := colPaperDim
			if i == tabNew {
				fg = colGreen
			}
			c.Border(x, y, w, h, 1, edge)
			c.Text(u.fSmall, x+pad, y+u.px(4), label, fg)
		}
		x += w + u.px(6)
	}
	c.Chip(u.fTiny, x+u.px(2), y+u.px(2), "R1", colAmberDim, false)
}

func (u *UI) drawList(c *Canvas) {
	u.drawTabs(c)
	top := u.listTop()
	lw := c.W * 58 / 100
	rows := u.rowsPerPage()
	rh := u.rowH()
	n := len(u.visible)
	sel := u.sel[u.tab]
	sc := u.scroll[u.tab]
	if sel < sc {
		sc = sel
	}
	if sel >= sc+rows {
		sc = sel - rows + 1
	}
	u.scroll[u.tab] = sc
	c.Fill(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), colPanel)
	c.Corners(u.px(8), top-u.px(4), lw-u.px(8), rows*rh+u.px(8), u.px(10), colAmberDim)
	if n == 0 {
		msg := "NO TAPES HERE"
		if u.tab == 3 {
			msg = "NOTHING ON THE CARD YET"
		}
		c.TextCenter(u.fBody, (lw+u.px(8))/2, top+u.px(60), msg, colPaperDim)
		c.TextCenter(u.fTiny, (lw+u.px(8))/2, top+u.px(90), "L1 / R1 TO FLIP SIDES", colAmberDim)
	}
	for i := 0; i < rows && sc+i < n; i++ {
		p := u.visible[sc+i]
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
		c.Text(u.fLCD, x+u.px(4), y+u.px(2), fmt.Sprintf("%02d", p.Index+1), dim)
		nx := x + u.px(36)
		// right-side markers
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
		if u.updates[p.Name] != "" {
			mark("▲UPD", colMagenta)
		} else if t := u.fresh[p]; t > 0 && u.tab == tabNew {
			mark(ageLabel(t, time.Now()), colGreen)
		} else if u.fresh[p] > 0 && !u.installed(p) {
			mark("NEW", colGreen)
		} else if u.manifests[p.Name] != nil {
			mark("●", colCyan)
		} else if u.onCard[p.Name] != "" {
			mark("○", colCyan)
		}
		if p.OwnFiles() {
			mark("BYO", colAmber)
		}
		if p.ManualReason() != "" {
			mark("PC", colPaperDim)
		}
		name := u.fBody.Ellipsize(p.Name, rx-nx-u.px(4))
		c.Text(u.fBody, nx, y+u.px(3), name, fg)
	}
	// scrollbar
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
	if p := u.selected(); p != nil {
		spin := time.Now().Before(u.seekUntil)
		a := 0.0
		if spin {
			a = u.angle()
		}
		prog := float64(sel) / math.Max(1, float64(n-1))
		porter := "BY " + strings.ToUpper(strings.Join(p.Porter, ", "))
		c.Cassette(px, top-u.px(4), pw, u.px(150), p.Name, porter, a, prog, u.fBody, u.fTiny)
		y := top + u.px(158)
		kv := func(k, v string, col color.RGBA) {
			c.Text(u.fTiny, px+u.px(4), y+u.px(2), k, colPaperDim)
			c.Text(u.fSmall, px+u.px(70), y, u.fSmall.Ellipsize(v, pw-u.px(74)), col)
			y += u.fSmall.height + u.px(4)
		}
		kv("TYPE", strings.ToUpper(strings.Join(p.Categories, " / ")), colPaper)
		kv("STATUS", strings.ToUpper(p.Status), statusColor(p.Status))
		if p.OwnFiles() {
			kv("FILES", "BRING YOUR OWN", colAmber)
		} else {
			kv("FILES", "INCLUDED", colCyan)
		}
		if t := u.fresh[p]; t > 0 {
			kv("ADDED", ageLabel(t, time.Now()), colGreen)
		}
		switch {
		case u.updates[p.Name] != "":
			kv("STATE", "UPDATE → "+u.updates[p.Name], colMagenta)
		case u.manifests[p.Name] != nil:
			kv("STATE", "ON CARD "+u.manifests[p.Name].Version, colCyan)
		case u.onCard[p.Name] != "":
			kv("STATE", "ON CARD (MANUAL)", colCyan)
		case p.ManualReason() != "":
			kv("STATE", "PC INSTALL ONLY", colPaperDim)
		default:
			kv("STATE", "NOT INSTALLED", colPaperDim)
		}
		y += u.px(4)
		c.HLineDashed(px, y, pw, u.px(4), u.px(3), colGrid)
		y += u.px(8)
		bottom := c.H - u.px(40)
		for _, line := range u.fTiny.Wrap(p.Notes, pw-u.px(4)) {
			if y+u.fTiny.height > bottom {
				break
			}
			c.Text(u.fTiny, px+u.px(2), y, line, colPaperDim)
			y += u.fTiny.height + u.px(1)
		}
	}
	right := ""
	if u.cat != nil {
		right = strings.ToUpper(u.cat.Source) + " CATALOG"
	}
	if u.checking != "" {
		right = u.checking + strings.Repeat("·", u.frame/4%4)
	}
	u.footer(c, [][2]string{{"A", "PLAY"}, {"B", "EXIT"}, {"Y", "UPDATES"}, {"SEL", "MIXTAPE"}}, right)
}

func statusColor(s string) color.RGBA {
	switch s {
	case "playable":
		return colGreen
	case "experimental":
		return colAmber
	case "prerelease":
		return colMagenta
	}
	return colRed
}

func (u *UI) shotBox() (int, int) {
	w := u.W*50/100 - u.px(24)
	return w, w * 3 / 4
}

func (u *UI) drawDetail(c *Canvas) {
	p := u.cur
	top := u.headerH() + u.px(16)
	// CRT monitor
	sw, sh := u.shotBox()
	fx, fy := u.px(14), top
	fw, fh := sw+u.px(16), sh+u.px(16)
	c.Fill(fx, fy, fw, fh, colPanel2)
	c.Border(fx, fy, fw, fh, 1, colGrid)
	c.Corners(fx-u.px(3), fy-u.px(3), fw+u.px(6), fh+u.px(6), u.px(12), colCyan)
	ix, iy := fx+u.px(8), fy+u.px(8)
	c.Fill(ix, iy, sw, sh, colBlack)
	if img := u.imgs[p.Image]; img != nil {
		b := img.Bounds()
		dx := ix + (sw-b.Dx())/2
		dy := iy + (sh-b.Dy())/2
		c.DrawImage(img, dx, dy, b.Dx(), b.Dy())
		c.Scanlines(ix, iy, sw, sh, 0.28)
	} else {
		label := "TUNING"
		seed := uint32(u.frame)
		if u.imgErr[p.Image] || p.Image == "" {
			label = "NO SIGNAL"
			seed = 7
		}
		c.Noise(ix, iy, sw, sh, seed)
		lw := u.fLCDBig.Width(label) + u.px(20)
		c.Fill(ix+(sw-lw)/2, iy+sh/2-u.px(22), lw, u.px(40), colBG)
		c.TextCenter(u.fLCDBig, ix+sw/2, iy+sh/2-u.px(18), label, colAmber)
	}
	c.Text(u.fTiny, fx, fy+fh+u.px(6), fmt.Sprintf("TRK %02d/%02d", p.Index+1, len(u.cat.Ports)), colAmberDim)
	c.TextRight(u.fTiny, fx+fw, fy+fh+u.px(6), "◀ L1  R1 ▶", colAmberDim)

	// info column
	x := fx + fw + u.px(18)
	w := c.W - x - u.px(14)
	y := top - u.px(2)
	for i, line := range u.fBig.Wrap(p.Name, w) {
		if i == 2 {
			break
		}
		c.TextGlow(u.fBig, x, y, line, colAmber)
		y += u.fBig.height
	}
	c.Text(u.fTiny, x, y+u.px(2), u.fTiny.Ellipsize("BY "+strings.ToUpper(strings.Join(p.Porter, ", ")), w), colCyan)
	y += u.fTiny.height + u.px(10)
	cx := x
	cx = c.Chip(u.fTiny, cx, y, strings.ToUpper(p.Kind()), colPaperDim, false)
	cx = c.Chip(u.fTiny, cx, y, strings.ToUpper(p.Status), statusColor(p.Status), false)
	if p.OwnFiles() {
		c.Chip(u.fTiny, cx, y, "BYO FILES", colAmber, true)
	} else {
		c.Chip(u.fTiny, cx, y, "FREE", colCyan, true)
	}
	y += u.fTiny.height + u.px(16)
	// release block
	c.Fill(x, y, w, u.px(100), colPanel)
	c.Corners(x, y, w, u.px(100), u.px(8), colAmberDim)
	ry := y + u.px(8)
	kv := func(k, v string, col color.RGBA) {
		c.Text(u.fTiny, x+u.px(10), ry+u.px(2), k, colPaperDim)
		c.Text(u.fSmall, x+u.px(84), ry, u.fSmall.Ellipsize(v, w-u.px(92)), col)
		ry += u.fSmall.height + u.px(3)
	}
	plan := u.plans[p.Name]
	switch {
	case u.planBusy[p.Name]:
		kv("RELEASE", "SCANNING"+strings.Repeat("·", u.frame/3%4), colAmber)
	case u.planErr[p.Name] != "":
		kv("RELEASE", "UNREACHABLE", colMagenta)
	case plan != nil && plan.Problem != "":
		kv("RELEASE", "MANUAL INSTALL", colPaperDim)
	case plan != nil:
		v := plan.Version
		if plan.Snapshot {
			v = "LATEST SOURCE"
		}
		kv("RELEASE", v, colPaper)
		if !plan.Date.IsZero() {
			kv("DATE", plan.Date.Format("2006-01-02"), colPaper)
		}
		if sz := plan.Size(); sz > 0 {
			kv("SIZE", human(sz), colPaper)
		}
	}
	if m := u.manifests[p.Name]; m != nil {
		col := colCyan
		s := "ON CARD " + m.Version
		if u.updates[p.Name] != "" {
			col, s = colMagenta, "UPDATE AVAILABLE"
		}
		kv("STATE", s, col)
	} else if where := u.onCard[p.Name]; where != "" {
		kv("STATE", "FOUND "+where, colCyan)
	}
	y += u.px(108)

	// liner notes, full width
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
	if plan != nil && plan.Problem != "" {
		add("▲ "+plan.Problem, colMagenta)
	}
	if e := u.planErr[p.Name]; e != "" {
		add("▲ "+e, colMagenta)
	}
	add(p.Notes, colPaper)
	if plan != nil && plan.Snapshot {
		add("No releases are published, so Mixtape installs the newest files from the project's repository.", colPaperDim)
	}
	if p.Recipe != nil && p.Recipe.Note != "" {
		add(p.Recipe.Note, colPaperDim)
	}
	add("SRC "+p.Upstream, colCyanDim)
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

	items := [][2]string{}
	if plan != nil && plan.Problem == "" && !u.planBusy[p.Name] {
		label := "INSTALL"
		if u.updates[p.Name] != "" {
			label = "UPDATE"
		} else if u.manifests[p.Name] != nil || u.onCard[p.Name] != "" {
			label = "REINSTALL"
		}
		items = append(items, [2]string{"A", label})
	}
	if u.manifests[p.Name] != nil {
		items = append(items, [2]string{"X", "ERASE"})
	}
	items = append(items, [2]string{"Y", "RESCAN"}, [2]string{"B", "BACK"})
	u.footer(c, items, "")
}

func (u *UI) panel(c *Canvas, w, h int, accent color.RGBA) (int, int) {
	c.Blend(0, 0, c.W, c.H, colBlack, 0.72)
	x, y := (c.W-w)/2, (c.H-h)/2
	c.Fill(x, y, w, h, colPanel)
	c.Border(x, y, w, h, 1, colGrid)
	c.Fill(x, y, w, u.px(3), accent)
	c.Corners(x-u.px(4), y-u.px(4), w+u.px(8), h+u.px(8), u.px(14), accent)
	return x, y
}

func (u *UI) drawBusy(c *Canvas) {
	w, h := c.W*84/100, u.px(300)
	x, y := u.panel(c, w, h, colRed)
	// blinking REC
	if u.frame/8%2 == 0 {
		c.Circle(x+u.px(22), y+u.px(24), u.px(7), colRed, true, 0)
	}
	c.TextGlow(u.fBig, x+u.px(38), y+u.px(12), "REC", colRed)
	c.Text(u.fSmall, x+u.px(100), y+u.px(16), u.fSmall.Ellipsize(strings.ToUpper(u.busyName), w-u.px(116)), colPaper)
	frac := 0.0
	if u.prog.Total > 0 {
		frac = math.Min(1, float64(u.prog.Done)/float64(u.prog.Total))
	}
	cw := u.px(300)
	c.Cassette(x+(w-cw)/2, y+u.px(50), cw, u.px(130), u.busyName, u.prog.Phase, u.angle()*1.6, frac, u.fBody, u.fTiny)
	vy := y + u.px(196)
	vu := frac
	if u.prog.Total <= 0 {
		// unknown size: bounce the meter like a live signal
		vu = 0.35 + 0.3*math.Abs(math.Sin(float64(u.frame)*0.25))
	}
	c.VU(x+u.px(24), vy, w-u.px(48), u.px(16), 28, vu)
	status := u.prog.Phase
	if u.prog.Total > 0 {
		status += fmt.Sprintf("   %s / %s", human(u.prog.Done), human(u.prog.Total))
	} else if u.prog.Done > 0 {
		status += "   " + human(u.prog.Done)
	}
	c.Text(u.fSmall, x+u.px(24), vy+u.px(26), status, colAmber)
	if u.prog.Total > 0 {
		c.TextRight(u.fLCD, x+w-u.px(24), vy+u.px(22), fmt.Sprintf("%3d%%", int(frac*100)), colAmber)
	}
	c.Button(u.fTiny, x+u.px(24), y+h-u.px(30), "B", "ABORT", colPaperDim)
}

func (u *UI) drawMsg(c *Canvas) {
	w := c.W * 84 / 100
	var lines []string
	for _, para := range u.msgBody {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, u.fSmall.Wrap(para, w-u.px(40))...)
	}
	maxLines := (c.H*80/100 - u.px(100)) / (u.fSmall.height + u.px(2))
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	h := u.px(96) + len(lines)*(u.fSmall.height+u.px(2))
	x, y := u.panel(c, w, h, u.msgCol)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(16), u.msgTitle, u.msgCol)
	ly := y + u.px(52)
	for _, l := range lines {
		c.Text(u.fSmall, x+u.px(20), ly, l, colPaper)
		ly += u.fSmall.height + u.px(2)
	}
	c.Button(u.fTiny, x+u.px(20), y+h-u.px(30), "A", "OK", colAmber)
}

func (u *UI) drawConfirm(c *Canvas) {
	w, h := c.W*76/100, u.px(150)
	x, y := u.panel(c, w, h, colMagenta)
	c.TextGlow(u.fBig, x+u.px(20), y+u.px(16), "CONFIRM", colMagenta)
	ly := y + u.px(54)
	for _, l := range u.fSmall.Wrap(u.confirmQ, w-u.px(40)) {
		c.Text(u.fSmall, x+u.px(20), ly, l, colPaper)
		ly += u.fSmall.height + u.px(2)
	}
	bx := c.Button(u.fTiny, x+u.px(20), y+h-u.px(30), "A", u.confirmOK, colMagenta)
	c.Button(u.fTiny, bx, y+h-u.px(30), "B", "CANCEL", colPaperDim)
}

// drawWhatsNew is the NEW ARRIVALS window: apps and ports added to the catalog
// since the last visit.
func (u *UI) drawWhatsNew(c *Canvas) {
	n := len(u.arrivals)
	rowH := u.fSmall.height + u.fTiny.height + u.px(14)
	maxRows := max(1, (c.H*82/100-u.px(130))/rowH)
	rows := min(n, maxRows)
	w := c.W * 86 / 100
	h := u.px(118) + rows*rowH
	x, y := u.panel(c, w, h, colGreen)

	c.TextGlow(u.fBig, x+u.px(20), y+u.px(14), "NEW ARRIVALS", colGreen)
	count := fmt.Sprintf("%02d", n)
	c.TextRight(u.fLCDBig, x+w-u.px(20), y+u.px(8), count, colGreen)
	sub := "ADDED SINCE YOUR LAST VISIT"
	if n == 1 {
		sub = "1 NEW TAPE " + sub
	} else {
		sub = fmt.Sprintf("%d NEW TAPES %s", n, sub)
	}
	c.Text(u.fTiny, x+u.px(20), y+u.px(44), sub, colCyan)
	stripeY := y + u.px(64)
	t := u.px(2)
	c.Fill(x+u.px(20), stripeY, w-u.px(40), t, colAmber)
	c.Fill(x+u.px(20), stripeY+t, w-u.px(40), t, colMagenta)
	c.Fill(x+u.px(20), stripeY+2*t, w-u.px(40), t, colCyan)

	if u.wnSel < u.wnScroll {
		u.wnScroll = u.wnSel
	}
	if u.wnSel >= u.wnScroll+rows {
		u.wnScroll = u.wnSel - rows + 1
	}
	ly := stripeY + u.px(12)
	lx, lw := x+u.px(16), w-u.px(32)
	for i := 0; i < rows && u.wnScroll+i < n; i++ {
		idx := u.wnScroll + i
		p := u.arrivals[idx]
		ry := ly + i*rowH
		isSel := idx == u.wnSel
		fg, dim, chip := colPaper, colPaperDim, colGreen
		if isSel {
			c.Fill(lx, ry, lw, rowH-u.px(4), colAmber)
			c.Fill(lx, ry, u.px(4), rowH-u.px(4), colCyan)
			fg, dim, chip = colBG, colAmberDk, colBG
		} else if i%2 == 1 {
			c.Fill(lx, ry, lw, rowH-u.px(4), colPanel2)
		}
		// right: kind chip
		kind := p.Kind()
		kw := u.fTiny.Width(kind) + u.px(12)
		kx := lx + lw - kw - u.px(10)
		c.Chip(u.fTiny, kx, ry+u.px(6), kind, chip, false)
		tx := lx + u.px(14)
		c.Text(u.fSmall, tx, ry+u.px(4), u.fSmall.Ellipsize(p.Name, kx-tx-u.px(10)), fg)
		info := "BY " + strings.ToUpper(strings.Join(p.Porter, ", "))
		if p.Notes != "" {
			info += "  ·  " + p.Notes
		}
		c.Text(u.fTiny, tx, ry+u.px(6)+u.fSmall.height, u.fTiny.Ellipsize(info, lw-u.px(28)), dim)
	}
	if n > rows { // scrollbar
		th := rows * rowH
		c.Fill(x+w-u.px(10), ly, u.px(3), th, colGrid)
		bh := max(u.px(12), th*rows/n)
		c.Fill(x+w-u.px(10), ly+(th-bh)*u.wnScroll/max(1, n-rows), u.px(3), bh, colGreen)
	}
	by := y + h - u.px(30)
	bx := c.Button(u.fTiny, x+u.px(20), by, "A", "OPEN", colAmber)
	c.Button(u.fTiny, bx, by, "B", "CLOSE", colPaperDim)
	c.TextRight(u.fTiny, x+w-u.px(20), by+u.px(1), "ALSO IN THE NEW TAB", colPaperDim)
}

func (u *UI) drawBoot(c *Canvas) {
	cx := c.W / 2
	u.drawStripes(c, c.H/2-u.px(120))
	t := "MIXTAPE"
	c.TextGlow(u.fLogo, cx-u.fLogo.Width(t)/2, c.H/2-u.px(100), t, colAmber)
	c.TextCenter(u.fTiny, cx, c.H/2-u.px(66), "COMMUNITY PORT DECK FOR ONION OS", colCyan)
	cw := u.px(260)
	c.Cassette(cx-cw/2, c.H/2-u.px(40), cw, u.px(120), "SIDE A", "COMMUNITY PORTS VOL.1", u.angle(), float64(u.frame%80)/80, u.fBody, u.fTiny)
	msg := "LOADING CATALOG" + strings.Repeat(".", u.frame/4%4)
	c.Text(u.fSmall, cx-u.fSmall.Width("LOADING CATALOG...")/2, c.H/2+u.px(100), msg, colAmber)
	c.TextCenter(u.fTiny, cx, c.H-u.px(26), "CATALOG: MIYOOMINI-PORTS BY PRODUCDEVITY  ·  v"+version, colPaperDim)
}

// ---------- device status ----------

func wifiUp() bool {
	b, err := os.ReadFile("/sys/class/net/wlan0/operstate")
	return err == nil && strings.TrimSpace(string(b)) == "up"
}

func battery() int {
	b, err := os.ReadFile("/tmp/percBat")
	if err != nil {
		return -1
	}
	var v int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &v); err != nil || v < 0 || v > 100 {
		return -1
	}
	return v
}
