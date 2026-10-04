// Guide filters: role chips + level + search. Searching ignores the level and
// opens what matches, so a question always finds its answer.
(function () {
  const q = document.getElementById('gdQ'), none = document.getElementById('gdNone');
  const items = document.querySelectorAll('.gd, .gd-start');
  const val = n => document.querySelector('input[name=' + n + ']:checked').value;
  function apply() {
    const who = val('gdWho'), lv = val('gdLv'), t = q.value.trim().toLowerCase();
    let shown = 0;
    items.forEach(el => {
      const forWho = who === 'all' || el.dataset.for.split(' ').includes(who);
      const start = el.classList.contains('gd-start');
      let on;
      if (t) on = !start && forWho && el.textContent.toLowerCase().includes(t);
      else on = forWho && (lv === 'all' || el.dataset.lv === lv) && !(start && who === 'all');
      el.hidden = !on;
      if (on && t && el.tagName === 'DETAILS') el.open = true;
      if (on) shown++;
    });
    none.hidden = shown > 0;
  }
  document.querySelectorAll('input[name=gdWho], input[name=gdLv]').forEach(i => i.addEventListener('change', apply));
  q.addEventListener('input', apply);
  apply();
})();
