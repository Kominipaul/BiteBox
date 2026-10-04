(function () {
  // Countdowns: phase + time left for every row, from the device clock
  // (every 20s, no server calls). Late / ending rows get highlighted.
  function tick() {
    const now = Date.now(), n = { seated: 0, late: 0, soon: 0, ending: 0 };
    document.querySelectorAll("tr[data-bk]").forEach(tr => {
      const b = { status: tr.dataset.status, start: +tr.dataset.start, end: +tr.dataset.end };
      const ph = BookingTime.phase(b, now), cd = tr.querySelector("[data-cd]");
      if (ph === "ending") n.seated++;
      if (n[ph] !== undefined) n[ph]++;
      tr.classList.toggle("is-late", ph === "late");
      tr.classList.toggle("is-ending", ph === "ending");
      tr.classList.toggle("is-now", ph === "seated");
      cd.textContent = ph === "done" || (ph === "later" && b.start - now > 864e5) ? "" : BookingTime.text(b, now);
      cd.className = "cd cd-" + ph;
    });
    const sum = document.querySelector("[data-now-sum]");
    if (sum) {
      Object.keys(n).forEach(k => { const el = sum.querySelector(`[data-n="${k}"]`); el.textContent = n[k]; el.parentElement.hidden = !n[k]; });
      sum.hidden = !(n.seated || n.late || n.soon);
    }
  }
  tick(); setInterval(tick, 20000);
  document.body.addEventListener("htmx:afterSwap", e => { if (e.detail.target.id === "rows") tick(); });
  // Refresh only when a booking actually changes (no timed polling).
  Live("/admin/floor/ws", () => htmx.trigger("#rows", "refresh"));
  // Saved (the layer itself closes in bk_layout): refresh the list.
  document.body.addEventListener("bookingSaved", e => {
    const banner = document.getElementById("savedBanner");
    banner.querySelector("span").textContent = `Booking ${e.detail.value} saved.`;
    banner.hidden = false;
    clearTimeout(banner.t); banner.t = setTimeout(() => banner.hidden = true, 5000);
    htmx.trigger("#rows", "refresh");
  });
  // Arriving from the Floor ("Book this spot") or an old link.
  const q = new URLSearchParams(location.search);
  if (q.get("edit")) Layer.open(`/admin/bookings/${encodeURIComponent(q.get("edit"))}/edit`);
  else if (q.get("new")) {
    const pre = new URLSearchParams();
    ["table", "date", "time"].forEach(k => q.get(k) && pre.set(k, q.get(k)));
    Layer.open("/admin/bookings/new?" + pre);
  }
  if (q.get("new") || q.get("edit")) history.replaceState(null, "", location.pathname);
})();
