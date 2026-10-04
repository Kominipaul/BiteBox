window.Layer = (function () {
  const layer = document.getElementById("bkModal"), panel = layer.querySelector(".layer-panel"), scrim = layer.querySelector(".layer-scrim"), body = document.getElementById("bkModalBody");
  const phone = () => innerWidth <= 760; // a bottom sheet on phones, a centred card elsewhere
  const isOpen = () => !layer.hidden && layer.classList.contains("open");
  let uninert = null, opener = null;

  async function open(url) {
    await htmx.ajax("GET", url, { target: "#bkModalBody", swap: "innerHTML" });
    reveal();
    const hash = url.split("#")[1];
    if (hash) { const el = body.querySelector("#" + CSS.escape(hash)); if (el) setTimeout(() => el.scrollIntoView({ block: "start", behavior: "smooth" }), 320); return; }
    const first = body.querySelector("input[name=name]");
    if (first && !first.value && !phone()) setTimeout(() => first.focus({ preventScroll: true }), 300);
  }

  // show(html): the same layer with content built in the page (Floor spot details).
  function show(html) { body.innerHTML = html; reveal(); }
  function reveal() {
    if (isOpen()) return;
    opener = document.activeElement;
    layer.hidden = false;
    Sheet.lock(); uninert = Sheet.inertBackground(layer);
    void layer.offsetWidth; // start the slide-in from the closed position
    layer.classList.add("open");
  }
  const isShowing = sel => isOpen() && !!body.querySelector(sel);

  // close(): slide out with the same soft curve as the way in. A swipe has
  // already carried it off-screen, so it hides instantly (instant = true).
  function close(instant) {
    if (!isOpen()) return;
    layer.classList.remove("open");
    Sheet.unlock(); if (uninert) { uninert(); uninert = null; }
    document.dispatchEvent(new Event("layer:close"));
    if (opener && opener.focus && document.contains(opener)) opener.focus({ preventScroll: true });
    const finish = () => { if (!layer.classList.contains("open")) { layer.hidden = true; body.innerHTML = ""; } };
    if (instant === true) {
      layer.classList.add("instant"); finish();
      requestAnimationFrame(() => layer.classList.remove("instant"));
      return;
    }
    let done = false; const end = () => { if (!done) { done = true; finish(); } };
    panel.addEventListener("transitionend", e => { if (e.target === panel) end(); }, { once: true });
    setTimeout(end, 480);
  }
  document.addEventListener("click", e => {
    const b = e.target.closest("[data-modal]");
    if (b) { e.preventDefault(); open(b.dataset.modal); return; }
    if (e.target.closest("[data-close]")) close();
  });
  document.addEventListener("keydown", e => { if (e.key === "Escape") close(); });
  // Swipe the whole sheet down (from anywhere, not just the header) on phones.
  Sheet.drag(panel, { axis: "y", sign: 1, scrim, enabled: () => phone() && isOpen(), onDismiss: () => close(true) });

  const toastEl = document.getElementById("toast");
  function toast(msg) {
    toastEl.textContent = msg; toastEl.hidden = false;
    requestAnimationFrame(() => toastEl.classList.add("show"));
    clearTimeout(toast.t); toast.t = setTimeout(() => { toastEl.classList.remove("show"); setTimeout(() => toastEl.hidden = true, 250); }, 3000);
  }
  document.body.addEventListener("bookingSaved", e => { close(); toast(`Booking ${e.detail.value} saved`); });
  document.body.addEventListener("productSaved", e => { close(); toast(e.detail.value); });
  document.body.addEventListener("staffSaved", e => { close(); toast(e.detail.value); });
  document.body.addEventListener("closeLayer", () => close()); // a save whose page updates itself live
  // A save the server refused (400/409…): show its message in the layer.
  document.body.addEventListener("htmx:responseError", e => {
    if (!e.detail.elt.closest || !e.detail.elt.closest(".layer")) return;
    const slot = body.querySelector("[data-error]");
    if (slot) { slot.textContent = e.detail.xhr.responseText || "Couldn't save."; slot.hidden = false; slot.scrollIntoView({ block: "nearest" }); }
  });
  // Walk-ins (booking form) are seated right now.
  document.addEventListener("change", e => {
    if (e.target.name !== "source" || e.target.value !== "walk_in") return;
    const f = e.target.form, d = new Date(), p = n => String(n).padStart(2, "0");
    f.date.value = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
    f.time.value = `${p(d.getHours())}:${p(d.getMinutes())}`;
  });
  // Password fields: the eye shows/hides what's typed.
  document.addEventListener("click", e => {
    const eye = e.target.closest(".pw-eye"); if (!eye) return;
    const inp = eye.parentElement.querySelector("input");
    inp.type = inp.type === "password" ? "text" : "password";
    eye.classList.toggle("on", inp.type === "text");
    eye.setAttribute("aria-label", inp.type === "text" ? "Hide password" : "Show password");
  });
  // Staff page access: "Custom" unlocks the ticks; off shows the role's defaults.
  document.addEventListener("change", e => {
    const form = e.target.form; if (!form || !form.querySelector(".access")) return;
    if (!e.target.matches(".access-custom, input[name=role], input[name=department]")) return;
    const custom = form.querySelector(".access-custom").checked, grid = form.querySelector(".access-grid");
    grid.classList.toggle("is-default", !custom);
    const role = form.querySelector("input[name=role]:checked, input[name=department]:checked");
    const defs = role ? (role.dataset.defaults || "").split(",") : [];
    grid.querySelectorAll("input").forEach(i => { i.disabled = !custom; if (!custom) i.checked = defs.includes(i.value); });
  });
  return { open, show, isShowing, close, toast };
})();

// Booking timing, shared by the Bookings list and the Floor: which phase a
// booking is in and how long is left, from its start/end (unix ms) and the
// device clock, so countdowns tick without asking the server.
window.BookingTime = {
  phase(b, now) {
    if (b.status === "seated") return now >= b.end ? "over" : b.end - now <= 15 * 60e3 ? "ending" : "seated";
    if (b.status === "confirmed") {
      if (now >= b.end) return "over";
      if (now >= b.start) return "late";
      return b.start - now <= 60 * 60e3 ? "soon" : "later";
    }
    return "done";
  },
  dur(ms) {
    const m = Math.max(0, Math.round(ms / 60e3));
    if (m < 60) return m + "m";
    if (m < 1440) return Math.floor(m / 60) + "h" + (m % 60 ? " " + String(m % 60).padStart(2, "0") + "m" : "");
    return Math.round(m / 1440) + "d";
  },
  text(b, now) {
    switch (this.phase(b, now)) {
      case "seated": case "ending": return this.dur(b.end - now) + " left";
      case "late": return "late " + this.dur(now - b.start);
      case "soon": case "later": return "in " + this.dur(b.start - now);
      case "over": return "time's up";
    }
    return "";
  },
};

// Live(path, onChange, onState): the server pings a websocket on every
// change; onChange re-fetches. Nothing is fetched while the tab is hidden —
// one catch-up when it's shown again. Reconnects with backoff; a reconnect
// also catches up, since pings may have been missed while it was down.
window.Live = function (path, onChange, onState) {
  let first = true, dirty = false, wait = 3000;
  const run = () => { if (document.hidden) dirty = true; else { dirty = false; onChange(); } };
  document.addEventListener("visibilitychange", () => { if (!document.hidden && dirty) run(); });
  (function connect() {
    const ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + path);
    ws.onopen = () => { wait = 3000; if (onState) onState(true); };
    ws.onmessage = () => { if (first) { first = false; return; } run(); };  // the first "hello": page is already fresh
    ws.onclose = e => {
      if (onState) onState(false);
      if (e.code === 4001) { location.href = "/login"; return; }
      first = false;
      setTimeout(connect, wait); wait = Math.min(wait * 2, 30000);
    };
  })();
};
