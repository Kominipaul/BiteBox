  // Phone menu: slides in from the left; tap outside, swipe it away, or Esc closes.
  (function () {
    const side = document.getElementById("side"), scrim = document.getElementById("sideScrim"), btn = document.getElementById("menuBtn");
    const phone = () => innerWidth <= 960;
    let uninert = null;
    const isOpen = () => side.classList.contains("open");
    function set(open, instant) {
      if (open === isOpen()) return;
      if (instant) { side.classList.add("instant"); scrim.classList.add("instant"); requestAnimationFrame(() => { side.classList.remove("instant"); scrim.classList.remove("instant"); }); }
      side.classList.toggle("open", open); scrim.classList.toggle("open", open);
      btn.setAttribute("aria-expanded", open);
      if (open) { Sheet.lock(); uninert = Sheet.inertBackground(side); } else { Sheet.unlock(); if (uninert) { uninert(); uninert = null; } }
    }
    btn.onclick = () => set(true);
    document.getElementById("moreBtn").onclick = () => set(true);
    scrim.onclick = () => set(false);
    document.addEventListener("keydown", e => { if (e.key === "Escape") set(false); });
    // A link in the menu navigates: close as it goes, so Back doesn't return to an open menu.
    side.addEventListener("click", e => { if (e.target.closest("a[href]") && phone()) set(false, true); });
    Sheet.drag(side, { axis: "x", sign: -1, scrim, enabled: () => phone() && isOpen(), onDismiss: () => set(false, true) });
  })();
