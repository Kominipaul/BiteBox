// EGO floor plan (Navarinou, Kalamata): the map artwork and spot shapes,
// shared by the guest booking page (/book) and the staff/admin floor
// (/admin/bookings). World is 720x1180, north up, sea at the bottom;
// `land` draws it rotated a quarter turn for wide screens.
window.EgoFloor = (function () {
  const WP = 720, HP = 1180;
  const SIZE = { round: [44, 44], high: [28, 28], lounge: [52, 52], rect4: [64, 36], set: [50, 56], cabana: [80, 64] };
  const f1 = n => +(+n).toFixed(1);
  const esc = s => String(s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // Tables from the server -> spots. Unplaced ones (x/y < 0) line up below
  // the map until an admin drags them onto it.
  function place(tables) {
    let i = 0;
    return tables.map(t => {
      const kind = SIZE[t.shape] ? t.shape : "round", [w, h] = SIZE[kind];
      let x = t.x, y = t.y;
      const placed = x >= 0 && y >= 0;
      if (!placed) { x = 60 + (i % 7) * 100; y = HP + 70 + Math.floor(i / 7) * 100; i++; }
      return Object.assign({}, t, { id: String(t.n), kind, w, h, x, y, placed });
    });
  }
  const height = spots => Math.max(HP, ...spots.map(s => s.y + s.h / 2 + 40));

  function geom(land) {
    const T = (x, y) => land ? [y, WP - x] : [x, y];
    return {
      T,
      R(x, y, w, h, a) { if (land) [x, y, w, h] = [y, WP - x - w, h, w]; return `<rect x="${f1(x)}" y="${f1(y)}" width="${f1(w)}" height="${f1(h)}" ${a}/>`; },
      C(x, y, r, a) { const [p, q] = T(x, y); return `<circle cx="${f1(p)}" cy="${f1(q)}" r="${r}" ${a}/>`; },
      TX(x, y, txt, a, rot) { const [p, q] = T(x, y); return `<text x="${f1(p)}" y="${f1(q)}" ${a}${rot && land ? ` transform="rotate(-90 ${f1(p)} ${f1(q)})"` : ""}>${txt}</text>`; },
      LN(x1, y1, x2, y2, a) { const [p, q] = T(x1, y1), [r, s] = T(x2, y2); return `<line x1="${f1(p)}" y1="${f1(q)}" x2="${f1(r)}" y2="${f1(s)}" ${a}/>`; },
      PTH(pts, close, a) { return `<path d="${pts.map((p, i) => { const [x, y] = T(p[0], p[1]); return (i ? "L" : "M") + f1(x) + " " + f1(y); }).join("")}${close ? "Z" : ""}" ${a}/>`; },
    };
  }

  // Screen-space SVG point -> world coordinates (inverse of T).
  const toWorld = (land, p) => land ? { x: WP - p.y, y: p.x } : { x: p.x, y: p.y };

  function background(land, H, extraLabel) {
    const { R, C, TX, LN, PTH } = geom(land);
    const tree = (x, y, r) => { const p = []; for (let i = 0; i <= 28; i++) { const a = i / 28 * Math.PI * 2; const rr = r * (1 + .09 * Math.sin(a * 5) + .05 * Math.sin(a * 9)); p.push([x + Math.cos(a) * rr, y + Math.sin(a) * rr]); } return PTH(p, true, 'class="tree"'); };
    const palm = (x, y) => { let o = ""; for (let i = 0; i < 7; i++) { const a = i / 7 * Math.PI * 2; o += LN(x, y, x + Math.cos(a) * 16, y + Math.sin(a) * 16, 'class="palm"'); } return o + C(x, y, 3, 'class="palm-c"'); };
    const shoreY = x => 1112 + 5 * Math.sin(x / 41) + 3 * Math.sin(x / 14);
    const pts = []; for (let x = -40; x <= WP + 40; x += 8) pts.push([x, shoreY(x)]);
    const sea = [[-3000, shoreY(-40)], ...pts, [WP + 3000, shoreY(WP + 40)], [WP + 3000, H + 3000], [-3000, H + 3000]];
    let waves = "";
    for (let r = 0; r < 12; r++) for (let i = -8; i < 14; i++) { const x = 10 + i * 96 + (r % 2 ? 48 : 0), y = 1140 + r * 24; waves += PTH([[x, y], [x + 12, y - 6], [x + 24, y], [x + 36, y - 6], [x + 48, y]], false, 'class="wave"'); }
    let hedge = ""; for (let y = 22; y <= 700; y += 20) hedge += C(116, y, 9, 'class="hedge"');
    let planksP = ""; for (let x = 500; x < 700; x += 14) planksP += LN(x, 292, x, 618, 'class="plank"');
    let beams = ""; for (let y = 310; y < 618; y += 60) beams += LN(490, y, 700, y, 'class="beam"');
    let zebra = ""; for (let i = 0; i < 6; i++) zebra += R(598, 748 + i * 12, 52, 6, 'class="zebra"');
    let board = ""; for (let y = 862; y < 1110; y += 12) board += LN(342, y, 378, y, 'class="plank"');
    return `
  <defs><pattern id="planks" width="40" height="16" patternUnits="userSpaceOnUse"${land ? ' patternTransform="rotate(90)"' : ""}><line class="plank" x1="0" y1="15" x2="40" y2="15"/></pattern></defs>
  ${R(-3000, -3000, WP + 6000, H + 6000, 'class="l-sand"')}
  ${R(-3000, -3000, WP + 6000, 3714, 'class="l-land"')}
  ${hedge}
  ${R(112, 12, WP - 110, 700, 'class="plot" rx="10"')}
  ${R(130, 24, 240, 250, 'class="building" rx="4"')}
  ${LN(130, 149, 370, 149, 'class="ridge"')}${LN(250, 24, 250, 274, 'class="ridge"')}
  ${TX(250, 140, "Kitchen", 'class="fixture-text"', true)}${TX(250, 162, "Indoor bar", 'class="zlabel" text-anchor="middle" dominant-baseline="central" font-size="12"', true)}
  ${R(390, 24, 310, 250, 'class="l-deck" rx="8"')}${R(390, 24, 310, 250, 'fill="url(#planks)" rx="8"')}
  ${TX(470, 150, "Terrace", 'class="zlabel" text-anchor="middle" dominant-baseline="central"', true)}
  ${R(130, 290, 340, 320, 'class="garden" rx="8"')}
  ${tree(205, 560, 40)}${tree(400, 556, 44)}${tree(310, 560, 26)}
  ${R(150, 304, 200, 40, 'class="fixture" rx="20"')}${TX(250, 324, "Garden bar", 'class="fixture-text"', true)}
  ${R(490, 290, 210, 330, 'class="l-deck" rx="8"')}${planksP}${beams}
  ${TX(595, 302, "Pergola", 'class="zlabel" text-anchor="middle" dominant-baseline="central" font-size="12"', true)}
  ${R(130, 630, 570, 78, 'class="paving" rx="8"')}
  ${R(556, 642, 72, 54, 'class="fixture" rx="8"')}${TX(592, 669, "Host", 'class="fixture-text"', true)}
  ${TX(668, 670, "Entrance", 'class="zlabel" text-anchor="middle" dominant-baseline="central" font-size="12"', true)}
  ${R(-3000, 714, WP + 6000, 28, 'class="walk"')}
  ${R(-3000, 742, WP + 6000, 76, 'class="road"')}
  ${LN(-3000, 780, WP + 3000, 780, 'class="roadline"')}
  ${zebra}
  ${R(150, 770, 100, 20, 'class="road"')}${TX(200, 780, "Navarinou", 'class="roadlabel" text-anchor="middle" dominant-baseline="central"', true)}
  ${R(-3000, 818, WP + 6000, 38, 'class="walk"')}
  ${palm(90, 837)}${palm(300, 837)}${palm(540, 837)}
  ${R(342, 856, 36, 258, 'class="board"')}${board}
  ${tree(655, 888, 38)}
  ${PTH(sea, true, 'class="l-sea"')}
  ${PTH([[-3000, shoreY(-40)], ...pts, [WP + 3000, shoreY(WP + 40)]], false, 'class="foamline"')}
  ${waves}
  ${TX(360, 1152, "Messinian Gulf", 'class="zlabel sea" text-anchor="middle" dominant-baseline="central"', true)}
  ${H > HP && extraLabel ? TX(24, HP + 24, esc(extraLabel), 'class="zlabel"') : ""}`;
  }

  function shapeSVG(s) {
    const hw = s.w / 2, hh = s.h / 2; let seats = "", body = "", accent = "";
    const ring = (n, r, sz, off = 0) => { let o = ""; for (let i = 0; i < n; i++) { const a = off + i * 2 * Math.PI / n; o += `<circle class="seat" cx="${f1(Math.cos(a) * r)}" cy="${f1(Math.sin(a) * r)}" r="${sz}"/>`; } return o; };
    if (s.kind === "round") { seats = ring(4, hw + 8, 5, Math.PI / 4); body = `<circle class="body" r="${hw}"/>`; }
    else if (s.kind === "high") { seats = ring(4, hw + 7, 4, Math.PI / 4); body = `<circle class="body" r="${hw}"/>`; }
    else if (s.kind === "lounge") {
      accent = `<circle class="accent" r="${hw + 18}" stroke-dasharray="4 5"/>`;
      seats = ring(6, hw + 9, 6, Math.PI / 6); body = `<circle class="body" r="${hw}"/>`;
    } else if (s.kind === "rect4") {
      [-16, 16].forEach(x => { seats += `<rect class="seat" x="${x - 7}" y="${-hh - 10}" width="14" height="6" rx="3"/><rect class="seat" x="${x - 7}" y="${hh + 4}" width="14" height="6" rx="3"/>`; });
      body = `<rect class="body" x="${-hw}" y="${-hh}" width="${s.w}" height="${s.h}" rx="6"/>`;
    } else if (s.kind === "set") {
      body = `<rect class="body" x="-24" y="-22" width="22" height="50" rx="6"/><rect class="body" x="2" y="-22" width="22" height="50" rx="6"/>`;
      accent = `<circle class="accent" cx="0" cy="-10" r="17"/>`;
    } else if (s.kind === "cabana") {
      body = `<rect class="body" x="${-hw}" y="${-hh}" width="${s.w}" height="${s.h}" rx="6"/>`;
      accent = `<rect class="accent" x="-28" y="-18" width="56" height="40" rx="9"/>`;
      seats = [[-hw, -hh], [hw, -hh], [-hw, hh], [hw, hh]].map(([x, y]) => `<circle class="seat" cx="${x}" cy="${y}" r="3.5"/>`).join("");
    }
    return `<rect class="hit" x="${-hw - 12}" y="${-hh - 12}" width="${s.w + 24}" height="${s.h + 24}" rx="12"/>${accent}${seats}${body}`;
  }

  function spotSVG(s, land) {
    const [x, y] = geom(land).T(s.x, s.y);
    const ty = s.kind === "set" ? 14 : s.kind === "cabana" ? 2 : 0;
    const sm = (s.kind === "set" || s.kind === "high") ? " sm" : "";
    return `<g class="spot" data-id="${s.id}" role="button" tabindex="0" transform="translate(${f1(x)} ${f1(y)})"><g${land ? ' transform="rotate(-90)"' : ""}>${shapeSVG(s)}</g><text class="code${sm}" x="${land ? ty : 0}" y="${land ? 0 : ty}">${esc(s.label)}</text></g>`;
  }

  // Pinch-zoom for the map alone (staff Floor): two fingers zoom around the
  // pinch point, one finger pans while zoomed (and scrolls the page when not),
  // taps on spots still work, "Reset" brings it back. The page never zooms.
  // The zoom sits on the <svg> itself, so redraws (live updates) keep it.
  function zoomable(wrap, svg) {
    const MAX = 4;
    let s = 1, tx = 0, ty = 0, g = null, moved = false;
    wrap.classList.add("zoomable");
    const reset = document.createElement("button");
    reset.type = "button"; reset.className = "map-reset"; reset.textContent = "Reset zoom"; reset.hidden = true;
    wrap.appendChild(reset);
    const pt = t => { const r = wrap.getBoundingClientRect(); return [t.clientX - r.left, t.clientY - r.top]; };
    function apply() {
      const W = wrap.clientWidth, H = wrap.clientHeight;
      s = Math.min(MAX, Math.max(1, s));
      tx = Math.min(0, Math.max(W - W * s, tx)); ty = Math.min(0, Math.max(H - H * s, ty));
      svg.style.transform = s > 1 ? `translate(${tx}px,${ty}px) scale(${s})` : "";
      wrap.classList.toggle("zoomed", s > 1); // zoomed: one finger pans the map instead of the page
      reset.hidden = s === 1;
    }
    function start(e) {
      const t = e.touches;
      if (t.length === 2) {
        const [a, b] = [pt(t[0]), pt(t[1])];
        g = { d: Math.hypot(a[0] - b[0], a[1] - b[1]) || 1, m: [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2], s, tx, ty };
      } else if (t.length === 1 && s > 1) g = { p: pt(t[0]), tx, ty };
      else g = null;
    }
    wrap.addEventListener("touchstart", e => { moved = false; start(e); }, { passive: true });
    wrap.addEventListener("touchmove", e => {
      if (!g) return;
      const t = e.touches;
      if (t.length === 2 && g.d) {
        const [a, b] = [pt(t[0]), pt(t[1])], m = [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2];
        s = g.s * Math.hypot(a[0] - b[0], a[1] - b[1]) / g.d;
        const k = Math.min(MAX, Math.max(1, s)) / g.s; // keep the point under the fingers put
        tx = m[0] - (g.m[0] - g.tx) * k; ty = m[1] - (g.m[1] - g.ty) * k;
      } else if (t.length === 1 && g.p) {
        const p = pt(t[0]);
        tx = g.tx + p[0] - g.p[0]; ty = g.ty + p[1] - g.p[1];
      } else return;
      if (e.cancelable) e.preventDefault();
      moved = true; apply();
    }, { passive: false });
    wrap.addEventListener("touchend", e => start(e)); // 2 -> 1 finger: carry on panning from here, no jump
    // A pinch or pan that ends on a spot isn't a tap on it.
    wrap.addEventListener("click", e => { if (moved) { e.stopPropagation(); e.preventDefault(); moved = false; } }, true);
    reset.addEventListener("click", e => { e.stopPropagation(); s = 1; tx = ty = 0; apply(); });
    return { reset: () => { s = 1; tx = ty = 0; apply(); } };
  }

  return { WP, HP, SIZE, place, height, background, spotSVG, geom, toWorld, esc, zoomable };
})();
