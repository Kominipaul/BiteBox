/* Screen info: what the phone reports (safe areas, viewport, display mode), live.
   Open it from the menu ("Screen info") or with ?debug=1; tap it to hide.
   Diagnostic only: it never changes the layout. */
(function () {
  "use strict";
  var box = null, timer = 0, probe;
  function render() {
    if (!box) return;
    var vv = window.visualViewport || {}, st = getComputedStyle(probe);
    box.textContent = [
      "installed app: " + (navigator.standalone === true || matchMedia("(display-mode: standalone)").matches),
      "inner: " + innerWidth + "x" + innerHeight + "   visual: " + Math.round(vv.width || 0) + "x" + Math.round(vv.height || 0),
      "screen: " + screen.width + "x" + screen.height + "   dpr " + devicePixelRatio,
      "safe-area  top " + st.paddingTop + "  bottom " + st.paddingBottom,
      "scrollY " + Math.round(scrollY),
      "tap to hide"
    ].join("\n");
  }
  function toggle() {
    if (box) { box.remove(); probe.remove(); box = null; clearInterval(timer); return; }
    probe = document.createElement("div");
    probe.style.cssText = "position:fixed;left:0;top:0;visibility:hidden;pointer-events:none;padding:env(safe-area-inset-top,0px) 0 env(safe-area-inset-bottom,0px)";
    box = document.createElement("pre");
    box.style.cssText = "position:fixed;left:6px;top:calc(env(safe-area-inset-top,0px) + 6px);z-index:99999;margin:0;padding:8px 10px;border-radius:10px;background:rgba(0,0,0,.88);color:#7CFC98;font:11px/1.35 ui-monospace,Menlo,monospace;white-space:pre-wrap;max-width:92vw";
    box.onclick = toggle;
    document.body.append(probe, box);
    render(); timer = setInterval(render, 1000);
  }
  if (/[?&]debug\b/.test(location.search)) addEventListener("DOMContentLoaded", toggle);
  document.addEventListener("click", function (e) { if (e.target.closest && e.target.closest("[data-screeninfo]")) { e.preventDefault(); toggle(); } });
})();

/* Installed staff app only: no pinch or double-tap zoom, so it behaves like a
   native app. In a normal browser tab zoom still works (people who need
   bigger text keep it). iOS ignores "user-scalable=no" since iOS 10, so:
   touch-action lets the page scroll but not zoom (iOS 13+, Android), and
   Safari's own gesture events are cancelled for older iOS. */
(function () {
  "use strict";
  if (!(navigator.standalone === true || matchMedia("(display-mode: standalone)").matches)) return;
  document.documentElement.style.touchAction = "pan-x pan-y";
  ["gesturestart", "gesturechange"].forEach(function (t) {
    document.addEventListener(t, function (e) { e.preventDefault(); }, { passive: false });
  });
})();
