//go:build ignore

// Regenerates static/icons/icon-{192,512}.png — the PWA/notification icons
// used by handlers.WebManifest, the per-table GuestWebManifest, and
// static/sw.js's showNotification calls. A literal "bite" mark: a rounded
// plate/box shape (the previous version's own motif — kept, since it
// already read as a plate) with a circular bite taken out of one corner
// plus a couple of stray crumbs — bold, high-contrast, single-glance
// readable at a 40px home-screen size, which fine detail (cutlery, a QR
// pattern, actual text) all fail at. Real raster files, not inline SVG —
// app icons and notification icons both need one.
//
// Run from the repo root whenever the brand colors change:
//
//	go run scripts/gen-icons.go
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

var (
	accent = color.RGBA{0xD9, 0xA4, 0x41, 0xFF} // --accent
	bgBase = color.RGBA{0x14, 0x10, 0x0D, 0xFF}  // --bg-base
)

// fillRoundedRect draws a filled rounded rectangle into img.
func fillRoundedRect(img draw.Image, x0, y0, x1, y1, radius int, c color.Color) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			inCornerBox := (x < x0+radius || x >= x1-radius) && (y < y0+radius || y >= y1-radius)
			if !inCornerBox {
				img.Set(x, y, c)
				continue
			}
			ccx, ccy := x0+radius, y0+radius
			if x >= x1-radius {
				ccx = x1 - radius - 1
			}
			if y >= y1-radius {
				ccy = y1 - radius - 1
			}
			dx, dy := x-ccx, y-ccy
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			}
		}
	}
}

// fillCircle draws a filled circle centered at (cx, cy) into img — used
// both for the bite itself (a big one, in the background color, punched
// into the plate) and the crumbs (small ones, in the plate's own color).
func fillCircle(img draw.Image, cx, cy, radius int, c color.Color) {
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			}
		}
	}
}

func makeIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// Bold accent-gold background — the OS applies its own mask
	// (circle/squircle) on top, so no need to pre-round this outer edge.
	draw.Draw(img, img.Bounds(), &image.Uniform{accent}, image.Point{}, draw.Src)

	// The "plate": a large dark rounded square, generously sized (78% of
	// the icon) so the bite taken out of its corner still leaves an
	// unambiguous, chunky shape at a 40px home-screen size — the previous
	// version's inner cutout square read as an abstract donut/frame at
	// that size, not as anything in particular.
	plate := size * 78 / 100
	px0, py0 := (size-plate)/2, (size-plate)/2
	px1, py1 := px0+plate, py0+plate
	fillRoundedRect(img, px0, py0, px1, py1, plate/6, bgBase)

	// The bite: a circle in the background color, centered just outside
	// the plate's top-right corner so it overlaps and "eats into" it —
	// sized big enough (28% of the icon) to read unambiguously as a bite
	// rather than a rounded-off corner, even shrunk to a ~40px home-screen
	// icon.
	biteRadius := size * 28 / 100
	biteCx, biteCy := px1-size*3/100, py0+size*3/100
	fillCircle(img, biteCx, biteCy, biteRadius, accent)

	// Two stray crumbs sitting in the gold pocket the bite just opened up
	// (below-right of the plate's corner) — in the plate's own dark
	// color, sized to still show as distinct dots (not just noise) at
	// small sizes.
	fillCircle(img, px1-size*10/100, py0+size*30/100, size*5/100, bgBase)
	fillCircle(img, px1+size*2/100, py0+size*18/100, size*3/100, bgBase)

	return img
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func main() {
	if err := os.MkdirAll("static/icons", 0o755); err != nil {
		panic(err)
	}
	writePNG("static/icons/icon-192.png", makeIcon(192))
	writePNG("static/icons/icon-512.png", makeIcon(512))
}
