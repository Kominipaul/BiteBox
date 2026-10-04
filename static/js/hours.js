// Recommended picker: picked dishes are chips; typing shows up to 12 more
// (built from the hidden pool, which the live updates keep current).
(function () {
  const input = document.getElementById('rpSearch'), out = document.getElementById('rpResults');
  function filter() {
    const q = input.value.trim().toLowerCase();
    out.innerHTML = '';
    const pool = document.getElementById('rpPool');
    if (!q || !pool) return;
    let n = 0;
    pool.content.querySelectorAll('.rp-chip').forEach(c => {
      if (n < 12 && c.dataset.search.toLowerCase().includes(q)) { out.appendChild(c.cloneNode(true)); n++; }
    });
    if (!n) out.innerHTML = '<small class="hours-note">No dish matches.</small>';
    htmx.process(out);
  }
  input.addEventListener('input', filter);
  new MutationObserver(filter).observe(document.getElementById('picksWrap'), { childList: true });
})();

// Menu form (templates/_service_menu_form.html, loaded into the side layer):
// day presets, whole kitchen/bar, and the dish picker (picked dishes always
// show; typing shows up to 12 matches). Delegated, since the form arrives
// after this file has run.
(function () {
  function filter(form) {
    const q = form.querySelector('.sm-dish-q');
    if (!q) return;
    const t = q.value.trim().toLowerCase(); let n = 0;
    form.querySelectorAll('.sm-dish').forEach(d => {
      const picked = d.querySelector('input').checked;
      const hit = t && d.dataset.search.toLowerCase().includes(t) && n < 12;
      d.hidden = !(picked || hit); if (hit) n++;
    });
  }
  document.addEventListener('click', e => {
    const days = e.target.closest('[data-sm-days]');
    if (days) {
      days.closest('form').querySelectorAll('input[name=days]').forEach(i => { i.checked = days.dataset.smDays.includes(i.value); });
    }
    const all = e.target.closest('[data-sm-all]');
    if (all) {
      all.closest('.sm-dept').querySelectorAll('input[name=categories]').forEach(i => { i.checked = all.dataset.smAll === 'on'; });
    }
  });
  document.addEventListener('input', e => {
    if (e.target.matches('.sm-dish-q')) filter(e.target.closest('.sm-form'));
  });
  document.addEventListener('change', e => {
    if (e.target.name === 'products' && e.target.closest('.sm-form')) filter(e.target.closest('.sm-form'));
  });
  htmx.onLoad(el => {
    const form = el.matches && el.matches('.sm-form') ? el : el.querySelector && el.querySelector('.sm-form');
    if (form) filter(form);
  });
})();
