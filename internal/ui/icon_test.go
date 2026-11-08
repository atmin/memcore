package ui

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestIconPNG(t *testing.T) {
	for _, size := range []int{16, 64, 256} {
		data, err := IconPNG(size)
		if err != nil {
			t.Fatalf("IconPNG(%d): %v", size, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode IconPNG(%d): %v", size, err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("IconPNG(%d) is %d x %d", size, b.Dx(), b.Dy())
		}
	}
}

func TestICNS(t *testing.T) {
	data, err := ICNS()
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "icns" {
		t.Fatalf("ICNS starts with %q, want icns", data[:4])
	}
	if n := binary.BigEndian.Uint32(data[4:8]); int(n) != len(data) {
		t.Fatalf("the header says %d bytes, and the file has %d", n, len(data))
	}
	want := map[string]int{"icp4": 16, "icp5": 32, "icp6": 64, "ic07": 128, "ic08": 256, "ic09": 512, "ic10": 1024,
		"ic11": 32, "ic12": 64, "ic13": 256, "ic14": 512}
	seen := map[string]bool{}
	for rest := data[8:]; len(rest) > 0; {
		if len(rest) < 8 {
			t.Fatalf("an entry header is cut off: %d bytes left", len(rest))
		}
		kind, n := string(rest[:4]), int(binary.BigEndian.Uint32(rest[4:8]))
		if n < 8 || n > len(rest) {
			t.Fatalf("the entry %s says %d bytes, and %d are left", kind, n, len(rest))
		}
		img, err := png.Decode(bytes.NewReader(rest[8:n]))
		if err != nil {
			t.Fatalf("decode the entry %s: %v", kind, err)
		}
		if size, ok := want[kind]; !ok {
			t.Errorf("unexpected entry %s", kind)
		} else if img.Bounds().Dx() != size {
			t.Errorf("the entry %s is %d px, want %d", kind, img.Bounds().Dx(), size)
		}
		seen[kind] = true
		rest = rest[n:]
	}
	for kind := range want {
		if !seen[kind] {
			t.Errorf("ICNS has no entry %s", kind)
		}
	}
}
