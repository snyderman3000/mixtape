// Package patch applies ROM patches in the IPS, UPS and BPS formats.
package patch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
)

// Format identifies a patch format.
type Format string

const (
	IPS Format = "ips"
	UPS Format = "ups"
	BPS Format = "bps"
)

// maxROM caps output size: the largest GBA game is 32 MB and the Miyoo has 128 MB of RAM.
const maxROM = 64 << 20

// Info describes a patch before it's applied.
type Info struct {
	Format    Format
	SourceCRC uint32 // UPS/BPS only (0 for IPS)
	TargetCRC uint32 // UPS/BPS only
	SourceLen uint64 // UPS/BPS only
	TargetLen uint64 // UPS/BPS only
	HasCRC    bool
}

var (
	ErrUnknownFormat = errors.New("not an IPS, UPS or BPS patch")
	ErrCorrupt       = errors.New("patch file is damaged")
	ErrSourceCRC     = errors.New("this ROM isn't the one the patch was made for")
	ErrTargetCRC     = errors.New("patched ROM failed its checksum")
)

// Detect reads the patch header.
func Detect(p []byte) (Format, error) {
	switch {
	case bytes.HasPrefix(p, []byte("PATCH")):
		return IPS, nil
	case bytes.HasPrefix(p, []byte("UPS1")):
		return UPS, nil
	case bytes.HasPrefix(p, []byte("BPS1")):
		return BPS, nil
	}
	return "", ErrUnknownFormat
}

// FormatFromName guesses a patch format from a file name.
func FormatFromName(name string) (Format, bool) {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".ips"):
		return IPS, true
	case strings.HasSuffix(n, ".ups"):
		return UPS, true
	case strings.HasSuffix(n, ".bps"):
		return BPS, true
	}
	return "", false
}

// Inspect returns what can be known about a patch without a ROM.
func Inspect(p []byte) (Info, error) {
	f, err := Detect(p)
	if err != nil {
		return Info{}, err
	}
	info := Info{Format: f}
	switch f {
	case IPS:
		_, err = ipsRecords(p) // validates structure
	case UPS, BPS:
		if len(p) < 16 {
			return info, ErrCorrupt
		}
		if crc32.ChecksumIEEE(p[:len(p)-4]) != binary.LittleEndian.Uint32(p[len(p)-4:]) {
			return info, fmt.Errorf("%w (checksum mismatch)", ErrCorrupt)
		}
		r := &reader{b: p, pos: 4}
		info.SourceLen, _ = r.vlq()
		info.TargetLen, _ = r.vlq()
		if r.err != nil {
			return info, ErrCorrupt
		}
		n := len(p)
		info.SourceCRC = binary.LittleEndian.Uint32(p[n-12:])
		info.TargetCRC = binary.LittleEndian.Uint32(p[n-8:])
		info.HasCRC = true
	}
	return info, err
}

// Apply patches src and returns the new ROM. src is never modified.
func Apply(src, p []byte) ([]byte, error) {
	f, err := Detect(p)
	if err != nil {
		return nil, err
	}
	switch f {
	case IPS:
		return applyIPS(src, p)
	case UPS:
		return applyUPS(src, p)
	default:
		return applyBPS(src, p)
	}
}

// ---------- IPS ----------

type ipsRecord struct {
	off  int
	data []byte // nil for RLE
	rle  bool
	n    int
	val  byte
}

func ipsRecords(p []byte) ([]ipsRecord, error) {
	var recs []ipsRecord
	i := 5
	for {
		if i+3 > len(p) {
			return nil, fmt.Errorf("%w (IPS ends early)", ErrCorrupt)
		}
		if string(p[i:i+3]) == "EOF" {
			// optional 3-byte truncate size follows (Lunar IPS extension)
			return recs, nil
		}
		off := int(p[i])<<16 | int(p[i+1])<<8 | int(p[i+2])
		i += 3
		if i+2 > len(p) {
			return nil, ErrCorrupt
		}
		size := int(p[i])<<8 | int(p[i+1])
		i += 2
		if size == 0 {
			if i+3 > len(p) {
				return nil, ErrCorrupt
			}
			n := int(p[i])<<8 | int(p[i+1])
			recs = append(recs, ipsRecord{off: off, rle: true, n: n, val: p[i+2]})
			i += 3
			continue
		}
		if i+size > len(p) {
			return nil, ErrCorrupt
		}
		recs = append(recs, ipsRecord{off: off, data: p[i : i+size], n: size})
		i += size
	}
}

func applyIPS(src, p []byte) ([]byte, error) {
	recs, err := ipsRecords(p)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), src...)
	for _, r := range recs {
		end := r.off + r.n
		if end > len(out) {
			if end > maxROM {
				return nil, ErrCorrupt
			}
			out = append(out, make([]byte, end-len(out))...)
		}
		if r.rle {
			for k := r.off; k < end; k++ {
				out[k] = r.val
			}
		} else {
			copy(out[r.off:end], r.data)
		}
	}
	// Lunar IPS truncation extension
	eof := bytes.LastIndex(p, []byte("EOF"))
	if eof >= 0 && len(p) == eof+6 {
		t := int(p[eof+3])<<16 | int(p[eof+4])<<8 | int(p[eof+5])
		if t < len(out) {
			out = out[:t]
		}
	}
	return out, nil
}

// ---------- shared ----------

type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) byte() byte {
	if r.pos >= len(r.b) {
		r.err = ErrCorrupt
		return 0
	}
	c := r.b[r.pos]
	r.pos++
	return c
}

// vlq decodes the byuu/beat variable-length number used by UPS and BPS.
func (r *reader) vlq() (uint64, error) {
	var data, shift uint64 = 0, 1
	for i := 0; i < 10; i++ {
		x := r.byte()
		if r.err != nil {
			return 0, r.err
		}
		data += uint64(x&0x7f) * shift
		if x&0x80 != 0 {
			return data, nil
		}
		shift <<= 7
		data += shift
	}
	r.err = ErrCorrupt
	return 0, r.err
}

func footer(p []byte) (src, dst, self uint32, err error) {
	if len(p) < 16 {
		return 0, 0, 0, ErrCorrupt
	}
	n := len(p)
	src = binary.LittleEndian.Uint32(p[n-12:])
	dst = binary.LittleEndian.Uint32(p[n-8:])
	self = binary.LittleEndian.Uint32(p[n-4:])
	if crc32.ChecksumIEEE(p[:n-4]) != self {
		return 0, 0, 0, fmt.Errorf("%w (checksum mismatch)", ErrCorrupt)
	}
	return
}

// ---------- UPS ----------

func applyUPS(src, p []byte) ([]byte, error) {
	srcCRC, dstCRC, _, err := footer(p)
	if err != nil {
		return nil, err
	}
	r := &reader{b: p[:len(p)-12], pos: 4}
	inLen, _ := r.vlq()
	outLen, _ := r.vlq()
	if r.err != nil {
		return nil, ErrCorrupt
	}
	// UPS is symmetric: patching the target produces the source.
	c := crc32.ChecksumIEEE(src)
	forward := true
	switch {
	case uint64(len(src)) == inLen && c == srcCRC:
	case uint64(len(src)) == outLen && c == dstCRC:
		forward = false
		inLen, outLen = outLen, inLen
	default:
		return nil, ErrSourceCRC
	}
	if outLen > maxROM {
		return nil, ErrCorrupt
	}
	out := make([]byte, outLen)
	copy(out, src)
	pos := uint64(0)
	for r.pos < len(r.b) {
		skip, err := r.vlq()
		if err != nil {
			return nil, err
		}
		pos += skip
		for {
			x := r.byte()
			if r.err != nil {
				return nil, ErrCorrupt
			}
			if pos < outLen {
				var s byte
				if pos < uint64(len(src)) {
					s = src[pos]
				}
				out[pos] = s ^ x
			}
			pos++
			if x == 0 {
				break
			}
		}
	}
	want := dstCRC
	if !forward {
		want = srcCRC
	}
	if crc32.ChecksumIEEE(out) != want {
		return nil, ErrTargetCRC
	}
	return out, nil
}

// ---------- BPS ----------

func applyBPS(src, p []byte) ([]byte, error) {
	srcCRC, dstCRC, _, err := footer(p)
	if err != nil {
		return nil, err
	}
	r := &reader{b: p[:len(p)-12], pos: 4}
	srcLen, _ := r.vlq()
	dstLen, _ := r.vlq()
	metaLen, _ := r.vlq()
	if r.err != nil {
		return nil, ErrCorrupt
	}
	r.pos += int(metaLen)
	if uint64(len(src)) != srcLen || crc32.ChecksumIEEE(src) != srcCRC {
		return nil, ErrSourceCRC
	}
	if dstLen > maxROM {
		return nil, ErrCorrupt
	}
	out := make([]byte, dstLen)
	var outPos, srcRel, dstRel int64
	for r.pos < len(r.b) {
		d, err := r.vlq()
		if err != nil {
			return nil, err
		}
		cmd := d & 3
		n := int64(d>>2) + 1
		if outPos+n > int64(dstLen) {
			return nil, ErrCorrupt
		}
		switch cmd {
		case 0: // SourceRead
			if outPos+n > int64(len(src)) {
				return nil, ErrCorrupt
			}
			copy(out[outPos:outPos+n], src[outPos:outPos+n])
		case 1: // TargetRead
			if r.pos+int(n) > len(r.b) {
				return nil, ErrCorrupt
			}
			copy(out[outPos:outPos+n], r.b[r.pos:r.pos+int(n)])
			r.pos += int(n)
		case 2, 3: // SourceCopy / TargetCopy
			o, err := r.vlq()
			if err != nil {
				return nil, err
			}
			delta := int64(o >> 1)
			if o&1 != 0 {
				delta = -delta
			}
			if cmd == 2 {
				srcRel += delta
				if srcRel < 0 || srcRel+n > int64(len(src)) {
					return nil, ErrCorrupt
				}
				copy(out[outPos:outPos+n], src[srcRel:srcRel+n])
				srcRel += n
			} else {
				dstRel += delta
				if dstRel < 0 || dstRel >= outPos {
					return nil, ErrCorrupt
				}
				for k := int64(0); k < n; k++ { // byte-by-byte: ranges may overlap
					out[outPos+k] = out[dstRel]
					dstRel++
				}
			}
		}
		outPos += n
	}
	if crc32.ChecksumIEEE(out) != dstCRC {
		return nil, ErrTargetCRC
	}
	return out, nil
}
