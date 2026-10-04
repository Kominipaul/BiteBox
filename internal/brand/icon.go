package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The venue's app icon: what "Add to Home Screen" puts on the phone, the
// browser tab, and the PWA install prompt. Generated from the saved theme so
// every venue gets its own instead of one shared placeholder:
//   - an uploaded icon (or a squarish main logo) is centred on a plain
//     background chosen to contrast with it;
//   - otherwise a monogram: the venue's first letter on its primary colour.
//
// iOS only shows an opaque, square PNG (it rounds the corners itself), so this
// is always fully opaque.

// Mono is the venue's one-letter mark: the first letter or digit of its name.
func Mono(name string) string {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "•"
}

// CSSString quotes s for a CSS content: "…" value.
func CSSString(s string) string {
	// \3C keeps "</style>" from ever appearing when the CSS sits in a page.
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ", "<", `\3C `).Replace(s)
	return `"` + s + `"`
}

func parseRGBA(h string) color.RGBA {
	c := parse(h)
	return color.RGBA{uint8(c.r), uint8(c.g), uint8(c.b), 255}
}

// RenderIcon draws a size×size icon. logo may be nil (monogram).
func RenderIcon(size int, t Theme, name string, logo image.Image) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg, fg := parseRGBA(t.Primary), parseRGBA(On(t.Primary))
	if logo != nil {
		// Plain background that contrasts with the logo: white behind a dark
		// logo, near-black behind a light one.
		if logoLuma(logo) > 0.5 {
			bg = color.RGBA{18, 18, 20, 255}
		} else {
			bg = color.RGBA{255, 255, 255, 255}
		}
	}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	if logo == nil {
		// Monogram: a soft light from the top, so it reads as a finished app
		// icon rather than a flat swatch. The bottom row is exactly the primary.
		for y := 0; y < size; y++ {
			c := parseRGBA(mix(t.Primary, "#FFFFFF", 0.14*(1-float64(y)/float64(size-1))))
			draw.Draw(img, image.Rect(0, y, size, y+1), &image.Uniform{c}, image.Point{}, draw.Src)
		}
	}
	if logo != nil {
		b := logo.Bounds()
		box := float64(size) * 0.66 // leave a margin: iOS rounds the corners
		sc := box / float64(max(b.Dx(), b.Dy()))
		w, h := max(1, int(float64(b.Dx())*sc)), max(1, int(float64(b.Dy())*sc))
		dst := image.Rect((size-w)/2, (size-h)/2, (size-w)/2+w, (size-h)/2+h)
		xdraw.CatmullRom.Scale(img, dst, logo, b, xdraw.Over, nil)
	} else {
		drawLetter(img, Mono(name), fg, size)
	}
	var out bytes.Buffer
	png.Encode(&out, img)
	return out.Bytes()
}

// logoLuma is the average brightness (0–1) of the logo's visible pixels.
func logoLuma(m image.Image) float64 {
	b := m.Bounds()
	var sum, n float64
	step := max(1, max(b.Dx(), b.Dy())/64)
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, a := m.At(x, y).RGBA()
			if a < 0x4000 {
				continue // transparent
			}
			sum += (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)) / float64(a)
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / n
}

func drawLetter(img *image.RGBA, letter string, fg color.RGBA, size int) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(size) * 0.62, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: &image.Uniform{fg}, Face: face}
	b, adv := d.BoundString(letter)
	// Centre the glyph's ink box, not its advance width.
	x := fixed.I(size)/2 - (b.Min.X+b.Max.X)/2
	y := fixed.I(size)/2 - (b.Min.Y+b.Max.Y)/2
	_ = adv
	d.Dot = fixed.Point26_6{X: x, Y: y}
	d.DrawString(letter)
}
