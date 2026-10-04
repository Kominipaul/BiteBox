(function () {
  const $ = id => document.getElementById(id), esc = EgoFloor.esc, BT = BookingTime;
  const BASE = JSON.parse(document.getElementById("pageData").textContent).base; // /admin, or /worker when opened from the staff app
  const LABEL = { free: "Free", soon: "Booked within the hour", reserved: "Reserved", late: "Late, not here yet", seated: "Seated",
    occupied: "QR guest ordering", off: "Out of service" };
  const hm = ms => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  const PILL = { seated: "seated", off: "cancelled", occupied: "no_show", late: "no_show", free: "completed" };
  const svg = $("floor"), wrap = $("mapWrap"), ctl = $("controls");
  let data = { Floor: [], Bookings: [] }, spots = [], picked = null, land = false;
  const q = () => new URLSearchParams(new FormData(ctl)).toString();

  async function load() {
    try { data = await (await fetch("/admin/floor/live?" + q(), { cache: "no-store" })).json(); } catch (_) { return; }
    render();
  }

  // Everything below is drawn from `data` + the device clock, so the
  // countdowns and late/ending states move on their own between updates.
  function render() {
    const now = Date.now();
    if (data.Live) data.Floor.forEach(t => {
      if (!t.bk || t.off) return;
      const ph = BT.phase(t.bk, now);
      if (ph === "late") t.st = "late";
      if (ph === "over" || ph === "done") { t.st = t.qr ? "occupied" : "free"; t.bk = null; }
    });
    const live = data.Bookings.filter(b => BT.phase(b, now) !== "over");
    const count = ph => live.filter(b => BT.phase(b, now) === ph).length;
    $("sFree").textContent = data.Floor.filter(t => t.st === "free").length;
    $("sSeated").textContent = data.Live ? count("seated") + count("ending") : data.Seated;
    $("sLate").textContent = data.Live ? count("late") : "–";
    $("lateStat").classList.toggle("alert-stat", data.Live && count("late") > 0);
    $("sBookings").textContent = data.Active; $("sCovers").textContent = data.Covers;
    $("sQR").textContent = data.Floor.filter(t => t.qr).length; $("sHelp").textContent = data.Help;
    $("helpStat").classList.toggle("alert-stat", data.Help > 0);
    $("dayLabel").textContent = data.Live ? `Live now · ${hm(now)}` : `${data.DateLabel} at ${data.Time}`;
    $("nowBtn").classList.toggle("primary", !data.Live);
    draw(now); panel(now);
  }

  // Unplaced tables (made in the map editor but not dragged on yet) sit in a
  // strip under the map so every QR table is reachable from here.
  function draw(now) {
    spots = EgoFloor.place(data.Floor);
    const H = EgoFloor.height(spots);
    land = wrap.clientWidth >= 700;
    svg.setAttribute("viewBox", land ? `0 0 ${H} ${EgoFloor.WP}` : `0 0 ${EgoFloor.WP} ${H}`);
    svg.innerHTML = EgoFloor.background(land, H, "Not on the map") + `<g id="spots">${spots.map(s => EgoFloor.spotSVG(s, land)).join("")}</g>`;
    svg.querySelectorAll(".spot").forEach(g => {
      const s = spots.find(x => x.id === g.dataset.id);
      const ph = s.bk ? BT.phase(s.bk, now) : "";
      g.setAttribute("class", `spot ${s.st}${s.help ? " help" : ""}${ph === "ending" ? " ending" : ""}${picked === s.n ? " picked" : ""}`);
      g.setAttribute("aria-label", `${s.label}, ${s.help ? "needs help, " : ""}${LABEL[s.st]}${s.bk ? ", " + BT.text(s.bk, now) : ""}`);
      // Countdown under the spot's code: time left, "late 12m", "in 25m".
      if (s.bk && ["seated", "late", "reserved", "soon"].includes(s.st)) {
        const code = g.querySelector(".code");
        const t = document.createElementNS("http://www.w3.org/2000/svg", "text");
        t.setAttribute("class", "cd cd-" + ph);
        t.setAttribute("x", code.getAttribute("x"));
        t.setAttribute("y", +code.getAttribute("y") + 17);
        t.textContent = BT.text(s.bk, now).replace(" left", "");
        g.appendChild(t);
      }
    });
  }
  const zoom = EgoFloor.zoomable(wrap, svg);
  new ResizeObserver(() => { if ((wrap.clientWidth >= 700) !== land) { zoom.reset(); draw(Date.now()); } }).observe(wrap);

  const pick = id => { picked = +id || null; draw(Date.now()); panel(Date.now(), true); };
  // Phones: closing the details sheet (X, scrim, swipe, Esc) un-picks the spot.
  document.addEventListener("layer:close", () => { if (picked && phone()) { picked = null; draw(Date.now()); } });
  svg.addEventListener("click", e => { const g = e.target.closest(".spot"); if (g) pick(g.dataset.id); });
  svg.addEventListener("keydown", e => { const g = e.target.closest && e.target.closest(".spot"); if (g && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); pick(g.dataset.id); } });

  const post = async (url, body) => {
    const res = await fetch(url, { method: "POST", body: body ? new URLSearchParams(body) : undefined });
    if (!res.ok) Layer.toast("Couldn't do that. Try again.");
    load();
  };
  const setStatus = (b, status) => post(`/admin/bookings/${b.id}/status`, { status });

  // One booking as a row: spot, who, countdown, and the next action.
  function bookingRow(b, now) {
    const ph = BT.phase(b, now);
    const acts = b.status === "seated"
      ? `<button type="button" class="act sm" data-act="completed" data-id="${b.id}" title="They've left: frees the spot now">Finish</button>`
      : `<button type="button" class="act green sm" data-act="seated" data-id="${b.id}">Seat</button>` +
        (ph === "late" ? `<button type="button" class="act ghost sm" data-act="no_show" data-id="${b.id}" title="Frees the spot now">No-show</button>` : "");
    return `<div class="brow ph-${ph}">
      <button type="button" class="bspot" data-pick="${b.n}">${esc(b.spot)}</button>
      <div class="bwho"><b>${esc(b.name)}</b><small>${b.guests} ${b.guests === 1 ? "guest" : "guests"} · ${hm(b.start)}–${hm(b.end)}</small></div>
      <span class="cd cd-${ph}">${BT.text(b, now)}</span>
      <div class="bacts">${acts}</div>
    </div>`;
  }

  function wire(p) {
    p.querySelectorAll("[data-pick]").forEach(b => b.onclick = () => pick(b.dataset.pick));
    p.querySelectorAll("[data-act]").forEach(el => el.onclick = () => {
      const b = data.Bookings.find(x => x.id === +el.dataset.id) || (spots.find(s => s.bk && s.bk.id === +el.dataset.id) || {}).bk;
      const ask = { no_show: `Mark ${b.name} as a no-show? The spot is freed now.`, completed: `Finish ${b.name}'s booking? The spot is freed now.` }[el.dataset.act];
      if (!ask || confirm(ask)) setStatus(b, el.dataset.act);
    });
  }

  // Desktop: the side panel shows "Right now", or the picked spot in place of it.
  // Phones: the panel sits under the map, so a picked spot opens on top in the
  // shared layer (bottom sheet) instead. Live refreshes only redraw that sheet
  // while it's still what the layer shows (not after "Edit" swapped the form in).
  const phone = () => innerWidth <= 760; // same breakpoint as Layer's bottom sheet
  function panel(now, opening) {
    const side = $("panel"), t = spots.find(s => s.n === picked), sheet = phone();
    if (!t || sheet) { side.innerHTML = board(now); wire(side); }
    if (!t) { if (Layer.isShowing(".spot-detail")) Layer.close(); return; }
    if (sheet && !opening && !Layer.isShowing(".spot-detail")) return;
    const c = Object.fromEntries(new FormData(ctl));
    const b = t.bk, ph = b ? BT.phase(b, now) : "";
    const title = `${esc(t.label)} <span class="pill ${PILL[t.st] || "confirmed"}">${esc(LABEL[t.st])}</span>`;
    const sub = `${esc(t.zone)} · seats ${t.min}–${t.max} · QR table ${t.n}${t.online ? "" : " · not bookable online"}${t.placed ? "" : " · not on the map"}`;
    const body = `
      ${t.help ? `<div class="banner err" role="alert">A guest at this table asked for help.</div>` : ""}
      ${b ? `<div class="bcard ph-${ph}">
          <div class="bcard-top"><b>${esc(b.name)}</b><span class="cd cd-${ph}">${BT.text(b, now)}</span></div>
          <small>${b.guests} ${b.guests === 1 ? "guest" : "guests"} · ${hm(b.start)}–${hm(b.end)} · ${esc(b.code)}</small>
          ${b.phone ? `<a href="tel:${esc(b.phone)}">${esc(b.phone)}</a>` : ""}
          ${b.notes ? `<p class="note">“${esc(b.notes)}”</p>` : ""}
          ${b.status === "seated" ? `<div class="meter"><i style="width:${Math.min(100, Math.max(0, (now - b.start) / (b.end - b.start) * 100)).toFixed(0)}%"></i></div>` : ""}
          <div class="bacts">
            ${b.status === "seated"
              ? `<button type="button" class="act" data-act="completed" data-id="${b.id}">Finish · free spot</button>`
              : `<button type="button" class="act green" data-act="seated" data-id="${b.id}">Seat them</button>
                 <button type="button" class="act ghost" data-act="no_show" data-id="${b.id}">No-show</button>`}
            <button type="button" class="act ghost" data-modal="/admin/bookings/${b.id}/edit">Edit</button>
          </div>
        </div>` : ""}
      ${t.qr && t.st !== "occupied" ? `<p class="sub">A guest is ordering here by QR right now.</p>` : ""}
      ${t.day.length ? `<div class="f">Today at this spot</div><ul>${t.day.map(d => `<li>${esc(d)}</li>`).join("")}</ul>` : `<p class="sub">No bookings on this day.</p>`}
      <div class="stack">
        ${t.help ? `<button type="button" class="act primary" id="pHelp">Got it, on my way</button>` : ""}
        <a class="act ${t.help || b ? "" : "primary"}" href="${BASE}/bookings?new=1&table=${t.n}&date=${encodeURIComponent(c.date)}&time=${encodeURIComponent(c.time || data.Time)}">Book this spot</a>
        ${t.qr ? `<button type="button" class="act danger" id="pRelease">Release table (guest left)</button>` : ""}
        <button type="button" class="act" id="pService">${t.off ? "Put back in service" : "Take out of service"}</button>
      </div>`;
    let p;
    if (sheet) {
      Layer.show(`<header class="modal-head">
        <div><h2>${title}</h2><p>${sub}</p></div>
        <button type="button" class="modal-x" data-close aria-label="Close">
          <svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
        </button>
      </header>
      <div class="modal-body spot-detail">${body}</div>`);
      p = document.querySelector(".spot-detail");
    } else {
      p = side;
      p.innerHTML = `<button type="button" class="panel-back" data-pick="">← Right now</button>
        <h3>${title}</h3><p class="sub">${sub}</p>${body}`;
    }
    wire(p);
    if (t.help) $("pHelp").onclick = () => post(`/worker/tables/${t.n}/assist/clear`);
    if (t.qr) $("pRelease").onclick = () => { if (confirm(`Release ${t.label}? The guest's QR session and unsent cart are cleared.`)) post(`/admin/tables/${t.n}/release`); };
    $("pService").onclick = () => post(`/admin/tables/${t.n}/service`);
  }

  // "Right now": what needs doing, most urgent first.
  function board(now) {
    const help = data.Floor.filter(s => s.help);
    const bs = data.Bookings.map(b => ({ b, ph: BT.phase(b, now) }));
    const sec = (title, cls, list, empty) => list.length || empty
      ? `<section class="bsec ${cls}"><h4>${title}<span>${list.length}</span></h4>${list.length ? list.join("") : `<p class="sub">${empty}</p>`}</section>` : "";
    const rows = (phs, sort) => bs.filter(x => phs.includes(x.ph)).sort(sort).map(x => bookingRow(x.b, now));
    const html =
      (help.length ? `<section class="bsec s-help"><h4>Needs help<span>${help.length}</span></h4><div class="stack">${help.map(s => `<button type="button" class="act danger" data-pick="${s.n}">${esc(s.label)}</button>`).join("")}</div></section>` : "") +
      sec("Late, not here yet", "s-late", rows(["late"], (a, b) => a.b.start - b.b.start)) +
      sec("Ending soon", "s-ending", rows(["ending"], (a, b) => a.b.end - b.b.end)) +
      sec("Arriving within the hour", "s-soon", rows(["soon"], (a, b) => a.b.start - b.b.start)) +
      sec("Seated now", "s-seated", rows(["seated"], (a, b) => a.b.end - b.b.end), data.Live ? "Nobody from a booking is seated." : "") +
      sec("Later today", "s-later", rows(["later"], (a, b) => a.b.start - b.b.start).slice(0, 8));
    return `<h3>Right now</h3><p class="sub">Tap a spot on the map, or a code below, for its details.</p>` +
      (html || `<p class="sub">No more bookings today.</p>`);
  }

  // Live: the server pings on every change (guest scans in or leaves, calls a
  // waiter, a booking is made or changed); we re-fetch. The clock ticks the
  // countdowns locally. Polling only while the socket is down.
  let live = false;
  Live("/admin/floor/ws", load, on => { live = on; $("liveDot").classList.toggle("on", on); });
  ctl.addEventListener("change", load);
  setInterval(() => { if (!document.hidden) render(); }, 20000);
  setInterval(() => { if (!live && !document.hidden) load(); }, 60000);
  render(); load();
})();
