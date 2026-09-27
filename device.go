package main

// Miyoo Mini framebuffer + evdev input. Mirrors Onion's own display_reset():
// read var screeninfo, pan to yoffset 0, then write rotated 180° (panel is
// mounted upside down on the Mini / Mini+).

import (
	"encoding/binary"
	"io"
	"os"
	"syscall"
	"unsafe"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioPutVScreenInfo = 0x4601
	fbioGetFScreenInfo = 0x4602
)

type Framebuffer struct {
	f      *os.File
	mem    []byte
	W, H   int
	stride int
	flip   bool
}

func ioctl(fd uintptr, req uintptr, p unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(p))
	if e != 0 {
		return e
	}
	return nil
}

func OpenFramebuffer() (*Framebuffer, error) {
	f, err := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fb := &Framebuffer{f: f, W: 640, H: 480, flip: os.Getenv("MIXTAPE_NOFLIP") == ""}
	var v [40]uint32 // struct fb_var_screeninfo
	bpp := 32
	xvirt := 640
	if ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v[0])) == nil && v[0] > 0 {
		fb.W, fb.H = int(v[0]), int(v[1])
		xvirt = int(v[2])
		bpp = int(v[6])
		v[5] = 0 // yoffset
		_ = ioctl(f.Fd(), fbioPutVScreenInfo, unsafe.Pointer(&v[0]))
	}
	if bpp != 32 {
		bpp = 32
	}
	fb.stride = xvirt * 4
	smem := fb.stride * fb.H
	var fix [96]byte // struct fb_fix_screeninfo
	if ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fix[0])) == nil {
		ul := int(unsafe.Sizeof(uintptr(0)))
		sl := int(binary.LittleEndian.Uint32(fix[16+ul:]))
		lloff := 16 + ul + 4*4 + 2*3
		lloff = (lloff + 3) &^ 3
		if ll := int(binary.LittleEndian.Uint32(fix[lloff:])); ll >= fb.W*4 {
			fb.stride = ll
		}
		if sl >= fb.stride*fb.H {
			smem = sl
		}
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, smem, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, err
	}
	fb.mem = mem
	return fb, nil
}

// Present copies the canvas (RGBA) to the framebuffer (BGRA), rotated if needed.
func (fb *Framebuffer) Present(c *Canvas) {
	w, h := fb.W, fb.H
	if c.W < w {
		w = c.W
	}
	if c.H < h {
		h = c.H
	}
	for y := 0; y < h; y++ {
		sy := y
		if fb.flip {
			sy = fb.H - 1 - y
		}
		if sy >= c.H {
			continue
		}
		src := c.Pix[sy*c.Stride:]
		dst := fb.mem[y*fb.stride:]
		if fb.flip {
			for x := 0; x < w; x++ {
				so := (fb.W - 1 - x) * 4
				if fb.W-1-x >= c.W {
					continue
				}
				d := x * 4
				dst[d], dst[d+1], dst[d+2], dst[d+3] = src[so+2], src[so+1], src[so], 0xff
			}
		} else {
			for x := 0; x < w; x++ {
				d := x * 4
				dst[d], dst[d+1], dst[d+2], dst[d+3] = src[d+2], src[d+1], src[d], 0xff
			}
		}
	}
}

func (fb *Framebuffer) Clear() {
	for i := range fb.mem {
		fb.mem[i] = 0
	}
}

func (fb *Framebuffer) Close() {
	if fb.mem != nil {
		syscall.Munmap(fb.mem)
	}
	fb.f.Close()
}

// Miyoo button → Linux key code mapping.
const (
	BtnMenu   = 1   // ESC
	BtnL2     = 15  // TAB
	BtnR2     = 14  // BACKSPACE
	BtnL1     = 18  // E
	BtnR1     = 20  // T
	BtnStart  = 28  // ENTER
	BtnB      = 29  // LCTRL
	BtnX      = 42  // LSHIFT
	BtnY      = 56  // LALT
	BtnA      = 57  // SPACE
	BtnSelect = 97  // RCTRL
	BtnUp     = 103 //
	BtnLeft   = 105
	BtnRight  = 106
	BtnDown   = 108
	BtnPower  = 116
)

// ReadInput streams key presses (and auto-repeat for the d-pad and shoulders).
func ReadInput(path string, out chan<- int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	go func() {
		defer f.Close()
		ev := 16
		if unsafe.Sizeof(uintptr(0)) == 8 {
			ev = 24
		}
		buf := make([]byte, ev)
		for {
			if _, err := io.ReadFull(f, buf); err != nil {
				return
			}
			o := ev - 8
			typ := binary.LittleEndian.Uint16(buf[o:])
			code := int(binary.LittleEndian.Uint16(buf[o+2:]))
			val := int32(binary.LittleEndian.Uint32(buf[o+4:]))
			if typ != 1 {
				continue
			}
			repeatable := code == BtnUp || code == BtnDown || code == BtnLeft || code == BtnRight || code == BtnL1 || code == BtnR1
			if val == 1 || (val == 2 && repeatable) {
				select {
				case out <- code:
				default:
				}
			}
		}
	}()
	return nil
}
