package imageproc

import (
	"encoding/binary"
	"image"
)

// orientationExifTag is the EXIF tag ID (0x0112) that carries a JPEG's
// intended display orientation — see readJPEGOrientation.
const orientationExifTag = 0x0112

// readJPEGOrientation scans a JPEG's own bytes for an EXIF APP1 segment and
// returns its Orientation tag (1-8, per the EXIF spec — 1 means "normal,
// no rotation needed"). It returns 1 (do nothing) for anything that isn't a
// well-formed JPEG-with-EXIF: a PNG/GIF, a JPEG with no EXIF at all (most
// screenshots, most non-camera sources), or any parse failure along the
// way. A camera photo missing or mangled metadata should still get stored,
// just without the auto-rotate this enables — never a reason to reject the
// upload outright.
//
// Why this exists at all: a phone held sideways doesn't rotate the actual
// sensor data before saving — it writes the frame as captured and sets this
// tag so a viewer knows to rotate it for display. Go's stdlib image/jpeg
// decoder has no idea this tag exists; it decodes exactly the raw grid,
// and re-encoding via image/jpeg (see Process) doesn't carry the tag
// forward either. Left alone, that's a portrait photo silently stored (and
// then served to every guest) rotated 90°.
func readJPEGOrientation(data []byte) int {
	// Every JPEG starts with the SOI marker; anything else (a PNG, a GIF,
	// garbage) has no EXIF to find here.
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}

	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 1 // not a well-formed marker stream — give up quietly
		}
		marker := data[pos+1]

		// SOS (Start of Scan) is where the actual compressed image data
		// begins — EXIF (APP1) always appears before it, so reaching this
		// with no match means there isn't one.
		if marker == 0xDA {
			return 1
		}
		// A handful of markers (0x01, the RST0-RST7 restart markers
		// 0xD0-0xD9) carry no length/payload of their own.
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			pos += 2
			continue
		}
		if pos+4 > len(data) {
			return 1
		}
		segLen := int(data[pos+2])<<8 | int(data[pos+3])
		if segLen < 2 || pos+2+segLen > len(data) {
			return 1 // truncated/malformed segment length
		}

		if marker == 0xE1 { // APP1 — where EXIF lives, when present
			if o := parseExifOrientation(data[pos+4 : pos+2+segLen]); o != 0 {
				return o
			}
			return 1 // an APP1 segment that isn't EXIF, or has no Orientation tag
		}
		pos += 2 + segLen
	}
	return 1
}

// parseExifOrientation reads the Orientation tag out of one EXIF APP1
// segment's payload (everything after the segment's own length bytes), or
// returns 0 if it isn't there or the segment doesn't parse as valid TIFF —
// 0 rather than 1 so readJPEGOrientation can tell "not found" apart from
// "found and it's 1" if that distinction ever matters, though both
// currently mean the same thing to the caller.
func parseExifOrientation(seg []byte) int {
	if len(seg) < 10 || string(seg[0:6]) != "Exif\x00\x00" {
		return 0
	}
	tiff := seg[6:]
	if len(tiff) < 8 {
		return 0
	}

	var bo binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}

	ifdOffset := bo.Uint32(tiff[4:8])
	if int(ifdOffset)+2 > len(tiff) {
		return 0
	}
	numEntries := int(bo.Uint16(tiff[ifdOffset:]))
	entriesStart := int(ifdOffset) + 2

	for i := 0; i < numEntries; i++ {
		entryOff := entriesStart + i*12
		if entryOff+12 > len(tiff) {
			break
		}
		tag := bo.Uint16(tiff[entryOff:])
		if tag != orientationExifTag {
			continue
		}
		// Orientation is always type SHORT (3), count 1 — its value fits
		// directly in the entry's 4-byte value field (the first 2 bytes of
		// it, for this type), never needs the offset-elsewhere indirection
		// a longer/variable-length tag value would.
		valType := bo.Uint16(tiff[entryOff+2:])
		if valType != 3 {
			return 0
		}
		val := int(bo.Uint16(tiff[entryOff+8:]))
		if val < 1 || val > 8 {
			return 0
		}
		return val
	}
	return 0
}

// applyEXIFOrientation returns img rotated/flipped upright per orientation
// (an EXIF Orientation tag value, 1-8) — or img itself, unchanged, for 1
// (already normal) or any value outside 1-8 this package doesn't
// recognize. Values 5-8 combine a flip with a 90° turn — see the EXIF
// spec's own orientation diagram; there's no way to describe them as
// "just" a rotation or "just" a flip.
func applyEXIFOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return flipHorizontal(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipVertical(img)
	case 5:
		return flipHorizontal(rotate90CW(img))
	case 6:
		return rotate90CW(img)
	case 7:
		return flipHorizontal(rotate90CCW(img))
	case 8:
		return rotate90CCW(img)
	default:
		return img
	}
}

// rotate90CW, rotate90CCW, rotate180, flipHorizontal, and flipVertical all
// walk every pixel once via the generic image.Image interface (At/Set)
// rather than assuming a concrete underlying type — the input here can be
// *image.YCbCr (a decoded JPEG), *image.NRGBA/*image.Paletted (PNG/GIF), or
// *image.RGBA (already resized). That's only ever a handful of megapixels
// at most by the time these run (see Process — orientation is corrected
// *after* resizeToMax, not before, specifically so this generic pixel-by-
// pixel walk has as little work to do as possible), so the interface
// overhead doesn't matter here the way it would on a hot path.

func rotate90CW(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate90CCW(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipHorizontal(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipVertical(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
