(function () {
  const $ = id => document.getElementById(id), esc = EgoFloor.esc;
  const SHAPES = JSON.parse(document.getElementById("pageData").textContent).shapes;
  const SHAPE_NAMES = { round: "Round table", rect4: "Long table (4)", high: "High table", lounge: "Lounge", set: "Sunbed set", cabana: "Cabana" };
  const svg = $("floor"), wrap = $("mapWrap");
  let tables = [], spots = [], picked = null, land = false, H = EgoFloor.HP;
  const byN = n => spots.find(s => s.n === n);
  const flash = msg => { $("saved").textContent = msg; clearTimeout(flash.t); flash.t = setTimeout(() => $("saved").textContent = "", 2000); };

  async function load() {
    tables = await (await fetch("/admin/map/data", { cache: "no-store" })).json();
    $("zoneList").innerHTML = [...new Set(tables.map(t => t.zone))].map(z => `<option value="${esc(z)}">`).join("");
    draw(); panel();
  }

  function draw() {
    spots = EgoFloor.place(tables);
    H = EgoFloor.height(spots);
    land = wrap.clientWidth >= 700;
    svg.setAttribute("viewBox", land ? `0 0 ${H} ${EgoFloor.WP}` : `0 0 ${EgoFloor.WP} ${H}`);
    svg.innerHTML = EgoFloor.background(land, H, "Not placed yet: drag onto the map") + `<g id="spots">${spots.map(s => EgoFloor.spotSVG(s, land)).join("")}</g>`;
    svg.querySelectorAll(".spot").forEach(g => {
      const s = byN(+g.dataset.id);
      g.setAttribute("class", `spot${s.off ? " off" : ""}${picked === s.n ? " picked" : ""}`);
      g.setAttribute("aria-label", `${s.label}, drag to move, Enter to edit`);
    });
  }
  new ResizeObserver(() => { if ((wrap.clientWidth >= 700) !== land) draw(); }).observe(wrap);

  // Drag to move (saved on release); a tap without moving selects.
  const worldPoint = e => {
    const p = svg.createSVGPoint(); p.x = e.clientX; p.y = e.clientY;
    return EgoFloor.toWorld(land, p.matrixTransform(svg.getScreenCTM().inverse()));
  };
  let drag = null;
  svg.addEventListener("pointerdown", e => {
    const g = e.target.closest(".spot"); if (!g) return;
    const s = byN(+g.dataset.id), p = worldPoint(e);
    drag = { g, s, dx: p.x - s.x, dy: p.y - s.y, x0: e.clientX, y0: e.clientY, moved: false };
    try { svg.setPointerCapture(e.pointerId); } catch (_) {}
  });
  svg.addEventListener("pointermove", e => {
    if (!drag) return;
    if (!drag.moved && Math.hypot(e.clientX - drag.x0, e.clientY - drag.y0) < 5) return;
    drag.moved = true; drag.g.classList.add("dragging");
    const p = worldPoint(e), s = drag.s, snap = v => Math.round(v / 5) * 5;
    s.x = Math.min(EgoFloor.WP - 20, Math.max(20, snap(p.x - drag.dx)));
    s.y = Math.min(EgoFloor.HP - 20, Math.max(20, snap(p.y - drag.dy)));
    const [a, b] = EgoFloor.geom(land).T(s.x, s.y);
    drag.g.setAttribute("transform", `translate(${a} ${b})`);
  });
  const endDrag = async e => {
    if (!drag) return;
    const { s, moved } = drag; drag = null;
    if (e.type === "pointercancel") return draw();
    if (!moved) { picked = s.n; draw(); panel(); return; }
    const t = tables.find(t => t.n === s.n); t.x = s.x; t.y = s.y;
    picked = s.n; draw(); panel();
    const r = await fetch(`/admin/tables/${s.n}/position`, { method: "POST", body: new URLSearchParams({ x: s.x, y: s.y }) });
    flash(r.ok ? `${s.label} moved` : "Couldn't save the position");
  };
  svg.addEventListener("pointerup", endDrag);
  svg.addEventListener("pointercancel", endDrag);
  svg.addEventListener("keydown", e => {
    const g = e.target.closest && e.target.closest(".spot");
    if (g && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); picked = +g.dataset.id; draw(); panel(); }
  });

  function panel() {
    const p = $("panel"), t = byN(picked);
    if (!t) { p.innerHTML = `<h3>Pick a spot</h3><p class="sub">Tap a spot on the map to edit its label, zone, seats, prices and online hours.</p><p class="sub">${tables.length} spots on the floor.</p>`; return; }
    p.innerHTML = `<h3>${esc(t.label)}</h3><p class="sub">QR table ${t.n}${t.off ? " · out of service" : ""}</p>
      <form id="propForm" class="stack">
        <div class="grid">
          <label class="field">Label<input class="input" name="label" maxlength="12" value="${esc(t.label)}"></label>
          <label class="field">Zone<input class="input" name="zone" maxlength="40" list="zoneList" value="${esc(t.zone)}" required></label>
        </div>
        <label class="field">Shape<select class="input" name="shape">${SHAPES.map(s => `<option value="${s}" ${s === t.shape ? "selected" : ""}>${SHAPE_NAMES[s] || s}</option>`).join("")}</select></label>
        <div class="grid">
          <label class="field">Min guests<input class="input" type="number" name="min_guests" min="1" max="50" value="${t.min}"></label>
          <label class="field">Max guests<input class="input" type="number" name="max_guests" min="1" max="50" value="${t.max}"></label>
        </div>
        <div class="grid">
          <label class="field"><span><span class="feeLbl">${t.shape === "set" ? "Booking fee" : "Deposit"}</span> €</span><input class="input" type="number" name="fee" min="0" step="1" value="${t.fee}"></label>
          <label class="field">Min spend €<input class="input" type="number" name="min_spend" min="0" step="1" value="${t.minSpend}"></label>
        </div>
        <label class="field">Length (minutes) <span class="opt">0 = venue default</span><input class="input" type="number" name="duration" min="0" max="1440" step="15" value="${t.dur}"></label>
        <label class="check"><input type="checkbox" name="full_day" value="1" ${t.fullDay ? "checked" : ""}> Booked for the full day</label>
        <div class="grid">
          <label class="field"><span class="fromLbl"></span><input class="input" type="time" name="avail_from" value="${esc(t.from)}"></label>
          <label class="field"><span class="untilLbl"></span><input class="input" type="time" name="avail_until" value="${esc(t.until)}"></label>
        </div>
        <label class="check"><input type="checkbox" name="bookable" value="1" ${t.online ? "checked" : ""}> Guests can book it online</label>
        <span class="err-text" id="propMsg" role="alert"></span>
        <button class="act primary" type="submit">Save spot</button>
        <button class="act danger" type="button" id="delBtn">${"Remove spot"}</button>
      </form>`;
    const f = $("propForm");
    const labels = () => {
      p.querySelector(".fromLbl").textContent = f.full_day.checked ? "Day from" : "Online from";
      p.querySelector(".untilLbl").textContent = f.full_day.checked ? "Day until" : "Online until";
      p.querySelector(".feeLbl").textContent = f.shape.value === "set" ? "Booking fee" : "Deposit";
    };
    labels(); f.full_day.onchange = labels; f.shape.onchange = labels;
    f.onsubmit = async e => {
      e.preventDefault(); $("propMsg").textContent = "";
      const r = await fetch(`/admin/tables/${t.n}/layout`, { method: "POST", body: new URLSearchParams(new FormData(f)) });
      if (!r.ok) { $("propMsg").textContent = await r.text(); return; }
      flash("Saved"); await load();
    };
    $("delBtn").onclick = async () => {
      if (!confirm(`Remove ${t.label} from the floor? Its QR table ${t.n} stops working.`)) return;
      const r = await fetch(`/admin/map/spots/${t.n}/delete`, { method: "POST" });
      if (!r.ok) { $("propMsg").textContent = await r.text(); return; }
      picked = null; flash("Removed"); await load();
    };
  }

  $("addForm").onsubmit = async e => {
    e.preventDefault();
    const body = new URLSearchParams(new FormData(e.target)); body.set("x", 360); body.set("y", 500);
    const r = await fetch("/admin/map/spots", { method: "POST", body });
    if (!r.ok) { flash(await r.text()); return; }
    picked = (await r.json()).n; flash("Spot added"); await load();
  };

  load();
})();
