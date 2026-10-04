// Package brand is the venue's look: colours, fonts, logos and login page,
// edited on /admin/system and saved as one JSON blob in settings. Every
// page links /brand.css, generated here by CSS, which sets the design
// tokens every stylesheet already reads (staff.css, site.css, the guest
// menu, the order feed, login…), so one saved theme restyles the whole
// app without touching page templates.
package brand

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Theme struct {
	Preset  string `json:"preset"`
	Tagline string `json:"tagline"`

	LogoURL   string  `json:"logo"`
	LogoRatio float64 `json:"logo_ratio"` // width/height; wide logos stand alone, square ones sit next to the name
	IconURL   string  `json:"icon"`

	LoginTitle    string `json:"login_title"`
	LoginSubtitle string `json:"login_subtitle"`
	LoginLogoURL  string `json:"login_logo"`
	LoginLayout   string `json:"login_layout"` // card | split
	LoginBg       string `json:"login_bg"`     // solid | gradient | image
	LoginFrom     string `json:"login_from"`
	LoginTo       string `json:"login_to"`
	LoginImage    string `json:"login_image"`

	Mode       string `json:"mode"` // light | dark
	Primary    string `json:"primary"`
	Accent     string `json:"accent"`
	Background string `json:"background"`
	Surface    string `json:"surface"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Danger     string `json:"danger"`

	FontHead string `json:"font_head"`
	FontBody string `json:"font_body"`
	Corners  string `json:"corners"` // sharp | slight | rounded | pill
	Buttons  string `json:"buttons"` // solid | outline | soft
	Shadows  string `json:"shadows"` // none | subtle | strong
	Header   string `json:"header"`  // light | dark | primary

	SavedAt time.Time `json:"saved_at,omitempty"`
	SavedBy string    `json:"saved_by,omitempty"`
}

type Option struct{ Key, Label string }

type Font struct {
	Key, Label, Family string
	Body               bool // readable at paragraph size (some are display-only)
	faces              []string
}

// Fonts are all self-hosted (static/fonts): no third-party requests, and a
// browser only downloads the ones a page actually uses. They're Latin
// subsets — Greek text falls back to the system font.
var Fonts = []Font{
	{"archivo", "Archivo", `"Archivo"`, true, []string{`"Archivo";font-weight:400 800;src:url(/static/fonts/archivo-latin.woff2)`}},
	{"work-sans", "Work Sans", `"Work Sans"`, true, []string{`"Work Sans";font-weight:400 600;src:url(/static/fonts/work-sans-latin.woff2)`, `"Work Sans";font-weight:700;src:url(/static/fonts/work-sans-latin-700.woff2)`}},
	{"system", "System (fastest)", `system-ui`, true, nil},
	{"poppins", "Poppins (headings)", `"Poppins"`, false, []string{`"Poppins";font-weight:600 800;src:url(/static/fonts/poppins-latin.woff2)`}},
	{"playfair", "Playfair Display (headings)", `"Playfair Display"`, false, []string{`"Playfair Display";font-weight:600;src:url(/static/fonts/playfair-display-latin.woff2)`}},
	{"fraunces", "Fraunces (headings)", `"Fraunces"`, false, []string{`"Fraunces";font-weight:400 800;src:url(/static/fonts/fraunces-latin.woff2)`}},
	{"plex-mono", "IBM Plex Mono (headings)", `"IBM Plex Mono"`, false, []string{`"IBM Plex Mono";font-weight:500 700;src:url(/static/fonts/ibm-plex-mono-latin.woff2)`}},
	{"jetbrains", "JetBrains Mono (headings)", `"JetBrains Mono"`, false, []string{`"JetBrains Mono";font-weight:400 800;src:url(/static/fonts/jetbrains-mono-latin.woff2)`}},
}

var (
	Corners  = []Option{{"sharp", "Sharp"}, {"slight", "Slight"}, {"rounded", "Rounded"}, {"pill", "Pill"}}
	Buttons  = []Option{{"solid", "Solid"}, {"outline", "Outline"}, {"soft", "Soft"}}
	Shadows  = []Option{{"none", "None"}, {"subtle", "Subtle"}, {"strong", "Strong"}}
	Headers  = []Option{{"light", "Light"}, {"dark", "Dark"}, {"primary", "Primary colour"}}
	Modes    = []Option{{"light", "Light"}, {"dark", "Dark"}}
	Layouts  = []Option{{"card", "Centered card"}, {"split", "Split screen"}}
	LoginBgs = []Option{{"solid", "Solid colour"}, {"gradient", "Gradient"}, {"image", "Image"}}
)

type Preset struct {
	Key, Name, About string
	Theme            Theme
}

// Presets are complete looks; the first is the default for a new venue.
var Presets = []Preset{
	{"golden", "Golden hour", "The original: warm dark wood and brass gold.", Theme{
		Mode: "dark", Primary: "#CB9A5E", Accent: "#8DB2D4", Background: "#110E0B", Surface: "#1A1511",
		Text: "#F3ECE2", Muted: "#978876", Success: "#9CC27E", Warning: "#E6BF7E", Danger: "#E58B74",
		FontHead: "playfair", FontBody: "archivo", Corners: "rounded", Buttons: "solid", Shadows: "none", Header: "dark",
		LoginLayout: "card", LoginBg: "gradient", LoginFrom: "#1F1810", LoginTo: "#110E0B"}},
	{"bistro", "Bistro", "Restaurant: linen white, bottle green, terracotta.", Theme{
		Mode: "light", Primary: "#2F5D46", Accent: "#C8793F", Background: "#F6F2EA", Surface: "#FFFFFF",
		Text: "#1F2A24", Muted: "#6B7A71", Success: "#2E8B57", Warning: "#B7791F", Danger: "#C0392B",
		FontHead: "fraunces", FontBody: "work-sans", Corners: "rounded", Buttons: "solid", Shadows: "subtle", Header: "light",
		LoginLayout: "split", LoginBg: "gradient", LoginFrom: "#2F5D46", LoginTo: "#1B3A2B"}},
	{"neon", "Neon night", "Bar & club: black, hot pink, electric blue.", Theme{
		Mode: "dark", Primary: "#FF3D8B", Accent: "#3DDCFF", Background: "#09090E", Surface: "#14141C",
		Text: "#F4F4F8", Muted: "#8A8AA0", Success: "#3EE08A", Warning: "#FFC23D", Danger: "#FF5A5A",
		FontHead: "poppins", FontBody: "archivo", Corners: "pill", Buttons: "solid", Shadows: "strong", Header: "dark",
		LoginLayout: "split", LoginBg: "gradient", LoginFrom: "#3A0A24", LoginTo: "#09090E"}},
	{"fresh", "Fresh market", "Café & store: clean white, ocean blue, mint.", Theme{
		Mode: "light", Primary: "#1F6FEB", Accent: "#0FA99A", Background: "#F3F6FA", Surface: "#FFFFFF",
		Text: "#13202E", Muted: "#617287", Success: "#16A34A", Warning: "#D97706", Danger: "#DC2626",
		FontHead: "poppins", FontBody: "work-sans", Corners: "slight", Buttons: "solid", Shadows: "subtle", Header: "primary",
		LoginLayout: "card", LoginBg: "solid", LoginFrom: "#E8EFF8", LoginTo: "#F3F6FA"}},
	{"industrial", "Industrial", "Burger & beer hall: concrete grey, safety amber.", Theme{
		Mode: "dark", Primary: "#F5A524", Accent: "#60A5FA", Background: "#101215", Surface: "#1A1D22",
		Text: "#ECEEF1", Muted: "#8A919B", Success: "#4ADE80", Warning: "#FACC15", Danger: "#F87171",
		FontHead: "jetbrains", FontBody: "archivo", Corners: "sharp", Buttons: "outline", Shadows: "none", Header: "dark",
		LoginLayout: "card", LoginBg: "solid", LoginFrom: "#101215", LoginTo: "#101215"}},
}

func PresetByKey(key string) (Preset, bool) {
	for _, p := range Presets {
		if p.Key == key {
			return p, true
		}
	}
	return Presets[0], false
}

// Default is a fresh venue's theme: the first preset.
func Default() Theme {
	t := Presets[0].Theme
	t.Preset = Presets[0].Key
	return t
}

var hexRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// uploadRe: logos/images must be our own uploads (no arbitrary URLs end up
// inside CSS url()).
var uploadRe = regexp.MustCompile(`^/static/uploads/brand/[0-9a-f]+\.(png|jpg)$`)

func pick(v string, opts []Option) string {
	for _, o := range opts {
		if o.Key == v {
			return v
		}
	}
	return opts[0].Key
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Clean makes any submitted theme safe to render into CSS/HTML: every
// colour a #RRGGBB (else the preset's), every choice from its list, every
// image one of our uploads. Nothing a form sends reaches CSS unchecked.
func (t Theme) Clean() Theme {
	base, _ := PresetByKey(t.Preset)
	d := base.Theme
	t.Preset = base.Key
	for _, c := range []struct {
		v   *string
		def string
	}{
		{&t.Primary, d.Primary}, {&t.Accent, d.Accent}, {&t.Background, d.Background}, {&t.Surface, d.Surface},
		{&t.Text, d.Text}, {&t.Muted, d.Muted}, {&t.Success, d.Success}, {&t.Warning, d.Warning}, {&t.Danger, d.Danger},
		{&t.LoginFrom, d.LoginFrom}, {&t.LoginTo, d.LoginTo},
	} {
		if !hexRe.MatchString(*c.v) {
			*c.v = c.def
		}
		*c.v = strings.ToUpper(*c.v)
	}
	for _, u := range []*string{&t.LogoURL, &t.IconURL, &t.LoginLogoURL, &t.LoginImage} {
		if !uploadRe.MatchString(*u) {
			*u = ""
		}
	}
	if t.LogoURL == "" || t.LogoRatio < .2 || t.LogoRatio > 8 {
		t.LogoRatio = 1
	}
	t.Mode = pick(t.Mode, Modes)
	t.Corners = pick(t.Corners, Corners)
	t.Buttons = pick(t.Buttons, Buttons)
	t.Shadows = pick(t.Shadows, Shadows)
	t.Header = pick(t.Header, Headers)
	t.LoginLayout = pick(t.LoginLayout, Layouts)
	t.LoginBg = pick(t.LoginBg, LoginBgs)
	if t.LoginBg == "image" && t.LoginImage == "" {
		t.LoginBg = "gradient"
	}
	t.FontHead = fontOr(t.FontHead, d.FontHead, false).Key
	t.FontBody = fontOr(t.FontBody, d.FontBody, true).Key
	t.Tagline = clip(t.Tagline, 120)
	t.LoginTitle = clip(t.LoginTitle, 60)
	t.LoginSubtitle = clip(t.LoginSubtitle, 120)
	return t
}

func fontOr(key, def string, body bool) Font {
	for _, f := range Fonts {
		if f.Key == key && (f.Body || !body) {
			return f
		}
	}
	for _, f := range Fonts {
		if f.Key == def {
			return f
		}
	}
	return Fonts[0]
}

// ---------- colour maths ----------

type rgb struct{ r, g, b float64 }

func parse(h string) rgb {
	v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	return rgb{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255)}
}

func (c rgb) hex() string {
	f := func(x float64) int { return int(math.Round(math.Max(0, math.Min(255, x)))) }
	return fmt.Sprintf("#%02X%02X%02X", f(c.r), f(c.g), f(c.b))
}

// mix is a colour w of the way from a to b.
func mix(a, b string, w float64) string {
	x, y := parse(a), parse(b)
	return rgb{x.r + (y.r-x.r)*w, x.g + (y.g-x.g)*w, x.b + (y.b-x.b)*w}.hex()
}

func alpha(h string, a float64) string {
	c := parse(h)
	return fmt.Sprintf("rgba(%d,%d,%d,%.2f)", int(c.r), int(c.g), int(c.b), a)
}

func luminance(h string) float64 {
	c := parse(h)
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// On is black or white text for a coloured background. White wins on
// mid-tones (a strong blue or pink reads better with white, even where the
// strict contrast maths is a tie); black only on genuinely light colours.
func On(bg string) string {
	if luminance(bg) > 0.3 {
		return "#111111"
	}
	return "#FFFFFF"
}

func IsDark(h string) bool { return luminance(h) < 0.18 }

// ---------- CSS ----------

// palette is one surface's full colour set; the page and the header each
// get one (the header can be light, dark or the primary colour).
type palette struct{ bg, surface, text, muted, primary, onPrimary string }

func (t Theme) palettes() (page, head palette) {
	page = palette{t.Background, t.Surface, t.Text, t.Muted, t.Primary, On(t.Primary)}
	switch t.Header {
	case "primary":
		on := On(t.Primary)
		head = palette{t.Primary, mix(t.Primary, on, .08), on, mix(on, t.Primary, .3), on, t.Primary}
	case "light":
		if IsDark(t.Background) {
			head = palette{"#F7F7F5", "#FFFFFF", "#17171A", "#6B6B73", t.Primary, On(t.Primary)}
		} else {
			head = palette{mix(t.Surface, t.Background, .35), t.Surface, t.Text, t.Muted, t.Primary, On(t.Primary)}
		}
	default: // dark
		if IsDark(t.Background) {
			head = palette{mix(t.Background, t.Surface, .35), t.Surface, t.Text, t.Muted, t.Primary, On(t.Primary)}
		} else {
			head = palette{"#15171B", "#1E2126", "#F2F3F5", "#9AA0AA", t.Primary, On(t.Primary)}
		}
	}
	return
}

// vars renders every design-token name the app's stylesheets use for one
// palette: staff.css/booking (--bg, --gold, --ink…) and the guest menu,
// site.css, order feed and login pages (--bg-base, --accent…).
func (t Theme) vars(p palette) string {
	raise := mix(p.surface, p.text, .045)
	line := mix(p.surface, p.text, .10)
	lineStrong := mix(p.surface, p.text, .22)
	strong := mix(p.primary, p.text, .22)
	shadow := "0,0,0"
	if !IsDark(p.bg) {
		shadow = "20,24,32"
	}
	card, pop := "none", fmt.Sprintf("0 18px 40px -18px rgba(%s,.55)", shadow)
	switch t.Shadows {
	case "subtle":
		card = fmt.Sprintf("0 1px 2px rgba(%s,.06),0 6px 16px -10px rgba(%s,.28)", shadow, shadow)
	case "strong":
		card = fmt.Sprintf("0 2px 4px rgba(%s,.12),0 16px 36px -16px rgba(%s,.55)", shadow, shadow)
		pop = fmt.Sprintf("0 30px 70px -24px rgba(%s,.75)", shadow)
	}
	btnBg, btnInk, btnLine := p.primary, p.onPrimary, "transparent"
	switch t.Buttons {
	case "outline":
		btnBg, btnInk, btnLine = "transparent", p.primary, p.primary
	case "soft":
		btnBg, btnInk = alpha(p.primary, .16), strong
	}
	scheme := "dark"
	if !IsDark(p.bg) {
		scheme = "light"
	}
	return fmt.Sprintf(`color-scheme:%s;
--bg:%s;--side:%s;--surface:%s;--raise:%s;--line:%s;--line-strong:%s;--ink:%s;--muted:%s;--faint:%s;
--gold:%s;--gold-ink:%s;--gold-soft:%s;--gold-line:%s;--hover:%s;--glass:%s;--sand:%s;
--blue:%s;--blue-soft:%s;--green:%s;--green-soft:%s;--red:%s;--red-soft:%s;--amber:%s;--amber-soft:%s;--warn:%s;--err:%s;
--bg-base:%s;--bg-surface:%s;--bg-raised:%s;--bg-recessed:%s;--border:%s;--border-soft:%s;
--text-primary:%s;--text-secondary:%s;--text-muted:%s;--on-accent:%s;
--accent:%s;--accent-strong:%s;--accent-soft:%s;
--success:%s;--success-soft:%s;--danger:%s;--danger-soft:%s;--warning:%s;--info:%s;--info-soft:%s;--neutral:%s;--neutral-soft:%s;
--btn-bg:%s;--btn-ink:%s;--btn-line:%s;--shadow-card:%s;--shadow-pop:%s;`,
		scheme,
		p.bg, mix(p.bg, p.surface, .35), p.surface, raise, line, lineStrong, p.text, p.muted, mix(p.muted, p.bg, .3),
		p.primary, p.onPrimary, alpha(p.primary, .14), alpha(p.primary, .45), alpha(p.primary, .06), alpha(p.surface, .92), mix(p.bg, p.surface, .5),
		t.Accent, alpha(t.Accent, .14), t.Success, alpha(t.Success, .14), t.Danger, alpha(t.Danger, .14), t.Warning, alpha(t.Warning, .14), t.Warning, t.Danger,
		p.bg, p.surface, raise, mix(p.bg, p.surface, .5), line, mix(p.surface, p.text, .06),
		p.text, mix(p.text, p.muted, .45), p.muted, p.onPrimary,
		p.primary, strong, alpha(p.primary, .15),
		t.Success, alpha(t.Success, .15), t.Danger, alpha(t.Danger, .15), t.Warning, t.Accent, alpha(t.Accent, .15), p.muted, alpha(p.muted, .14),
		btnBg, btnInk, btnLine, card, pop)
}

// primaryButtons are the main call-to-action buttons across all pages
// (staff, guest menu, booking, order feed, login) that follow the Buttons
// style.
const primaryButtons = ".act.primary,.mbtn.primary,.btn:not(.ghost),.btn-primary,.add-btn,.add-custom-btn,.notify-btn,.dj-submit," +
	".checkout-btn,.place-order-btn,.confirm-btn-ok,.login-btn,.cta,.oc-go"

var radii = map[string][3]int{"sharp": {0, 0, 2}, "slight": {4, 6, 8}, "rounded": {9, 12, 16}, "pill": {12, 16, 22}}

// CSS is the stylesheet for a theme. scope is ":root" for real pages, or a
// container selector (the live preview on /admin/system).
func (t Theme) CSS(scope string) string {
	page, head := t.palettes()
	fh, fb := fontOr(t.FontHead, "", false), fontOr(t.FontBody, "", true)
	r := radii[t.Corners]
	btnR := fmt.Sprintf("%dpx", r[1])
	if t.Corners == "pill" {
		btnR = "999px"
	}
	var b strings.Builder
	seen := map[string]bool{}
	for _, f := range []Font{fh, fb} {
		if seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		for _, face := range f.faces {
			fmt.Fprintf(&b, "@font-face{font-family:%s;font-display:swap}\n", face)
		}
	}
	sans := `system-ui,-apple-system,"Segoe UI",Roboto,Arial,sans-serif`
	fmt.Fprintf(&b, "%s{%s\n--font:%s,%s;--font-body:%s,%s;--font-head:%s,%s;--font-display:%s,%s;--font-wordmark:%s,%s;\n--r-sm:%dpx;--r-md:%dpx;--r-lg:%dpx;--r-btn:%s;--radius-sm:%dpx;--radius-md:%dpx;--radius-lg:%dpx;}\n",
		scope, t.vars(page), fb.Family, sans, fb.Family, sans, fh.Family, sans, fh.Family, sans, fh.Family, sans,
		r[0], r[1], r[2], btnR, r[0], r[1], r[2])
	// Header & sidebar get their own palette; everything inside them reads it.
	fmt.Fprintf(&b, "%s :is(.side,.mtop,.mtabs,.topbar,header.bar,.pv-head){%s}\n", scope, t.vars(head))
	fmt.Fprintf(&b, "%s :is(.side,.mtop,.mtabs,.topbar,header.bar,.pv-head){background:var(--bg);color:var(--ink)}\n", scope)
	// Corners, buttons and shadows restyle the shared components of every
	// page family. "Rounded / solid / none" is how the stylesheets are
	// drawn already, so nothing is overridden for those.
	if t.Corners != "rounded" {
		fmt.Fprintf(&b, "%s{--radius-pill:%s}\n", scope, btnR)
		fmt.Fprintf(&b, "%s :is(.card,.side-card,.brand-strip,.kpi-card,.panel,.item-photo-wrap,.pcard,.od-lines,.od-totals,.oc,.help-call,.sk,.prep,.pay-sum){border-radius:var(--r-lg)}\n", scope)
		fmt.Fprintf(&b, "%s :is(.input,.f input,.f select,.f textarea,.banner,.stat,.choice,.access-item,.field-input,.seg,.side-panel li,.nav a){border-radius:var(--r-md)}\n", scope)
		fmt.Fprintf(&b, "%s :is(.act,.mbtn,.more-btn,.btn,.btn-primary,.icon-btn,.seg label,.leave-btn,.cat-tab,.oc-go,.w-btn){border-radius:var(--r-btn)}\n", scope)
		fmt.Fprintf(&b, "%s :is(.pill,.srole,.mbadge,.host-tag){border-radius:var(--r-sm)}\n", scope)
	}
	if t.Buttons != "solid" {
		fmt.Fprintf(&b, "%s :is(%s){background:var(--btn-bg);color:var(--btn-ink);border-color:var(--btn-line);box-shadow:inset 0 0 0 1.5px var(--btn-line)}\n", scope, primaryButtons)
	}
	if t.Shadows != "none" {
		fmt.Fprintf(&b, "%s :is(.card,.side-card,.stat,.kpi-card,.panel,.brand-strip){box-shadow:var(--shadow-card)}\n", scope)
	}
	// Headings and the venue name use the heading font.
	fmt.Fprintf(&b, "%s :is(h1,.brand b,.wordmark,.login-title,.pv-h){font-family:var(--font-head)}\n", scope)
	return b.String()
}

// MonoCSS turns every .brand-mark (the small square logo slot in the admin
// sidebar, staff app, login, guest menu) into the venue's monogram: its first
// letter in the heading font. Replaces the generic glyph it used to show;
// hidden again once a real logo is uploaded (LogoCSS, emitted after this).
func MonoCSS(scope, name string) string {
	return scope + "{--monogram:" + CSSString(Mono(name)) + "}\n" +
		scope + " .brand-mark{display:grid;place-items:center;font-family:var(--font-head);font-weight:800;font-size:21px;line-height:1;letter-spacing:0}\n" +
		scope + " .brand-mark svg{display:none}\n" +
		scope + " .brand-mark::before{content:var(--monogram)}\n" +
		scope + " .brand-strip .brand-mark,.pv-mark{font-size:15px}\n"
}

// HeaderBg is the colour of the top bar and tab bar. The installed staff app uses
// it as its window/launch colour, so any strip iOS leaves below the page blends
// with the tab bar.
func (t Theme) HeaderBg() string { _, head := t.palettes(); return head.bg }

// Wide is a logo that carries the name itself, so it's shown on its own.
func (t Theme) Wide() bool { return t.LogoURL != "" && t.LogoRatio >= 1.8 }

// LogoCSS shows the uploaded logo in every .brand-logo slot (sidebar,
// headers, login) — sized by its own aspect ratio — in place of the
// default mark (.brand-mark); a wide logo also hides the written name.
func (t Theme) LogoCSS(scope string) string {
	if t.LogoURL == "" {
		return scope + " .brand-logo{display:none}\n"
	}
	css := fmt.Sprintf("%s .brand-logo{display:block;flex:none;height:var(--logo-h,40px);aspect-ratio:%.3f;max-width:190px;background:url(%q) left center/contain no-repeat}\n%s .brand-mark{display:none}\n",
		scope, t.LogoRatio, t.LogoURL, scope)
	if t.Wide() {
		css += scope + " .brand-name{position:absolute!important;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}\n"
	}
	return css
}

// LoginCSS is the /login background for this theme.
func (t Theme) LoginCSS() string {
	bg := t.LoginFrom
	switch t.LoginBg {
	case "gradient":
		bg = fmt.Sprintf("linear-gradient(145deg,%s,%s)", t.LoginFrom, t.LoginTo)
	case "image":
		bg = fmt.Sprintf("%s url(%q) center/cover no-repeat", t.LoginTo, t.LoginImage)
	}
	return bg
}
