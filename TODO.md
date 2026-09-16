# BiteBox — Status & TODO

Working notes on what's been built, what's still rough, and ideas for later.
Update this as things change — it's meant to be read before picking up work again.

## Done

### Product deletion, forced default-password change, call-waiter audit trail; investigated (not a bug) the customize-panel/tab-reset item
Worked through the "Missing / rough edges" and "Maybe later" backlog, per
instruction to skip anything touching myDATA/ΑΑΔΕ, real online payment
processing, legal/compliance work outside the server itself, or integration
with any other external platform (so DJ-tip-paid workflow, split-bill
payment collection, the default-password/table-URL items — already decided
final, see below — and the reverse-proxy header note, a deploy-checklist
item rather than code, were all left alone).

- **Product deletion.** `AdminDeleteProduct` (`DELETE /admin/products/{id}`)
  removes a product and its `product_ingredients` rows for good — order
  history is untouched, since `order_items` snapshots name/price at order
  time and has no FK to `products`. Found a real hazard while building it:
  `GetActiveOrdersByCategories` (the kitchen/bar live feed) actually INNER
  JOINs `order_items` to `products` for category routing, so deleting a
  product still sitting in a pending/preparing/ready order would silently
  drop that order from staff's live view. Added `ProductHasActiveOrders` as
  a guard — deletion is blocked with a 409 and a plain-language explanation
  until the order is served/completed/cancelled; hiding (`AdminToggleProduct`)
  stays the answer for "stop selling this but an order is still in flight."
  Deleting also removes the product's own local photo files. Along the way,
  a real pre-existing bug surfaced: `ORDER BY created_at` with no tiebreaker
  across 8 queries (`orders.go`, `db.go`) sorts unstably on same-second
  writes, since SQLite's `CURRENT_TIMESTAMP` only has 1-second resolution —
  caught by a test failure (a delete-test's order briefly outranking a
  cart-test's own order in "most recent"), fixed everywhere with `, id`
  tiebreakers, not just patched at the one call site that happened to fail.
  6 new tests; verified live (create → delete → gone from menu but the order
  history entry survives; active-order delete correctly blocked with 409).
- **Forced password change for the seeded default accounts.** Every seeded
  account (`admin`/`admin123` etc.) now carries `must_change_password`;
  `RequireRole` redirects it to `/change-password` on every request except
  that route itself until it's cleared, so a fresh install can't stay on a
  password published in this app's own source. New `templates/
  change_password.html` (matching `login.html`'s look), self-service
  `ChangePasswordSubmit` (validates current password, new ≥6 chars, new ≠
  confirm mismatch, new ≠ current), and a voluntary "Change password" link
  in both dashboard topbars for anyone who wants to change it later, not
  just the forced first time. A one-time backfill flags any *existing*
  install's accounts that are still on their known default password (and
  only those — an admin who already changed it, or a staff account an admin
  created/reset with a real password, is never touched). 7 new tests;
  verified live end-to-end (default login → forced redirect → blocked from
  `/admin` until changed → wrong-current-password rejected → correct change
  clears the flag → old password stops working → fresh login goes straight
  through).
- **Real call-waiter acknowledgment audit trail.** `ClearAssistance` now
  takes the acknowledging staff member's username and writes an
  `assistance_log` row (table, who, requested-at, acknowledged-at,
  wait-seconds) in the same transaction that clears the live flag, instead
  of just dropping the request on the floor with no record. New admin
  dashboard panel ("🔔 Call-waiter log", `/admin/assistance-log`) lists
  acknowledgments newest-first with a human wait label ("42s" / "3m 07s").
  Double-acknowledging (or any other path clearing an already-clear flag)
  logs nothing — there's no acknowledger of a request that wasn't actually
  pending. wait_seconds is computed in SQLite's own `julianday()` math
  rather than in Go, for the same RFC3339-vs-`CURRENT_TIMESTAMP`-text
  mismatch reason `ReleaseIdleTables` already had to work around. 4 new
  tests (including one that backdates a request 5 real minutes and checks
  the logged duration matches); verified live (guest calls waiter → admin
  acknowledges → log panel shows "Table 5 → acknowledged by admin, waited
  10s").
- **Investigated "customize-panel/category-tab state resets on every live
  menu push" — not reproducible, no fix needed.** Traced the actual
  mechanism: `/menu/ws` only ever OOB-swaps `#product-list` (plus
  `#cart-refresh-trigger`, and `#dj-section`/`#djConfirm` when DJ requests
  are on) — confirmed by connecting a real websocket client and dumping the
  raw frames across two live product toggles, not just reading the code.
  `#customizeOverlay`/`#customizeChips` (the modal) is a sibling element
  outside that swapped subtree entirely, and `host_menu.html` already
  copies its content from the clicked product card at *open* time — a
  snapshot, not a live reference — so a swap elsewhere can't touch it.
  `.cat-tabs` is likewise a sibling never targeted by any OOB push, and its
  "active" tab selection is already re-applied after every
  `afterSwap`/`oobAfterSwap` by `applyCategoryFilter` (present since the
  original live-updates commit). Both mechanisms this TODO item worried
  about already can't happen — most likely this was accurate at some
  earlier point and fixed as a side effect of the "Customize did nothing"
  body-delegation fix above, and this note just never got removed. Added
  `TestBroadcastMenuNeverTouchesModalOrTabBar` to pin the contract so a
  future change to `BroadcastMenu` can't silently start OOB-swapping either
  element without a test catching it.

### Menu & Inventory screen redesign — 7 concrete UI problems, not a restyle
Reported as 7 specific, numbered defects on the admin "Menu & inventory"
screen (a screenshot's worth of clipped text, mis-shapen controls, and one
possibly-broken icon), not a vague "make it nicer." Root-caused each before
touching CSS:

- **Clipped "Pric"/"VAT 24..."/"Stock" text**: `.add-form-product` was a
  9-column grid with hand-picked `fr` ratios guessed against desktop width
  — at a realistic panel width the price column landed ~69px against
  "Price €"'s own ~73px, VAT ~84px against "VAT 24% (standard)"'s ~150px+.
  Replaced with flex rows, each field given a real min-width sized to its
  own placeholder/option text, `flex-wrap` so an overflowing field drops to
  its own line instead of squeezing. Grouped into rows by what the fields
  actually are (identity+pricing / description / media+inventory), which
  also directly answered two more of the seven:
- **Photo controls** (take/choose/paste-URL) unified into one labeled,
  bordered `.photo-cluster`/`.photo-row-cluster` (add-form and per-row
  versions) instead of 3-4 unrelated, differently-shaped controls — this is
  also what the per-row "icon strip that looked like debug UI" turned out
  to be: a real, working photo-preview/take/choose/remove control with no
  container of its own tying it together.
- **Submit button** moved to its own `.form-actions` row, not left
  in-between inline-with-inputs and fully separate.
- **Per-row control heights unified** (~37-38px) via padding only, not
  font-size — computed the actual mismatch first: stepper ~32px, VAT select
  ~28px, status pill ~23px, buttons ~29-30px, four different heights.
  Status pill + Save + Hide/Show merged into one `.row-status-actions`
  cluster (was two adjacent grid cells with nothing marking them related);
  kept the pill *shape* for status (consistent with role-badge/category-
  pill/live-pill elsewhere in this app — the existing convention, not an
  inconsistency to fix).
- **Ingredient chip's stray "•"**: best diagnosis without a screenshot —
  the remove "×" (real, working `hx-delete`) was rendered at 10px with a
  ~1.25px stroke in the tag's own pastel color on a barely-there
  `rgba(0,0,0,.15)` circle, plausible to misread as a dot. Fixed with a
  higher-contrast icon color and a touch more size — flagged as inferred,
  not confirmed, in case it doesn't fully resolve it.
- **Scrollbar crowding the category tab row**: `.menu-scroll-panel`/
  `.scroll-panel` only reserved 4px of padding, not enough for a real OS
  scrollbar. Added `scrollbar-gutter:stable` + matching padding on the tabs
  row above it.

Verified against the real rendered HTML/CSS (no headless browser available
in this environment) at both a normal desktop width and 768px, computing
the actual box math rather than assuming. `go build`/`vet`/`test` clean.

### Admin dashboard mobile audit — 13 concrete findings, fixed systemically
Asked to audit the admin dashboard's mobile UI at 360/390/430px *before*
touching anything, then fix with one consistent system rather than
per-button patches. Computed exact box models at each width instead of
guessing (no headless browser available here) — found real defects,
including one serious one a previous session's fix had only partially
addressed:

- **`.staff-manage-body` popover could render mostly/entirely off-screen.**
  It anchors `right:0` to its own `<details>` trigger — correct on desktop,
  but the mobile flex-wrap staff row can land that trigger anywhere,
  including near the row's *left* edge, in which case a 260px popover
  anchored `right:0` renders mostly negative-x and untappable. An earlier
  session's fix only capped the popover's *width*, not this positioning
  root cause. Fixed with `margin-left:auto` (the standard flex-wrap "always
  sit at the row's right edge, even after wrapping" trick), which restores
  the assumption `right:0` depends on.
- **Systemic touch-target shortfall**: `.btn`/`.btn-sm` sat at 40px/36px
  under `(pointer:coarse)`, `.period-tab`/`.menu-cat-tab` at ~33px — all
  short of Apple HIG's 44px. Unified via one new `--tap-min:44px` token
  (the file already had color/radius/font tokens, nothing spacing-related
  — didn't retrofit a full spacing scale for this, out of proportion to a
  touch-target fix) applied consistently, rather than a different guess
  per selector.
- `.ingredient-tag-remove` kept visually small (18px) on purpose — growing
  it to 44px would make ingredient chips look bloated in their tight
  7px-gap row. Used an invisible `::before{inset:-13px}` hit-area instead:
  a real 44×44 tap zone with no visual size change.
- Grid-stretch bug: collapsing `.product-row`/`.add-form-product` to one
  column on mobile stretched naturally-compact cells (`.stock-stepper`,
  `.status-pill`, a checkbox) to the full row width via Grid's default
  `justify-items:stretch`, leaving dead space and mis-shapen pills. Fixed
  with `justify-self:start` scoped to those specific children, not a
  blanket rule (`.product-main` genuinely needs the stretch).
- Smaller fixes: `.brand-eyebrow` now truncates instead of wrapping to 2
  lines on a narrow phone with a real venue name; `.section-heading`
  switched from `align-items:baseline` to `center` (a text heading against
  a non-text controls row was a few px off); Save/Hide-from-menu given
  `flex:1` on mobile instead of left-packing with a dead gap after them;
  icon-only photo buttons given `aria-label` (only had `title`, which never
  surfaces on a touchscreen).
- Checked explicitly and found clean: no hover-only-revealed controls
  anywhere on this page, and no fixed-bottom element (so no safe-area-inset
  concern here — that's a `host_menu.html` question, not admin).
  `:focus-visible` confirmed untouched by any of the above.

Verified all 13 by recomputing the box math against the final CSS at
360/390/430px, plus a live render through the real handler. `go build`/
`vet`/`test` clean.

### Real image-compression pipeline: two variants, EXIF-aware, hash-named, HEIC handled client-side
Follow-up to the earlier "photos rotated 90°" fix — this replaces the
single-resize pipeline with the two-variant, content-hashed one a spec
asked for explicitly. `internal/imageproc.Process` now returns a `Result`
(`Full`, `Thumb`, `Hash`) instead of one `[]byte`:

- **Two variants, not one**: `full` capped at 1600px longest edge (same as
  before), a new `thumb` capped at 640px — derived by downscaling the
  *already-resized, already-oriented* full image rather than re-decoding
  the original, so the expensive part (orientation correction, a full
  pixel walk) only ever runs once per upload regardless of how many
  variants come out of it.
- **Resampling**: switched `resizeToMax` from `draw.BiLinear` to
  `draw.CatmullRom` — the highest-quality kernel `x/image/draw` ships.
  There's no true Lanczos in that package; a dedicated resampling library
  for just that one filter wasn't worth it for a difference a few-hundred-
  px food photo won't show.
- **Hash-named storage**: `<hash>_full.jpg`/`<hash>_thumb.jpg` under a new
  per-product subdirectory (`static/uploads/products/<id>/...`), replacing
  the old `<id>-<timestamp>.jpg` scheme — a re-upload of unchanged bytes
  now lands on the same URL instead of manufacturing a new one, which
  matters because `main.go`'s static-file `Cache-Control` for this
  directory is `immutable, max-age=31536000`: safe specifically because a
  hash-named URL's content genuinely never changes.
- **Size-budget sanity check**: `checkSizeBudget` logs a warning (not an
  error — never blocks the upload) when a variant comes out more than 2x
  its target (full ~250KB, thumb ~40KB) — a bad quality setting or a
  pathological source image, not something to reject over.
- **Schema**: kept `models.Product.ImageURL` as the only photo column —
  added a `ThumbURL` field to the *display-only* `productView` struct
  (admin.go), derived from `ImageURL` by filename convention
  (`thumbURLFor`, photo.go) rather than a second DB column/migration. The
  smallest change that gets a real thumbnail URL to both templates
  (`_guest_product_list.html`'s menu-list photo, `_product_list.html`'s
  admin preview) — an admin-pasted external URL (never processed by this
  pipeline) just returns unchanged from `thumbURLFor`, since there's no
  separate thumbnail file for it to point at.
- **EXIF**: already stripped as a side effect of re-encoding through
  `image/jpeg` (which never writes an EXIF segment of its own) — no
  separate strip step needed, just confirmed and documented.

**HEIC/HEIF — went with option (c), client-side conversion + a clear
server-side rejection, not a cgo binding (libvips/libheif) or shelling out
to `heif-convert`.** Both of those need an actual C library present on the
deploy host, which this app's own stated deploy story (README: "one Go
binary, one SQLite file, no external services... runs on a Raspberry Pi")
doesn't allow for — Debian/Pi base images don't ship libvips/libheif by
default, and cross-compiling a cgo dependency for ARM adds real friction
this app has otherwise avoided everywhere (self-hosted fonts instead of a
CDN, no Docker/Compose, etc.). Practically: HEIC is ~exclusively an iPhone
Safari problem (Android cameras are essentially always JPEG) — and Safari
is also the *only* browser that can actually decode a HEIC file to draw it
into a `<canvas>`, so `admin_dashboard.html`'s upload JS now detects a
`.heic`/`.heif` file (by extension or reported MIME type) and redraws it
through a canvas into a real JPEG before it's ever sent, in exactly the
one browser where that's possible. Anywhere else, that conversion attempt
just fails silently and the original file goes through — caught server-
side by `imageproc.ErrHEICNotSupported`, a complete, actionable sentence
imageproc.go's error already is, not a generic "process the photo failed."
Progressive JPEG encoding was skipped for the identical reason (Go's
stdlib `image/jpeg` has no progressive mode; libjpeg-turbo does, same
cgo/system-library tradeoff) — baseline JPEG at this file size loads fine
in practice. WebP output (the brief's stretch goal) was skipped too — Go
has no WebP *encoder* without cgo either, only a decoder.

Verified live against a real running server, not just `go test`: a real
3000x2000 JPEG upload produced a 164KB full + 46KB thumb (both under their
2x warning thresholds, no warning logged), both served with the
`immutable` cache header, and a hand-built fake-HEIC upload (real
ISO-BMFF `ftyp` box with a `heic` brand) got rejected with exactly the
HEIC-specific message, not a generic one. Tests:
`internal/imageproc/imageproc_test.go` (both variants' dimension caps
including no-upscale, HEIC detection via a real `ftyp`/`heic` box,
hash stability/uniqueness, the budget-warning log logic in isolation, and
a synthetic "photo-like" — smooth multi-frequency, not flat or pure-noise
— image staying within budget) and `internal/handlers/photo_test.go`
(both variants written to and deleted from disk together, HEIC rejected
through the real HTTP handler with the actual message in the response
body).

### Guest menu restyle (v3) — new palette/type/spacing system, applied from a hand-edited mockup
User took the standalone snapshot from the previous entry, restyled it
externally (their own real browser/devtools), and pasted the result back
to apply for real. Confirmed first that it was a pure `<style>`-block
change (plus the venue-mark SVG's stroke reading `var(--accent)` instead
of a literal hex) — every `hx-*`, `ws-connect`, id, class, and `data-*`
attribute the JS/htmx depend on was untouched, and `_guest_product_list.html`
needed zero changes (same item-card DOM, just newly-hidden `.item-leader`
and restyled classes) — so this was a clean drop-in, not a re-architecture.

- New design tokens: a real 8px spacing scale (`--sp-1..--sp-8`) and type
  scale (`--tx-2xs..--tx-2xl`) replace one-off pixel values throughout;
  tuned warm-dark palette; every "chip" control (cat tabs, tip amounts,
  payment method, ingredients) now a consistent pill shape.
- New type system: Playfair Display (section titles/modal titles only),
  Work Sans (body + item names — previously a competing serif), Poppins
  (venue wordmark only), IBM Plex Mono (prices/codes) — replacing
  Fraunces/Archivo/JetBrains Mono in those roles.
- Menu item rows: flat hairline-divided list instead of a bordered card,
  bigger (112px) photo thumbnail.
- Hero panel simplified: dropped a wood-plank/tribal-pattern treatment
  from an intermediate draft, now a plain raised panel with a soft top
  glow and a thin gold rule under the venue name.
- **Fonts: self-hosted, not the Google Fonts `@import` the pasted draft's
  own `<style>` block led with.** That import would have reintroduced
  exactly the CDN round-trip this file's *existing* font comment already
  documents fixing once (~2.35s/235kB per load) — the pasted draft's own
  comment even flagged this as optional ("swap for self-hosted... if
  you'd rather"). Fetched the actual woff2 files
  (`static/fonts/{playfair-display,work-sans,work-sans-latin-700,ibm-plex-mono,poppins}-latin.woff2`,
  latin subset only, same convention as the existing three) and added
  matching `@font-face` rules instead. Old Archivo/Fraunces/JetBrains Mono
  files/rules were *kept*, not deleted — every new `--font-*` stack lists
  the old family as its fallback (the pasted draft's own intent), so
  they're still load-bearing.
- **Not carried over, on the pasted draft's own explicit instruction**: an
  `onerror` handler on the demo photo that swapped in a hardcoded inline
  placeholder SVG on load failure. That was a snapshot-only workaround (the
  photo's URL pointed at a dev server nothing was listening on wherever the
  file was being previewed) — real templates keep the original "remove the
  `.item-photo-wrap` entirely on error" behavior.
- Verified: rendered the real page through the real handler (not just the
  snapshot) against a full 172-product seed — fonts resolve to the new
  self-hosted files (no `fonts.googleapis.com` reference anywhere in the
  response), venue-mark SVG uses `var(--accent)`, item-card markup
  unchanged, `<style>` block's braces balance. `go build`/`vet`/`test`
  clean.

### Fixed: uploaded photos came out rotated 90°
Reported as "the images are rotated 90 degrees to the left." Root cause:
`internal/imageproc.Process` decodes an upload with Go's stdlib
`image.Decode` and re-encodes it with `image/jpeg` — neither reads nor
writes the EXIF `Orientation` tag. A phone held upright while shooting
doesn't rotate the sensor data before saving; it writes the frame as
captured and sets that tag so a viewer knows to rotate it for display.
Ignoring the tag on decode and dropping it on re-encode meant the "as
captured" (sideways) pixel grid became the *only* copy this app ever
stored or served — correct metadata lost, wrong pixels kept, every viewer
(admin dashboard, guest menu) shows it rotated.

Fixed with a small self-contained EXIF-orientation reader
(`internal/imageproc/orientation.go` — hand-rolled JPEG-marker + TIFF-IFD0
walk for just the one tag, rather than pulling in a general-purpose EXIF
dependency for it) plus the 8 EXIF orientation transforms (rotate 90°
CW/CCW/180°, horizontal/vertical flip, and the 3 flip+rotate combinations).
Applied *after* `resizeToMax`, not before — a 90°-multiple transform costs
the same either way, but running it on the already-shrunk (≤1600px) image
instead of the up-to-40MP original keeps it cheap. Covers all 8 orientation
values, not just the common "phone held upright" case (6), since a half-
done fix here would just trade one silently-wrong rotation for another.

Tests (`internal/imageproc/orientation_test.go`): the tag parser against
all 8 values embedded in a real (spliced-together) EXIF JPEG, defaulting to
"normal" for a JPEG with no EXIF / a PNG / garbage input, and a real
end-to-end `Process()` check — build a landscape red/blue-split JPEG tagged
"rotate 90° CW," confirm the output comes out portrait with the color split
landing on the correct edges (not just that the dimensions swapped).

### Fixed: uploaded photos and any edit at all got stuck on a product that already had one (the real cause of "stock won't save")
Follow-up to the entry directly below, which cleared the backend/template
logic for the stock stepper and Unlimited toggle but couldn't reproduce a
real failure — because it never sent a resubmitted local image_url the way
the real form does. Reported again against real data ("Omelette is stuck
sold out, nothing brings it back" + "uploaded images don't work"), and this
time it reproduced immediately.

**Root cause**: `_product_list.html`'s hidden `image_url` input always
carries the product's *current* value and gets resubmitted on *every* Save
— including a plain stock change that never touches the photo. The moment
a product has an uploaded photo (or the seeded demo one), that value is a
local `/static/uploads/...` (or `/static/demo/...`) path, not a pasted
http(s) URL. `parseImageURL` only accepted blank or http(s), so that
resubmitted local path failed validation and the whole update 400'd —
silently, since htmx doesn't swap in an error response by default. Net
effect: once *any* photo ever got attached to a product, that product was
frozen — Save, the stock stepper, the Unlimited toggle, none of it could
ever change again (visibility toggling still worked; it's a separate
endpoint that never touches image_url) — matching both "my uploads don't
work" (the *next* edit after an upload looks broken) and "stock won't
save" from the earlier entry, which happened not to hit this because its
test payloads never included a resubmitted local path.

Fixed by having `parseImageURL` also accept the two paths this app itself
ever generates (`internal/handlers/admin.go`'s new
`localImagePathPrefixes`: the upload dir and the demo asset dir) round-
tripping unchanged, alongside blank and http(s). Neither is admin-typed —
accepting them back isn't trusting new input, it's the same value this
handler (or the seed) wrote in the first place. Added
`TestAdminUpdateProductRoundTripsExistingLocalPhotoURL`
(`internal/handlers/photo_test.go`) reproducing the exact request shape
(upload a photo, then a plain stock-only edit that resubmits the resulting
local path) — confirmed it fails with the old code (400) and passes with
the fix.

Verified against the real venue database, not just a scratch copy (after a
checksummed `PRAGMA wal_checkpoint` + file-copy backup to `backups/`, same
as `scripts/backup-db.sh` does): the real Omelette row was actually stuck
exactly as reported (stock 0, Save/Unlimited both silently rejected);
replaying its real Save request against the fixed binary returned 200 and
the guest menu no longer shows it sold out. Also found and removed one
leftover row, `SmokeTestItem` (id 173) — dead test data from an earlier
session's "verified live end-to-end" pass that got hidden rather than
cleaned up, cluttering the real admin product list. `order_items` rows
denormalize `name`/`price` at order time and don't foreign-key against
`products`, so deleting it left order history intact. There's still no
in-app way to delete a product at all (only hide) — noted below, not
built, since it wasn't asked for.

### Investigated "stock doesn't save / Unlimited doesn't work," fixed a real broken-image bug + admin mobile layout regressions
The stock stepper + Save and the ∞/Unlimited toggle were reported broken.
Reproduced the exact form submissions AdminUpdateProduct's own template
sends (`internal/handlers/admin.go`, `_product_list.html`) against a real
running server, both with an explicit stock value and with the field
omitted (disabled input, the ∞-checked case) — both persisted correctly and
round-tripped through a re-render (`go test ./...` also covers this). No
code change was needed there; if this is still happening for a real user,
the next step is confirming which server binary/process they're actually
hitting, not more backend changes.

While verifying that in a real browser-shaped request, found and fixed
three actual bugs along the way:

- **The one seeded demo product photo (Omelette) rendered as a broken
  image everywhere** — admin dashboard *and* the guest menu. It was stored
  as an inline `data:image/svg+xml;base64,...` URI (`seed_ego.go`'s
  `demoBrunchPhoto`); Go's `html/template` contextually auto-escapes any
  `src="{{ .ImageURL }}"` and only ever passes through `http:`, `https:`,
  and `mailto:` schemes, silently defanging everything else (including
  `data:`) to `#ZgotmplZ`. Fixed by moving that seed image to a real file
  (`static/demo/omelette-demo.svg`, referenced by path — no scheme, so
  nothing to filter) instead of inlining it, which also stops re-sending
  that ~1KB string in every hidden `image_url` field and every
  `admin/menu/ws` push touching that row. Added a one-time, idempotent
  `UPDATE` in `db.go`'s migrations to repair any database already seeded
  with the broken value — a fresh reseed alone wouldn't have fixed an
  existing install's data. (Real admin-uploaded photos and pasted URLs were
  never affected — `parseImageURL` already restricts pasted URLs to
  http(s), and uploads are written to `/static/uploads/...` paths, neither
  of which this filter touches.)
- **Two of the three admin settings forms didn't actually collapse to one
  column on a phone** (Venue settings, Business details), despite the
  shared `.add-form` grid having exactly that responsive behavior. Cause:
  both the initial page render *and* their post-Save re-render fragments
  (`_venue_panel.html`, `_business_panel.html`) used an inline
  `style="grid-template-columns:..."`, which — same as the `.add-form-*`
  modifier-class comment already on this file explains for the pattern
  this duplicated — always overrides the stylesheet's `@media` rule no
  matter what it says. The initial SSR render of the Venue panel happened
  to also carry the correct `add-form-venue` class already, masking the bug
  until the first Save swapped in the broken inline-style version; Business
  details never had a modifier class at all. Fixed by giving Business
  details its own `.add-form-business` modifier (added to the same mobile
  breakpoint as the others) and pointing both fragments and both inline
  admin_dashboard.html forms at their modifier classes instead of a literal
  style attribute — one definition of each form's column layout, used
  consistently on first load and every re-render.
- **Topbar could push "Live"/"Log out" off-screen on a phone with a long
  venue name** — `.brand`/`.topbar-right` had no shrink behavior, so a
  venue name near the 80-char cap plus a narrow viewport had nowhere to
  give. `.venue-name` now truncates with an ellipsis instead of forcing its
  siblings out of the visible area.

Also, not bugs but genuine rough edges given the mobile-UX complaint:
touch targets on the stock stepper, ingredient-tag remove button, and
category/period tab buttons were sized for a mouse cursor (some as small as
18px); added a `@media (pointer:coarse)` block sizing those up for a
fingertip specifically (not a viewport-width breakpoint, so a touch laptop
or landscape phone at a wide viewport still gets them, and a narrow desktop
window doesn't). And for "don't let big photos slow the site": server-side
resizing/re-encoding already existed (`internal/imageproc`, ≤1600px
longest edge, JPEG q82) — what was missing was cache headers, so a
returning guest re-fetched every product photo from disk on every page
load. Added `Cache-Control: public, max-age=31536000, immutable` for
uploaded photos (each upload gets a unique filename — see
`writeProductPhoto` — so a given URL's bytes genuinely never change) and a
day's cache for vendored JS, fonts, demo assets, and PWA icons.

Verified end-to-end against a real running server (not just `go test`):
fresh install and a deliberately-broken existing database for the image
migration; the actual multipart POSTs `_product_list.html`'s Save button
and the ∞ toggle send, with and without the disabled stock field; the
post-Save HTML for both settings forms; `Cache-Control` headers on real
responses. No headed browser/devtools available in this environment, so
the mobile layout changes are verified by reading the rendered HTML/CSS
and reasoning through the cascade, not a real-device screenshot.

### Guest-side phone notifications ("your order is ready") — the customer half of Web Push
The staff-side entry directly below covers a kitchen/bar/waiter's phone.
This is the other half of the same ask: the *guest* who ordered gets a real
lock-screen notification too, without keeping the table page open — "Order
confirmed" the instant checkout succeeds, "Your order is ready! 🎉" the
instant staff flips it to ready. Same mechanism (VAPID Web Push, no
third-party provider), same VAPID identity as the staff feature, deliberately
kept as a separate subscription table and code path rather than reusing
`push_subscriptions` — a guest is never an authenticated `users` row.

- `guest_push_subscriptions` (session_id, endpoint UNIQUE, p256dh, auth) +
  `internal/db/push.go`'s guest-prefixed functions — keyed off the same
  anonymous `bitebox_session` cookie every other guest action already keys
  off (`GuestSessionCookieName`/`resolveHostedTable`), not a user_id.
  Upserts on endpoint like the staff table does.
- `handlers.GuestPushSubscribe`/`GuestPushUnsubscribe`
  (`POST /table/{number}/push/{subscribe,unsubscribe}` — the `{number}` is
  cosmetic, same "resolve from the session cookie, never trust the URL"
  rule `RequestAssistance`/`Leave` already follow) and `notifyGuestSession`,
  which mirrors `notifyDepartments` exactly: best-effort per device, drops a
  subscription the moment its push service answers 404/410. Rate-limited
  (`guestPushLimiter`, 20/min/IP) the same way cart actions/DJ requests are.
  Same SSRF-guard endpoint allowlist (`isAllowedPushEndpoint`) applies —
  it's shared code, not duplicated.
- Wired at the same two moments as the staff alerts, right next to them:
  `cart.go`'s `Checkout` → `pushOrderConfirmedAlert`, `worker.go`'s
  `WorkerUpdateOrderStatus` → `pushOrderReadyAlert` when the new status is
  `ready`. Both tag the notification `guest-order-{id}`, so a guest with two
  orders open gets two distinct notifications, and "ready" replaces
  "confirmed" for the *same* order instead of stacking a second one.
- **Per-table PWA manifest, not the staff dashboard's fixed one.**
  `handlers.GuestWebManifest` (`GET /manifest-guest.json?table=N`) generates
  `start_url` as `/table/{N}` on the fly — `host_menu.html` links it with
  its own table number in the query string. This matters more than it
  sounds: iOS relaunches a home-screen-installed PWA at the manifest's
  start_url, not wherever "Add to Home Screen" was tapped from — without a
  per-table start_url every guest's icon would reopen the same fixed page
  regardless of which table they scanned. Same iOS legacy meta tags as the
  staff dashboard (`apple-touch-icon`, `apple-mobile-web-app-capable`,
  `-status-bar-style`) added to `host_menu.html`'s head; reuses the
  existing origin-root `sw.js` as-is (its push/notificationclick handlers
  were already generic — no worker-specific assumptions to change).
- **`host_menu.html`'s own enable-alerts UI**, separate from the worker
  dashboard's small icon-only toggle since a customer isn't staff: a
  dismissible card ("🔔 Order alerts — get a real notification... — Enable
  order alerts") that only ever appears when push is actually usable, with
  copy explaining *why* before the permission prompt ever fires — the
  browser-permission prompt itself only ever fires from that button's own
  click handler, never blind on page load, since a denied prompt has no
  second chance on iOS or anywhere else. On iOS in a plain Safari tab
  (where push is categorically unavailable, permission or not) this shows
  a different, mutually-exclusive card instead: "Tap Share → Add to Home
  Screen." Both dismiss to `sessionStorage` (not `localStorage`) — "not
  now" means "don't ask again this visit," matching the guest session
  cookie's own session-only lifetime.
- **Fallback hook, deliberately a no-op.** `notifyGuestFallback` — called
  from `notifyGuestSession` when a session has no subscription at all (iOS
  Safari tab, declined permission, unsupported browser) — is where an
  SMS/email fallback would go. Left as a documented TODO rather than
  guessed at: it implies collecting a phone number/email at checkout (a
  product decision not asked for here) and a real third-party account
  (Twilio, an email provider) this app has no credentials for. Signature
  matches `notifyGuestSession`'s own payload so wiring one in later is a
  one-line call.
- Tests: `internal/db/push_test.go` (guest upsert-on-endpoint, session
  filtering, scoped-delete-can't-touch-another-session, unconditional
  delete) and `internal/handlers/push_test.go` (subscribe/unsubscribe
  round-trip, bad-body rejection including the SSRF-endpoint case, no-
  session-cookie rejection, cross-session unsubscribe can't delete another
  guest's device). Verified live end-to-end against the running server too:
  created a real table via the admin API, subscribed a fake device to it,
  placed a real order (checkout succeeded, `pushOrderConfirmedAlert` fired
  and attempted a real encrypted send — failed only because the test
  device's keys were fabricated garbage, not real EC points, same
  non-crashing failure mode the staff feature's own manual verification
  hit), confirmed `/manifest-guest.json?table=777` returned
  `start_url: "/table/777"`, confirmed the SSRF guard 400s a non-push-
  service endpoint on the guest path too. Test artifacts (table, product,
  order) cleaned up afterward through the app's own admin endpoints, not by
  hand-editing the DB file.

### Real phone notifications for new orders / ready-for-pickup (Web Push)
Follow-up to the in-page order-feed alert banner+chime: that only helps
someone already looking at the dashboard screen. This is the actual "don't
have to open the app" ask — a lock-screen-visible push, the same mechanism
an SMS or chat app's notification uses, via the standard Web Push API
(VAPID, no third-party push provider account needed).

- `settings.vapid_public_key`/`vapid_private_key` — this server's Web Push
  identity, generated once on first run (`handlers.InitPush`, called from
  `main.go` right after `db.InitDB`) and persisted, since a subscription is
  only ever valid for the public key it was created against.
- `push_subscriptions` table + `internal/db/push.go` — one row per device a
  staff member has enabled alerts on, tied to their account.
  `GetPushSubscriptionsForDepartments` mirrors the order feed's own
  admin/superworker "sees everything" rule (`orderFeedBuckets`) rather than
  requiring an exact department match.
  `internal/handlers/push.go` — `PushSubscribe`/`PushUnsubscribe`
  (`POST /worker/push/{subscribe,unsubscribe}`, any logged-in staff
  account) and `notifyDepartments`, which sends via
  `github.com/SherClockHolmes/webpush-go` and cleans up a subscription the
  moment its push service returns 404/410 (endpoint's dead) rather than
  retrying it forever.
- Wired at the two moments that matter: `cart.go`'s `Checkout` →
  `pushNewOrderAlert` (department resolved the same way
  `authorizeDepartmentMutation` already does, via
  `db.OrderHasAnyCategory` — a mixed order reaches both kitchen and bar),
  and `worker.go`'s `WorkerUpdateOrderStatus` → `pushReadyAlert` when the
  new status is "ready" (waiter department).
- `static/sw.js` (service worker, served at the origin root by
  `handlers.ServiceWorker` — not under `/static/*`, so its control scope
  covers the whole app) + `static/manifest.json` + generated
  `static/icons/icon-{192,512}.png` (flat recreation of the dashboard's own
  brand-mark shape, in-theme colors — `scripts/gen-icons.go` regenerates
  them if the brand colors ever change).
  `worker_dashboard.html` registers the service worker and adds a topbar
  toggle (📵/📱) that requests `Notification.requestPermission()` and
  `pushManager.subscribe()`s, POSTing the result to `/worker/push/subscribe`.
- **iOS caveat, not fixable from this side**: Safari only allows Push once
  the page has been added to the Home Screen and is opened from there
  (16.4+) — a plain browser tab can't subscribe at all, permission or not.
  The toggle detects this (`navigator.standalone` / `display-mode:
  standalone`) and shows an explanatory toast instead of a broken
  permission prompt. Android Chrome/Edge and desktop browsers work
  directly, no install step needed.
- **HTTPS caveat**: Web Push is a secure-context API — it works today
  against `localhost` for browser-based testing, but a real phone needs
  the server reachable over real HTTPS (a deployed domain, or a tunnel
  like ngrok/cloudflared for local testing on a device) before this does
  anything on that device. Not a code gap — the browser platform itself
  refuses to subscribe over plain HTTP to a non-localhost origin.
- `PUSH_CONTACT` env var sets the VAPID JWT's operator contact (defaults to
  a placeholder) — deliberately not the logged-in user's own email; a real
  contact only matters once this is actually deployed behind HTTPS.
- Tests: `internal/db/push_test.go` (upsert-on-endpoint, department
  filtering, admin/superworker always included, deactivated accounts
  excluded, delete), `internal/handlers/push_test.go` (subscribe/
  unsubscribe handler round-trip, bad-body rejection, item summary
  formatting). Manually verified end-to-end: subscribed a fake device,
  placed a bar-category order, confirmed `pushNewOrderAlert` correctly
  resolved the bar department and attempted a real encrypted send (which
  failed only because the test device's keys were fabricated garbage, not
  real EC points — failed exactly the way it should: logged, no crash,
  checkout still succeeded).

### Order-feed alert banner + chime (in-page, no phone required)
The first pass at "notify staff when an order comes in" — before the ask
was clarified to mean *real* phone notifications (above), this covers
anyone actually looking at the worker dashboard. Still valuable on its own
(a mounted kitchen screen doesn't have a "phone" to push to), and stacks
with the Web Push alert rather than being replaced by it.

- `_order_feed.html` order cards carry `data-status` (the real status, not
  the decorative NEW/READY badge — client JS prunes that after 5 minutes,
  which would make a same-element detector miss a late ready flip).
- `worker_dashboard.html`: diffs each order-feed re-render against what it
  saw last time (`checkForNewOrderAlerts`) — a never-before-seen card is a
  new order (or, on the waiter's ready-only feed, a ready one); an
  already-known card flipping not-ready → ready (possible on the
  superworker/admin combined view) fires the ready alert too. A themed
  slide-in banner (red for new, purple for ready — matching the existing
  `.action-ready` color) plus a synthesized Web Audio chime (no audio
  asset to ship), a mute toggle persisted in `localStorage`, and a
  flashing tab title when the tab isn't focused. First page load only
  primes the diff state — no banner storm for orders already on screen.

### Demo Greek-receipt structure (VAT breakdown, ΑΦΜ/ΔΟΥ, sequential number, fabricated MARK)
Explicitly a demo/project ask, not for the real EGO venue — confirmed with
the user before building anything: dummy data only, no real business
details, and critically **no myDATA/ΑΑΔΕ integration or connection to any
Greek government system**. That distinction matters enough that it's
enforced at every layer, not just described in a comment: a giant on-page
banner (screen *and* print — if this is ever actually printed on paper, it
still says DEMO in red), doc comments on every model method that produces
receipt content, and a handler test that fails if that banner text is ever
accidentally removed from the template.

Real compliance (myDATA transmission, certified fiscal mechanism) isn't
something this app attempts or claims — that requires an actual regulatory
relationship with a licensed provider, not more Go code. This is
structure only: what the *shape* of a Greek retail receipt (Απόδειξη
Λιανικής) looks like, for a project/demo context.

- `products.vat_rate` / `order_items.vat_rate` (snapshotted at order time,
  same "immutable once ordered" reasoning as name/price) — 24/13/6,
  Greece's real three rates, admin-selectable per product (add-form and
  each row's own edit form). Seed data varies rates by department (kitchen
  → 13%, bar → 24%) specifically so the demo receipt shows a real
  multi-line breakdown instead of one flat rate.
- `settings.business_vat_number`/`tax_office` (ΑΦΜ/ΔΟΥ) — new "Business
  details (demo)" admin panel, obviously-fake defaults out of the box.
- `models.Order.VATBreakdown()` — groups items by rate, backs out net/VAT
  from the VAT-inclusive gross price via the standard extraction formula
  (net = gross / (1 + rate/100)). `ReceiptNumber()` formats the existing
  `orders.id` (already sequential/gapless — the actual structural property
  a real receipt number needs) to look like one; `DemoMARK()` fabricates a
  myDATA-shaped registration mark, deterministic per order.
- `AdminOrderReceipt` (`GET /admin/orders/{id}/receipt`) — standalone
  printable page (own `_receipt.html`, not a dashboard fragment), linked
  from every order-history row. Narrow/monospace/dashed-line styling
  deliberately reads as "this is a receipt," which is exactly why the
  demo banner has to be unmissable on it.
- Tests: `VATBreakdown` (single rate, multi-rate grouping/sorting, empty
  order), settings/product/order-item round-trips, and a handler test that
  explicitly asserts the DEMO banner and the myDATA disclaimer are present
  in the rendered page — the one test in this whole session written
  specifically to prevent a *content* regression, not just a logic one.

### Admin-configurable cash-orders toggle
Asked for as a new `venue_settings` table with its own validation
middleware — implemented on the existing `settings` singleton instead
(same row as `dj_requests_enabled`/`venue_name`), and as inline validation
in `CartHandlers.Checkout` rather than net/http middleware: this codebase
already has exactly this pattern (`DJRequest` checks
`settings.DJRequestsEnabled` inline the same way), and generic middleware
wrapping just one endpoint's one form field would mean either double-
parsing the request body or awkwardly stashing state between middleware
and handler for no real benefit over the one-line check the existing
pattern already gets for free.

- `settings.allow_cash_orders` (migration, default 1/on — matches the ask:
  local dev and every existing venue's cash flow keep working unless an
  admin deliberately turns it off). `db.SetAllowCashOrders`.
- `AdminToggleCashOrders` (mirrors `AdminToggleDJRequests` exactly) — new
  "Payment methods" panel on the admin dashboard. Broadcasts by reusing
  `BroadcastAllMenus`'s existing cart-refresh nudge (see menu.go's
  `cartRefreshTrigger`) rather than a new broadcast path: any guest with
  checkout already open re-fetches `#cart-summary` — and therefore this
  setting — the same way it already does for a price/stock change. This
  is what "dynamically fetches venue settings on load" turned into: no
  separate settings-fetch endpoint, the existing live-refresh mechanism
  already covers it.
- `resolvePaymentMethod` (cart.go): an explicit "cash" pick while it's
  disabled is rejected outright (403, clear message) — the checkout screen
  already hides that option, so reaching this means a stale tab (admin
  toggled it after the guest's page loaded, before the live nudge caught
  up) or a direct/tampered POST, not a case to silently paper over. A
  blank/unrecognized value isn't a rejection, just falls back to whichever
  of cash/card is still actually offered — preserves the pre-existing
  "blank means cash" default when cash is on, falls back to card when it's
  off.
- `_cart_summary.html`: cash chip simply isn't rendered when disabled
  (card becomes the sole, pre-selected option), with a one-line "This
  venue is currently card-only" note — same "genuine absence, not a
  greyed-out state" pattern the DJ-request form already uses.
- Tests: `resolvePaymentMethod` (all four allowed/blocked × cash/card/
  blank combinations), a full-handler `Checkout` test (cash rejected while
  off, succeeds immediately once re-enabled), and a settings round-trip
  test. Verified live end-to-end too: toggled off, cash chip disappeared
  from a real checkout screen, an explicit cash POST got a real 403, a
  blank one placed a card order correctly, toggled back on.

*(A trailing, unrelated fragment on this request — "pdf user resits for
greece law" — didn't parse into anything actionable connected to this
feature. Flagged back to the user rather than guessed at or acted on.)*

### Fixed: product photos didn't render at all + real photo uploads (camera + gallery), server-side resizing
Two problems, reported together. First, **every product photo was broken**
since the security pass two sessions ago: `img-src 'self' data:` in the CSP
silently blocked any externally-hosted photo URL (the whole point of the
`image_url` field) — browser just refused to load it, no visible error
past a broken-image icon. Widened to `img-src 'self' data: https:`; images
can't execute script, so this doesn't carry the risk a broader
script-src/connect-src relaxation would.

Second, and the bigger ask: real uploads, not just a URL field.

- **`internal/imageproc`** — every uploaded photo, whatever a phone camera
  or gallery actually produced, comes out a resized, re-encoded JPEG.
  Guards, in order: raw upload capped at 20MB; `image.DecodeConfig` (reads
  only the header) checked against a 40-megapixel cap *before* the real
  decode, specifically against a small file claiming an enormous
  resolution — decoding that fully first would allocate gigabytes for
  what's only a few KB on disk; then resized (`golang.org/x/image/draw`,
  bilinear) so the longer edge is at most 1600px, which is the actual
  answer to "don't store photos at some crazy camera resolution" — a
  modern phone shoots 3000-4000px+ per side, and this app never displays a
  product photo wider than a few hundred px even at high DPI. Never trusts
  the client to have already resized anything, same "never trust client
  input" rule as prices/table numbers/ingredient names elsewhere in this
  app.
- **Two ways in, from the same file input**: "📷 Take photo" toggles the
  hidden `<input type=file>`'s `capture="environment"` attribute before
  clicking it (opens the phone's camera directly), "🖼️ Choose file"
  clears that attribute first (opens the normal picker/gallery) — one
  element, no `DataTransfer` trickery needed to merge two inputs into one
  form field. Live thumbnail preview via `FileReader`, client-side only
  (the server-side resize is what's actually authoritative).
- **Storage**: `static/uploads/products/{id}-{nanotime}.jpg`, served by
  the existing static file handler — no new route needed. Filenames are
  timestamped so a re-upload can't collide with (or get browser/live-push
  cached over) the previous one. Replacing or clearing a photo deletes its
  old file; `AdminCreateProduct` validates and decodes the photo *before*
  the DB write, so a bad upload rejects the whole request cleanly instead
  of creating the product and silently dropping the photo.
- **URL field kept** as an alternative (now that CSP actually allows it) —
  an upload takes priority if both are given. This is the actual
  "practical ways to add photos" the ask was after: upload from the device
  in hand, or point at wherever a venue's photos already live.
- **One seeded demo photo** — the "add a photo to the brunch [items]" ask.
  Deliberately a small inline SVG data URI, not a hotlinked external image:
  nothing to go dead, get rate-limited, or silently break the exact "photos
  don't render" complaint this session started with. Real venue photos go
  through the upload flow or a pasted URL; this is demo data only.
- Tests: `internal/imageproc` (resize-down math, small-image passthrough,
  PNG input, non-image/oversized/decompression-bomb rejection) and
  handler-level multipart upload tests (create-with-photo end to end
  against a real temp file on disk, invalid-photo-blocks-the-whole-request,
  replace-deletes-the-old-file, remove_photo clears both the DB field and
  the file). Verified live against the running server too: a real
  3000×2000 JPEG uploaded via curl came back stored at 1600×1066, correctly
  aspect-ratio-preserved, served with the right content-type.

### Security audit follow-through (SEC-01–15) + the "what's missing besides card payment" backlog
Two connected passes in the same session. First, worked through
`SECURITY_AUDIT.md` end to end — verified every finding against the actual
code rather than trusting the audit's own claims (two, SEC-04 and SEC-11,
turned out worse in practice than described: both were exploitable with
*no* auth at all), fixed 14 of 15, deliberately left SEC-09 (hardcoded seed
credentials) as an intentional, documented dev-convenience bypass rather
than fake-fixing it, and diverged from the audit's own suggested fix on
SEC-10 after checking Go's `net/http` source: `ReadTimeout`/`WriteTimeout`
would have silently killed every WebSocket ~15s after connecting (gorilla's
`Upgrade` only clears the write deadline post-handshake, never the read
one) — this app's entire live-update mechanism. See `SECURITY_AUDIT.md`
itself for the per-finding detail; each entry there is now tagged
`✅ DONE`, left as the full original write-up (SEC-09, tagged
`<!-- STATUS: DEV_ONLY - CONVENIENCE BYPASS -->`), or untouched pending a
product decision (SEC-13 — see below).

Then, asked "what's missing besides card payment," answered from a real
read of the code (not TODO.md's own possibly-stale list — caught and fixed
one drift below), and built everything from that list the user said yes to:

- **Table auto-release.** New `tables.last_activity_at`, touched on every
  real guest action (view, cart add/remove/checkout, DJ request — all
  funnel through `resolveHostedTable` in cart.go, one spot covers all of
  them). A background sweep (`StartIdleTableSweep`, table.go) force-releases
  anything idle 45+ minutes, clearing its cart too. **Caught a real bug
  writing the test for this**: the first cutoff comparison bound a Go
  `time.Time` directly against `last_activity_at` (written by SQLite's own
  `CURRENT_TIMESTAMP`) — different string formats (`T`-separator vs. space,
  timezone offset vs. none), so SQLite's text comparison doesn't sort them
  chronologically at all; every occupied table would've looked idle within
  the first sweep. Same underlying class of bug as the UTC-vs-localtime
  revenue fix further down this file — fixed the same way, doing the
  cutoff arithmetic in SQLite's own `datetime()` instead of Go's.
- **Automated tests, for real this time.** `go test ./...` now runs 30+
  tests across every package — auth (password hashing, cookie Secure-flag
  derivation), wshub (pub/sub isolation — the SEC-11 class of bug), cart
  (concurrency-safe under `-race`, customization-line separation), models,
  and handler-level regression tests for the two access-control fixes
  above (SEC-04's Leave IDOR, the new bar/kitchen mutation boundary). DB
  and handler tests run against a throwaway SQLite file in a temp
  directory (handler tests additionally symlink in the real
  `templates`/`static` dirs, since `template.ParseFiles` and the DB both
  use paths relative to the process's working directory) — never the real
  `bitebox.db`.
- **Order history pagination.** `GetOrderHistory` takes `limit, offset`
  now; the admin panel loads 50 at a time with a "Load more" button
  (`AdminOrderHistoryPage`) instead of the old flat 200-row cutoff.
- **Staff password reset + account editing + an audit trail.**
  `AdminResetStaffPassword` (admin sets a new password directly — no
  email system exists for self-service), `AdminUpdateStaff` (rename,
  change department; role is deliberately not editable this way — see its
  doc comment), and a new `audit_log` table logged from every staff-
  management action, surfaced as its own dashboard panel.
- **A DB backup story.** `scripts/backup-db.sh` (cron-friendly, uses
  sqlite3's own `.backup`, prunes beyond 30) plus an admin dashboard
  "Download backup" button (`VACUUM INTO` a temp file, streams it, deletes
  it) for an ad-hoc copy with no CLI needed.
- **Staff tipping.** Optional tip at checkout (flat-amount chips, same
  interaction pattern as the DJ tip — deliberately a separate chip
  group/ids so the two don't cross-talk), stored as `orders.tip_amount`,
  kept separate from `total_amount` so every existing stock/revenue-per-
  item computation stays untouched. `Order.GrandTotal()`/`PerPersonTotal()`
  are what guest-facing/CSV totals actually use now.
- **Split-bill, informational only.** Optional `orders.split_count` at
  checkout, shown as "€X per person" on the receipt. No per-guest payment
  collection exists or was added — one host's session still pays for the
  whole table; this is purely a display divisor.
- **The bar/kitchen mutation boundary, closed.** Previously read-side
  only (flagged as accepted-for-now in this file) — a bar worker who knew
  or guessed a kitchen order's id could still `POST` a status/paid/cancel
  mutation on it directly. `authorizeDepartmentMutation` (worker.go) now
  checks the same way the read-side feed already filters.
- **Guest-facing rate limiting**, previously fully absent (see "Maybe
  later" below — now resolved): cart add/remove and DJ requests both
  throttled per-IP, reusing the same limiter the login/table-claim
  endpoints already had.
- **Product photos.** `products.image_url` — a plain URL field, not an
  upload (no file-storage infra exists for guest- or admin-supplied
  binaries); blank falls back to the existing text-only card.
- **"Call waiter."** Guest-facing button (`RequestAssistance`,
  resolved from the caller's own session like every other guest action,
  never a client-supplied table number), surfaced live to staff two ways:
  a banner on the worker dashboard (new `worker:assistance` topic) and a
  badge + one-tap acknowledge on the admin table grid.
- **SEC-13, partially.** Sequential `/table/{n}` URLs are still guessable
  (unchanged) — asked the user whether to add per-table secret tokens
  (which means reprinting every physical QR code at a live venue, EGO)
  and they chose to keep the existing rate-limit-only mitigation rather
  than take on that reprint. Documented as the deliberate final state in
  `SECURITY_AUDIT.md`, not left ambiguous.

**Caught and fixed one doc-drift bug along the way**: this file's own
"Missing / rough edges" list below still said *"'Extra' ingredients have
no guest-facing flow"* — checked the actual template and that's been false
since the ingredient-customization session further down this file; the
line was just never removed after that fix landed. Removed below.

### Verified two previously-unconfirmed behaviors in a real browser; ops hygiene + cosmetic cleanup pass
Walked the "Missing / rough edges" list with the user and knocked out the
cheap/safe items, plus actually verified (not guessed) the two things that
were flagged as unchecked:

- **Cart-refresh-trigger poke: confirmed working, in a real browser.**
  Set up headless Chromium (Playwright) and watched network traffic directly:
  adding an item fires the expected extra `GET /cart/summary` the instant
  the OOB-swapped `#cart-refresh-trigger` lands in the DOM — htmx really does
  auto-fire `hx-trigger="load"` on an OOB-inserted node. No fallback needed.
- **htmx `ws` extension reconnect across a server restart: confirmed working.**
  Same browser session, killed and restarted the server mid-session: both
  `/menu/ws` and `/table/{n}/ws` sockets showed a burst of failed reconnect
  attempts (`ERR_CONNECTION_REFUSED`) while the server was down, then
  successfully reopened on their own within ~1s of it coming back — no
  manual page reload needed. A live widget survives a server restart.
- **Graceful shutdown added.** `main.go` now installs a `SIGINT`/`SIGTERM`
  handler and calls `http.Server.Shutdown` with a 10s drain deadline instead
  of letting the OS kill the process mid-request. Known partial limitation,
  stated honestly: Go's `Shutdown` doesn't wait on already-hijacked
  connections, and `gorilla/websocket` hijacks the underlying TCP conn on
  upgrade — so this drains in-flight plain HTTP requests cleanly, but open
  WebSocket connections still just drop when the process actually exits.
  Fixing that fully would mean the hub tracking and closing every live
  connection itself; not done here, noted for later if it ever matters.
- **`bitebox.db` untracked from git going forward.** Added `.gitignore`
  (covers the db file itself plus its `-shm`/`-wal`/`-journal` WAL sidecars)
  and ran `git rm --cached bitebox.db` — stays in past history, stops being
  a footgun on every future `git status`. `scripts/reset-db.sh` / first-run
  seeding are the source of a working database from here on, not git.
- **Removed the dead plain-HTTP polling routes** (`/worker/orders/feed`,
  `/worker/dj/feed`, `/table/{number}/order-status`) and their handlers
  (`WorkerOrdersFeed`, `WorkerDJFeed`, `OrderStatusPoll`) — confirmed via
  grep first that nothing in any template still pointed at them (the
  websocket migration replaced every caller). The underlying `renderX`
  helpers they called stay, since the POST mutation handlers still use them
  to answer the actor's own request.
- **Removed the dormant `.pay-online` CSS class** from
  `worker_dashboard.html` — dead weight from the pasted design; this app has
  only ever emitted `cash`/`card`, never an "online" payment badge.
- **Venue name is a real setting now, not a hardcoded string.** Was
  literally `"Ouzeri Marina"` baked into two templates. Extended the same
  singleton-settings pattern the DJ toggle uses: `settings.venue_name`
  column (defaults to the old hardcoded value, so upgrading changes nothing
  until an admin actually edits it), a new "Venue settings" panel on the
  admin dashboard, `db.SetVenueName`, and `BroadcastVenueName` — renaming
  the venue updates every guest already browsing the menu live (pushed over
  the existing `/menu/ws` topic, same reasoning as the DJ section) and the
  admin's own header, in the same request, via a hand-built OOB fragment
  (skipped the usual `oobWrap` helper here since the target element also
  carries `class="name"`, which a generic OOB wrapper would have clobbered).

### DJ song requests are now an admin-controlled venue toggle
Some venues don't run a DJ / don't want song requests taking tips right now —
the "Request a song to DJ" form is no longer permanently on the guest menu.
New singleton `settings` table (`id=1` row, `dj_requests_enabled`, defaults
on) via `db.GetSettings`/`db.SetDJRequestsEnabled`. Admin dashboard gets a new
"DJ song requests" panel (enabled/disabled pill + toggle button, posts to
`/admin/settings/dj-requests/toggle`).

Guest-facing behavior is genuine absence, not a greyed-out state: the form
lives in its own partial (`_dj_section.html`, `{{ if .DJRequestsEnabled }}`)
pushed as an OOB fragment into `#dj-section` over the *existing* `/menu/ws`
connection (reused rather than adding a new socket — it's a venue-wide
setting, not per-table, and OOB swaps target by element id regardless of
which connection delivers them). Toggling in the admin dashboard broadcasts
live: a guest already looking at the menu sees the form vanish/reappear with
no reload.

Deliberately kept `#song-status` (the DJ's accept/reject decision widget)
structurally separate and always present, wired to the *other* existing
socket (`/table/{number}/ws`) instead of `/menu/ws` — two reasons: (1) a
guest who already sent a request before the admin disabled the feature
should still see its outcome, not have it vanish along with the form: and
(2) `/menu/ws`'s initial payload is async relative to `/table/.../ws`'s, so
folding song-status into the same gated fragment would race whichever
connection's snapshot lands first. `_song_status.html` now wraps its content
in a `.section` card only when there's an actual request to show, so an
idle table doesn't display an empty bordered box.

Also closed a gap the toggle alone wouldn't have caught: hiding the form
client-side doesn't stop a guest from `POST`ing `/dj/request` directly.
`DJRequest` (dj.go) now re-checks `db.GetSettings().DJRequestsEnabled`
server-side and 403s if the feature is off — the toggle is authoritative,
not just cosmetic. Verified end-to-end over a live websocket connection:
initial `/menu/ws` payload includes the form when enabled; toggling off
mid-connection broadcasts an OOB swap that removes it; direct POSTs are
rejected with the feature off and accepted with it on; re-enabling restores
the form live.

### Extra ingredients now show in the guest customize panel; investigated (not a bug) "everything served by admin"
**Extras were completely missing from the guest UI** — the customize panel
only ever rendered `kind == "removable"` ingredients; "extra" ones existed
in the data model (admin could tag them) but had zero guest-facing path.
Unified into one interaction, matching the color language asked for:
- Removable ingredients start **green** ("included by default"); tapping
  excludes them (greys out, strikethrough — unchanged).
- Extra ingredients start **blue** ("available, not included"); tapping
  adds them, turning **green** — the same "this is on your order" color a
  kept removable ingredient has. Tapping again reverts to blue.
- One consistent rule: green always means "on your order," for both kinds.

Backend needed a real second parallel field to support this — a cart line's
identity now depends on *two* customization sets, not one:
`models.OrderItem.Extras []string` alongside the existing `Excluded`, a new
`order_items.extra_ingredients` column (same JSON-snapshot convention),
`cart.Store.Add`/`Remove` now key a line by `(productID, excluded set,
extras set)` — `sameExclusions` renamed `sameSet` and reused for both.
`validCustomization` (cart.go) replaced the removable-only validator,
checking submitted `excluded`/`extras` names against the product's actual
ingredients of each specific kind before trusting either. Extras are free
(no price change) — nothing asked for pricing them, so nothing invents it.
`_guest_product_list.html`'s "Customize" link now shows whenever a product
has *any* ingredient tag (`HasCustomizableIngredient`, was removable-only).
Extras display everywhere exclusions already did (worker feed, guest's
placed-orders list, cart summary) — same treatment, blue-tinted instead of
muted.

Verified end-to-end: a product tagged with both a removable and an extra
ingredient showed both chips correctly colored; adding "no onion", "+extra
cheese", and "no onion +extra cheese" of the same product produced three
genuinely distinct cart lines (not merged); all three exclusion/extra sets
persisted correctly through checkout and displayed correctly to both the
kitchen and the guest.

**Investigated "all orders seem to be served by admin"** — tested
exhaustively (waiter marks served → `served_by = "waiter"`; manager marks
served → `"manager"`; admin marks served → `"admin"`), and the attribution
is correct in every case: `WorkerUpdateOrderStatus` always records
whichever account's session actually performed the action, with no
special-casing anywhere that could override it. **This is not a bug.** The
likely explanation: testing everything through a single `admin` login,
which — as the superuser bypass — legitimately *can* act on any order from
any department, so of course it gets recorded as who did it. To see
per-role attribution, use the actual seeded department accounts (see
below) instead of doing every step through `admin`.

### Fixed: guest "Customize" did nothing (real bug) + 5-department restructure + polish
**Root cause of the customize bug, found and fixed**: `host_menu.html`'s
click handler for the menu grid was bound directly to `#product-list` via
`document.getElementById('product-list').addEventListener(...)`. But
`#product-list` gets fully replaced (`hx-swap-oob="true"` defaults to
*outerHTML*, a brand-new DOM node) by `/menu/ws`'s very first push, which
arrives almost immediately after page load — orphaning that listener before
a guest could realistically click anything. Every *other* listener in this
app already correctly delegates from `document`/`document.body` (admin
dashboard, worker dashboard) — this was the one place that didn't. Fixed by
switching to body-delegated listeners (`e.target.closest('#product-list
.item-card')`), and converted the `#cart-summary` listeners the same way
too even though they weren't actually broken (that div is only ever
innerHTML-swapped, never OOB-replaced) — for defense-in-depth/consistency,
so a future change to how it's updated can't silently reintroduce this
exact bug. **General lesson for this codebase**: any custom (non-htmx)
`addEventListener` must delegate from a stable ancestor, never bind
directly to an element that any websocket topic OOB-targets — worth
checking first whenever a client-side interaction "does nothing" on a
live-updated page.

**Department model restructured** — the old single "staff" department
conflated two different jobs. Now five, matching what was asked for:
`superworker` (renamed from "staff" — manages/sees literally everything),
`waiter` (new — sees *only* orders marked `ready` by kitchen/bar, any
category; never sees pending/preparing at all, that's not their job),
`bar`/`kitchen` (unchanged: category-filtered, pending/preparing only,
drops out once marked ready), `dj` (unchanged). New `db.GetReadyOrders()`
for the waiter's cross-category ready-only feed;
`orderFeedBucket()`/`renderOrderFeedHTML()`/`BroadcastOrderFeed()` in
worker.go now switch on four order-visible buckets instead of two. Verified
the exact handoff end-to-end with the new seed accounts: kitchen marks an
order ready → vanishes from kitchen's own feed → waiter's feed (which
showed nothing while it was pending/preparing) now shows it with a READY
badge → waiter marks served → gone everywhere, `served_by` correctly
recorded as the waiter.

**One seed account per department**, replacing the old generic
admin/staff pair: `admin`/`admin123` (role=admin), `manager`/`manager123`
(worker, superworker), `waiter`/`waiter123`, `bar`/`bar123`,
`kitchen`/`kitchen123`, `dj`/`dj123` — all in `seedDefaultUsers` (db.go),
one place, so `scripts/reset-db.sh`'s success message no longer hardcodes
a second (and now provably driftable) copy of the account list — it just
points at the server's own startup log line instead.

**Fixed: seeded products all landed in "Other."** The seed INSERT never
set a category, silently relying on the column default — Mojito/Heineken
now seed as Drinks, Cheeseburger/Club Sandwich as Food.

**Polish**: the admin ingredient chips' delete `×` was `color:inherit`
(picking up the pill's own green/blue), not red — fixed to
`var(--danger)`, and moved off inline styles onto real CSS classes
(`.ingredient-tag`, `.ingredient-tag-remove`, `.ingredient-add-form` in
admin_dashboard.html) since the inline-styled version I'd bolted on
in a hurry looked visibly cramped next to the rest of the polished
dashboard. Also caught a real inconsistency while looking for "remove
should be red": the table grid's "Remove" (permanent delete) was styled as
neutral gray while "Force release" (a milder, reversible action) was
already red — backwards. Both are `.btn-outline-danger` now.

### Guest UI rebuild, ingredient customization, order cancellation/refunds, ready→served handoff
The user supplied a new dark-themed guest ordering page design and asked for
it to be fully wired up, plus several backend-only asks bundled into the
same message. In order of how much backend they actually needed:

**Ingredient customization — a real, new data model.** Guests can now
exclude default ingredients per line (e.g. "Cheeseburger, no onion"), and a
different exclusion set is a genuinely separate cart line, not a quantity
bump on the same one:
- New `product_ingredients` table (`id, product_id, name, kind`) — `kind`
  is `"removable"` (default-included, guest can exclude — the only kind
  the guest UI acts on) or `"extra"` (admin can tag these too, but no
  guest-facing add-on/pricing flow exists yet — storing the data model
  now means that's a template change later, not a schema one).
- `cart.Store.Add`/`Remove` now key a line by `(productID, sorted excluded
  set)`, not just `productID` — `sameExclusions()` does the order-
  independent comparison. `models.OrderItem` gained `Excluded []string`,
  persisted through checkout as a JSON snapshot in a new
  `order_items.excluded_ingredients` column (same "immutable at order
  time" reasoning as the existing name/price snapshot).
- Never trust client-supplied ingredient names: `validRemovableExcluded`
  (cart.go) filters whatever the guest's request claims down to that
  product's actual removable ingredients before it ever reaches the cart.
- Admin manages ingredients inline per product row (`_product_list.html`)
  — add via a small form, remove via a `×` on each chip
  (`AdminAddIngredient`/`AdminDeleteIngredient`, admin.go).
- `db.GetIngredientsByProductIDs` batch-fetches for *all* products in one
  query — both the guest menu and admin product list render N products
  without N+1 ingredient queries, deliberately, given the performance
  complaint elsewhere in the same message.

**Order cancellation + refunds, guest-side.** Workers already had
cancellation (see the previous entry below); guests didn't.
`GuestCancelOrder` (guest_orders.go) — unpaid orders cancel instantly
(nothing to reverse); already-paid orders can't self-cancel (no payment
gateway exists to reverse a charge through) — instead `db.RequestRefund`
flags the order (`refund_requested`), which surfaces immediately to staff
(a badge on their order card) and to the guest ("refund requested, ask
staff"). Staff complete the actual cancellation themselves once they've
handled the cash refund in person — this is the "if paid, a refund option
should appear" ask, implemented as a flag-and-notify flow rather than any
automated money movement, since none exists in this app.

**Fixed a real gap my own earlier change had introduced: kitchen/bar
marking something done made it vanish for everyone, including the waiter
who needed to go deliver it.** The previous session's "served = terminal,
drops from every feed" design didn't distinguish "kitchen is done cooking"
from "a waiter delivered it to the table" — those are different people's
jobs. Added a `ready` status between `preparing` and `served`
(`OrderStatusFlow` is now pending→preparing→ready→served→completed).
Department-scoped feeds now have two different exclusion sets instead of
one shared one (orders.go): `GetActiveOrdersByCategory` (bar/kitchen) stops
showing an order once it's `ready` — their job's done — while
`GetActiveOrders` (staff/admin, unfiltered) keeps showing it through
`ready`, which is the actual "pop up to the waiter" behavior asked for.
Both fall out of the *same* generic `NextOrderStatus`/button mechanism
already in place — kitchen's card says "Mark ready", staff's card for that
same order (once it reaches them) says "Mark served", no new per-role
logic needed ("staff-worker is all in one, no logic changes" held).
Verified this exact handoff end-to-end: kitchen marks ready → order
disappears from kitchen's own feed → still present in staff's feed with a
READY badge → guest sees "Ready — on its way!" live → staff marks served →
gone from every feed, `served_by` recorded. Also tightened
`CancelOrder`: a `ready` order can no longer be cancelled (food's already
made) — previously only served/completed/cancelled were blocked.

**Guest page**: `host_menu.html` and its fragments (`_guest_product_list.html`,
`_cart_summary.html`, new `_placed_orders.html`) rebuilt to the supplied
design, wired to real endpoints instead of the mockup's fully-client-side
fake cart. Notable wiring decisions:
- The mockup's cart was a plain JS array; this app's cart is server-side
  (`cart.Store`), so every interaction (customize-and-add, line +/-,
  payment method, note) had to become a real request. Per-line +/- and the
  customize panel's "Add to order" needed a specific ingredient-set
  targeted, which htmx's declarative attributes can't express dynamically
  — `submitCartAction()` builds a throwaway `<form>` with repeated
  `excluded` hidden inputs and fires it via `htmx.ajax(..., {source: form})`,
  which serializes same-name fields as repeated params the same way a
  native multi-checkbox form submission would (verified server-side via
  `r.Form["excluded"]`).
  the payment-method chips / note field live in a different part of the DOM
  than the sticky checkout button — `hx-include` pulls them in by selector
  rather than needing a shared enclosing form.
- New "Your orders" section replaces the old single-order `#order-status`
  widget entirely with a full list (`db.GetOrdersBySession`,
  `renderPlacedOrdersHTML`/`BroadcastPlacedOrders`) — every order this
  guest session placed, live-updated, each with its own cancel/refund
  button. The old widget's code (`renderOrderStatusHTML`, `OrderStatusPoll`)
  was left in place, unused, matching this project's established "don't
  rip out working code just because the UI moved on" pattern — it's
  harmless dead code, not a regression risk.
- Card payment has no real processor (matches the mockup's own "Demo"
  label) — selecting it just marks the order paid immediately at creation
  time (`models.PaymentMethodCard`), simulating an instant charge, instead
  of collected in person like cash.
- DJ section kept its already-working backend (`/dj/request`, live
  `#song-status`) — only the markup/CSS changed to match the new theme.

Tested the whole thing end-to-end against the running server: customized
add-to-cart (excluding an ingredient) produced a separate cart line from a
same-product-different-exclusion add, correctly did *not* merge with a
plain add of the same product; per-line increment matched the exact
customization variant, not just the product id; checkout correctly
persisted the note, the per-line exclusions, and set card orders to
`paid` immediately; the full ready/served handoff (above); guest self-
cancel on an unpaid order; refund-request flow on a paid one; and that a
`ready` order correctly rejects a cancel attempt (409).

### Live updates over WebSockets (replaced polling)
Previously the worker dashboard and guest table page re-fetched HTML fragments
on a timer (`hx-trigger="every 3s"` / `every 5s"`). That's gone — the server now
pushes updates the instant something changes.

- `internal/wshub/hub.go` — minimal topic-based pub/sub hub (`Subscribe`,
  `Unsubscribe`, `Broadcast`), one buffered channel per subscriber.
- `internal/handlers/ws.go` — websocket upgrade handlers (`WorkerOrdersWS`,
  `WorkerDJWS`, `TableStatusWS`), `oobWrap()` helper that wraps a fragment as
  an `hx-swap-oob` div for the htmx `ws` extension, `pumpWS()` (sends an
  initial snapshot on connect, then relays broadcasts until disconnect).
- Mutation handlers (`worker.go`, `table.go`, `cart.go`, `dj.go`) now broadcast
  to the relevant topic after every DB write — new order, status change,
  paid, DJ accept/reject, DJ request.
- Routes: `GET /table/{number}/ws`, `GET /worker/orders/ws`, `GET /worker/dj/ws`
  (the last two behind the existing `RequireRole` middleware, so auth is
  enforced on the socket handshake same as any other worker route).
- Templates (`worker_dashboard.html`, `host_menu.html`) use
  `hx-ext="ws" ws-connect="..."` instead of polling `hx-trigger`s.
- Old plain-HTTP fragment routes (`/worker/orders/feed`, `/worker/dj/feed`,
  `/table/{number}/order-status`) were **kept**, just no longer called by the
  UI — harmless to leave, see "Maybe later" below.
- Verified end-to-end with a throwaway Go websocket client: initial snapshot
  on connect, live push on mutation, and unauthenticated worker socket
  connections correctly rejected (303, not upgraded).
- Added dependency: `github.com/gorilla/websocket`.

### DB reset script (manual, not wired into anything)
- `scripts/reset-db.sh` — deletes `bitebox.db` (+ `-wal`/`-shm`/`-journal`
  siblings) and recreates it via `cmd/resetdb`. Prompts for confirmation;
  pass `-y` to skip. Run it yourself with `./scripts/reset-db.sh` — nothing
  in the app calls it automatically.
- `cmd/resetdb/main.go` — just calls `db.InitDB()`, reusing the server's own
  schema/seed code instead of duplicating SQL in the shell script.
- Reseeds: table 1 (`available`), 4 menu products, users `admin/admin123`
  and `staff/staff123`.

### Fixed: couldn't log in / tables stuck occupied
Root cause was operational, not a code bug: a leftover `go run` server
process from earlier testing was still holding port 8080 with a stale
sqlite file handle (from before a manual db delete+recreate). Killed it;
fresh server + fresh db logged in fine. Real behavior worth remembering:
- Table occupancy is persisted in `bitebox.db`, **on purpose** — a server
  restart alone does not free tables. Use `scripts/reset-db.sh` if you want
  a clean slate.
- If the app ever behaves inconsistently after a restart, check
  `ss -ltnp | grep 8080` for a stray process before assuming a code bug.

### Live menu sync (price/hide changes reach guests, even mid-cart)
- New global `"menu"` websocket topic/route `GET /menu/ws` (`internal/handlers/menu.go`,
  `MenuWS` in `ws.go`) — pushes the guest product list the instant an admin
  creates/edits/hides a product (`AdminCreateProduct`/`AdminUpdateProduct`/
  `AdminToggleProduct` in `admin.go` all call `BroadcastMenu()`).
- **The cart-refresh nudge**: the server doesn't track who has what in their
  cart, so it can't render each guest's cart-summary directly. Instead every
  menu broadcast also ships a tiny OOB "poke" (`cartRefreshTrigger` in
  `menu.go`) — a hidden `#cart-refresh-trigger` div that, once swapped in,
  fires its own `hx-get="/cart/summary"` to refresh `#cart-summary` from that
  guest's own session. Verified this reaches the socket correctly; the actual
  client-side htmx auto-fire wasn't checked in a real browser (see Maybe later).
- `GET /cart/summary` (`CartHandlers.Summary` in `cart.go`) — renders the
  caller's cart fresh from their session cookie; used both for that nudge and
  for the page's own initial load (`host_menu.html`'s `#cart-summary` div now
  starts empty and `hx-get`s this on load, same "start empty, get filled"
  pattern as the other live widgets).
- **Cart items now resolve against live product data on every render**
  (`resolveCart()` in `cart.go`), not the add-time snapshot — so a price
  change or hide shows up immediately, and an item that's gone unavailable
  is flagged (`Unavailable`), excluded from the total, and blocks checkout
  (409) until removed. `host_menu.html`'s own inline product/cart markup was
  removed in favor of this — it's the only rendering path now.

### DJ decision shown live to the guest; payment only implied on accept
- `song_requests` gained a `table_number` column (migration in `db.go`) so a
  request can be traced back to the table that sent it — it had no guest
  linkage at all before this.
- `DJRequest` (`dj.go`) now requires the caller to be hosting a table
  (via the same `resolveHostedTable` cart.go already used) and stores it.
- New `#song-status` widget on `host_menu.html`, live via the same
  `/table/{number}/ws` connection order-status already uses (`renderSongStatusHTML`/
  `BroadcastSongStatus` in `table.go`, `_song_status.html`) — pending →
  "waiting for the DJ"; accepted → "pay €X cash to the DJ/waiter"; rejected →
  "declined, no charge". `WorkerDJAccept`/`WorkerDJReject` in `worker.go`
  broadcast the decision the moment staff acts.
- No real payment gateway exists in this app (everything is "tell the guest
  what they owe, staff collects cash") — "money sent only if accepted" is
  implemented as messaging, not a new payment/mark-paid flow. If a "DJ mark
  tip paid" workflow (like orders have) turns out to be wanted, that's a
  separate ask — flagged in Maybe later.
- Worker DJ feed (`_dj_feed.html`) now also shows which table sent each
  request, using the new column.

### Stock tracking
- The `products.stock` column already existed (schema + `models.Product`)
  but was never surfaced or enforced anywhere. Convention: `-1` = unlimited/
  untracked (the column default), `>=0` = a real count.
- Admin can now set/edit it (`admin_dashboard.html` / `_product_list.html`
  stock input, `db.CreateProduct`/`UpdateProduct` take a `stock` param).
- Guest menu shows "Only N left" (≤5) or "Sold Out" (`_guest_product_list.html`);
  the Add button is disabled at 0.
- `CartHandlers.Add` soft-checks stock vs. what's already in that cart before
  allowing another add (409 if it would exceed stock).
- The **hard** guarantee is in `db.CreateOrder` (`orders.go`): each item's
  stock is decremented atomically inside the order transaction
  (`UPDATE ... WHERE stock = -1 OR stock >= ?`); if two guests race for the
  last unit, the loser's whole order rolls back with `ErrInsufficientStock`
  (surfaced to that guest as a 409 asking them to review their cart).
- Checkout also calls `BroadcastMenu()` on success, so a sellout is reflected
  live for everyone else browsing, not just the buyer.

Verified all three of the above end-to-end with the same throwaway websocket
client approach as the earlier polling→websocket work: admin creating a
stock=1 product pushed live to a connected menu socket; adding it to a cart
then hiding it server-side flagged it unavailable and blocked checkout;
submitting/accepting/rejecting a DJ request pushed the right message to the
right table; buying the last unit pushed "Sold Out" live and a second add
attempt correctly 409'd.

### Fixed: real performance bug ("menu takes forever to load")
Root cause, confirmed: `sql.Open("sqlite3", "./bitebox.db")` had zero tuning —
no WAL mode, no busy_timeout. `database/sql`'s connection pool opens several
concurrent connections to the same SQLite file by default; SQLite's default
rollback-journal mode takes an exclusive lock per write and fsyncs
synchronously on every transaction. This app had grown steadily more
concurrent background DB activity over the session (every websocket
broadcast re-queries, a 5s liveness check per open staff socket) — with no
busy_timeout, that's lock contention, and the fsync-per-write pattern is
especially slow under WSL2's virtualized filesystem. This was a real,
diagnosable bug, not a "just add caching" situation. Fixed with one change:
`sql.Open("sqlite3", "./bitebox.db?_journal_mode=WAL&_busy_timeout=5000")`
(db.go) — WAL allows concurrent readers alongside one writer and
checkpoints instead of fsyncing every write. Verified: 20 concurrent table-
view loads while admin/worker sockets were also active completed in ~16ms
total with zero "database is locked" errors in the log (grepped for
"locked"/"busy"/"timeout" across a full test session — none). If load ever
gets heavy enough that this stops being enough, the next lever is
`SetMaxOpenConns`, not re-diagnosing from scratch.

### Waiter UI rebuild (reused across staff/bar/kitchen), order cancellation, served-by attribution, login reskin
The user supplied a new dark-themed order-card design ("Nikos" mockup) for
the worker dashboard and asked for it to be made fully functional, reused
as-is across every non-DJ department (each still scoped to its own existing
permission — no new access-control logic needed there, already correct from
the department work), plus a few backend pieces the new UI implied.

**Backend additions actually needed** (kept deliberately minimal — a lot of
the pasted JS, e.g. the elapsed-time urgency color gradient and the periodic
re-tick, is pure client-side cosmetics that never touches the server, per
the user's own instruction to keep that logic front-end-only):
- **Order cancellation is new** — the old app had no way to void an order.
  `db.CancelOrder(orderID)` (orders.go): one transaction that rejects
  (`ErrCannotCancel`) if the order's already served/completed/cancelled,
  otherwise restores each item's stock (mirroring `CreateOrder`'s decrement,
  same `stock = -1` unlimited sentinel respected) and sets
  `status = 'cancelled'`. New `models.OrderStatusCancelled` — deliberately
  *not* part of `OrderStatusFlow`'s forward progression (it's a branch, not
  a step), and deliberately not reachable through the generic
  `/worker/orders/{id}/status` endpoint — only through the dedicated
  `/worker/orders/{id}/cancel` route, so stock restoration can never be
  bypassed by posting the status directly.
- **The new UI only exposes two lifecycle buttons** (Start preparing, Mark
  served) — no third "Mark Completed" step. Rather than inventing a status
  value nothing sets, `GetActiveOrders`/`GetActiveOrdersByCategory` now
  exclude `served`/`completed`/`cancelled` (previously just `completed`),
  so an order simply drops out of the live feed the moment it's marked
  served — verified this end-to-end, no extra transition needed.
- **Served-by attribution**: new `orders.served_by` column
  (`db.SetServedBy`), set from `WorkerUpdateOrderStatus` whenever the target
  status is `served`, using the acting user's own username via
  `UserFromContext`. Shown in the admin order-history panel and the CSV
  export.
- **"Undo" on the toast is a real request, not a fake local revert** — since
  every other connected client already sees the real state the instant the
  original action's request succeeded (via the existing broadcast
  mechanism), a client-side-only undo would leave that one browser tab
  disagreeing with everyone else's live view. So: undoing "Start preparing"/
  "Mark served" re-posts to the *same* `/status` endpoint with the
  order's pre-click status (`WorkerUpdateOrderStatus` already accepted any
  valid status, not just the next one in sequence — no backend change
  needed there). Undoing "Mark paid" needed a genuinely new endpoint though:
  `WorkerMarkUnpaid` (`/worker/orders/{id}/unpaid`), the missing symmetric
  half of the existing `WorkerMarkPaid`.
- Today/week/month order counts (`GetStatsForPeriod`) now exclude cancelled
  orders — a cancelled order never actually happened, shouldn't count.

**Frontend**: `_order_feed.html` rewritten to the new card markup, fed real
data (`data-ordered-at="{{ .CreatedAt.UnixMilli }}"`, `data-order-id`) —
the pasted JS's demo `seedMinutesAgo` fakery was dropped in favor of that
real timestamp. `worker_dashboard.html` rebuilt around it: real username/
department in the topbar (`departmentLabel` map in worker.go), the DJ
terminal restyled to match the same visual language (no DJ-specific mockup
was given, so this is my own extension of the established component
classes — `_dj_feed.html` reuses `.order-card`), and the toast/undo/urgency-
timer JS re-triggered on `htmx:afterSwap`/`htmx:oobAfterSwap` — the pasted
version only ran once on page load, but here the feed's content gets
wholesale-replaced by live pushes from *anyone's* action, not just this
tab's own clicks, so the client-side enhancements need to re-apply after
every swap, not just once.

**Bug caught while testing**: the new `_order_feed.html` ranged over `.`
directly, but `renderOrderFeedHTML` (unchanged) wraps orders in
`{"Orders": ...}` like every other fragment template in this app — a
data-shape mismatch that made every order-feed request 500. Caught via a
disposable throwaway `cmd/` test program that called the real render
pipeline directly (template.Execute surfaces "can't evaluate field ID in
type interface {}" clearly) rather than eyeballing the diff — worth
remembering as a fast way to isolate a template/data mismatch instead of
guessing from the live server's stack-trace-free 500.

**Login page**: `login.html` reskinned to the same dark theme — CSS only,
the form's method/action/field names are byte-for-byte unchanged, so no
handler-side risk.

### Live stock reservations, live table grid, and live kick-on-deactivate
Three more fixes/features in the same session as the reconnect-storm fix,
all following the same "nothing needs a refresh" thread:

**Stock now visibly updates the instant someone adds to their cart** — not
just at checkout. Previously `products.stock` was only ever decremented at
checkout (correctly, atomically); adding to a cart just did a soft
same-session check. Now:
- `cart.Store.ReservedQuantity(productID)` (cart.go) sums a product's
  quantity across *every* active cart, not just one guest's own.
- A new display-only `productView.AvailableStock` (`admin.go`) = raw DB
  stock minus that reservation total (floored at 0; -1/unlimited passes
  through unchanged). This is computed at render time, never written back —
  the raw `Stock` field is untouched, specifically so the admin dashboard's
  editable stock stepper/Save action can never accidentally overwrite real
  inventory with a reservation-adjusted number. The admin menu row shows
  both: the editable raw count, and a small "N in carts right now" note
  alongside it.
- `CartHandlers.Add`'s stock check now uses the cross-session reserved
  total (previously it only checked the calling guest's own cart, so two
  guests could each "successfully" grab the last unit at the same time).
- Both the guest-facing menu (`/menu/ws`) and — new — the admin dashboard's
  own menu list (`/admin/menu/ws`, `BroadcastAdminMenu`/`BroadcastAllMenus`)
  push on every cart add/remove/checkout, not just admin-side edits.

**Admin table grid is now live** (`/admin/tables/ws`, `BroadcastAdminTables`)
— previously only fetched once on page load. Broadcasts on every direction
a table's state can change: a guest claiming it (`TableHandlers.View`),
leaving it (`TableHandlers.Leave`), or an admin creating/force-releasing/
removing one. Force-release also now clears the kicked guest's cart
(`CartStore.Clear`) — otherwise their reservation would sit there forever
with no other path to release it, since they've been kicked and can't
remove it themselves.

**Fixed: deactivating a worker didn't kick out an already-open tab.**
Deactivation already killed the session server-side and blocked new
logins (see the earlier fix in this file), but a worker with the dashboard
*already open* kept receiving live updates indefinitely — nothing
re-checked their session once the websocket was established. Fixed with a
5-second periodic re-check inside `pumpWS` (ws.go) for every staff-
authenticated socket (order-feed, dj-feed, admin stats/menu/tables): the
moment `db.GetUserBySessionID` fails, the connection is closed with a
custom WebSocket close code (4001), and a small static script in both
`worker_dashboard.html` and `admin_dashboard.html` listens for
`htmx:wsClose` with that code and redirects to `/login`. Verified
end-to-end with a raw websocket client: deactivating a logged-in worker
mid-connection closes their socket with code 4001 within ~5s.

A new package-level `handlers.CartStore` (ws.go, set once in main.go,
same pattern as `Hub`) gives admin.go/menu.go read/clear access to the
shared cart store without restructuring every admin handler into a
`*_ struct` method.

### Fixed: admin stats websocket reconnect storm
`_admin_stats.html` had `hx-ext="ws" ws-connect="..."` on the *same* div
that the websocket's own broadcasts OOB-replace. Every push (including the
connection's own initial snapshot) re-inserted that `ws-connect` attribute,
so htmx tore down and reopened the connection on every single message —
which itself immediately received an initial snapshot containing
`ws-connect` again, an infinite self-sustaining loop (visible in the access
log as dozens of `admin/stats/ws` requests per second, status 000). Fixed
by splitting it into two nested divs: an outer `#admin-stats-socket` that
owns `hx-ext="ws" ws-connect="...?period=X"` and is *only* ever touched by
the period-tab outerHTML swap, and an inner `#admin-stats` (a `{{define}}`
block in the same template file) that's the only thing sent over the
websocket itself, with no `ws-connect` in it at all
(`renderAdminStatsInnerHTML` vs. `renderAdminStatsHTML` in stats.go).
**General rule worth remembering for any future htmx-ws work in this app:
never let a websocket's own broadcast content re-include the `ws-connect`
element it's being delivered through** — every other live widget in this
app (order-feed, dj-feed, product-list, order-status, song-status) already
followed this correctly by keeping the `ws-connect` wrapper *outside* the
OOB-swapped id; admin stats was the one place that got collapsed into a
single div, because period-switching needed the connection itself to be
reconnectable.

### Fixed: "Today's Overview" revenue/order counter not updating
Two separate problems, both fixed:
1. **The numbers were never live** — `AdminHome` computed them once at page
   load and nothing pushed changes after that. Fixed the same way as
   everything else in this app: new `"admin:stats"` websocket topic/route
   `GET /admin/stats/ws` (`internal/handlers/stats.go`, `AdminStatsWS` in
   `ws.go`), admin dashboard's stat cards wrapped in `hx-ext="ws"
   ws-connect="/admin/stats/ws"` with `id="admin-stats"`. `BroadcastAdminStats()`
   is called from the two places that actually change either number:
   `cart.Checkout` (order count) and `WorkerMarkPaid` (revenue, since only
   paid orders count).
2. **The bigger bug**: `GetTodayRevenue`/`GetTodayOrderCount` (`db/orders.go`)
   compared `date(created_at)` — a bare UTC date, since SQLite's
   `CURRENT_TIMESTAMP` default is UTC — against `date('now', 'localtime')`, a
   local date. On this machine (UTC+3) that meant orders placed between local
   midnight and ~3am silently didn't count as "today" at all. Fixed by
   applying `'localtime'` to *both* sides of the comparison. This was
   probably the real cause of "not updating" — worth remembering if numbers
   ever look wrong again: check for exactly this kind of bare-vs-localtime
   date mismatch before assuming it's a caching/live-update problem.
   `admin_dashboard.html`'s order-history timestamps now also render via
   `.CreatedAt.Local` for the same reason (they were displaying UTC).

### Order history (all-time, not just today)
- Every order was already fully persisted (`orders`/`order_items` tables) —
  nothing needed to change there. Added `db.GetOrderHistory(limit)`
  (`orders.go`) — all orders regardless of status, newest first, items
  populated, capped at `orderHistoryLimit = 200` (a sane bound, not real
  pagination — see Maybe later).
- New "📜 Order History" section in `admin_dashboard.html`, inside a
  `.scroll-panel` (`max-height: 420px; overflow-y: auto`) so it doesn't turn
  the whole admin page into an endless scroll — the page stays fixed-height,
  only that panel scrolls. Shows table #, order #, local timestamp, full item
  list, status badges, and total per order.
- Deliberately **not** live-updated over websocket (unlike everything else
  in this app) — it's a look-back view, not something that needs to change
  under the admin's cursor. Reflects the state as of the last time `/admin`
  was loaded/refreshed.

### Admin dashboard rebuild — dark theme, departments, staff, real analytics
Full visual + functional rebuild of `/admin` to a design the user provided
(dark "Ouzeri Marina" theme, Manrope/IBM Plex Mono). Every element that
looked interactive in the mockup is now wired to real data — nothing is
decorative-only except the "Live" pill and venue name (hardcoded, no
settings system exists to back either).

**Departments — real permissions, not labels.** This was the one explicit
scope decision: worker accounts now have a `department` (`staff`/`bar`/
`kitchen`/`dj`), and it actually restricts access, not just display:
- `models.DepartmentCategory` maps `bar`→Drinks, `kitchen`→Food, others→
  unfiltered. `db.GetActiveOrdersByCategory` (orders.go) returns whole
  orders (all items, for context) that contain at least one item in that
  category — category is read live off `products.category` via join, not a
  historical snapshot.
- `RequireDepartment(...)` (middleware.go) gates routes; admins bypass it
  entirely. `/worker/orders/*` requires staff/bar/kitchen (not dj);
  `/worker/dj/*` requires dj only. Enforced at the route level, not just
  hidden in the UI — verified a bar-department session gets a 403 hitting
  `/worker/dj/feed` directly, and vice versa.
- The order-feed websocket got a 3rd axis: `topicWorkerOrders(bucket)` where
  bucket is `staff`/`bar`/`kitchen`. `BroadcastOrderFeed()` (worker.go)
  renders and pushes to all three on every order change, since one change
  can affect the unfiltered view and a filtered one at once.
- `worker_dashboard.html` shows/hides the order feed and DJ terminal
  sections per department (`ShowOrders`/`ShowDJ` computed in `WorkerHome`).
  Not visually redesigned — only the admin dashboard was in scope for the
  new look.

**Staff & access panel** (new — `internal/handlers/staff.go`,
`templates/_staff_list.html`):
- `users` gained `department`, `is_active`, `created_at`, `last_seen_at`
  (migration in db.go). `RequireRole` now calls `db.TouchLastSeen` on every
  authenticated request, driving "Active now / Active Xm ago / Offline ·
  Xd ago" (`computeStaffStatus`).
- Create account: one dropdown combining role+department (matching the
  mockup's single select) — `admin`, or a department value that implies
  `role=worker`.
- Deactivate kills access **immediately**: deletes all the account's
  `auth_sessions` rows *and* blocks future logins. Initially only did the
  session-kill — testing caught that `LoginSubmit` never checked
  `is_active`, so a deactivated account could just log back in with a new
  session. Fixed in auth.go. Worth remembering: any future auth-adjacent
  change should re-check both the "kill existing access" and "block new
  access" halves independently, they're not the same check.
- Self-deactivation is blocked (`AdminDeactivateStaff` compares against the
  caller's own id via `UserFromContext`).

**Product categories & stock, in the new menu UI:**
- `products.category` (Drinks/Food/Other, migration in db.go). Admin sets
  it via the add-product form and each row's hidden field (see below).
- The product row's only *visible* editable control is the stock stepper
  (+/- adjust a number input client-side, "Save" persists it) — matches the
  mockup exactly, which doesn't expose name/price editing on the row at
  all. "Save" still POSTs the full existing `/admin/products/{id}` update
  (name/price/category carried as hidden inputs, unchanged) — no new
  endpoint, reused what already existed.
- Low-stock alert banner: real, computed in Go (`findLowStockProduct`,
  admin.go) — picks the tightest available, stock-tracked (not unlimited)
  product at 1-5 units. Hidden entirely if nothing qualifies (no fake
  placeholder alert).

**Real revenue analytics** (previously only "today" existed):
- `db.GetStatsForPeriod("today"|"week"|"month")` (orders.go) — week is a
  rolling 7-day window (not calendar week, to dodge Monday-vs-Sunday
  ambiguity), month is the current calendar month. Avg order = revenue ÷
  *paid* order count, matching the KPI card's own "Per paid order" caption.
- Period tabs are a genuinely nice htmx trick worth remembering: the
  `#admin-stats` div carries its own `hx-ext="ws" ws-connect="...?period=X"`
  baked in, and clicking a tab does an **outerHTML** swap of that whole div
  (fresh markup, fresh `ws-connect` URL) — this tears down the old
  websocket and opens a new one scoped to the new period automatically. No
  manual reconnect logic needed. `BroadcastAdminStats()` pushes to all
  three period topics on every stats-affecting write, since e.g. marking an
  order paid can shift today/week/month simultaneously.
- 7-day revenue trend bar chart (`db.GetRevenueTrend`) and a week-over-week
  delta (`db.GetRevenueForRange`) — both real, computed at page load (not
  live-pushed; a slow-moving 7-day aggregate doesn't need sub-second
  freshness the way "today" does).
- CSV export (`GET /admin/export.csv`) — all-time, uncapped
  (`GetOrderHistory(-1)`; SQLite's `LIMIT -1` means unlimited).

**Reused, not rebuilt:** htmx's "start empty, `hx-get` on `{load}`, `{swap
into a static div}`" pattern (already established for cart-summary,
order-status, etc.) now also drives the menu list, table grid, staff list,
and stats block — each got a small dedicated `GET` fragment endpoint
(`AdminProductList`, `AdminTableList`, `AdminStaffList`, `AdminStatsPeriod`)
instead of duplicating render logic between the full-page and fragment
templates.

## Missing / rough edges

- **No pagination on "Your orders."** Capped at the most recent 20 per
  session (`placedOrdersLimit`) — fine for a single table visit, would
  need real pagination if guests ever accumulate more than that in one
  sitting (unlikely, but noted). Order *history* (the admin panel) got
  real pagination this session — this is specifically the guest-facing
  per-session list, which didn't.
- **Toast "Undo" targets an order id, not a DOM position** — correct even
  if the feed re-renders from someone else's action between your click and
  hitting Undo, but the toast's *wording* was written at click time, so in
  that window it could theoretically describe a table/order that's since
  changed further. Narrow edge case at this app's traffic level, not
  something to over-build for now.
- **Default seeded passwords** (`admin123` etc.) — intentional, documented
  dev/demo convenience, not an oversight. See `SECURITY_AUDIT.md`'s SEC-09
  (tagged `DEV_ONLY - CONVENIENCE BYPASS`) for the full reasoning on why
  this stays as-is rather than getting a fake-fix. Must change before any
  real deployment.
- **Sequential, guessable `/table/{n}` URLs** (`SECURITY_AUDIT.md`'s
  SEC-13) — rate-limited (20 new claims/IP/10min) but not actually closed;
  closing it for real means per-table secret tokens, which means
  reprinting every physical QR code at a live venue. Asked, user chose to
  keep the mitigation rather than take on that reprint — this is the
  deliberate final state, not a gap waiting on a decision.
- **DJ song-status only tracks the latest request per table.** If a guest
  sends a second request before the first is resolved, the widget switches
  to showing the new one; the first's outcome is still in the DB but no
  longer surfaced to that guest.
- **No "DJ tip paid" workflow.** Unlike orders (`payment_status` + "Mark
  Paid"), an accepted song request has no staff-side way to record the tip
  as collected. Only add this if actually asked for.
- **No split-bill payment collection** — `orders.split_count` is purely a
  "divide the total by N on the receipt" display, since there's no
  per-guest payment collection in this app at all (one host session always
  pays for the whole table, same as before).

## Maybe later (not needed now, just noted)

- If this ever sits behind a reverse proxy / HTTPS, confirm the proxy passes
  through `Upgrade`/`Connection` headers for the websocket routes, and that
  the `ws://` vs `wss://` scheme htmx infers from the page matches.
