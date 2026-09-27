package main

// Software drawing on an RGBA canvas plus cached TrueType text.

import (
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/body.ttf
var bodyTTF []byte

//go:embed assets/title.ttf
var titleTTF []byte

//go:embed assets/lcd.ttf
var lcdTTF []byte

// Palette: cassette-futurism amber phosphor with cyan/magenta accents.
var (
	colBG       = rgb(0x07, 0x08, 0x0a)
	colPanel    = rgb(0x10, 0x12, 0x15)
	colPanel2   = rgb(0x17, 0x1a, 0x1e)
	colGrid     = rgb(0x22, 0x26, 0x2c)
	colAmber    = rgb(0xff, 0xb0, 0x00)
	colAmberDim = rgb(0x8a, 0x5e, 0x00)
	colAmberDk  = rgb(0x3a, 0x28, 0x00)
	colCyan     = rgb(0x2e, 0xe6, 0xd6)
	colCyanDim  = rgb(0x12, 0x5e, 0x58)
	colMagenta  = rgb(0xff, 0x2e, 0x88)
	colMagDim   = rgb(0x5c, 0x10, 0x31)
	colRed      = rgb(0xff, 0x3b, 0x30)
	colGreen    = rgb(0x7d, 0xff, 0x6a)
	colPaper    = rgb(0xe8, 0xe2, 0xd0)
	colPaperDim = rgb(0x8c, 0x88, 0x7c)
	colBlack    = rgb(0, 0, 0)
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

type Canvas struct {
	*image.RGBA
	W, H int
}

func NewCanvas(w, h int) *Canvas {
	return &Canvas{RGBA: image.NewRGBA(image.Rect(0, 0, w, h)), W: w, H: h}
}

func (c *Canvas) Fill(x, y, w, h int, col color.RGBA) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.Rect)
	if r.Empty() {
		return
	}
	// fast row fill
	p := c.Pix
	st := c.Stride
	row := make([]byte, r.Dx()*4)
	for i := 0; i < len(row); i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = col.R, col.G, col.B, 255
	}
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		o := yy*st + r.Min.X*4
		copy(p[o:o+len(row)], row)
	}
}

// Blend fills a rectangle blending col over existing pixels with alpha a (0..1).
func (c *Canvas) Blend(x, y, w, h int, col color.RGBA, a float64) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.Rect)
	ia := int(a * 256)
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		o := yy*c.Stride + r.Min.X*4
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			c.Pix[o] = uint8((int(c.Pix[o])*(256-ia) + int(col.R)*ia) >> 8)
			c.Pix[o+1] = uint8((int(c.Pix[o+1])*(256-ia) + int(col.G)*ia) >> 8)
			c.Pix[o+2] = uint8((int(c.Pix[o+2])*(256-ia) + int(col.B)*ia) >> 8)
			o += 4
		}
	}
}

func (c *Canvas) Set(x, y int, col color.RGBA) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	o := y*c.Stride + x*4
	c.Pix[o], c.Pix[o+1], c.Pix[o+2], c.Pix[o+3] = col.R, col.G, col.B, 255
}

func (c *Canvas) Border(x, y, w, h, t int, col color.RGBA) {
	c.Fill(x, y, w, t, col)
	c.Fill(x, y+h-t, w, t, col)
	c.Fill(x, y, t, h, col)
	c.Fill(x+w-t, y, t, h, col)
}

// Corners draws HUD-style corner brackets.
func (c *Canvas) Corners(x, y, w, h, l int, col color.RGBA) {
	c.Fill(x, y, l, 2, col)
	c.Fill(x, y, 2, l, col)
	c.Fill(x+w-l, y, l, 2, col)
	c.Fill(x+w-2, y, 2, l, col)
	c.Fill(x, y+h-2, l, 2, col)
	c.Fill(x, y+h-l, 2, l, col)
	c.Fill(x+w-l, y+h-2, l, 2, col)
	c.Fill(x+w-2, y+h-l, 2, l, col)
}

func (c *Canvas) HLineDashed(x, y, w, dash, gap int, col color.RGBA) {
	for i := x; i < x+w; i += dash + gap {
		n := dash
		if i+n > x+w {
			n = x + w - i
		}
		c.Fill(i, y, n, 1, col)
	}
}

func (c *Canvas) Circle(cx, cy, r int, col color.RGBA, filled bool, thick int) {
	r2 := float64(r) * float64(r)
	in := float64(r-thick) * float64(r-thick)
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			d := float64(x*x + y*y)
			if d <= r2 && (filled || d >= in) {
				c.Set(cx+x, cy+y, col)
			}
		}
	}
}

// Line with Bresenham, thickness 1.
func (c *Canvas) Line(x0, y0, x1, y1 int, col color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.Set(x0, y0, col)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Scanlines darkens every other row — the CRT look. Applied to a region.
func (c *Canvas) Scanlines(x, y, w, h int, strength float64) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.Rect)
	k := int((1 - strength) * 256)
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		if yy%2 == 0 {
			continue
		}
		o := yy*c.Stride + r.Min.X*4
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			c.Pix[o] = uint8(int(c.Pix[o]) * k >> 8)
			c.Pix[o+1] = uint8(int(c.Pix[o+1]) * k >> 8)
			c.Pix[o+2] = uint8(int(c.Pix[o+2]) * k >> 8)
			o += 4
		}
	}
}

// DrawImage scales src into the destination rect (nearest-neighbour).
func (c *Canvas) DrawImage(src *image.RGBA, x, y, w, h int) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	for dy := 0; dy < h; dy++ {
		py := y + dy
		if py < 0 || py >= c.H {
			continue
		}
		sy := sb.Min.Y + dy*sh/h
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < 0 || px >= c.W {
				continue
			}
			sx := sb.Min.X + dx*sw/w
			so := (sy-sb.Min.Y)*src.Stride + (sx-sb.Min.X)*4
			do := py*c.Stride + px*4
			c.Pix[do], c.Pix[do+1], c.Pix[do+2], c.Pix[do+3] = src.Pix[so], src.Pix[so+1], src.Pix[so+2], 255
		}
	}
}

// ---------- text ----------

type glyph struct {
	mask    *image.Alpha
	off     image.Point
	advance int
}

type Font struct {
	face    font.Face
	cache   map[rune]*glyph
	ascent  int
	height  int
	fixedSp bool
}

func loadFont(ttf []byte, size float64) *Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	m := face.Metrics()
	return &Font{face: face, cache: map[rune]*glyph{}, ascent: m.Ascent.Ceil(), height: m.Height.Ceil()}
}

func (f *Font) glyph(r rune) *glyph {
	if g, ok := f.cache[r]; ok {
		return g
	}
	if g := f.symbol(r); g != nil {
		f.cache[r] = g
		return g
	}
	dr, mask, mp, adv, ok := f.face.Glyph(fixed.P(0, 0), r)
	if !ok {
		if r != '?' {
			g := f.glyph('?')
			f.cache[r] = g
			return g
		}
		f.cache[r] = &glyph{advance: adv.Round()}
		return f.cache[r]
	}
	a := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	draw.Draw(a, a.Bounds(), mask, mp, draw.Src)
	g := &glyph{mask: a, off: dr.Min, advance: adv.Round()}
	f.cache[r] = g
	return g
}

// symbol synthesises UI glyphs the bundled fonts don't have (triangles, dots, arrows).
func (f *Font) symbol(r rune) *glyph {
	switch r {
	case '▲', '●', '○', '◀', '▶', '↕', '→', '✓':
	default:
		return nil
	}
	s := f.ascent * 7 / 10 // symbol box size
	if s < 6 {
		s = 6
	}
	w := s
	if r == '→' {
		w = s * 3 / 2
	}
	m := image.NewAlpha(image.Rect(0, 0, w, s))
	set := func(x, y int) {
		if x >= 0 && y >= 0 && x < w && y < s {
			m.Pix[y*m.Stride+x] = 255
		}
	}
	c := float64(s-1) / 2
	for y := 0; y < s; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x), float64(y)
			d := math.Hypot(fx-c, fy-c)
			switch r {
			case '▲':
				if math.Abs(fx-c) <= fy/2+0.5 {
					set(x, y)
				}
			case '●':
				if d <= c+0.3 {
					set(x, y)
				}
			case '○':
				if d <= c+0.3 && d >= c-1.6 {
					set(x, y)
				}
			case '◀':
				if math.Abs(fy-c) <= fx/2+0.5 {
					set(x, y)
				}
			case '▶':
				if math.Abs(fy-c) <= (float64(s-1)-fx)/2+0.5 {
					set(x, y)
				}
			case '↕':
				hy := math.Min(fy, float64(s-1)-fy)
				if math.Abs(fx-c) <= 0.8 || (hy < c*0.7 && math.Abs(fx-c) <= hy+0.5) {
					set(x, y)
				}
			case '→':
				tip := float64(w - 1)
				if (math.Abs(fy-c) <= 0.8 && fx < tip-1) || (fx >= tip-c && math.Abs(fy-c) <= (tip-fx)+0.3) {
					set(x, y)
				}
			case '✓':
				if (x < s/2 && math.Abs(fy-(c+fx*0.6)) <= 1) || (x >= s/2-1 && math.Abs(fy-(float64(s-1)-(fx-float64(s/2))*1.4)) <= 1) {
					set(x, y)
				}
			}
		}
	}
	top := -f.ascent*36/100 - s/2
	return &glyph{mask: m, off: image.Pt(1, top), advance: w + 3}
}

func (f *Font) Width(s string) int {
	w := 0
	for _, r := range s {
		w += f.glyph(r).advance
	}
	return w
}

// Text draws s with its top-left at (x,y). Returns the x after the text.
func (c *Canvas) Text(f *Font, x, y int, s string, col color.RGBA) int {
	base := y + f.ascent
	for _, r := range s {
		g := f.glyph(r)
		if g.mask != nil {
			gx, gy := x+g.off.X, base+g.off.Y
			b := g.mask.Bounds()
			for yy := 0; yy < b.Dy(); yy++ {
				py := gy + yy
				if py < 0 || py >= c.H {
					continue
				}
				for xx := 0; xx < b.Dx(); xx++ {
					px := gx + xx
					if px < 0 || px >= c.W {
						continue
					}
					a := int(g.mask.Pix[yy*g.mask.Stride+xx])
					if a == 0 {
						continue
					}
					o := py*c.Stride + px*4
					c.Pix[o] = uint8((int(c.Pix[o])*(255-a) + int(col.R)*a) / 255)
					c.Pix[o+1] = uint8((int(c.Pix[o+1])*(255-a) + int(col.G)*a) / 255)
					c.Pix[o+2] = uint8((int(c.Pix[o+2])*(255-a) + int(col.B)*a) / 255)
				}
			}
		}
		x += g.advance
	}
	return x
}

// TextGlow draws text with a soft phosphor halo.
func (c *Canvas) TextGlow(f *Font, x, y int, s string, col color.RGBA) int {
	halo := mix(colBG, col, 0.28)
	for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		c.Text(f, x+d[0], y+d[1], s, halo)
	}
	return c.Text(f, x, y, s, col)
}

func (c *Canvas) TextRight(f *Font, right, y int, s string, col color.RGBA) {
	c.Text(f, right-f.Width(s), y, s, col)
}

func (c *Canvas) TextCenter(f *Font, cx, y int, s string, col color.RGBA) {
	c.Text(f, cx-f.Width(s)/2, y, s, col)
}

// Ellipsize trims s to fit width w.
func (f *Font) Ellipsize(s string, w int) string {
	if f.Width(s) <= w {
		return s
	}
	for len(s) > 0 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
		if f.Width(s+"…") <= w {
			return s + "…"
		}
	}
	return ""
}

// Wrap breaks text into lines no wider than w.
func (f *Font) Wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, wd := range words {
			try := wd
			if line != "" {
				try = line + " " + wd
			}
			if f.Width(try) <= w {
				line = try
				continue
			}
			if line != "" {
				out = append(out, line)
			}
			for f.Width(wd) > w { // hard-break very long tokens (URLs)
				cut := len(wd)
				for cut > 1 && f.Width(wd[:cut]) > w {
					cut--
				}
				out = append(out, wd[:cut])
				wd = wd[cut:]
			}
			line = wd
		}
		out = append(out, line)
	}
	return out
}

// ---------- signature widgets ----------

// Reel draws a cassette reel hub with spokes rotated by angle.
func (c *Canvas) Reel(cx, cy, r int, angle float64, tape int, col, dim color.RGBA) {
	if tape > r {
		c.Circle(cx, cy, tape, colAmberDk, true, 0)
		c.Circle(cx, cy, tape, mix(colAmberDk, colAmberDim, 0.5), false, 1)
	}
	c.Circle(cx, cy, r, colPanel2, true, 0)
	c.Circle(cx, cy, r, col, false, 2)
	c.Circle(cx, cy, r/3, dim, true, 0)
	for i := 0; i < 6; i++ {
		a := angle + float64(i)*math.Pi/3
		x1 := cx + int(math.Cos(a)*float64(r/3))
		y1 := cy + int(math.Sin(a)*float64(r/3))
		x2 := cx + int(math.Cos(a)*float64(r-3))
		y2 := cy + int(math.Sin(a)*float64(r-3))
		if i%2 == 0 {
			c.Line(x1, y1, x2, y2, col)
			c.Line(x1+1, y1, x2+1, y2, col)
		}
	}
	c.Circle(cx, cy, 3, colBG, true, 0)
}

// Cassette draws a stylised tape shell with a label and two reels.
func (c *Canvas) Cassette(x, y, w, h int, label, sub string, angle, progress float64, fLabel, fSub *Font) {
	c.Fill(x, y, w, h, colPanel2)
	c.Border(x, y, w, h, 2, colAmberDim)
	// screws
	for _, p := range [][2]int{{x + 8, y + 8}, {x + w - 9, y + 8}, {x + 8, y + h - 9}, {x + w - 9, y + h - 9}} {
		c.Circle(p[0], p[1], 3, colGrid, true, 0)
	}
	// label strip with retro stripes
	lx, ly, lw, lh := x+16, y+12, w-32, h*44/100
	c.Fill(lx, ly, lw, lh, colPaper)
	stripeY := ly + lh - 14
	c.Fill(lx, stripeY, lw, 4, colAmber)
	c.Fill(lx, stripeY+4, lw, 4, colMagenta)
	c.Fill(lx, stripeY+8, lw, 4, colCyan)
	c.Text(fLabel, lx+8, ly+5, fLabel.Ellipsize(label, lw-16), colBlack)
	c.Text(fSub, lx+8, ly+5+fLabel.height, fSub.Ellipsize(sub, lw-16), rgb(0x55, 0x50, 0x44))
	// window
	wy := ly + lh + 8
	wh := h - (wy - y) - 16
	wx, ww := x+w/2-w*30/100, w*60/100
	c.Fill(wx, wy, ww, wh, colBG)
	c.Border(wx, wy, ww, wh, 1, colGrid)
	r := wh * 28 / 100
	if r < 6 {
		r = 6
	}
	cy := wy + wh/2
	lcx, rcx := wx+ww/4, wx+ww*3/4
	// tape spooled from left to right according to progress
	maxT := wh/2 - 2
	lt := r + int(float64(maxT-r)*(1-progress))
	rt := r + int(float64(maxT-r)*progress)
	c.Reel(lcx, cy, r, angle, lt, colAmber, colAmberDim)
	c.Reel(rcx, cy, r, angle, rt, colAmber, colAmberDim)
	// bottom trapezoid
	c.Fill(x+w/2-w/4, y+h-12, w/2, 10, colPanel)
	c.Border(x+w/2-w/4, y+h-12, w/2, 10, 1, colGrid)
}

// VU draws a segmented LED meter, lit fraction v in 0..1.
func (c *Canvas) VU(x, y, w, h, segs int, v float64) {
	gap := 3
	sw := (w - gap*(segs-1)) / segs
	lit := int(math.Round(v * float64(segs)))
	for i := 0; i < segs; i++ {
		col := colGreen
		t := float64(i) / float64(segs)
		switch {
		case t > 0.85:
			col = colRed
		case t > 0.65:
			col = colAmber
		}
		sx := x + i*(sw+gap)
		if i < lit {
			c.Fill(sx, y, sw, h, col)
			c.Fill(sx, y, sw, 2, mix(col, colPaper, 0.5))
		} else {
			c.Fill(sx, y, sw, h, mix(colBG, col, 0.14))
		}
	}
}

// Button draws a controller button glyph with a caption and returns the x after it.
func (c *Canvas) Button(f *Font, x, y int, btn, caption string, col color.RGBA) int {
	bw := f.Width(btn) + 10
	if bw < f.height+2 {
		bw = f.height + 2
	}
	c.Fill(x, y, bw, f.height+2, col)
	c.Text(f, x+(bw-f.Width(btn))/2, y+1, btn, colBG)
	x += bw + 6
	x = c.Text(f, x, y+1, caption, colPaperDim)
	return x + 14
}

// Chip draws a small outlined tag.
func (c *Canvas) Chip(f *Font, x, y int, s string, col color.RGBA, filled bool) int {
	w := f.Width(s) + 12
	h := f.height + 4
	if filled {
		c.Fill(x, y, w, h, col)
		c.Text(f, x+6, y+2, s, colBG)
	} else {
		c.Border(x, y, w, h, 1, col)
		c.Text(f, x+6, y+2, s, col)
	}
	return x + w + 6
}

// Noise fills a region with animated static ("NO SIGNAL").
func (c *Canvas) Noise(x, y, w, h int, seed uint32) {
	s := seed*2654435761 + 1
	for yy := y; yy < y+h; yy += 2 {
		for xx := x; xx < x+w; xx += 2 {
			s ^= s << 13
			s ^= s >> 17
			s ^= s << 5
			v := uint8(s>>24) / 3
			col := rgb(v, v, v+v/4)
			c.Set(xx, yy, col)
			c.Set(xx+1, yy, col)
			c.Set(xx, yy+1, col)
			c.Set(xx+1, yy+1, col)
		}
	}
}
