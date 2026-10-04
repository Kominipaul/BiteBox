  // Export what the filters currently show.
  const exp = document.getElementById("exportCsv");
  if (exp) exp.addEventListener("click", e => {
    e.currentTarget.href = "/admin/export.csv?" + new URLSearchParams(new FormData(document.getElementById("orderFilters")));
  });
  // Rows open the order panel with Enter too.
  document.addEventListener("keydown", e => {
    const r = e.target.closest && e.target.closest("tr.orow");
    if (r && e.key === "Enter") Layer.open(r.dataset.modal);
  });
