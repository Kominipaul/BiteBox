/* Sheet: native-feeling touch gestures for every sheet, drawer and banner.

   What makes it feel native rather than "web":
   - The panel tracks the finger 1:1 (translate3d, one write per frame), and
     the dimmed backdrop fades with it.
   - Letting go decides by distance AND flick velocity, then finishes with a
     decelerating curve from where your finger left it: no jump, no restart.
   - A sheet only takes over when you drag toward closing while its content is
     scrolled to the top; otherwise the content scrolls as normal. Horizontal
     or diagonal starts are left alone (chip rows, sliders).
   - Dragging the wrong way resists like a rubber band.
   - The page behind cannot scroll or be tapped while a sheet is open (scroll
     lock + inert), and focus returns where it was.
   - Respects "reduce motion". Touch only: on desktop it never gets in the way.

   API: Sheet.drag(panel, opts) · Sheet.lock() / Sheet.unlock() · Sheet.inertBackground(keepEl)
   opts: axis "y"|"x" (or a function), sign +1|-1 = the closing direction
   (down / right = +1; sign 0 = either way), scrim, enabled(), onDismiss(). */
(function () {
  "use strict";
  const EASE = "cubic-bezier(.32,.72,0,1)"; // iOS-style: fast start, long soft landing
  const reduce = matchMedia("(prefers-reduced-motion: reduce)");
  const clamp = (v, a, b) => Math.min(b, Math.max(a, v));
  const fn = v => typeof v === "function" ? v() : v;
  // How deliberate a swipe must be. Tuned against accidental closes: a small
  // wobble, a diagonal scroll or a quick twitch no longer counts.
  const SLOP = 10;      // px of movement before we decide tap / scroll / drag
  const DOMINANCE = 1.6; // the closing axis must win by this much over the other axis
  const RATIO = 0.4;    // dragged this share of the panel → close on release
  const FLICK = 0.6;    // px/ms: a fast flick closes early…
  const FLICK_MIN = 48; // …but only after this many px of real travel

  // ---------- Page scroll lock ----------
  // overflow:hidden on <html> keeps the scroll position (no jump) and works on
  // current iOS/Android; the touchmove guard covers the rest: anything outside
  // an open sheet panel can't be dragged around behind it.
  let locks = 0, undo = null, guard = null;
  function lock() {
    if (locks++) return;
    const de = document.documentElement, b = document.body;
    const gap = innerWidth - de.clientWidth, prev = { o: de.style.overflow, p: b.style.paddingRight, ob: b.style.overscrollBehavior };
    de.style.overflow = "hidden"; b.style.overscrollBehavior = "none";
    if (gap > 0) b.style.paddingRight = gap + "px"; // no layout shift when the scrollbar disappears (desktop)
    guard = e => { if (e.cancelable && !(e.target.closest && e.target.closest("[data-sheet-panel]"))) e.preventDefault(); };
    document.addEventListener("touchmove", guard, { passive: false });
    undo = () => { de.style.overflow = prev.o; b.style.paddingRight = prev.p; b.style.overscrollBehavior = prev.ob; document.removeEventListener("touchmove", guard); };
  }
  function unlock() {
    if (locks === 0 || --locks > 0) return;
    undo && undo(); undo = guard = null;
  }

  // ---------- Inert background (no taps, no focus, hidden from screen readers) ----------
  function inertBackground(keep) {
    const els = [...document.body.children].filter(c =>
      !/^(SCRIPT|LINK|STYLE|TEMPLATE)$/.test(c.tagName) && !c.contains(keep) && !keep.contains(c) && !c.hasAttribute("inert") && !c.matches("[data-sheet-keep]"));
    els.forEach(c => c.setAttribute("inert", ""));
    return () => els.forEach(c => c.removeAttribute("inert"));
  }

  // ---------- The drag ----------
  function scrollerOf(el, root, axis) {
    for (let n = el; n; n = n.parentElement) {
      const cs = getComputedStyle(n);
      const can = axis === "y" ? /(auto|scroll)/.test(cs.overflowY) && n.scrollHeight > n.clientHeight + 1
                               : /(auto|scroll)/.test(cs.overflowX) && n.scrollWidth > n.clientWidth + 1;
      if (can) return n;
      if (n === root) break;
    }
    return null;
  }
  // Can this scroller still scroll away from the closing direction? (i.e. is it at its start)
  function atStart(s, axis, sign) {
    if (axis === "y") return sign > 0 ? s.scrollTop <= 0 : sign < 0 ? s.scrollTop >= s.scrollHeight - s.clientHeight - 1 : true;
    return sign > 0 ? s.scrollLeft <= 0 : sign < 0 ? s.scrollLeft >= s.scrollWidth - s.clientWidth - 1 : true;
  }

  function drag(panel, o) {
    panel.setAttribute("data-sheet-panel", "");
    let st = null, busy = false, raf = 0;
    const axis = () => fn(o.axis) || "y";
    const sign = o.sign === undefined ? 1 : o.sign;
    const size = () => axis() === "y" ? panel.offsetHeight : panel.offsetWidth;
    const move = off => axis() === "y" ? `translate3d(0,${off}px,0)` : `translate3d(${off}px,0,0)`;
    const dur = ms => reduce.matches ? 1 : ms;

    // One write per frame. The touch "slop" (the first few px a browser eats
    // before it reports movement) would leave the panel permanently behind the
    // finger, so the engage offset (st.base) melts away over ~150ms: no jump,
    // and the panel ends up exactly under the finger.
    function paint(now) {
      raf = 0;
      if (!st || st.mode !== "drag") return;
      const dt = Math.min(64, now - (st.pt || now)); st.pt = now;
      st.base *= Math.exp(-dt / 55);
      if (Math.abs(st.base) < 0.4) st.base = 0;
      let off = st.main - st.base;
      if (sign !== 0 && Math.sign(off) === -sign) off = -sign * 56 * (1 - Math.exp(-Math.abs(off) / 140)); // rubber band
      st.off = off;
      panel.style.transform = move(off);
      if (o.scrim) o.scrim.style.opacity = String(1 - clamp(Math.abs(off) / size(), 0, 1) * 0.95);
      if (st.base !== 0) raf = requestAnimationFrame(paint);
    }

    panel.addEventListener("touchstart", e => {
      if (busy || e.touches.length !== 1 || (o.enabled && !o.enabled())) { st = null; return; }
      const t = e.touches[0], a = axis();
      st = { x0: t.clientX, y0: t.clientY, mode: "pending", off: 0, base: 0, a, scroller: scrollerOf(e.target, panel, a), pts: [[a === "y" ? t.clientY : t.clientX, e.timeStamp]] };
    }, { passive: true });

    panel.addEventListener("touchmove", e => {
      if (!st || st.mode === "skip") return;
      const t = e.touches[0], a = st.a;
      const main = a === "y" ? t.clientY - st.y0 : t.clientX - st.x0, cross = a === "y" ? t.clientX - st.x0 : t.clientY - st.y0;
      if (st.mode === "pending") {
        if (Math.abs(main) < SLOP && Math.abs(cross) < SLOP) return;                 // still a tap
        const closing = sign === 0 || Math.sign(main) === sign;
        const start = !st.scroller || atStart(st.scroller, a, sign || Math.sign(main));
        if (Math.abs(main) < Math.abs(cross) * DOMINANCE || !closing || !start) { st.mode = "skip"; return; } // let the content scroll
        st.mode = "drag"; st.base = st.main = main; st.pt = 0;                        // start where the panel is: no jump
        panel.style.transition = "none"; panel.style.willChange = "transform";
        if (o.scrim) o.scrim.style.transition = "none";
      }
      if (!e.cancelable) { st.mode = "skip"; snap(false, 0); return; }                // the browser took the gesture: back off
      e.preventDefault();
      st.main = main;
      st.pts.push([a === "y" ? t.clientY : t.clientX, e.timeStamp]);
      if (st.pts.length > 8) st.pts.shift();
      if (!raf) raf = requestAnimationFrame(paint);
    }, { passive: false });

    // px/ms along the drag axis over the last ~100ms. A finger that paused
    // before lifting has stopped: zero, like on iOS (so "drag, hold, let go"
    // never counts as a flick).
    function velocity(s, at) {
      const last = s.pts[s.pts.length - 1];
      if (at - last[1] > 80) return 0;
      let first = s.pts[0];
      for (const p of s.pts) if (last[1] - p[1] <= 100) { first = p; break; }
      const dt = last[1] - first[1];
      return dt > 0 ? (last[0] - first[0]) / dt : 0;
    }

    function release(cancelled, at) {
      const s = st; st = null;
      if (raf) { cancelAnimationFrame(raf); raf = 0; }
      if (!s || s.mode !== "drag") return;
      const dir = sign || Math.sign(s.off) || 1;
      const toward = s.off * dir, v = velocity(s, at) * dir, sz = size();
      const dismiss = !cancelled && (toward > sz * (o.ratio || RATIO) || (v > (o.flick || FLICK) && toward > FLICK_MIN));
      snap(dismiss, dismiss ? clamp((sz - toward) / Math.max(v, 1.1), 180, 340) : 380, dismiss ? dir * sz : 0);
    }
    panel.addEventListener("touchend", e => release(false, e.timeStamp));
    panel.addEventListener("touchcancel", e => release(true, e.timeStamp));

    // Finish from wherever the finger let go, with a soft landing.
    function snap(dismiss, ms, target) {
      busy = true;
      panel.style.transition = `transform ${dur(ms)}ms ${EASE}`;
      panel.style.transform = move(target || 0);
      if (o.scrim) { o.scrim.style.transition = `opacity ${dur(ms)}ms ${EASE}`; o.scrim.style.opacity = dismiss ? "0" : "1"; }
      let done = false;
      const end = () => {
        if (done) return; done = true;
        panel.removeEventListener("transitionend", te);
        if (dismiss) o.onDismiss && o.onDismiss(); // the caller hides it instantly: it's already off-screen
        panel.style.transition = panel.style.transform = panel.style.willChange = "";
        if (o.scrim) { o.scrim.style.transition = ""; o.scrim.style.opacity = ""; }
        busy = false;
      };
      const te = e => { if (e.target === panel && e.propertyName === "transform") end(); };
      panel.addEventListener("transitionend", te);
      setTimeout(end, dur(ms) + 90);
    }
    return { get busy() { return busy; } };
  }

  // Leaving the page with a sheet open (then coming back with Back, which may
  // restore this exact page from memory) must never leave it locked or inert.
  addEventListener("pagehide", () => {
    while (locks > 0) unlock();
    document.querySelectorAll("[inert]").forEach(e => e.removeAttribute("inert"));
  });

  window.Sheet = { drag, lock, unlock, inertBackground, EASE };
})();
