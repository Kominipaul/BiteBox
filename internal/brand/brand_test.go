package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"
)

// TestCleanRejectsUnsafeValues: whatever a form posts, only hex colours,
// listed options and our own uploads can reach the generated CSS.
func TestCleanRejectsUnsafeValues(t *testing.T) {
	bad := Theme{
		Preset: "nope", Primary: "red;}body{display:none", Background: "#12345", Text: "#abcdef",
		LogoURL: "https://evil.example/x.png", LoginImage: `/static/uploads/brand/a.png")`, Corners: "blobby",
		FontHead: `"Comic";}`, FontBody: "playfair", LoginBg: "image",
	}
	c := bad.Clean()
	def := Presets[0].Theme
	if c.Preset != Presets[0].Key || c.Primary != def.Primary || c.Background != def.Background {
		t.Errorf("bad colours/preset kept: %+v", c)
	}
	if c.Text != "#ABCDEF" {
		t.Errorf("valid colour lost: %q", c.Text)
	}
	if c.LogoURL != "" || c.LoginImage != "" || c.LoginBg != "gradient" {
		t.Errorf("foreign image kept: logo %q bg %q/%q", c.LogoURL, c.LoginBg, c.LoginImage)
	}
	if c.Corners != "sharp" || c.FontHead != def.FontHead || c.FontBody != def.FontBody {
		t.Errorf("options not validated: corners %q heads %q body %q (display-only font can't be body text)", c.Corners, c.FontHead, c.FontBody)
	}
	css := c.CSS(":root")
	if strings.Contains(css, "evil") || strings.Contains(css, "display:none") {
		t.Fatalf("unsafe value reached CSS:\n%s", css)
	}
}

func TestPresetsAreCompleteAndReadable(t *testing.T) {
	for _, p := range Presets {
		th := p.Theme
		th.Preset, th.LogoRatio = p.Key, 1
		if c := th.Clean(); c != th {
			t.Errorf("preset %s changes when cleaned:\n%+v\n%+v", p.Key, th, c)
		}
		css := th.CSS(":root")
		for _, v := range []string{"--gold:" + th.Primary, "--bg-base:" + th.Background, "--accent:" + th.Primary, "--on-accent:"} {
			if !strings.Contains(css, v) {
				t.Errorf("preset %s: missing %s", p.Key, v)
			}
		}
	}
	if On("#FFFFFF") != "#111111" || On("#000000") != "#FFFFFF" || On("#FF3D8B") != "#FFFFFF" || On("#CB9A5E") != "#111111" {
		t.Error("On() picks the less readable text colour")
	}
}

func TestMonoAndIcon(t *testing.T) {
	for in, want := range map[string]string{"EGO": "E", "  blue harbor": "B", "Ωmega": "Ω", "42 Club": "4", "": "•", "!!": "•"} {
		if got := Mono(in); got != want {
			t.Errorf("Mono(%q) = %q, want %q", in, got, want)
		}
	}
	th := Default()
	// Monogram icon: an opaque square in the primary colour with a contrasting letter.
	img, err := png.Decode(bytes.NewReader(RenderIcon(180, th, "EGO", nil)))
	if err != nil || img.Bounds().Dx() != 180 || img.Bounds().Dy() != 180 {
		t.Fatalf("icon is not a 180x180 png: %v", err)
	}
	bg := color.RGBAModel.Convert(img.At(2, 179)).(color.RGBA)
	if want := parseRGBA(th.Primary); bg != want {
		t.Errorf("bottom edge %v, want the primary colour %v", bg, want)
	}
	ink, fg := 0, parseRGBA(On(th.Primary))
	for y := 40; y < 140; y++ {
		for x := 40; x < 140; x++ {
			if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) == fg {
				ink++
			}
		}
	}
	if ink < 400 {
		t.Errorf("the letter isn't drawn in the middle (%d ink pixels)", ink)
	}
	for y := 0; y < 180; y += 30 { // opaque everywhere: iOS turns transparent icons black
		if _, _, _, a := img.At(90, y).RGBA(); a != 0xffff {
			t.Fatalf("icon has transparent pixels at y=%d", y)
		}
	}
	// A logo goes on a background that contrasts with it.
	dark, light := image.NewRGBA(image.Rect(0, 0, 40, 40)), image.NewRGBA(image.Rect(0, 0, 40, 40))
	draw.Draw(dark, dark.Bounds(), &image.Uniform{color.RGBA{10, 10, 10, 255}}, image.Point{}, draw.Src)
	draw.Draw(light, light.Bounds(), &image.Uniform{color.RGBA{250, 250, 250, 255}}, image.Point{}, draw.Src)
	onDark, _ := png.Decode(bytes.NewReader(RenderIcon(180, th, "EGO", dark)))
	onLight, _ := png.Decode(bytes.NewReader(RenderIcon(180, th, "EGO", light)))
	if r, _, _, _ := onDark.At(2, 2).RGBA(); r < 0xf000 {
		t.Error("a dark logo should sit on a light background")
	}
	if r, _, _, _ := onLight.At(2, 2).RGBA(); r > 0x3000 {
		t.Error("a light logo should sit on a dark background")
	}
	if css := MonoCSS(":root", `A"B`); !strings.Contains(css, `--monogram:"A"`) || !strings.Contains(css, ".brand-mark::before") {
		t.Errorf("monogram css: %s", css)
	}
}
