package handlers

import (
	"bytes"
	"html/template"
	"log"
	"sort"
	"time"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

// cartRefreshTrigger is a standing placeholder in host_menu.html
// (id="cart-refresh-trigger", hidden). Pushing this over the wire replaces
// it via OOB swap with an element that immediately fires its own hx-get,
// re-rendering #cart-summary from the guest's own session — the simplest
// way to keep a guest's already-added cart items in sync with admin price/
// availability changes without the server tracking who has what in their
// cart.
const cartRefreshTrigger = `<div id="cart-refresh-trigger" hx-swap-oob="true" hx-get="/cart/summary" hx-trigger="load" hx-target="#cart-summary" hx-swap="innerHTML" style="display:none;"></div>`

// guestMenu is what the guest menu shows right now: only dishes that can be
// ordered at this moment (Menus & hours, kitchen/bar switches), grouped by
// category, with Recommended / Popular rows first when switched on, and the
// notices explaining anything that isn't served. Shared by the /menu/ws
// payload and TableHandlers.View (which also needs the group names for the
// category tabs).
type guestMenuData struct {
	Groups           []productCategoryGroup
	Notices          []guestNotice
	RecommendedTitle string
	PopularTitle     string // "Popular tonight", "Popular this morning"…
}

func popularTitle(dp int) string {
	return [4]string{"Popular this morning", "Popular this afternoon", "Popular this evening", "Popular tonight"}[dp]
}

// guestMenu is the menu for a table in this area (menus can differ per area).
func guestMenu(zone string) (guestMenuData, error) { return guestMenuAt(zone, time.Now()) }

// guestMenuAt is the menu as a table in this area would see it at t.
func guestMenuAt(zone string, t time.Time) (guestMenuData, error) {
	var out guestMenuData
	products, err := db.GetProducts()
	if err != nil {
		return out, err
	}
	categories, err := db.GetCategories()
	if err != nil {
		return out, err
	}
	rules := loadMenuRulesAt(t)
	settings, _ := db.GetSettings()
	views := buildProductViews(products)
	orderable := views[:0:0]
	var later []productView // not now, but later today: shown greyed ("From 19:00")
	byID := map[int]productView{}
	for _, v := range views {
		if !rules.item(v.Product, zone).Orderable {
			if settings.ShowUpcoming {
				if next := rules.nextOrderable(v.Product, zone); !next.IsZero() {
					v.Later = "From " + next.Format("15:04")
					later = append(later, v)
				}
			}
			continue
		}
		if price, deal := rules.price(v.Product, zone); deal != "" {
			v.WasPrice, v.Price, v.Deal = v.Price, price, deal
		}
		orderable = append(orderable, v)
		if v.AvailableStock != 0 { // suggestions never lead with a sold-out dish
			byID[v.ID] = v
		}
	}
	out.Notices = rules.guestNotices(products, zone)
	out.RecommendedTitle = settings.RecommendedTitle
	// Popularity is the stored snapshot (see StartPopularityRanker), never
	// live counts, so nothing shuffles after each order. Suggestions follow
	// the clock twice over: only dishes orderable right now (a closed
	// kitchen means no food), ranked by what sells at this time of day.
	snap, _ := db.PopularitySnapshot()
	dp := models.DaypartOf(rules.now.Hour())
	out.PopularTitle = popularTitle(dp)
	if settings.SortByPopularity {
		sort.SliceStable(orderable, func(i, j int) bool { return snap[orderable[i].ID].Total() > snap[orderable[j].ID].Total() })
	}
	byNow := func(list []productView) []productView {
		out := append([]productView(nil), list...)
		sort.SliceStable(out, func(i, j int) bool { return snap[out[i].ID][dp] > snap[out[j].ID][dp] })
		return out
	}
	var top []productCategoryGroup
	if settings.ShowRecommended {
		var picks []productView
		for _, v := range orderable {
			if v.Recommended && v.AvailableStock != 0 {
				picks = append(picks, v)
			}
		}
		if len(picks) > 0 { // what sells at this hour first
			top = append(top, productCategoryGroup{Name: "Recommended", Products: byNow(picks)})
		}
	}
	if settings.ShowPopular {
		count := settings.PopularCount
		if count <= 0 {
			count = 6
		}
		inScope := func(v productView) bool {
			return settings.PopularScope == "all" || settings.PopularScope == "" || rules.dept[v.Category] == settings.PopularScope
		}
		pick := func(score func(models.Sales) int) []productView {
			var cands []productView
			for _, v := range orderable {
				if inScope(v) && v.AvailableStock != 0 && score(snap[v.ID]) > 0 {
					cands = append(cands, v)
				}
			}
			sort.SliceStable(cands, func(i, j int) bool { return score(snap[cands[i].ID]) > score(snap[cands[j].ID]) })
			if len(cands) > count {
				cands = cands[:count]
			}
			return cands
		}
		pop := pick(func(s models.Sales) int { return s[dp] })
		if len(pop) < 2 { // too little data for this time of day: all-day best sellers (still only what's orderable now)
			pop = pick(func(s models.Sales) int { return s.Total() })
			out.PopularTitle = "Popular right now"
		}
		if len(pop) >= 2 { // one "best seller" isn't a list
			top = append(top, productCategoryGroup{Name: "Popular", Products: pop})
		}
	}
	groups := groupProductsByCategory(append(orderable, later...), categoryNames(categories))
	if settings.SortByPopularity {
		// Categories too: the ones selling best at this time of day first
		// (drinks rise at night, brunch in the morning), from the same stored
		// snapshot, so the order only moves with the daypart or a re-rank.
		score := func(g productCategoryGroup, now bool) int {
			n := 0
			for _, p := range g.Products {
				if now {
					n += snap[p.ID][dp]
				} else {
					n += snap[p.ID].Total()
				}
			}
			return n
		}
		useNow := false
		for _, g := range groups {
			if score(g, true) > 0 {
				useNow = true
				break
			}
		}
		sort.SliceStable(groups, func(i, j int) bool { return score(groups[i], useNow) > score(groups[j], useNow) })
	}
	out.Groups = append(top, groups...)
	return out, nil
}

func renderGuestProductListHTMLFrom(m guestMenuData) ([]byte, error) {
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_guest_product_list.html"))
	if err := tmpl.Execute(&buf, m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderGuestProductListHTML(zone string) ([]byte, error) {
	m, err := guestMenu(zone)
	if err != nil {
		return nil, err
	}
	return renderGuestProductListHTMLFrom(m)
}

// BroadcastMenu pushes the current guest menu to every table connected over
// /menu/ws, and nudges each of their carts to refresh (see cartRefreshTrigger)
// since a price/availability/stock change can affect items already added.
func BroadcastMenu() {
	// One menu per table area (Menus & hours can differ per area), each on
	// its own topic; tables with no area share "".
	zones, _ := db.Zones()
	for _, z := range append([]string{""}, zones...) {
		b, err := renderGuestProductListHTML(z)
		if err != nil {
			continue
		}
		Hub.Broadcast(topicMenuZone(z), append(oobWrap("product-list", b), []byte(cartRefreshTrigger)...))
	}
}

// topicMenuZone is the live guest menu for tables in one area.
func topicMenuZone(zone string) string { return topicMenu + ":" + zone }

// BroadcastAllMenus pushes fresh menu content to both the guest-facing live
// menu and the admin dashboard's menu list. Anything that changes what's
// orderable needs both kept in sync — an admin edit, or a guest reserving
// stock by adding it to their cart (which shifts live availability without
// touching the products.stock column at all; only checkout does that).
func BroadcastAllMenus() {
	BroadcastMenu()
	BroadcastAdminMenu()
	BroadcastHours()
}

func BroadcastAdminMenu() {
	b, err := renderAdminProductListHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicAdminMenu, oobWrap("menuList", b))
}

// renderDJSectionHTML renders the guest-facing "Request a song to DJ" form
// section — entirely absent when the admin has disabled it (not a disabled/
// greyed-out state, genuinely not there, matching "song requests aren't
// shown to the guest at all" when a venue doesn't want them). This is only
// the submission form; the #song-status decision widget is a separate,
// always-present element (see host_menu.html) so an admin flipping this off
// mid-visit doesn't hide the outcome of a request a guest already sent.
func renderDJSectionHTML() ([]byte, error) {
	settings, err := db.GetSettings()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_dj_section.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"DJRequestsEnabled": settings.DJRequestsEnabled}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastDJSection pushes the DJ request form's current on/off state to
// every guest connected over /menu/ws (a venue-wide setting, not per-table
// — reusing the global menu topic rather than needing a new connection).
func BroadcastDJSection() {
	b, err := renderDJSectionHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicMenu, oobWrap("dj-section", b))
}

// BroadcastVenueName pushes a renamed venue to every guest already browsing
// the menu, live. Unlike the DJ section, the venue name doesn't need its own
// render-on-connect step in MenuWS's initial payload — it's already part of
// host_menu.html's own server-rendered HTML at page-load time (see
// TableHandlers.View); this broadcast only matters for a tab that's already
// open when an admin renames the venue mid-service.
//
// Built by hand rather than via oobWrap: the target element also carries
// class="name" (host_menu.html's venue-banner styling), and an OOB swap
// replaces the whole element outerHTML — reusing oobWrap here would silently
// drop that class off the live-updated element.
func BroadcastVenueName(name string) {
	frag := `<div id="venue-name" class="name" hx-swap-oob="true">` + template.HTMLEscapeString(name) + `</div>`
	Hub.Broadcast(topicMenu, []byte(frag))
}

// rerankPopularity rebuilds the snapshot if it's due (monthly, first run,
// or a real spike: see models.NeedsRerank), or always when forced. Pushes
// fresh menus when it did.
func rerankPopularity(force bool) (bool, string) {
	now := time.Now()
	settings, _ := db.GetSettings()
	days := settings.PopularDays
	if days <= 0 {
		days = 30
	}
	reason := "re-ranked by hand"
	if !force {
		snap, _ := db.PopularitySnapshot()
		week, _ := db.UnitsSince(now.AddDate(0, 0, -7))
		var due bool
		if due, reason = models.NeedsRerank(db.PopularityRankedAt(), now, days, snap, week); !due {
			return false, ""
		}
	}
	if err := db.RecomputePopularity(days, now); err != nil {
		log.Printf("popularity re-rank: %v", err)
		return false, ""
	}
	log.Printf("📈 Popularity re-ranked (%s, last %d days)", reason, days)
	BroadcastAllMenus()
	return true, reason
}

// StartPopularityRanker checks every 6 hours (and at start) whether the
// popularity snapshot is due for a re-rank.
func StartPopularityRanker() {
	go func() {
		rerankPopularity(false)
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for range t.C {
			rerankPopularity(false)
		}
	}()
}
