package patch

import (
	"bytes"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// Fixtures in testdata were made with Flips (IPS, BPS delta, BPS linear) and a
// spec encoder for UPS that Flips' own UPS applier verified.
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyReferencePatches(t *testing.T) {
	src := read(t, "src.bin")
	for _, d := range []string{"same", "grow", "shrink"} {
		want := read(t, "dst_"+d+".bin")
		for _, p := range []string{d + ".ips", d + ".delta.bps", d + ".linear.bps", d + ".ups"} {
			got, err := Apply(src, read(t, p))
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s: output differs (len %d vs %d)", p, len(got), len(want))
			}
		}
	}
	if src2 := read(t, "src.bin"); !bytes.Equal(src, src2) {
		t.Fatal("source was modified")
	}
}

func TestChecksumPatchesRejectWrongROM(t *testing.T) {
	wrong := read(t, "src.bin")
	wrong[100] ^= 0xFF
	for _, p := range []string{"same.delta.bps", "same.linear.bps", "same.ups"} {
		if _, err := Apply(wrong, read(t, p)); !errors.Is(err, ErrSourceCRC) {
			t.Errorf("%s: err = %v, want ErrSourceCRC", p, err)
		}
	}
}

func TestUPSWorksBackwards(t *testing.T) {
	src := read(t, "src.bin")
	got, err := Apply(read(t, "dst_same.bin"), read(t, "same.ups"))
	if err != nil || !bytes.Equal(got, src) {
		t.Fatalf("reverse UPS: err=%v equal=%v", err, bytes.Equal(got, src))
	}
}

func TestInspect(t *testing.T) {
	src := read(t, "src.bin")
	dst := read(t, "dst_grow.bin")
	for _, p := range []string{"grow.delta.bps", "grow.ups"} {
		info, err := Inspect(read(t, p))
		if err != nil {
			t.Fatal(err)
		}
		if !info.HasCRC || info.SourceCRC != crc32.ChecksumIEEE(src) || info.TargetCRC != crc32.ChecksumIEEE(dst) ||
			info.SourceLen != uint64(len(src)) || info.TargetLen != uint64(len(dst)) {
			t.Errorf("%s: %+v", p, info)
		}
	}
	info, err := Inspect(read(t, "grow.ips"))
	if err != nil || info.Format != IPS || info.HasCRC {
		t.Errorf("ips inspect: %+v %v", info, err)
	}
	if _, err := Inspect([]byte("hello world")); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("garbage: %v", err)
	}
}

func TestIPSRLEAndTruncate(t *testing.T) {
	src := bytes.Repeat([]byte{1}, 32)
	p := []byte("PATCH")
	p = append(p, 0, 0, 4, 0, 0, 0, 5, 0xAA) // RLE: offset 4, 5 bytes of 0xAA
	p = append(p, 0, 0, 30, 0, 4, 9, 9, 9, 9) // grows the file to 34 bytes
	p = append(p, []byte("EOF")...)
	out, err := Apply(src, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 34 || !bytes.Equal(out[4:9], bytes.Repeat([]byte{0xAA}, 5)) || out[33] != 9 || out[3] != 1 {
		t.Fatalf("out = %v", out)
	}
	trunc := append(append([]byte(nil), p...), 0, 0, 20) // Lunar IPS truncate to 20 bytes
	out, err = Apply(src, trunc)
	if err != nil || len(out) != 20 {
		t.Fatalf("truncate: len=%d err=%v", len(out), err)
	}
}

// Damaged patches must fail cleanly, never panic.
func TestCorruptPatchesDontPanic(t *testing.T) {
	src := read(t, "src.bin")
	for _, name := range []string{"same.ips", "same.delta.bps", "same.linear.bps", "same.ups"} {
		p := read(t, name)
		for _, cut := range []int{5, 6, 9, 17, len(p) / 3, len(p) / 2, len(p) - 13, len(p) - 1} {
			if cut <= 0 || cut >= len(p) {
				continue
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s cut at %d: panic %v", name, cut, r)
					}
				}()
				Apply(src, p[:cut])
				Inspect(p[:cut])
			}()
		}
		// flip bytes in the middle
		q := append([]byte(nil), p...)
		for i := 8; i < len(q)-16; i += len(q) / 7 {
			q[i] ^= 0x5A
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s flipped: panic %v", name, r)
				}
			}()
			if _, err := Apply(src, q); err == nil && name != "same.ips" {
				t.Errorf("%s: damaged patch accepted", name)
			}
		}()
	}
}

func FuzzApply(f *testing.F) {
	for _, n := range []string{"same.ips", "same.delta.bps", "same.ups"} {
		b, _ := os.ReadFile(filepath.Join("testdata", n))
		f.Add(b[:min(len(b), 400)])
	}
	src := bytes.Repeat([]byte{7, 1, 2, 3}, 256)
	f.Fuzz(func(t *testing.T, p []byte) {
		Apply(src, p)
		Inspect(p)
	})
}
