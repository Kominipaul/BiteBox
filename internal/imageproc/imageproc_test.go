package imageproc

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"strings"
	"testing"
)

// solidJPEG/solidPNG build a minimal valid image of the given dimensions
// in-process — no fixture files needed, and it keeps the test fast and
// self-contained. A solid gradient compresses far smaller than a real
// photo, which is fine for dimension/orientation assertions but useless
// for the file-size-budget tests — see noisyJPEG for those.
func solidJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("building test JPEG: %v", err)
	}
	return buf.Bytes()
}

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("building test PNG: %v", err)
	}
	return buf.Bytes()
}

// noisyJPEG builds a synthetic "photo-like" image: several overlaid sine
// waves at different frequencies/phases per channel, which — unlike
// solidJPEG's flat gradient (compresses to near-nothing) or true per-pixel
// random noise (compresses far *worse* than any real photo, since a
// camera's optics/sensor always leave neighboring pixels correlated) —
// lands in a JPEG-compressed-size ballpark representative of a real food
// photo's actual detail, without needing a real photo fixture checked
// into the repo.
func noisyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Periods scaled to image size (roughly "a few dozen features across
	// the frame," not a few dozen *pixels*) so this still reads as
	// low-frequency, photo-like color/shading variation after a real
	// resize shrinks it — a period tighter than the target resolution can
	// resolve is exactly the sub-pixel noise real optics/resampling never
	// produces, and JPEG can't compress that away the way it can a real
	// photo's smoother detail.
	pw, ph := float64(w)/30, float64(h)/30
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x), float64(y)
			r := 128 + 100*math.Sin(fx/pw)*math.Cos(fy/ph)
			g := 128 + 100*math.Sin(fx/pw*1.7+fy/ph)
			b := 128 + 100*math.Cos(fx/pw-fy/ph*1.3)
			img.Set(x, y, color.RGBA{R: uint8(clamp255(r)), G: uint8(clamp255(g)), B: uint8(clamp255(b)), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		t.Fatalf("building photo-like test JPEG: %v", err)
	}
	return buf.Bytes()
}

func clamp255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func decodedSize(t *testing.T, jpegBytes []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		t.Fatalf("Process's own output isn't a valid JPEG: %v", err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func TestProcessResizesBothVariantsWithinTheirCaps(t *testing.T) {
	// Simulates a real phone camera photo — well past what this app ever
	// needs to store or serve.
	raw := solidJPEG(t, 4032, 3024)
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	fw, fh := decodedSize(t, result.Full.JPEG)
	if fw > FullMaxDimension || fh > FullMaxDimension {
		t.Fatalf("full variant is %dx%d, want both dimensions <= %d", fw, fh, FullMaxDimension)
	}
	if fw != FullMaxDimension {
		t.Errorf("full: expected the wider edge (%d) to be scaled to exactly FullMaxDimension (%d)", fw, FullMaxDimension)
	}
	wantFH := 3024 * FullMaxDimension / 4032
	if fh != wantFH {
		t.Errorf("full height = %d, want %d (aspect ratio not preserved)", fh, wantFH)
	}
	if result.Full.Width != fw || result.Full.Height != fh {
		t.Errorf("Full.Width/Height = %dx%d, want they match the actual encoded image %dx%d", result.Full.Width, result.Full.Height, fw, fh)
	}

	tw, th := decodedSize(t, result.Thumb.JPEG)
	if tw > ThumbMaxDimension || th > ThumbMaxDimension {
		t.Fatalf("thumb variant is %dx%d, want both dimensions <= %d", tw, th, ThumbMaxDimension)
	}
	if tw != ThumbMaxDimension {
		t.Errorf("thumb: expected the wider edge (%d) to be scaled to exactly ThumbMaxDimension (%d)", tw, ThumbMaxDimension)
	}
	// Aspect ratio should still read as 4:3 (allow ±1px for two independent
	// integer-rounded resizes — full-from-original, thumb-from-full).
	wantTH := 3024 * ThumbMaxDimension / 4032
	if d := th - wantTH; d < -1 || d > 1 {
		t.Errorf("thumb height = %d, want ~%d (aspect ratio not preserved)", th, wantTH)
	}
}

func TestProcessLeavesAlreadySmallImageUnscaled(t *testing.T) {
	raw := solidJPEG(t, 300, 200)
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	fw, fh := decodedSize(t, result.Full.JPEG)
	if fw != 300 || fh != 200 {
		t.Errorf("a small image's full variant should pass through at its original size, got %dx%d", fw, fh)
	}
	// Below both caps already — thumb should be the same untouched size too,
	// not upscaled to fill more of the box than the source photo actually has.
	tw, th := decodedSize(t, result.Thumb.JPEG)
	if tw != 300 || th != 200 {
		t.Errorf("thumb variant of an already-small image should also be unscaled, got %dx%d", tw, th)
	}
}

func TestProcessAcceptsPNG(t *testing.T) {
	raw := solidPNG(t, 500, 400)
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process should accept PNG input: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(result.Full.JPEG)); err != nil {
		t.Error("PNG input's full variant should still come out as a valid JPEG (every format normalizes to JPEG on output)")
	}
	if _, err := jpeg.Decode(bytes.NewReader(result.Thumb.JPEG)); err != nil {
		t.Error("PNG input's thumb variant should also be a valid JPEG")
	}
}

func TestProcessRejectsNonImageData(t *testing.T) {
	_, err := Process(strings.NewReader("this is definitely not an image, just plain text pretending to be one"))
	if err != ErrNotAnImage {
		t.Errorf("Process error = %v, want ErrNotAnImage", err)
	}
}

func TestProcessRejectsCorruptedImage(t *testing.T) {
	raw := solidJPEG(t, 200, 200)
	// Truncate mid-scan-data — a well-formed header followed by garbage,
	// not just "not an image at all" (TestProcessRejectsNonImageData
	// covers that case; this one exercises the full-decode failure path
	// specifically, after DecodeConfig has already succeeded on the
	// still-intact header).
	corrupt := raw[:len(raw)-len(raw)/3]
	_, err := Process(bytes.NewReader(corrupt))
	if err != ErrNotAnImage {
		t.Errorf("Process error = %v, want ErrNotAnImage for a truncated/corrupted JPEG", err)
	}
}

func TestProcessRejectsOversizedFile(t *testing.T) {
	junk := bytes.Repeat([]byte{0xFF}, MaxUploadBytes+1024)
	_, err := Process(bytes.NewReader(junk))
	if err != ErrTooLarge {
		t.Errorf("Process error = %v, want ErrTooLarge", err)
	}
}

func TestProcessRejectsDecompressionBombResolution(t *testing.T) {
	// A tiny *file* that nonetheless *claims* an enormous resolution in
	// its header — image.DecodeConfig must catch this before Process ever
	// attempts the full pixel decode (which would otherwise try to
	// allocate gigabytes for a file that's only a few KB on disk).
	var buf bytes.Buffer
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	buf.Write(pngHeader)
	// IHDR chunk claiming a 50000x50000 image (2.5 billion pixels), with a
	// bogus CRC — Process should reject on the pixel-count check in
	// DecodeConfig before it would ever get far enough to care about CRC
	// validity, so a real decoder's leniency/strictness here doesn't matter.
	ihdrData := []byte{
		0, 0, 195, 80, // width = 50000
		0, 0, 195, 80, // height = 50000
		8, 6, 0, 0, 0, // bit depth 8, color type 6 (RGBA), compression/filter/interlace 0
	}
	writeChunk := func(typ string, data []byte) {
		length := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
		buf.Write(length)
		buf.WriteString(typ)
		buf.Write(data)
		// A real CRC32 is required — Go's png decoder checksums every
		// chunk even during DecodeConfig, before this test ever reaches
		// the pixel-count guard it's meant to exercise.
		crc := crc32.ChecksumIEEE(append([]byte(typ), data...))
		buf.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	}
	writeChunk("IHDR", ihdrData)

	_, err := Process(bytes.NewReader(buf.Bytes()))
	if err != ErrTooManyPixels {
		t.Errorf("Process error = %v, want ErrTooManyPixels (this is the decompression-bomb guard)", err)
	}
}

// fakeHEIC builds a minimal ISO-BMFF "ftyp" box carrying a real HEIC major
// brand — enough for detectHEIC to recognize (and Process to reject) it,
// without needing an actual libheif-encoded fixture file.
func fakeHEIC(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	// box size (28: 4 size + 4 "ftyp" + 4 major_brand + 4 minor_version + 8
	// bytes of compatible_brands), then "ftyp", major_brand "heic",
	// minor_version, and one compatible_brand "mif1" — the same shape a
	// real iPhone HEIC file's ftyp box has.
	buf.Write([]byte{0, 0, 0, 28})
	buf.WriteString("ftyp")
	buf.WriteString("heic")
	buf.Write([]byte{0, 0, 0, 0})
	buf.WriteString("mif1")
	buf.WriteString("heic")
	return buf.Bytes()
}

func TestProcessRejectsHEICWithClearError(t *testing.T) {
	_, err := Process(bytes.NewReader(fakeHEIC(t)))
	if err != ErrHEICNotSupported {
		t.Fatalf("Process error = %v, want ErrHEICNotSupported", err)
	}
	if err.Error() == "" {
		t.Error("ErrHEICNotSupported should have a non-empty, user-facing message")
	}
}

func TestDetectHEICIgnoresOrdinaryImages(t *testing.T) {
	if detectHEIC(solidJPEG(t, 10, 10)) {
		t.Error("a plain JPEG should not be detected as HEIC")
	}
	if detectHEIC(solidPNG(t, 10, 10)) {
		t.Error("a plain PNG should not be detected as HEIC")
	}
	if detectHEIC([]byte("too short")) {
		t.Error("a too-short input should not be detected as HEIC")
	}
}

func TestProcessHashIsStableAndContentAddressed(t *testing.T) {
	rawA := solidJPEG(t, 300, 200)
	resultA1, err := Process(bytes.NewReader(rawA))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	resultA2, err := Process(bytes.NewReader(rawA))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resultA1.Hash != resultA2.Hash {
		t.Errorf("processing the same bytes twice gave different hashes (%q vs %q) — re-uploading an unchanged photo should land on the same URL", resultA1.Hash, resultA2.Hash)
	}
	if resultA1.Hash == "" {
		t.Error("Hash should not be empty")
	}

	rawB := solidJPEG(t, 301, 200) // one pixel different
	resultB, err := Process(bytes.NewReader(rawB))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resultA1.Hash == resultB.Hash {
		t.Error("visibly different images produced the same hash")
	}
}

// captureLogs temporarily redirects the standard logger so a test can
// assert on (or rule out) a log.Printf call, restoring it afterward.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)
	fn()
	return buf.String()
}

func TestCheckSizeBudgetWarnsOnlyWhenFarOverBudget(t *testing.T) {
	const budget = 1000

	logs := captureLogs(t, func() { checkSizeBudget("full", 1200, budget) })
	if logs != "" {
		t.Errorf("20%% over budget should not warn, got log output: %q", logs)
	}

	logs = captureLogs(t, func() { checkSizeBudget("full", 2001, budget) })
	if !strings.Contains(logs, "full") {
		t.Errorf("more than 2x budget should log a warning naming the variant, got: %q", logs)
	}
}

func TestProcessRealisticPhotoStaysWithinSizeBudgets(t *testing.T) {
	// noisyJPEG at a real camera resolution is the closest this test suite
	// gets to "an actual food photo" without checking in a fixture file —
	// enough real detail that JPEG can't compress it away to nothing,
	// which is what would make this assertion meaningless.
	raw := noisyJPEG(t, 3024, 4032)
	result, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Generous ceiling (budgetWarnFactor's own "worth a look" threshold,
	// not the tighter target budget) — this is a worst-case synthetic
	// noise image, noisier than any real photo, so some slack against the
	// soft target is expected; blowing *that* would mean something in the
	// pipeline (quality setting, resize caps) is genuinely off.
	if got := len(result.Full.JPEG); got > fullSizeBudget*budgetWarnFactor {
		t.Errorf("full variant is %dKB, more than %dx its %dKB budget even for worst-case noisy input", got/1024, budgetWarnFactor, fullSizeBudget/1024)
	}
	if got := len(result.Thumb.JPEG); got > thumbSizeBudget*budgetWarnFactor {
		t.Errorf("thumb variant is %dKB, more than %dx its %dKB budget even for worst-case noisy input", got/1024, budgetWarnFactor, thumbSizeBudget/1024)
	}
}
