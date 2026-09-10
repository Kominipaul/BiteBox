// Package imageproc turns an uploaded product photo — of whatever
// resolution and format a phone camera or gallery actually produced —
// into two small, predictable JPEGs (a full size and a thumbnail) safe to
// store and serve. Every guarantee here is enforced server-side: a browser
// can (and, via admin_dashboard.html's own JS, does) shrink/convert an
// image before it's ever sent, but nothing about this package trusts that
// happened. The same "never trust the client" rule the rest of this app
// already applies to prices, table numbers, and ingredient names.
//
// HEIC/HEIF (what an iPhone submits by default) is deliberately NOT
// decoded here — see ErrHEICNotSupported's doc comment for the reasoning.
package imageproc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"

	"golang.org/x/image/draw"
)

const (
	// MaxUploadBytes bounds the raw file before it's even decoded. A
	// modern phone photo is typically 2-8MB; 20MB leaves real headroom
	// without leaving the door open to an arbitrarily large upload eating
	// memory just to read it in.
	MaxUploadBytes = 20 << 20 // 20MB

	// MaxPixels bounds width*height, checked via image.DecodeConfig —
	// which reads only the file's header, not its full pixel data —
	// before the real decode runs. Without this, a small *file* claiming
	// an enormous *resolution* (a decompression bomb) would still get
	// fully decoded into memory, potentially gigabytes, before this
	// package ever gets a chance to shrink it back down.
	MaxPixels = 40_000_000 // 40MP — comfortably above any real phone camera

	// FullMaxDimension is the longest edge the "full" variant is resized
	// to, preserving aspect ratio — big enough for a guest's customize
	// sheet or any future larger view, well past what a phone camera
	// shoots (3000-4000px+ per side) needs to actually be stored at.
	FullMaxDimension = 1600

	// ThumbMaxDimension is the longest edge the "thumb" variant is
	// resized to — sized for the ~110-130px, object-fit:cover box the
	// menu list actually displays it in; 640px covers that comfortably
	// even at 4x DPI without ever asking a phone to download the full
	// 1600px copy just to show a thumbnail.
	ThumbMaxDimension = 640

	// JPEGQuality is the re-encode quality for both variants. Every
	// stored photo becomes a JPEG regardless of upload format — one
	// predictable, well-compressed format on disk rather than whatever
	// the client happened to send. 82 sits in the middle of the ~78-82
	// range that holds up on real food photos without visible blocking.
	JPEGQuality = 82

	// hashBytes is how much of the full variant's sha256 becomes its
	// filename (hex-encoded, so this is hashBytes*2 characters) — collision
	// risk at this length is astronomically below "this venue's entire
	// menu, re-photographed daily, for centuries," which is the actual
	// scale this app ever operates at.
	hashBytes = 16

	// fullSizeBudget/thumbSizeBudget are the target ceilings from the
	// design brief ("full" ~100-250KB, "thumb" under ~40KB) — sanity
	// checks, not hard limits: a real photo of a genuinely detailed dish
	// can legitimately land a bit over on a fixed quality setting, and
	// rejecting an otherwise-fine upload over a soft budget would be
	// worse than the disk space it saves. budgetWarnFactor is how far
	// over budget triggers a log warning (not an error) that something's
	// worth a human's attention — a bad quality setting, or a
	// pathologically noisy source image compressing far worse than a
	// normal photo.
	fullSizeBudget   = 250 << 10 // 250KB
	thumbSizeBudget  = 40 << 10  // 40KB
	budgetWarnFactor = 2
)

var (
	// ErrTooLarge means the raw upload exceeded MaxUploadBytes.
	ErrTooLarge = errors.New("image file is too large")
	// ErrTooManyPixels means the image's claimed resolution exceeded
	// MaxPixels — checked before the expensive full decode.
	ErrTooManyPixels = errors.New("image resolution is too large")
	// ErrNotAnImage means the file isn't a JPEG, PNG, or GIF Go's stdlib
	// can actually decode — includes a corrupt file, a non-image entirely,
	// or a format (other than HEIC, which gets its own clearer error) this
	// package doesn't support.
	ErrNotAnImage = errors.New("file is not a recognized image (JPEG, PNG, or GIF)")
	// ErrHEICNotSupported means the upload sniffed as HEIC/HEIF — the
	// format essentially every iPhone camera produces by default. See
	// this package's doc comment and detectHEIC's for the full reasoning;
	// short version: decoding HEIC server-side needs either a cgo binding
	// to libheif/libvips or shelling out to a system tool, both of which
	// require that C library actually be installed on whatever machine
	// this runs on. This app's one explicit deployment promise (see
	// README: "one Go binary, one SQLite file, no external services... a
	// Raspberry Pi") is incompatible with that, so this format is
	// rejected here with a message the frontend surfaces directly, and
	// converted client-side instead (see admin_dashboard.html's
	// heicFileToJPEG) in the one browser that can actually decode a HEIC
	// file to begin with — the same Safari that just produced it.
	ErrHEICNotSupported = errors.New("HEIC/HEIF photos aren't supported — please convert to JPEG first (iPhone: Settings > Camera > Formats > \"Most Compatible\"), or try the upload again in Safari")
)

// Variant is one resized, re-encoded copy of an uploaded photo.
type Variant struct {
	JPEG          []byte
	Width, Height int
}

// Result is everything Process produces from one upload: a "full" copy
// (see FullMaxDimension) and a "thumb" copy (see ThumbMaxDimension), plus
// a content hash of the full variant's bytes for the caller to name both
// files by (see internal/handlers/photo.go's writeProductPhoto) — a
// re-upload of visually-identical bytes gets the same hash, which is a
// feature: it lands on the same URL rather than manufacturing a new one
// for a photo that didn't actually change.
type Result struct {
	Full  Variant
	Thumb Variant
	// Hash is a hex-encoded, truncated sha256 of Full.JPEG.
	Hash string
}

// Process reads an uploaded image, validates it, and returns Result — two
// resized, re-encoded JPEGs ready to write to disk. It never panics on bad
// input; every failure mode returns one of this package's Err* values,
// which internal/handlers/photo.go's photoUploadErrorMessage maps to the
// text shown on the dashboard.
func Process(r io.Reader) (Result, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(raw) > MaxUploadBytes {
		return Result{}, ErrTooLarge
	}

	// Checked by content, not by filename/extension — a phone gallery
	// picker's reported name/MIME type is exactly the kind of client-
	// supplied metadata this package doesn't trust. detectHEIC reads the
	// ISO-BMFF container's own brand box, the same "sniff the actual
	// bytes" idea net/http.DetectContentType uses for the formats it
	// knows about (it doesn't know about HEIC specifically — see
	// detectHEIC's own comment on why this needs its own check).
	if detectHEIC(raw) {
		return Result{}, ErrHEICNotSupported
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrNotAnImage
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return Result{}, ErrTooManyPixels
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrNotAnImage
	}

	// Resize first, then correct orientation: a 90°-multiple rotate/flip
	// costs the same either way (it's still every pixel, once), but doing
	// it after resizeToMax means "every pixel" tops out around
	// FullMaxDimension², not the up-to-MaxPixels the original upload could
	// be — see orientation.go's own doc comment on why this matters. A
	// phone camera photo almost always carries an EXIF Orientation tag
	// (see readJPEGOrientation) rather than storing its pixels already
	// rotated upright.
	orientation := readJPEGOrientation(raw)
	full := applyEXIFOrientation(resizeToMax(img, FullMaxDimension), orientation)

	// Thumb is derived from the already-resized, already-upright full
	// image rather than the original decode — cheaper (every further
	// step now works on at most FullMaxDimension² pixels, not the
	// original's), and gives an identical result to resizing fresh from
	// the source: a further aspect-preserving downscale doesn't care
	// whether the pixels it's shrinking were already shrunk once.
	thumb := resizeToMax(full, ThumbMaxDimension)

	fullJPEG, err := encodeJPEG(full)
	if err != nil {
		return Result{}, err
	}
	thumbJPEG, err := encodeJPEG(thumb)
	if err != nil {
		return Result{}, err
	}

	checkSizeBudget("full", len(fullJPEG), fullSizeBudget)
	checkSizeBudget("thumb", len(thumbJPEG), thumbSizeBudget)

	sum := sha256.Sum256(fullJPEG)
	fb := full.Bounds()
	tb := thumb.Bounds()
	return Result{
		Full:  Variant{JPEG: fullJPEG, Width: fb.Dx(), Height: fb.Dy()},
		Thumb: Variant{JPEG: thumbJPEG, Width: tb.Dx(), Height: tb.Dy()},
		Hash:  hex.EncodeToString(sum[:hashBytes]),
	}, nil
}

// encodeJPEG re-encodes img as a JPEG at JPEGQuality. Go's stdlib
// image/jpeg encoder has no progressive-encoding mode (only baseline
// sequential) — libjpeg-turbo does, but wiring that in means the same
// cgo/system-library tradeoff as HEIC decoding (see ErrHEICNotSupported),
// which this app avoids for the same "single Go binary" reason. Baseline
// JPEG at this quality/size is still small and loads fine progressively
// enough in practice (a phone on the same wifi as the venue's server) that
// this isn't worth that tradeoff on its own.
//
// image/jpeg also never writes an EXIF segment of its own, so re-encoding
// through it already strips whatever GPS/device metadata the original
// upload carried — no separate "strip EXIF" step needed.
func encodeJPEG(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// checkSizeBudget logs a warning (never an error — this never blocks the
// upload) when an encoded variant comes out more than budgetWarnFactor
// times its target budget, so a quality setting that's quietly too
// generous, or a pathologically noisy source image, shows up in the logs
// instead of just slowly bloating disk usage unnoticed.
func checkSizeBudget(name string, actual, budget int) {
	if actual > budget*budgetWarnFactor {
		log.Printf("imageproc: %s variant is %dKB, more than %dx its ~%dKB budget — a pathological source image, or JPEGQuality may need lowering",
			name, actual/1024, budgetWarnFactor, budget/1024)
	}
}

// resizeToMax scales img down so its longer edge is at most maxDim,
// preserving aspect ratio — a no-op (aside from the eventual JPEG
// re-encode in Process) if it's already smaller, since there's no reason
// to resample (or, worse, upscale) an image that's already small.
// draw.CatmullRom (bicubic) is the highest-quality kernel x/image/draw
// ships — x/image/draw has no true Lanczos kernel, and pulling in a
// dedicated resampling library for that one filter isn't worth it for a
// difference a food photo scaled to a few hundred px won't show.
func resizeToMax(img image.Image, maxDim int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}

	var newW, newH int
	if w >= h {
		newW = maxDim
		newH = h * maxDim / w
	} else {
		newH = maxDim
		newW = w * maxDim / h
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// isoBMFFBrands are the 4-character ISO-BMFF "brand" codes that mean
// "this container holds HEIC/HEIF image data" — both the major_brand
// (bytes 8-11 of the file) and every compatible_brand listed after it are
// checked against this set, since a real-world HEIC file (an iPhone's, in
// particular) typically lists more than one.
var isoBMFFBrands = map[string]bool{
	"heic": true, "heix": true, "hevc": true, "hevx": true,
	"heim": true, "heis": true, "hevm": true, "hevs": true,
	"mif1": true, "msf1": true,
}

// detectHEIC reports whether raw is an ISO-BMFF container (the format
// family HEIC/HEIF, and also MP4/QuickTime, are built on) carrying a
// HEIC/HEIF brand. net/http.DetectContentType recognizes the generic
// ISO-BMFF "ftyp" box — enough to call it "video/mp4" — but doesn't look
// at *which* brand it is, so it can't tell a HEIC photo apart from an
// actual MP4 video; this package needs that distinction to give a HEIC
// upload its own clear, actionable error instead of the generic
// ErrNotAnImage. See the ISO/IEC 14496-12 box layout: 4-byte box size,
// 4-byte box type ("ftyp"), 4-byte major_brand, 4-byte minor_version, then
// a list of 4-byte compatible_brand entries filling out the rest of the
// box.
func detectHEIC(raw []byte) bool {
	if len(raw) < 12 || string(raw[4:8]) != "ftyp" {
		return false
	}
	if isoBMFFBrands[string(raw[8:12])] {
		return true
	}
	boxSize := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	if boxSize < 16 || boxSize > len(raw) {
		boxSize = len(raw)
	}
	for i := 16; i+4 <= boxSize; i += 4 {
		if isoBMFFBrands[string(raw[i:i+4])] {
			return true
		}
	}
	return false
}
