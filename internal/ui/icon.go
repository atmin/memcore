package ui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
)

// gridSize is the side of the pixel grid of the icon, in pixels.
const gridSize = 16

// rectPath matches one pixel run of the letters in icon.svg: "M<x> <y>h<w>v<h>H<x>z".
var rectPath = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)H\d+z`)

// IconGrid returns the pixel grid of the icon: true where a letter is. It reads the letters of icon.svg.
func IconGrid() ([gridSize][gridSize]bool, error) {
	var g [gridSize][gridSize]bool
	svg, err := static.ReadFile("static/icon.svg")
	if err != nil {
		return g, fmt.Errorf("read the icon: %w", err)
	}
	start := bytes.Index(svg, []byte(`class="letters"`))
	if start < 0 {
		return g, fmt.Errorf("read the icon: icon.svg holds no letters")
	}
	runs := rectPath.FindAllSubmatch(svg[start:], -1)
	if len(runs) == 0 {
		return g, fmt.Errorf("read the icon: icon.svg holds no pixel runs")
	}
	for _, r := range runs {
		var n [4]int
		for i := range n {
			n[i], _ = strconv.Atoi(string(r[i+1]))
		}
		x, y, w, h := n[0], n[1], n[2], n[3]
		for yy := y; yy < y+h && yy < gridSize; yy++ {
			for xx := x; xx < x+w && xx < gridSize; xx++ {
				g[yy][xx] = true
			}
		}
	}
	return g, nil
}

// IconImage draws the icon on a square of size pixels, with whole pixels for each cell of the grid. At 16 and
// 32 px, the grid fills the white square, as icon.svg draws it. From 64 px, it sits inset on a white tile with
// round corners, the shape of a macOS application icon, because the letters span the full width of the grid.
func IconImage(size int) (image.Image, error) {
	if size != 16 && size != 32 && (size <= 0 || size%64 != 0) {
		return nil, fmt.Errorf("draw the icon at %d px: the size is 16, 32 or a multiple of 64", size)
	}
	g, err := IconGrid()
	if err != nil {
		return nil, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	white, black := color.NRGBA{255, 255, 255, 255}, color.NRGBA{0, 0, 0, 255}
	cell, inner := size/gridSize, 0
	if size <= 32 {
		for y := range size {
			for x := range size {
				img.SetNRGBA(x, y, white)
			}
		}
	} else {
		// macOS draws an icon tile of 824 of 1024 pixels, with a corner radius of about 185.
		tile := size * 824 / 1024
		off := (size - tile) / 2
		radius := float64(tile) * 185 / 824
		for y := range tile {
			for x := range tile {
				if inRound(x, y, tile, radius) {
					img.SetNRGBA(off+x, off+y, white)
				}
			}
		}
		// A cell of a twentieth of the tile leaves two cells of margin on each side of the full-width letters.
		cell = tile / 20
		inner = (size - cell*gridSize) / 2
	}
	for gy := range gridSize {
		for gx := range gridSize {
			if !g[gy][gx] {
				continue
			}
			for y := range cell {
				for x := range cell {
					img.SetNRGBA(inner+gx*cell+x, inner+gy*cell+y, black)
				}
			}
		}
	}
	return img, nil
}

// inRound reports whether a pixel lies inside a square of side n with corners of the radius.
func inRound(x, y, n int, radius float64) bool {
	fx, fy := float64(x)+0.5, float64(y)+0.5
	cx, cy := fx, fy
	if fx < radius {
		cx = radius
	} else if fx > float64(n)-radius {
		cx = float64(n) - radius
	}
	if fy < radius {
		cy = radius
	} else if fy > float64(n)-radius {
		cy = float64(n) - radius
	}
	dx, dy := fx-cx, fy-cy
	return dx*dx+dy*dy <= radius*radius
}

// IconPNG returns the icon as a PNG of size pixels.
func IconPNG(size int) ([]byte, error) {
	img, err := IconImage(size)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, fmt.Errorf("encode the icon: %w", err)
	}
	return b.Bytes(), nil
}

// ICNS returns the icon as an .icns file of macOS, with PNG images from 16 to 1024 pixels.
func ICNS() ([]byte, error) {
	types := []struct {
		kind string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	var body bytes.Buffer
	for _, t := range types {
		data, err := IconPNG(t.size)
		if err != nil {
			return nil, err
		}
		body.WriteString(t.kind)
		binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}
