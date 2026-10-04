  // Forms marked data-reset-on-success clear after a successful htmx submit
  // (the CSP blocks inline hx-on handlers, which need eval).
  document.body.addEventListener('htmx:afterRequest', (e) => {
    const form = e.detail.elt;
    if (!e.detail.successful || !form.matches || !form.matches('form[data-reset-on-success]')) return;
    form.reset();
    const preview = form.querySelector('.photo-preview-thumb');
    if (preview) { preview.hidden = true; preview.src = ''; }
  });
  // ---- kicked out live: the server closes any of our websockets with
  // close code 4001 the moment our own account gets deactivated (checked
  // every few seconds server-side), so we don't sit on a stale page ----
  document.body.addEventListener('htmx:wsClose', (e) => {
    if (e.detail && e.detail.event && e.detail.event.code === 4001) {
      window.location.href = '/login';
    }
  });
