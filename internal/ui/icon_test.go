package ui

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

func TestIconSVG(t *testing.T) {
	note, err := os.ReadFile(filepath.Join("testdata", "icon-note.svg"))
	if err != nil {
		t.Fatal(err)
	}
	svg, err := static.ReadFile("static/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(svg, note) {
		t.Error("icon.svg differs from the SVG of the note")
	}
}

func TestIconGrid(t *testing.T) {
	g, err := IconGrid()
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 16 || len(g[0]) != 16 {
		t.Fatalf("the grid is %d x %d, want 16 x 16", len(g[0]), len(g))
	}
	for _, p := range []struct {
		x, y int
		set  bool
	}{{0, 3, true}, {15, 13, true}, {1, 3, false}, {0, 0, false}} {
		if g[p.y][p.x] != p.set {
			t.Errorf("the pixel (%d,%d) is %v, want %v", p.x, p.y, g[p.y][p.x], p.set)
		}
	}
	n := 0
	for _, row := range g {
		for _, on := range row {
			if on {
				n++
			}
		}
	}
	if n != 80 {
		t.Errorf("the grid has %d letter pixels, want 80", n)
	}
}

func TestIconImage(t *testing.T) {
	g, err := IconGrid()
	if err != nil {
		t.Fatal(err)
	}
	black := color.NRGBA{0, 0, 0, 255}
	white := color.NRGBA{255, 255, 255, 255}
	for _, size := range []int{16, 32, 64, 512, 1024} {
		img, err := IconImage(size)
		if err != nil {
			t.Fatalf("IconImage(%d): %v", size, err)
		}
		at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) }
		if size <= 32 {
			// Full bleed: each pixel takes the color of its cell.
			cell := size / 16
			for y := range size {
				for x := range size {
					want := white
					if g[y/cell][x/cell] {
						want = black
					}
					if at(x, y) != want {
						t.Fatalf("IconImage(%d) at (%d,%d) is %v, want %v", size, x, y, at(x, y), want)
					}
				}
			}
			continue
		}
		// Inset: each letter cell is whole black pixels on the white tile, and no letter pixel leaves the tile.
		tile := size * 824 / 1024
		off := (size - tile) / 2
		radius := float64(tile) * 185 / 824
		cell := tile / 20
		inner := (size - cell*16) / 2
		for gy := range 16 {
			for gx := range 16 {
				for y := inner + gy*cell; y < inner+(gy+1)*cell; y++ {
					for x := inner + gx*cell; x < inner+(gx+1)*cell; x++ {
						if !g[gy][gx] {
							continue
						}
						if at(x, y) != black {
							t.Fatalf("IconImage(%d) at (%d,%d) is %v, want black", size, x, y, at(x, y))
						}
						if !inRound(x-off, y-off, tile, radius) {
							t.Fatalf("IconImage(%d): the letter pixel (%d,%d) leaves the tile", size, x, y)
						}
					}
				}
			}
		}
		if c := at(size/2, off+1); c != white {
			t.Errorf("IconImage(%d): the tile at (%d,%d) is %v, want white", size, size/2, off+1, c)
		}
		if c := at(0, 0); c.A != 0 {
			t.Errorf("IconImage(%d): the corner outside the tile is %v, want transparent", size, c)
		}
	}
	for _, size := range []int{0, 24, 48, 100} {
		if _, err := IconImage(size); err == nil {
			t.Errorf("IconImage(%d) draws, want an error", size)
		}
	}
}
