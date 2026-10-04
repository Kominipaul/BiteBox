(function () {
  let role = "";
  function apply() {
    const q = (document.getElementById("staffSearch").value || "").trim().toLowerCase();
    document.querySelectorAll("#staff-list .srow-wrap").forEach(r => {
      const off = r.dataset.active !== "true";
      const okRole = role === "" ? true : role === "off" ? off : r.dataset.role === role && !off;
      r.hidden = !(okRole && (!q || r.dataset.name.toLowerCase().includes(q)));
    });
  }
  document.querySelectorAll(".sfilters .menu-cat-tab").forEach(b => b.onclick = () => {
    document.querySelectorAll(".sfilters .menu-cat-tab").forEach(x => x.classList.toggle("active", x === b));
    role = b.dataset.role; apply();
  });
  document.getElementById("staffSearch").addEventListener("input", apply);
  document.body.addEventListener("htmx:afterSwap", e => { if (e.detail.target.id === "staff-list") apply(); });
})();
