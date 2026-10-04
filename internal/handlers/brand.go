package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bitebox/internal/brand"
	"bitebox/internal/db"
	"bitebox/internal/imageproc"
)

// ----- the saved theme, cached: /brand.css is on every page load -----

var theme struct {
	sync.RWMutex
	loaded bool
	t      brand.Theme
	css    []byte
	etag   string
}

func currentTheme() (brand.Theme, []byte, string) {
	theme.RLock()
	if theme.loaded {
		defer theme.RUnlock()
		return theme.t, theme.css, theme.etag
	}
	theme.RUnlock()
	setTheme(db.GetTheme())
	return currentTheme()
}

func setTheme(t brand.Theme) {
	settings, _ := db.GetSettings()
	css := []byte(t.CSS(":root") + brand.MonoCSS(":root", settings.VenueName) + t.LogoCSS(":root") + ".login-bg{background:" + t.LoginCSS() + "}\n")
	sum := sha256.Sum256(css)
	theme.Lock()
	theme.t, theme.css, theme.etag, theme.loaded = t, css, `"`+hex.EncodeToString(sum[:8])+`"`, true
	theme.Unlock()
}

// RefreshTheme rebuilds the stylesheet and icons after the venue's name changes
// (the monogram is its first letter).
func RefreshTheme() { t, _, _ := currentTheme(); setTheme(t) }

// BrandCSS serves the theme stylesheet every page links last. Browsers
// revalidate it (a ~200-byte 304 while nothing changed), so a saved theme
// shows up on every device's next page view.
func BrandCSS(w http.ResponseWriter, r *http.Request) {
	_, css, etag := currentTheme()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(css)
}

// ----- the venue's app icon (favicon, home-screen icon, PWA icons) -----

var iconCache sync.Map // "etag/size" → PNG bytes

// brandLogo loads the uploaded small logo (or a squarish main logo) to put on
// the icon; nil means "use the monogram".
func brandLogo(t brand.Theme) image.Image {
	url := t.IconURL
	if url == "" && t.LogoURL != "" && !t.Wide() {
		url = t.LogoURL
	}
	if url == "" {
		return nil
	}
	f, err := os.Open(strings.TrimPrefix(url, "/"))
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

func iconPNG(size int) ([]byte, string) {
	t, _, etag := currentTheme()
	key := etag + "/" + strconv.Itoa(size)
	if b, ok := iconCache.Load(key); ok {
		return b.([]byte), key
	}
	settings, _ := db.GetSettings()
	b := brand.RenderIcon(size, t, settings.VenueName, brandLogo(t))
	iconCache.Store(key, b)
	return b, key
}

func serveIcon(w http.ResponseWriter, r *http.Request, size int) {
	b, tag := iconPNG(size)
	w.Header().Set("Content-Type", "image/png")
	// No long caching: the venue can change its logo any time. The ETag makes a
	// repeat visit a tiny 304 while it hasn't.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+tag+`"`)
	if r.Header.Get("If-None-Match") == `"`+tag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b)
}

var iconSizes = map[string]int{"32": 32, "64": 64, "128": 128, "180": 180, "192": 192, "512": 512}

// BrandIconPNG serves /brand/icon.png?s=<size> (default 64): the favicon and
// the manifests' 192 / 512 icons.
func BrandIconPNG(w http.ResponseWriter, r *http.Request) {
	size, ok := iconSizes[r.URL.Query().Get("s")]
	if !ok {
		size = 64
	}
	serveIcon(w, r, size)
}

// BrandAppleIcon is /brand/apple-touch-icon.png: the 180px, fully opaque,
// square PNG iOS puts on the home screen. Served directly (no redirect) since
// iOS won't follow one for this.
func BrandAppleIcon(w http.ResponseWriter, r *http.Request) { serveIcon(w, r, 180) }

// ----- /admin/system: the look & brand editor -----

func AdminSystemPage(w http.ResponseWriter, r *http.Request) {
	t, _, _ := currentTheme()
	presets := map[string]brand.Theme{}
	for _, p := range brand.Presets {
		presets[p.Key] = p.Theme
	}
	pj, _ := json.Marshal(presets)
	var head, body []brand.Font
	for _, f := range brand.Fonts {
		head = append(head, f)
		if f.Body {
			body = append(body, f)
		}
	}
	based, _ := brand.PresetByKey(t.Preset)
	type colour struct{ Key, Label, Value, Hint string }
	colours := []colour{
		{"primary", "Primary", t.Primary, "Buttons, links, highlights"}, {"accent", "Secondary / accent", t.Accent, "Info badges, seated tables"},
		{"background", "Background", t.Background, ""}, {"surface", "Cards & panels", t.Surface, ""},
		{"text", "Text", t.Text, ""}, {"muted", "Muted text", t.Muted, ""},
		{"success", "Success", t.Success, ""}, {"warning", "Warning", t.Warning, ""}, {"danger", "Danger", t.Danger, ""},
	}
	loginColours := []colour{{"login_from", "From", t.LoginFrom, ""}, {"login_to", "To", t.LoginTo, ""}}
	settings, _ := db.GetSettings()
	type upload struct{ Kind, URL string }
	uploads := map[string]upload{"logo": {"logo", t.LogoURL}, "icon": {"icon", t.IconURL},
		"login_logo": {"login_logo", t.LoginLogoURL}, "login_image": {"login_image", t.LoginImage}}
	staffPage(w, r, "adm_system.html", "system", map[string]interface{}{
		"T": t, "Based": based.Name, "Colours": colours, "LoginColours": loginColours, "Name": settings.VenueName, "Uploads": uploads, "Presets": brand.Presets, "PresetsJSON": template.JS(pj), // #nosec G203 -- pj is json.Marshal of our built-in presets
		"HeadFonts": head, "BodyFonts": body,
		"Modes": brand.Modes, "Corners": brand.Corners, "Buttons": brand.Buttons, "Shadows": brand.Shadows,
		"Headers": brand.Headers, "Layouts": brand.Layouts, "LoginBgs": brand.LoginBgs,
		"PreviewCSS": template.CSS(previewCSS(t, settings.VenueName)), // #nosec G203 -- built from validated theme values (brand.Theme.Clean); the name only as an escaped CSSString
	})
}

func previewCSS(t brand.Theme, name string) string {
	return t.CSS(".theme-scope") + brand.MonoCSS(".theme-scope", name) + t.LogoCSS(".theme-scope") + ".theme-scope .login-bg{background:" + t.LoginCSS() + "}\n"
}

// themeFromForm reads the editor form; field names are the Theme's JSON
// names. Clean() then validates every value.
func themeFromForm(r *http.Request) brand.Theme {
	r.ParseForm()
	m := map[string]interface{}{}
	for k, v := range r.PostForm {
		m[k] = v[0]
	}
	ratio, _ := strconv.ParseFloat(r.PostFormValue("logo_ratio"), 64)
	m["logo_ratio"] = ratio
	b, _ := json.Marshal(m)
	var t brand.Theme
	json.Unmarshal(b, &t)
	return t.Clean()
}

// AdminSystemPreview answers every edit with the preview's stylesheet only
// (scoped to the preview box): nothing is saved.
func AdminSystemPreview(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "<style>%s</style>", previewCSS(themeFromForm(r), clipName(r.PostFormValue("venue_name"))))
}

func AdminSystemSave(w http.ResponseWriter, r *http.Request) {
	t := themeFromForm(r)
	user, _ := UserFromContext(r)
	t.SavedAt, t.SavedBy = time.Now(), user.Username
	name := clipName(r.PostFormValue("venue_name"))
	if name == "" {
		http.Error(w, "The venue needs a name.", http.StatusUnprocessableEntity)
		return
	}
	if err := db.SaveTheme(t); err != nil {
		http.Error(w, "Could not save the theme.", http.StatusInternalServerError)
		return
	}
	db.SetVenueName(name)
	db.LogAudit(user.Username, "theme", "Look & brand", "Based on "+t.Preset)
	setTheme(t)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"themeSaved":%q}`, "Saved "+t.SavedAt.Format("Jan 2, 15:04")+" by "+t.SavedBy))
	w.WriteHeader(http.StatusNoContent)
}

func clipName(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}

// brandUploads: kind → longest edge and format. Logos stay PNG (keeps
// transparency); the login photo is a JPEG.
var brandUploads = map[string]struct {
	max int
	png bool
}{"logo": {600, true}, "icon": {512, true}, "login_logo": {600, true}, "login_image": {2000, false}}

const brandDir = "static/uploads/brand"

// AdminSystemUpload stores an image for the editor and returns its URL; it
// only becomes the venue's once the theme is saved.
func AdminSystemUpload(w http.ResponseWriter, r *http.Request) {
	if err := allowMultipartUpload(w, r); err != nil {
		http.Error(w, "That image is too big (max 20 MB).", http.StatusRequestEntityTooLarge)
		return
	}
	spec, ok := brandUploads[r.FormValue("kind")]
	file, _, err := r.FormFile("file")
	if !ok || err != nil {
		http.Error(w, "Pick an image to upload.", http.StatusBadRequest)
		return
	}
	defer file.Close()
	img, err := imageproc.Brand(file, spec.max, spec.png)
	if err != nil {
		msg := "That file isn't an image we can read. Use PNG, JPG or WebP."
		switch {
		case errors.Is(err, imageproc.ErrTooLarge):
			msg = "That image is too big (max 20 MB)."
		case errors.Is(err, imageproc.ErrHEICNotSupported):
			msg = "iPhone HEIC photos aren't supported. Share it as JPG first."
		}
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}
	ext := ".jpg"
	if spec.png {
		ext = ".png"
	}
	path := brandDir + "/" + img.Hash + ext
	// Public, served images: world-readable like the rest of static/.
	err = os.MkdirAll(brandDir, 0o755) // #nosec G301 -- public static directory
	if err == nil {
		err = os.WriteFile(path, img.Data, 0o644) // #nosec G306 -- public static file
	}
	if err != nil {
		log.Printf("brand upload: %v", err)
		http.Error(w, "Could not store the image.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"url": "/" + path, "ratio": float64(img.Width) / float64(img.Height)})
}
