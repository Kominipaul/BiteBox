  // ---- settings tab rail: switches which .settings-pane is visible; the
  // htmx forms/toggles inside each pane are untouched — only their shared
  // wrapper's visibility changes, so hx-target ids (#djTogglePanel etc.)
  // keep working exactly as before. ----
  document.querySelectorAll('.settings-nav-item').forEach(item => {
    item.addEventListener('click', () => {
      document.querySelectorAll('.settings-nav-item').forEach(i => {
        i.classList.remove('active'); i.setAttribute('aria-selected', 'false');
      });
      document.querySelectorAll('.settings-pane').forEach(p => p.classList.remove('active'));
      item.classList.add('active');
      item.setAttribute('aria-selected', 'true');
      const pane = document.querySelector('.settings-pane[data-pane="' + item.dataset.pane + '"]');
      if (pane) pane.classList.add('active');
    });
  });

