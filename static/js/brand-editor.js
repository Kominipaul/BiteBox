(function () {
  const form = document.getElementById("themeForm");
  const PRESETS = JSON.parse(document.getElementById("pageData").textContent).presets;
  // The four mode basics, used when flipping Light/Dark by hand.
  const MODE = {
    dark:  { background: "#111317", surface: "#1B1E24", text: "#F2F3F5", muted: "#8C93A0" },
    light: { background: "#F5F6F8", surface: "#FFFFFF", text: "#15181D", muted: "#6B7280" },
  };
  const field = n => form.elements[n];
  const hex = /^#[0-9a-f]{6}$/i;
  let dirty = false;
  const markDirty = () => {
    dirty = true;
    document.getElementById("dirtyNote").hidden = false;
    document.getElementById("saveTheme").disabled = false;
    document.getElementById("discard").disabled = false;
  };
  const refresh = () => htmx.trigger(form, "change");

  function setValue(name, v) {
    const el = field(name);
    if (!el || v === undefined || v === null) return;
    if (el instanceof RadioNodeList) { for (const r of el) r.checked = r.value === v; return; }
    el.value = v;
    const pick = el.closest(".cin") && el.closest(".cin").querySelector("input[type=color]");
    if (pick && hex.test(v)) pick.value = v;
  }

  // Presets fill in every look setting (not the name, logos or texts).
  const LOOK = ["mode","primary","accent","background","surface","text","muted","success","warning","danger",
    "font_head","font_body","corners","buttons","shadows","header","login_layout","login_bg","login_from","login_to"];
  function apply(key) {
    const p = PRESETS[key]; if (!p) return;
    LOOK.forEach(k => setValue(k, p[k]));
    field("preset").value = key;
    document.querySelectorAll(".pcard").forEach(c => c.classList.toggle("is-on", c.dataset.preset === key));
    document.getElementById("basedName").textContent = document.querySelector(`.pcard[data-preset="${key}"] .pc-body b`).textContent;
    syncPreview(); markDirty(); refresh();
  }
  form.addEventListener("click", e => {
    const b = e.target.closest("[data-apply]");
    if (b) apply(b.dataset.apply);
  });
  document.getElementById("resetPreset").onclick = () => apply(field("preset").value);
  document.getElementById("discard").onclick = () => { dirty = false; location.reload(); };

  // Colour pickers and their hex boxes mirror each other.
  form.addEventListener("input", e => {
    const t = e.target, cin = t.closest(".cin");
    if (cin) {
      const [pick, text] = cin.querySelectorAll("input");
      if (t === pick) text.value = pick.value.toUpperCase();
      else { const ok = hex.test(text.value); text.classList.toggle("bad", !ok); if (ok) pick.value = text.value; }
      if (t === pick) htmx.trigger(form, "change");
    }
    if (t.dataset.live) syncPreview();
    markDirty();
  });
  form.addEventListener("change", e => {
    if (e.target.name === "mode") {
      const m = MODE[e.target.value], p = PRESETS[field("preset").value];
      const src = p && p.mode === e.target.value ? p : m;
      ["background","surface","text","muted"].forEach(k => setValue(k, src[k]));
    }
    if (e.target.name === "login_layout") document.querySelector(".pv-login").dataset.layout = e.target.value;
    if (e.isTrusted) markDirty();
  });

  // Texts in the preview follow the fields as you type.
  function syncPreview() {
    document.querySelectorAll("#pv [data-show]").forEach(el => {
      const src = form.querySelector(`[data-live="${el.dataset.show}"]`);
      if (src) el.textContent = src.value.trim() || el.dataset.empty || "";
    });
  }
  document.querySelectorAll(".pv-tabs button").forEach(b => b.onclick = () => {
    document.querySelectorAll(".pv-tabs button").forEach(x => x.classList.toggle("on", x === b));
    document.querySelectorAll("#pv .pv").forEach(p => p.hidden = p.dataset.pane !== b.dataset.pv);
  });

  // Uploads: stored right away, used by the theme once it's saved.
  form.addEventListener("change", async e => {
    if (e.target.type !== "file" || !e.target.files.length) return;
    const box = e.target.closest(".upl"), kind = box.dataset.kind;
    const fd = new FormData(); fd.append("kind", kind); fd.append("file", e.target.files[0]);
    box.classList.add("busy");
    try {
      const res = await fetch("/admin/system/upload", { method: "POST", body: fd });
      if (!res.ok) throw new Error(await res.text());
      const out = await res.json();
      setUpload(box, out.url);
      if (kind === "logo") field("logo_ratio").value = out.ratio.toFixed(3);
    } catch (err) { Layer.toast(err.message || "Upload failed"); }
    box.classList.remove("busy"); e.target.value = "";
  });
  form.addEventListener("click", e => {
    const rm = e.target.closest(".upl-rm");
    if (rm) setUpload(rm.closest(".upl"), "");
  });
  function setUpload(box, url) {
    const img = box.querySelector("img");
    img.hidden = !url; if (url) img.src = url; else img.removeAttribute("src");
    box.querySelector(".upl-rm").hidden = !url;
    box.querySelector("input[type=hidden]").value = url;
    markDirty(); refresh();
  }

  // Saved: the whole page picks up the new look straight away.
  document.body.addEventListener("themeSaved", e => {
    dirty = false;
    ["dirtyNote"].forEach(id => document.getElementById(id).hidden = true);
    document.getElementById("saveTheme").disabled = true;
    document.getElementById("discard").disabled = true;
    document.getElementById("savedLine").textContent = "Last " + e.detail.value.charAt(0).toLowerCase() + e.detail.value.slice(1);
    const link = document.querySelector('link[href^="/brand.css"]');
    link.href = "/brand.css?" + Date.now();
    document.querySelectorAll(".brand-name").forEach(n => { if (!n.closest("#pv")) n.textContent = field("venue_name").value; });
    Layer.toast("Look & brand saved");
  });
  document.body.addEventListener("htmx:responseError", e => {
    if (e.detail.elt.id === "saveTheme") Layer.toast(e.detail.xhr.responseText || "Could not save");
  });
  window.addEventListener("beforeunload", e => { if (dirty) { e.preventDefault(); e.returnValue = ""; } });
})();
