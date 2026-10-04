/* Behaviours that used to be inline HTML attributes (onerror=, onsubmit=,
   onclick=, hx-on). The Content-Security-Policy runs no inline script at all,
   so pages mark elements with data-* hooks and this file, loaded in <head>,
   handles them for the whole page, including content htmx swaps in later. */
(function () {
  "use strict";

  // <img data-fallback>: a photo that fails to load disappears instead of
  // showing a broken-image icon. data-fallback="card" also marks its menu
  // card as photo-less. Capture phase: error events don't bubble.
  function dropImage(img) {
    if (img.dataset.fallback === "card") {
      const card = img.closest(".item-card");
      if (card) card.classList.add("no-photo");
      const wrap = img.closest(".item-photo-wrap");
      if (wrap) { wrap.remove(); return; }
    }
    img.remove();
  }
  document.addEventListener("error", function (e) {
    const img = e.target;
    if (img instanceof HTMLImageElement && img.hasAttribute("data-fallback")) dropImage(img);
  }, true);
  // An image that failed before this ran (cached failure, slow script).
  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("img[data-fallback]").forEach(function (img) {
      if (img.complete && img.naturalWidth === 0 && img.getAttribute("src")) dropImage(img);
    });
  });

  // <form data-nosubmit>: live filter forms that htmx sends on input; Enter
  // must not also submit them as a page load.
  document.addEventListener("submit", function (e) {
    if (e.target.matches && e.target.matches("form[data-nosubmit]")) e.preventDefault();
  });

  // <button data-print>: print / save as PDF.
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("[data-print]")) window.print();
  });

  // [data-alert-errors]: show the server's error text when an htmx request
  // from inside it fails (e.g. a station that can't be closed right now).
  document.addEventListener("htmx:responseError", function (e) {
    const elt = e.detail && e.detail.elt;
    if (elt && elt.closest && elt.closest("[data-alert-errors]")) alert(e.detail.xhr.responseText);
  });
})();
