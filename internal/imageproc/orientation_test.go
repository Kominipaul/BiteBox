package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// exifApp1 builds a complete APP1 marker segment (marker bytes included)
// carrying nothing but a single EXIF Orientation tag — just enough for
// readJPEGOrientation/Process to have something real to parse, not a
// faithful general-purpose EXIF writer.
func exifApp1(t *testing.T, orientation uint16) []byte {
	t.Helper()
	var tiff bytes.Buffer
	tiff.WriteString("II")                                              // little-endian
	tiff.Write([]byte{0x2A, 0x00})                                      // TIFF magic (42)
	tiff.Write([]byte{0x08, 0x00, 0, 0})                                // IFD0 offset: right after this 8-byte header
	tiff.Write([]byte{0x01, 0x00})                                      // 1 IFD entry
	tiff.Write([]byte{0x12, 0x01})                                      // tag 0x0112 = Orientation
	tiff.Write([]byte{0x03, 0x00})                                      // type 3 = SHORT
	tiff.Write([]byte{0x01, 0, 0, 0})                                   // count 1
	tiff.Write([]byte{byte(orientation), byte(orientation >> 8), 0, 0}) // value + padding
	tiff.Write([]byte{0, 0, 0, 0})                                      // next IFD offset: none

	var payload bytes.Buffer
	payload.WriteString("Exif\x00\x00")
	payload.Write(tiff.Bytes())

	segLen := payload.Len() + 2 // +2 for the length field itself
	var seg bytes.Buffer
	seg.Write([]byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)})
	seg.Write(payload.Bytes())
	return seg.Bytes()
}

// jpegWithOrientation builds a real w x h JPEG — left half one solid color,
// right half another, so a rotation/flip is visible in the *pixels*, not
// just in the reported dimensions — with an EXIF Orientation tag spliced in
// right after the SOI marker.
func jpegWithOrientation(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.Set(x, y, color.RGBA{R: 255, A: 255}) // left = red
			} else {
				img.Set(x, y, color.RGBA{B: 255, A: 255}) // right = blue
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("building test JPEG: %v", err)
	}
	plain := buf.Bytes()

	app1 := exifApp1(t, orientation)
	out := make([]byte, 0, len(plain)+len(app1))
	out = append(out, plain[:2]...) // SOI
	out = append(out, app1...)
	out = append(out, plain[2:]...)
	return out
}

func isRed(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > b
}
func isBlue(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return b > r
}

func TestReadJPEGOrientationParsesEmbeddedTag(t *testing.T) {
	for _, want := range []int{1, 2, 3, 4, 5, 6, 7, 8} {
		data := jpegWithOrientation(t, 40, 20, uint16(want))
		if got := readJPEGOrientation(data); got != want {
			t.Errorf("orientation %d: readJPEGOrientation = %d, want %d", want, got, want)
		}
	}
}

func TestReadJPEGOrientationDefaultsToNormalWithoutEXIF(t *testing.T) {
	if got := readJPEGOrientation(solidJPEG(t, 40, 20)); got != 1 {
		t.Errorf("plain JPEG with no EXIF: readJPEGOrientation = %d, want 1", got)
	}
	if got := readJPEGOrientation(solidPNG(t, 40, 20)); got != 1 {
		t.Errorf("PNG (no EXIF concept at all): readJPEGOrientation = %d, want 1", got)
	}
	if got := readJPEGOrientation([]byte("not an image")); got != 1 {
		t.Errorf("garbage input: readJPEGOrientation = %d, want 1", got)
	}
}

// TestProcessAutoRotatesEXIFOrientedUpload is the real end-to-end
// regression test: a phone camera's landscape sensor capture plus an EXIF
// tag saying "rotate 90° clockwise to display this correctly" (value 6,
// the single most common real-world case — a phone held upright while
// shooting) must come out of Process already rotated, since the JPEG
// re-encode here doesn't preserve EXIF for anything downstream to apply it
// from later.
func TestProcessAutoRotatesEXIFOrientedUpload(t *testing.T) {
	raw := jpegWithOrientation(t, 40, 20, 6) // landscape in, orientation says "rotate 90 CW"
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(result.Full.JPEG))
	if err != nil {
		t.Fatalf("Process's output isn't a valid JPEG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("output is %dx%d, want 20x40 (a 40x20 source rotated 90°)", b.Dx(), b.Dy())
	}
	// The source's left (red) half must land in the *top* of the rotated
	// image, and its right (blue) half in the *bottom* — see orientation.go's
	// rotate90CW: rotating a rectangle 90° clockwise turns its left edge
	// into its new top edge.
	if !isRed(img.At(10, 5)) {
		t.Error("top of the rotated image should be red (source's left half)")
	}
	if !isBlue(img.At(10, 35)) {
		t.Error("bottom of the rotated image should be blue (source's right half)")
	}
}

// TestProcessLeavesNormalOrientationUnrotated guards the far more common
// case — no EXIF, or an explicit "1: normal" — against ever being touched
// by the rotation path at all.
func TestProcessLeavesNormalOrientationUnrotated(t *testing.T) {
	raw := solidJPEG(t, 40, 20)
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(result.Full.JPEG))
	if err != nil {
		t.Fatalf("Process's output isn't a valid JPEG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 40 || b.Dy() != 20 {
		t.Errorf("output is %dx%d, want unchanged 40x20 — no EXIF means no rotation", b.Dx(), b.Dy())
	}
}
