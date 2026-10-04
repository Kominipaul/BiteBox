/* Staff app (/worker). The server renders every list and pushes changes over
   websockets; this file only does what needs the device: tabs, timers that
   tick on the phone's clock, alerts (sound, banner, vibration, lock-screen
   push), undo toasts, sold-out search and keeping a kitchen tablet awake. */
(function () {
  "use strict";
  const B = document.body, $ = id => document.getElementById(id);
  // Runs standalone at /worker (config on <body>) and embedded in the admin
  // shell at /admin/live (config on the [data-wk] wrapper): every element
  // that only one of them has is optional below.
  const ROOT = document.querySelector("[data-wk]") || B;
  const KIND = ROOT.dataset.kind, FIRST = ROOT.dataset.first;
  const ownEvent = e => !!(e.detail && e.detail.elt && e.detail.elt.closest && e.detail.elt.closest(".wk-root"));
  const store = {
    get: k => { try { return localStorage.getItem(k); } catch (_) { return null; } },
    set: (k, v) => { try { localStorage.setItem(k, v); } catch (_) {} },
  };
  const isCoarse = matchMedia("(pointer:coarse)").matches;

  // ---------- Tabs ----------
  const panels = [...document.querySelectorAll(".wpanel")];
  const tabs = [...document.querySelectorAll(".wtab[data-tab]")];
  let stockLoaded = 0;
  // htmx wires up its elements on DOMContentLoaded; anything that fires an
  // htmx event at page start has to wait until after that.
  const ready = fn => document.readyState === "loading" ? document.addEventListener("DOMContentLoaded", fn) : fn();
  function show(key) {
    if (!panels.length) return; // Floor / Bookings pages: no panels, just the chrome
    if (!panels.some(p => p.dataset.panel === key)) key = FIRST;
    panels.forEach(p => { p.hidden = p.dataset.panel !== key; });
    tabs.forEach(t => { if (t.dataset.tab === key) t.setAttribute("aria-current", "page"); else t.removeAttribute("aria-current"); });
    history.replaceState(null, "", "#" + key);
    try { sessionStorage.setItem("wk-tab", key); } catch (_) {}
    if (key === "stock" && Date.now() - stockLoaded > 60000) { stockLoaded = Date.now(); ready(() => htmx.trigger("#stock-list", "stockShow")); }
    window.scrollTo(0, 0);
  }
  tabs.forEach(t => t.addEventListener("click", () => show(t.dataset.tab)));
  let start = location.hash.slice(1);
  try { start = start || sessionStorage.getItem("wk-tab") || ""; } catch (_) {}
  show(start);

  // ---------- Badges + tab title ----------
  const baseTitle = document.title;
  const count = sel => { const m = document.querySelector(sel + " .feed-meta"); return m ? +m.dataset.total : null; };
  function setBadge(key, n, calm) {
    const b = document.querySelector(`[data-badge="${key}"]`);
    if (!b) return;
    b.hidden = !n; b.textContent = n > 99 ? "99+" : n; b.classList.toggle("calm", !!calm);
  }
  function badges() {
    const o = count("#order-feed");
    setBadge("queue", o, KIND === "waiter" || KIND === "kitchen" || KIND === "bar" ? false : true);
    setBadge("pay", count("#pay-list"));
    setBadge("stock", count("#stock-list"), true);
    setBadge("dj", count("#dj-feed"));
    document.title = (o ? `(${o}) ` : "") + baseTitle;
  }

  // ---------- Timers: tick on the device clock, no requests ----------
  const dur = m => m < 1 ? "just now" : m < 60 ? m + " min" : Math.floor(m / 60) + "h " + String(m % 60).padStart(2, "0") + "m";
  const mix = (a, b, p) => `color-mix(in srgb, var(--${b}) ${p}%, var(--${a}))`;
  // green → amber → red as a ticket ages; nothing fetched, purely visual.
  const urgency = m => m < 10 ? mix("green", "amber", m * 10) : m < 20 ? mix("amber", "red", (m - 10) * 10) : "var(--red)";
  function tick() {
    const now = Date.now();
    document.querySelectorAll(".oc[data-ordered-at],.oc[data-since]").forEach(card => {
      const t = +(card.dataset.orderedAt || card.dataset.since);
      const m = Math.max(0, Math.floor((now - t) / 60000));
      const age = card.querySelector("[data-age]");
      if (age) age.textContent = m < 1 ? "<1m" : m < 60 ? m + "m" : Math.floor(m / 60) + "h " + String(m % 60).padStart(2, "0") + "m";
      if (!card.closest(".st-ready") && !card.classList.contains("dj")) card.style.setProperty("--u", urgency(m));
    });
    document.querySelectorAll(".help-call[data-since]").forEach(el => {
      const m = Math.max(0, Math.floor((now - +el.dataset.since) / 60000));
      el.querySelector("[data-age]").textContent = m < 1 ? "just now" : "waiting " + dur(m);
    });
  }
  setInterval(tick, 30000);

  // ---------- Sound (synthesized: no audio file to ship) ----------
  const SOUND_KEY = "biteboxAlertSoundEnabled";
  let audio = null, soundOn = store.get(SOUND_KEY) !== "off";
  function ctx() {
    if (!audio) { try { audio = new (window.AudioContext || window.webkitAudioContext)(); } catch (_) { return null; } }
    if (audio.state === "suspended") audio.resume();
    return audio;
  }
  // Browsers only allow audio after a tap; the first tap anywhere primes it.
  document.addEventListener("pointerdown", () => ctx(), { once: true });
  function tone(c, f, at, d, g = 0.2) {
    const o = c.createOscillator(), v = c.createGain();
    o.type = "sine"; o.frequency.value = f;
    v.gain.setValueAtTime(0, at); v.gain.linearRampToValueAtTime(g, at + 0.02); v.gain.exponentialRampToValueAtTime(0.0001, at + d);
    o.connect(v).connect(c.destination); o.start(at); o.stop(at + d + 0.02);
  }
  function chime(kind) {
    if (!soundOn) return;
    const c = ctx(); if (!c) return;
    const t = c.currentTime;
    if (kind === "ready") { tone(c, 660, t, .16); tone(c, 990, t + .16, .22); }
    else if (kind === "help") { tone(c, 880, t, .14); tone(c, 880, t + .2, .14); tone(c, 1175, t + .4, .28, .26); }
    else if (kind === "song") { tone(c, 520, t, .12); tone(c, 780, t + .12, .18); }
    else tone(c, 760, t, .18);
  }
  function setSound(on) {
    soundOn = on; store.set(SOUND_KEY, on ? "on" : "off");
    const b = $("soundBtn"), s = $("setSound");
    if (b) { b.classList.toggle("muted", !on); b.setAttribute("aria-pressed", String(on)); b.title = on ? "Sound on. Tap to mute" : "Sound muted. Tap to turn on"; }
    if (s) s.setAttribute("aria-checked", String(on));
  }
  setSound(soundOn);
  const toggleSound = () => { ctx(); setSound(!soundOn); if (soundOn) chime("new"); };
  ["soundBtn", "setSound"].forEach(id => { const el = $(id); if (el) el.addEventListener("click", toggleSound); });

  // ---------- Toast + undo (undo is a real request, so every screen agrees) ----------
  let toastTimer = null;
  function toast(msg, undo) {
    const t = $("wToast"), u = $("wToastUndo");
    $("wToastMsg").textContent = msg;
    u.hidden = !undo; u.onclick = () => { undo && undo(); t.classList.remove("show"); };
    t.classList.add("show"); clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove("show"), undo ? 6000 : 3500);
  }
  function undoFor(el) {
    const d = el.dataset, url = d.undoUrl;
    if (d.undoIds) return async () => {
      for (const id of d.undoIds.split(",")) await fetch(`/worker/orders/${id}/unpaid`, { method: "POST", body: new URLSearchParams({ view: "pay" }) });
      htmx.trigger(B, "payRefresh");
    };
    if (!url) return null;
    if (d.undoStatus) return () => htmx.ajax("POST", url, { values: { status: d.undoStatus }, target: "#order-feed", swap: "innerHTML" });
    if (d.undoView === "pay") return () => htmx.ajax("POST", url, { values: { view: "pay" }, target: "#pay-list", swap: "innerHTML" });
    return () => htmx.ajax("POST", url, { target: "#order-feed", swap: "innerHTML" });
  }
  // The clicked button is replaced by the swap, so listen on it directly.
  B.addEventListener("htmx:beforeRequest", e => {
    const el = e.detail.elt;
    if (!el.dataset || !el.dataset.msg) return;
    const undo = undoFor(el), msg = el.dataset.msg;
    el.addEventListener("htmx:afterRequest", ev => { if (ev.detail.successful) toast(msg, undo); }, { once: true });
  });
  B.addEventListener("htmx:responseError", e => { if (ownEvent(e)) toast(e.detail.xhr.responseText || "That didn't work. Try again.", null); });
  B.addEventListener("htmx:sendError", e => { if (ownEvent(e)) toast("No connection. Try again.", null); });

  // ---------- Order / help / song alerts ----------
  let phoneOn = false; // a device with real push doesn't also need the in-page copy
  const seen = { orders: null, help: null, dj: null }; // null = not primed yet
  function lines(card) {
    return [...card.querySelectorAll(".oc-lines li:not(.other)")].map(li => {
      const q = li.querySelector(".q").textContent.trim(), n = li.querySelector(".n").textContent.trim();
      return q + " " + n;
    }).join(", ");
  }
  function scan(kind, sel, idOf, readyOf) {
    const root = document.querySelector(kind === "orders" ? "#order-feed" : kind === "help" ? "#assistance-alerts" : "#dj-feed");
    if (!root || !root.querySelector(".feed-meta")) return; // skeleton, not real content yet
    const now = new Map();
    root.querySelectorAll(sel).forEach(card => {
      const id = idOf(card), ready = readyOf ? readyOf(card) : false;
      now.set(id, ready);
      if (seen[kind]) {
        const was = seen[kind].get(id);
        if (was === undefined) notify(kind === "orders" ? (ready ? "ready" : "new") : kind === "help" ? "help" : "song", card);
        else if (!was && ready) notify("ready", card);
      }
    });
    seen[kind] = now; // first pass only primes: no banner for what was already there
  }
  function scanAll() {
    scan("orders", "#order-feed .oc", c => c.dataset.orderId, c => c.dataset.status === "ready");
    scan("help", ".help-call", c => c.dataset.help);
    scan("dj", ".oc.dj", c => c.dataset.djId);
  }
  function flashTitle(label) {
    if (!document.hidden) return;
    let on = false;
    clearInterval(flashTitle.t);
    flashTitle.t = setInterval(() => { document.title = on ? baseTitle : label; on = !on; }, 1000);
    document.addEventListener("visibilitychange", function stop() {
      if (document.hidden) return;
      clearInterval(flashTitle.t); document.title = baseTitle; document.removeEventListener("visibilitychange", stop);
    });
  }
  const ICON = { new: "🔔", ready: "✅", help: "🙋", song: "♪" };
  function notify(kind, card) {
    if (phoneOn && (kind === "new" || kind === "ready")) return; // lock-screen push already covers it
    const head = card.querySelector(".oc-table, .hc-text b");
    const where = card.matches(".help-call") ? head.textContent : "Table " + (head ? head.textContent.trim() : "");
    const title = { new: "New order", ready: "Ready to deliver", help: "", song: "Song request" }[kind] + (kind === "help" ? where : " · " + where);
    const detail = kind === "help" ? "" : kind === "song" ? (card.querySelector(".dj-song") || {}).textContent : lines(card);
    chime(kind); flashTitle(ICON[kind] + " " + title);
    if (navigator.vibrate) navigator.vibrate(kind === "help" ? [200, 100, 200, 100, 200] : 140);
    const stack = $("alert-stack"), b = document.createElement("div");
    b.className = "alert-banner alert-" + kind;
    const ic = document.createElement("span"); ic.className = "alert-icon"; ic.textContent = ICON[kind];
    const body = document.createElement("div"); body.className = "alert-body";
    const st = document.createElement("strong"); st.textContent = title;
    const it = document.createElement("span"); it.className = "alert-items"; it.textContent = detail || "";
    body.append(st, it);
    const x = document.createElement("button"); x.type = "button"; x.className = "alert-dismiss"; x.setAttribute("aria-label", "Dismiss"); x.textContent = "✕";
    const gone = () => { b.classList.remove("show"); setTimeout(() => b.remove(), 250); };
    x.onclick = gone;
    Sheet.drag(b, { axis: "x", sign: 0, ratio: 0.35, onDismiss: () => b.remove() }); // flick it away like a native banner
    b.append(ic, body, x); stack.prepend(b);
    while (stack.children.length > 3) stack.lastChild.remove();
    requestAnimationFrame(() => b.classList.add("show"));
    setTimeout(gone, kind === "help" ? 15000 : 8000);
  }

  // ---------- After any live update ----------
  let lastPay = 0;
  function afterUpdate(e) {
    const t = e.detail && (e.detail.target || e.detail.elt);
    tick(); badges(); scanAll();
    if (t && t.id === "order-feed" && document.getElementById("pay-list") && Date.now() - lastPay > 1200) {
      lastPay = Date.now(); htmx.trigger(B, "payRefresh"); // an order changed: refresh who owes what
    }
    if (t && t.closest && t.closest("#stock-list")) { filterStock(); stockBadge(); }
  }
  B.addEventListener("htmx:afterSwap", afterUpdate);
  B.addEventListener("htmx:oobAfterSwap", afterUpdate);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden && $("pay-list")) htmx.trigger(B, "payRefresh");
    if (!document.hidden && awakeOn && !wl) lock();
  });

  // ---------- Connection ----------
  let open = 0, offTimer = null;
  const setLive = on => {
    const p = $("livePill"), c = $("conn");
    if (p) { p.classList.toggle("off", !on); p.lastElementChild.textContent = on ? "Live" : "Offline"; }
    clearTimeout(offTimer);
    if (c) { if (on) c.hidden = true; else offTimer = setTimeout(() => { c.hidden = false; }, 4000); }
  };
  B.addEventListener("htmx:wsOpen", () => { open++; setLive(true); });
  B.addEventListener("htmx:wsClose", e => {
    if (e.detail && e.detail.event && e.detail.event.code === 4001) { location.href = "/login"; return; } // account deactivated
    open = Math.max(0, open - 1); if (!open) setLive(false);
  });
  addEventListener("offline", () => setLive(false));

  // ---------- Print a kitchen ticket ----------
  B.addEventListener("click", e => {
    const b = e.target.closest("[data-print]");
    if (!b) return;
    const card = b.closest(".oc"); card.classList.add("printing"); window.print();
  });
  addEventListener("afterprint", () => document.querySelectorAll(".oc.printing").forEach(c => c.classList.remove("printing")));

  // ---------- Sold out: find an item fast ----------
  const sq = $("stockQ");
  function filterStock() {
    if (!sq) return;
    const t = sq.value.trim().toLowerCase();
    let any = false;
    document.querySelectorAll("#stock-list .sk").forEach(r => { const m = !t || r.dataset.name.toLowerCase().includes(t); r.hidden = !m; any = any || m; });
    document.querySelectorAll("#stock-list [data-group]").forEach(g => { g.hidden = ![...g.querySelectorAll(".sk")].some(r => !r.hidden); });
    const e = document.querySelector("#stock-list .w-empty-filter"); if (e) e.hidden = any || !t;
  }
  if (sq) sq.addEventListener("input", filterStock);
  // The badge counts sold-out items; recount after any change to the list.
  function stockBadge() {
    const m = document.querySelector("#stock-list .feed-meta");
    if (m) { m.dataset.total = document.querySelectorAll("#stock-list .sk.out").length; badges(); }
  }
  B.addEventListener("stockChanged", e => toast(e.detail.name + (e.detail.out ? " is sold out" : " is back on the menu"), null));

  // ---------- Menu sheet ----------
  // A bottom sheet on phones (drag it down from anywhere), a side drawer on
  // wide screens. Behind it the app is inert and the page can't scroll.
  const sheet = $("sheet");
  if (sheet) {
    const panel = sheet.querySelector(".wsheet-panel"), scrim = sheet.querySelector(".wsheet-scrim");
    const phone = () => innerWidth < 900;
    let uninert = null, opener = null;
    const isOpen = () => sheet.classList.contains("open");
    function sheetOpen(o, instant) {
      if (o === isOpen()) return;
      if (o) {
        opener = document.activeElement; sheet.hidden = false;
        Sheet.lock(); uninert = Sheet.inertBackground(sheet);
        void sheet.offsetWidth; // start from the closed position so it slides in
        sheet.classList.add("open");
      } else {
        sheet.classList.remove("open");
        Sheet.unlock(); if (uninert) { uninert(); uninert = null; }
        if (opener && opener.focus) opener.focus({ preventScroll: true });
        const hide = () => { if (!isOpen()) sheet.hidden = true; };
        if (instant) { sheet.classList.add("instant"); hide(); requestAnimationFrame(() => sheet.classList.remove("instant")); }
        else { let d = false; const end = () => { if (!d) { d = true; hide(); } }; panel.addEventListener("transitionend", e => { if (e.target === panel) end(); }, { once: true }); setTimeout(end, 480); }
      }
    }
    ["menuBtn", "moreBtn"].forEach(id => { const el = $(id); if (el) el.onclick = () => sheetOpen(true); });
    sheet.addEventListener("click", e => {
      const g = e.target.closest("[data-goto]");
      if (g) { show(g.dataset.goto); sheetOpen(false); return; }
      if (e.target.closest("[data-close]")) sheetOpen(false);
    });
    document.addEventListener("keydown", e => { if (e.key === "Escape") sheetOpen(false); });
    Sheet.drag(panel, { axis: "y", sign: 1, scrim, enabled: () => phone() && isOpen(), onDismiss: () => sheetOpen(false, true) });
  }

  // ---------- Keep the screen on (mounted kitchen / bar tablet) ----------
  let wl = null, awakeOn = false;
  async function lock() { try { wl = await navigator.wakeLock.request("screen"); wl.addEventListener("release", () => { wl = null; }); } catch (_) {} }
  const awakeBtn = $("setAwake");
  if (awakeBtn && "wakeLock" in navigator && window.isSecureContext) {
    awakeBtn.hidden = false;
    const pref = store.get("wkAwake");
    awakeOn = pref === null ? ROOT.dataset.keepAwake === "true" : pref === "on";
    const paint = () => awakeBtn.setAttribute("aria-checked", String(awakeOn));
    paint(); if (awakeOn) lock();
    awakeBtn.onclick = () => {
      awakeOn = !awakeOn; store.set("wkAwake", awakeOn ? "on" : "off"); paint();
      if (awakeOn) lock(); else if (wl) { wl.release(); wl = null; }
    };
  }

  // ---------- Phone (lock-screen) alerts via Web Push ----------
  (function pushSetup() {
    const btn = $("setPhone"), hint = $("phoneHint");
    if (!btn) return;
    if (KIND === "dj") { btn.hidden = true; return; }
    const why = r => { btn.disabled = true; hint.textContent = r; };
    if (!window.isSecureContext) return why("Needs the site opened over HTTPS");
    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return why("This browser can't do phone alerts");
    if (!ROOT.dataset.vapid) return why("Not set up on this server yet");
    if (/iP(hone|ad|od)/.test(navigator.userAgent) && !(navigator.standalone || matchMedia("(display-mode: standalone)").matches)) return why("iPhone: Share → Add to Home Screen, then open it from there");
    const key = (s => { const p = "=".repeat((4 - s.length % 4) % 4), r = atob((s + p).replace(/-/g, "+").replace(/_/g, "/")); return Uint8Array.from(r, c => c.charCodeAt(0)); })(ROOT.dataset.vapid);
    let reg = null;
    const paint = on => { phoneOn = on; btn.setAttribute("aria-checked", String(on)); hint.textContent = on ? "On: alerts reach this phone even when locked" : "Alerts on the lock screen"; };
    navigator.serviceWorker.register("/sw.js").then(r => { reg = r; return r.pushManager.getSubscription(); }).then(s => paint(!!s))
      .catch(() => why("Couldn't set up alerts in this browser"));
    btn.onclick = async () => {
      try {
        const r = reg || await navigator.serviceWorker.ready;
        if (phoneOn) {
          const s = await r.pushManager.getSubscription();
          if (s) { await fetch("/worker/push/unsubscribe", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ endpoint: s.endpoint }) }); await s.unsubscribe(); }
          paint(false); toast("Phone alerts off on this device");
        } else {
          if (await Notification.requestPermission() !== "granted") return toast("Notifications are blocked. Allow them in the browser settings.");
          const s = await r.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
          await fetch("/worker/push/subscribe", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(s.toJSON()) });
          paint(true); toast("Phone alerts on for this device");
        }
      } catch (_) { toast("Couldn't change phone alerts on this device"); }
    };
  })();

  tick(); badges(); scanAll(); // prime alerts with what the page already shows
})();
