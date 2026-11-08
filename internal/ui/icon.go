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
const gridSize = 32

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

// IconImage draws the icon on a square of size pixels, in the shape of a macOS application icon: a white
// tile with round corners and a margin, and the black letters on it, scaled by whole pixels.
func IconImage(size int) (image.Image, error) {
	if size < 64 {
		big, err := IconImage(64)
		if err != nil {
			return nil, err
		}
		return shrink(big, size), nil
	}
	g, err := IconGrid()
	if err != nil {
		return nil, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	// macOS draws an icon tile of 824 of 1024 pixels, with a corner radius of about 185.
	tile := size * 824 / 1024
	off := (size - tile) / 2
	radius := float64(tile) * 185 / 824
	for y := 0; y < tile; y++ {
		for x := 0; x < tile; x++ {
			if inRound(x, y, tile, radius) {
				img.SetNRGBA(off+x, off+y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	cell := tile / gridSize
	if cell < 1 {
		cell = 1
	}
	inner := (size - cell*gridSize) / 2
	for gy := 0; gy < gridSize; gy++ {
		for gx := 0; gx < gridSize; gx++ {
			if !g[gy][gx] {
				continue
			}
			for y := 0; y < cell; y++ {
				for x := 0; x < cell; x++ {
					img.SetNRGBA(inner+gx*cell+x, inner+gy*cell+y, color.NRGBA{0, 0, 0, 255})
				}
			}
		}
	}
	return img, nil
}

// shrink scales a square image down to size pixels, by the average of each block.
func shrink(src image.Image, size int) image.Image {
	n := src.Bounds().Dx()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a, count uint32
			for sy := y * n / size; sy < (y+1)*n/size; sy++ {
				for sx := x * n / size; sx < (x+1)*n/size; sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					r, g, b, a = r+uint32(c.R)*uint32(c.A), g+uint32(c.G)*uint32(c.A), b+uint32(c.B)*uint32(c.A), a+uint32(c.A)
					count++
				}
			}
			if a == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / count)})
		}
	}
	return dst
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
