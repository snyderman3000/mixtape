package main

import (
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "golang.org/x/image/webp"
)

const maxImageBytes = 12 << 20

// Preview downloads (or loads from cache) a port's screenshot, downscaled to fit w×h.
func (env *Env) Preview(url string, w, h int) (*image.RGBA, error) {
	sum := sha1.Sum([]byte(url))
	cache := filepath.Join(env.DataDir, "cache", "img-"+hex.EncodeToString(sum[:8])+".png")
	if f, err := os.Open(cache); err == nil {
		img, err := png.Decode(f)
		f.Close()
		if err == nil {
			return toRGBA(img), nil
		}
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	cl := &http.Client{Timeout: 30 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, netHint(err)
	}
	defer resp.Body.Close()
	img, _, err := image.Decode(io.LimitReader(resp.Body, maxImageBytes)) // GIFs: first frame only
	if err != nil {
		return nil, err
	}
	out := fit(img, w, h)
	os.MkdirAll(filepath.Dir(cache), 0o755)
	if f, err := os.Create(cache); err == nil {
		png.Encode(f, out)
		f.Close()
	}
	return out, nil
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Bounds(), img, b.Min, draw.Src)
	return r
}

// fit box-filters the image down so it fits inside w×h, keeping aspect ratio.
func fit(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= w && sh <= h {
		return src
	}
	scale := float64(w) / float64(sw)
	if s2 := float64(h) / float64(sh); s2 < scale {
		scale = s2
	}
	dw, dh := int(float64(sw)*scale), int(float64(sh)*scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n int
			for yy := y0; yy < y1; yy++ {
				o := yy*src.Stride + x0*4
				for xx := x0; xx < x1; xx++ {
					r += int(src.Pix[o])
					g += int(src.Pix[o+1])
					b += int(src.Pix[o+2])
					n++
					o += 4
				}
			}
			d := y*dst.Stride + x*4
			dst.Pix[d], dst.Pix[d+1], dst.Pix[d+2], dst.Pix[d+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}
