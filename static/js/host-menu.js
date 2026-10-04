  // Staff released this table: the server swaps in #session-end with where
  // to go (see handlers.endGuestSession).
  // Navigate once: later live updates (menu pushes) fire this event again,
  // and a second navigation would cancel the first one's redirect.
  let leaving = false;
  document.body.addEventListener('htmx:oobAfterSwap', () => {
    const end = document.getElementById('session-end');
    if (leaving || !end || !end.dataset.go) return;
    leaving = true;
    location.replace(end.dataset.go);
  });
  // ---- sections + search (client-only; re-applied after every live menu
  // update, since #product-list is replaced wholesale). A tab shows one
  // section; typing searches every dish (name, description, category),
  // skipping the Recommended / Popular copies so nothing shows twice. ----
  let activeCat = 'all';
  const searchInput = document.getElementById('menuSearch');
  function applyCategoryFilter(){
    const list = document.getElementById('product-list');
    if (!list) return;
    const q = (searchInput.value || '').trim().toLowerCase();
    const tabs = [...list.querySelectorAll('.cat-tab')];
    if (!tabs.some(t => t.dataset.cat === activeCat)) activeCat = 'all'; // section gone (its menu ended)
    tabs.forEach(t => { const on = t.dataset.cat === activeCat; t.classList.toggle('active', on); t.setAttribute('aria-selected', on); });
    list.classList.toggle('searching', !!q);
    let shown = 0;
    list.querySelectorAll('.menu-list').forEach(g => {
      let n = 0;
      g.querySelectorAll('.item-card').forEach(c => {
        const hit = !q || (c.dataset.search || '').toLowerCase().includes(q);
        c.style.display = hit ? '' : 'none';
        if (hit) n++;
      });
      const vis = q ? (g.dataset.top !== '1' && n > 0) : (activeCat === 'all' || g.dataset.cat === activeCat);
      g.style.display = vis ? '' : 'none';
      const head = g.previousElementSibling;
      if (head && head.classList.contains('menu-group-super')) head.style.display = vis ? '' : 'none';
      if (vis) shown += n;
    });
    document.getElementById('searchEmpty').hidden = !q || shown > 0;
  }
  document.body.addEventListener('click', (e) => {
    const tab = e.target.closest('#product-list .cat-tab');
    if (!tab) return;
    activeCat = tab.dataset.cat;
    if (searchInput.value) searchInput.value = '';
    applyCategoryFilter();
    const list = document.getElementById('product-list');
    const top = list.getBoundingClientRect().top + scrollY - (parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--hdr-h')) || 64) - 8;
    if (scrollY > top) scrollTo({ top, behavior: 'smooth' });
  });
  searchInput.addEventListener('input', applyCategoryFilter);
  document.body.addEventListener('htmx:afterSwap', applyCategoryFilter);
  document.body.addEventListener('htmx:oobAfterSwap', applyCategoryFilter);
  new MutationObserver(applyCategoryFilter).observe(document.getElementById('product-list'), { childList: true });
  // The sticky tab row sits right under the sticky header, whatever its height.
  const topbarEl = document.querySelector('.topbar');
  new ResizeObserver(() => document.documentElement.style.setProperty('--hdr-h', topbarEl.offsetHeight + 'px')).observe(topbarEl);

  // ---- toast ----
  function showToast(msg){
    // msg carries a product/customization name straight from a data-*
    // attribute — appended as a text node rather than interpolated into
    // innerHTML, so it's never parsed as markup no matter what an admin
    // (or anything upstream of one) named a product.
    const t = document.getElementById('toast');
    t.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg> ';
    t.appendChild(document.createTextNode(msg));
    t.classList.add('show');
    clearTimeout(showToast._timer);
    showToast._timer = setTimeout(() => t.classList.remove('show'), 2600);
  }

  // ---------------------------------------------------------------
  // Order alerts (guest-side Web Push) — a real lock-screen notification
  // when this table's order is confirmed/ready, the same mechanism the
  // worker dashboard's phone-alert toggle uses (see worker_app.html's
  // initPhoneAlerts), just guest-facing: no login, subscriptions key off
  // the anonymous session cookie server-side (see GuestPushSubscribe).
  //
  // iOS gotcha, unavoidable from here: Safari only allows a page to
  // subscribe to push once it's been added to the Home Screen and is
  // *running* from there (16.4+) — a plain Safari tab can't, permission
  // granted or not. So on iOS this shows one of two mutually-exclusive
  // things, never both: the "Add to Home Screen" banner in a plain tab, or
  // the real enable button once launched standalone. Android Chrome/Edge
  // and desktop browsers can subscribe directly from a normal tab, no
  // install step, so they only ever see the enable card.
  // ---------------------------------------------------------------
  (function initOrderAlerts(){
    const { vapid: VAPID_PUBLIC_KEY, table: TABLE_NUMBER } = JSON.parse(document.getElementById("pageData").textContent);
    const isIOS = /iP(hone|ad|od)/.test(navigator.userAgent);
    const isStandalone = navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;

    const notifyCard = document.getElementById('notifyCard');
    const enableBtn = document.getElementById('notifyEnableBtn');
    const dismissBtn = document.getElementById('notifyDismissBtn');
    const iosBanner = document.getElementById('iosInstallBanner');
    const iosDismissBtn = document.getElementById('iosBannerDismissBtn');
    const bell = document.getElementById('alertsBell');
    // The Service sheet's bell mirrors this: "on", "ask" (tap = enable) or
    // "ios" (tap = how to get them on iPhone). Stays hidden when unsupported.
    function bellState(st){
      bell.hidden = false; bell.dataset.state = st;
      bell.setAttribute('aria-label', st === 'on' ? 'Order alerts are on' : 'Turn on order alerts');
    }
    bell.addEventListener('click', () => {
      const st = bell.dataset.state;
      if (st === 'on') { showToast('Order alerts are on for this phone'); return; }
      if (window.svcHub) window.svcHub.show('orders');
      if (st === 'ios') iosBanner.style.display = '';
      else { notifyCard.style.display = ''; enableBtn.click(); }
    });

    // sessionStorage (not localStorage): "Not now" means "don't ask again
    // this visit" — the guest session cookie itself is also session-only
    // (no explicit expiry, see GuestSessionCookieName), so this fades the
    // same way a fresh table claim would anyway.
    function dismissed(key){ try { return sessionStorage.getItem(key) === '1'; } catch (e) { return false; } }
    function dismiss(key){ try { sessionStorage.setItem(key, '1'); } catch (e) {} }

    dismissBtn.addEventListener('click', () => { notifyCard.style.display = 'none'; dismiss('bitebox_notify_dismissed'); });
    iosDismissBtn.addEventListener('click', () => { iosBanner.style.display = 'none'; dismiss('bitebox_ios_banner_dismissed'); });

    // iOS in a plain browser tab: push is categorically unavailable here,
    // no amount of permission-prompting changes that — point at the actual
    // fix (install to Home Screen) instead of showing a button that would
    // just silently fail.
    if (isIOS && !isStandalone) {
      bellState('ios');
      if (!dismissed('bitebox_ios_banner_dismissed')) iosBanner.style.display = '';
      return;
    }

    // Everything past this point needs real push support. Fail quiet for
    // guests (unlike the staff dashboard's explicit disabled-with-reason
    // button) — a customer with an unsupported browser shouldn't be shown
    // a broken feature at all, just nothing.
    if (!window.isSecureContext) return; // Web Push is a secure-context-only API (localhost exempted for local testing)
    if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) return;
    if (!VAPID_PUBLIC_KEY) return; // InitPush hasn't produced a key yet (server-side issue, not this browser's)

    function urlBase64ToUint8Array(base64String){
      const padding = '='.repeat((4 - base64String.length % 4) % 4);
      const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
      const rawData = atob(base64);
      const out = new Uint8Array(rawData.length);
      for (let i = 0; i < rawData.length; i++) out[i] = rawData.charCodeAt(i);
      return out;
    }

    let swRegistration = null;
    navigator.serviceWorker.register('/sw.js')
      .then(reg => { swRegistration = reg; return reg.pushManager.getSubscription(); })
      .then(sub => { // already subscribed on this device: nothing to ask for
        bellState(sub ? 'on' : 'ask');
        if (!sub && !dismissed('bitebox_notify_dismissed')) notifyCard.style.display = '';
      })
      .catch(() => {}); // registration failing just means no card shows; nothing actionable to tell a guest

    // The actual permission prompt — only ever fires from this click
    // handler, i.e. only after a guest has tapped a button whose label and
    // the card's own copy above it already explained *why* ("get notified
    // when your order is ready"). iOS users in particular tend to
    // permanently deny a blind/immediate permission popup, and there's no
    // second chance once denied — a browser never re-prompts after "Block."
    enableBtn.addEventListener('click', async () => {
      enableBtn.disabled = true;
      try {
        const permission = await Notification.requestPermission();
        if (permission !== 'granted') {
          showToast('Notifications are blocked. Allow them for this site in your browser settings to get order alerts');
          enableBtn.disabled = false;
          return;
        }
        const reg = swRegistration || await navigator.serviceWorker.ready;
        const sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(VAPID_PUBLIC_KEY),
        });
        await fetch('/table/' + TABLE_NUMBER + '/push/subscribe', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(sub.toJSON()),
        });
        notifyCard.style.display = 'none'; bellState('on');
        showToast('Order alerts enabled. We’ll notify you when it’s ready');
      } catch (e) {
        showToast('Could not enable order alerts on this device');
        enableBtn.disabled = false;
      }
    });
  })();

  // ---- confirm dialog: replaces the native browser confirm() for every
  // hx-confirm on this page (leave table, place/cancel order, cancel/
  // refund a placed order) with one that matches the site's own look.
  // htmx fires htmx:confirm for *every* request; e.detail.question is only
  // set when that element actually carries hx-confirm, so anything else
  // (e.g. plain hx-get/hx-post with no confirm attribute) passes through
  // untouched. ----
  const confirmOverlay = document.getElementById('confirmOverlay');
  const confirmMessage = document.getElementById('confirmMessage');
  let pendingConfirmResolve = null;
  function closeConfirmDialog(result){
    confirmOverlay.classList.remove('show');
    if (!cartSheet.classList.contains('show') && !customizeOverlay.classList.contains('show')) {
      document.body.style.overflow = '';
    }
    const resolve = pendingConfirmResolve;
    pendingConfirmResolve = null;
    if (resolve) resolve(result);
  }
  document.getElementById('confirmOkBtn').addEventListener('click', () => closeConfirmDialog(true));
  document.getElementById('confirmCancelBtn').addEventListener('click', () => closeConfirmDialog(false));
  confirmOverlay.addEventListener('click', (e) => { if (e.target === confirmOverlay) closeConfirmDialog(false); });
  document.body.addEventListener('htmx:confirm', (e) => {
    if (!e.detail.question) return;
    e.preventDefault();
    confirmMessage.textContent = e.detail.question;
    confirmOverlay.classList.add('show');
    document.body.style.overflow = 'hidden';
    pendingConfirmResolve = (ok) => { if (ok) e.detail.issueRequest(true); };
  });

  // ---- menu grid: quick add, customize modal, add-to-order-with-
  // customization (a real /cart/add request, built as a throwaway form so
  // repeated "excluded"/"extras" fields serialize correctly) ----
  function submitCartAction(url, productId, excluded, extras){
    const form = document.createElement('form');
    const pid = document.createElement('input');
    pid.type = 'hidden'; pid.name = 'product_id'; pid.value = productId;
    form.appendChild(pid);
    (excluded || []).forEach(name => {
      const inp = document.createElement('input');
      inp.type = 'hidden'; inp.name = 'excluded'; inp.value = name;
      form.appendChild(inp);
    });
    (extras || []).forEach(name => {
      const inp = document.createElement('input');
      inp.type = 'hidden'; inp.name = 'extras'; inp.value = name;
      form.appendChild(inp);
    });
    document.body.appendChild(form);
    htmx.ajax('POST', url, { source: form, target: '#cart-summary', swap: 'innerHTML' })
      .then(() => form.remove());
  }

  // Delegated from document.body, not bound to #product-list directly: any
  // menu websocket push OOB-replaces that whole div (outerHTML, a new DOM
  // node) — including the very next one after this server-rendered first
  // paint — which would silently orphan a listener attached straight to it.
  document.body.addEventListener('click', (e) => {
    const card = e.target.closest('#product-list .item-card');
    if (!card) return;

    const addBtn = e.target.closest('.add-btn');
    if (addBtn && !addBtn.disabled) {
      showToast(`Added ${card.dataset.name}`);
      addBtn.classList.remove('flash'); void addBtn.offsetWidth; addBtn.classList.add('flash');
      return;
    }
    if (e.target.closest('.customize-link')) {
      const btn = e.target.closest('.customize-link');
      openCustomizeModal(card, btn.dataset.productId, btn.dataset.name, btn.dataset.price);
    }
  });

  // ---- customize modal ----
  const customizeOverlay = document.getElementById('customizeOverlay');
  const customizeChips = document.getElementById('customizeChips');
  let customizeProductId = null;
  let customizeBasePrice = 0;

  // Recomputes the header price as base + whichever extras are currently
  // "included" — called on open and again on every chip tap, so ticking a
  // priced extra on/off updates what the guest sees they'll pay before they
  // ever hit "Add to order", not just after (the actual price charged is
  // still always server-recomputed at add time, see validCustomization in
  // cart.go — this is purely the modal's own live preview of that).
  function updateCustomizePriceDisplay(){
    let total = customizeBasePrice;
    customizeChips.querySelectorAll('.ing-chip[data-kind="extra"].included').forEach(c => {
      total += parseFloat(c.dataset.price) || 0;
    });
    document.getElementById('customizePrice').textContent = '€' + total.toFixed(2);
  }

  function openCustomizeModal(card, productId, name, price){
    customizeProductId = productId;
    customizeBasePrice = parseFloat(price) || 0;
    document.getElementById('customizeTitle').textContent = name;
    const source = card.querySelector('.customize-source');
    customizeChips.innerHTML = source ? source.innerHTML : '';
    updateCustomizePriceDisplay();
    customizeOverlay.classList.add('show');
    document.body.style.overflow = 'hidden';
  }
  function closeCustomizeModal(){
    customizeOverlay.classList.remove('show');
    if (!cartSheet.classList.contains('show')) document.body.style.overflow = '';
    customizeProductId = null;
  }
  customizeChips.addEventListener('click', (e) => {
    // Removable ingredients start "on" (green) — tapping excludes them.
    // Extra ingredients start "off" (blue) — tapping includes them (green,
    // same "on your order" color as an included removable one).
    const chip = e.target.closest('.ing-chip');
    if (!chip) return;
    if (chip.dataset.kind === 'extra') chip.classList.toggle('included');
    else chip.classList.toggle('excluded');
    updateCustomizePriceDisplay();
  });
  document.getElementById('addCustomBtn').addEventListener('click', () => {
    if (!customizeProductId) return;
    const excluded = Array.from(customizeChips.querySelectorAll('.ing-chip[data-kind="removable"].excluded')).map(c => c.dataset.ing);
    const extras = Array.from(customizeChips.querySelectorAll('.ing-chip[data-kind="extra"].included')).map(c => c.dataset.ing);
    submitCartAction('/cart/add', customizeProductId, excluded, extras);
    showToast(`Added ${document.getElementById('customizeTitle').textContent}`);
    closeCustomizeModal();
  });
  document.getElementById('closeCustomizeBtn').addEventListener('click', closeCustomizeModal);
  swipeToClose(document.getElementById('customizeModal'), { close: closeCustomizeModal, also: [customizeOverlay],
    enabled: () => customizeOverlay.classList.contains('show') && innerWidth < 600 });
  customizeOverlay.addEventListener('click', (e) => { if (e.target === customizeOverlay) closeCustomizeModal(); });

  // ---- "Your order" line stepper: +/- re-targets the exact customization
  // variant via its data-excluded/data-extras snapshot, not just the
  // product. ----
  document.body.addEventListener('click', (e) => {
    const btn = e.target.closest('#cart-summary .ol-btn');
    if (!btn) return;
    const line = btn.closest('.order-line');
    const productId = line.dataset.productId;
    let excluded = [], extras = [];
    try { excluded = JSON.parse(line.dataset.excluded || '[]'); } catch (err) {}
    try { extras = JSON.parse(line.dataset.extras || '[]'); } catch (err) {}
    const url = btn.dataset.action === 'inc' ? '/cart/add' : '/cart/remove';
    submitCartAction(url, productId, excluded, extras);
  });

  // ---- payment method chips ----
  document.body.addEventListener('click', (e) => {
    const chip = e.target.closest('#cart-summary .pay-method-chip');
    if (!chip) return;
    document.querySelectorAll('.pay-method-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    document.getElementById('paymentMethodInput').value = chip.dataset.method;
    const label = document.getElementById('placeOrderLabel');
    if (label) {
      label.textContent = chip.dataset.method === 'cash' ? 'Place order · Pay cash' : 'Place order · Pay by card';
    }
  });


  // ---- Swipe to close (same engine as the staff app, static/sheet.js) ----
  // The sheet follows the finger; a flick or a long enough drag closes it,
  // anything else springs back. After a swipe the sheet is already off
  // screen, so the close itself happens without a second animation.
  function closeInstantly(els, close){
    els.forEach(el => el.classList.add('sheet-instant'));
    close();
    requestAnimationFrame(() => requestAnimationFrame(() => els.forEach(el => el.classList.remove('sheet-instant'))));
  }
  function swipeToClose(panel, opts){
    if (!window.Sheet || !matchMedia('(pointer: coarse)').matches) return;
    Sheet.drag(panel, { axis: 'y', sign: 1, scrim: opts.scrim, enabled: opts.enabled,
      onDismiss: () => closeInstantly([panel].concat(opts.scrim ? [opts.scrim] : []).concat(opts.also || []), opts.close) });
  }

  // ---- Service (floating button + tabbed sheet) ----
  // Waiter call, your orders and song requests in one place. The button
  // mirrors what's going on: gold + ring while a waiter call waits, a green
  // tick once staff answer, a count of your active orders (green once one is
  // ready). The Song tab only exists while the DJ feature is on.
  (function initService(){
    const fab = document.getElementById('svcFab'), label = document.getElementById('svcFabLabel'),
          count = document.getElementById('svcFabCount'), tabCount = document.getElementById('ordersCount'),
          sheet = document.getElementById('svcSheet'), overlay = document.getElementById('svcOverlay'),
          panel = document.getElementById('assist-panel'), orders = document.getElementById('placed-orders'),
          dj = document.getElementById('dj-section'), song = document.getElementById('song-status'),
          tabs = [...sheet.querySelectorAll('.hub-tab')], musicTab = sheet.querySelector('[data-tab="music"]');
    let lastState = null, ackTimer = 0, ready = 0;
    const isOpen = () => sheet.classList.contains('show');

    function show(name){
      tabs.forEach(t => t.setAttribute('aria-selected', String(t.dataset.tab === name)));
      sheet.querySelectorAll('.hub-panel').forEach(p => { p.hidden = p.dataset.panel !== name; });
    }
    tabs.forEach(t => t.addEventListener('click', () => show(t.dataset.tab)));
    function open(tab){
      // Opens where it matters most: the waiting call, else a ready order.
      show(tab || (state() === 'pending' ? 'waiter' : ready ? 'orders' : 'waiter'));
      overlay.classList.add('show'); sheet.classList.add('show'); fab.setAttribute('aria-expanded', 'true');
    }
    function close(){
      overlay.classList.remove('show'); sheet.classList.remove('show'); fab.setAttribute('aria-expanded', 'false');
      // "On the way" is a one-off: next time the Waiter tab is the form again.
      const b = panel.querySelector('.as-banner'); if (b) { b.remove(); sync(); }
    }
    const state = () => { const el = panel.querySelector('[data-state]'); return el ? el.dataset.state : 'idle'; };
    function age(){
      const el = panel.querySelector('[data-since]'), out = panel.querySelector('[data-assist-age]');
      if (!el || !out) return;
      const m = Math.floor((Date.now() - +el.dataset.since) / 60000);
      out.textContent = m < 1 ? 'just now' : m === 1 ? '1 min ago' : m + ' min ago';
    }
    // Orders the guest opened or closed stay that way through live updates
    // (each one re-renders the whole list); untouched ones follow the server
    // default (only the newest order in progress open).
    const openState = new Map();
    orders.addEventListener('toggle', (e) => {
      const d = e.target;
      if (d.matches && d.matches('details.po')) openState.set(d.dataset.id, d.open);
    }, true);
    function restoreOpen(){
      orders.querySelectorAll('details.po').forEach(d => { if (openState.has(d.dataset.id)) d.open = openState.get(d.dataset.id); });
    }
    function sync(){
      restoreOpen();
      const st = panel.querySelector('.as-banner') ? 'ack' : state();
      const active = orders.querySelectorAll('.po[data-status="pending"],.po[data-status="preparing"],.po[data-status="ready"]').length;
      const nowReady = orders.querySelectorAll('.po[data-status="ready"]').length;
      if (nowReady > ready && lastState !== null && !isOpen()) showToast('Your order is ready');
      ready = nowReady;
      fab.classList.toggle('is-pending', st === 'pending');
      fab.classList.toggle('is-ack', st === 'ack');
      label.textContent = st === 'pending' ? 'Waiter called' : st === 'ack' ? 'On the way' : ready ? 'Order ready' : 'Service';
      [count, tabCount].forEach(c => { c.hidden = !active; c.textContent = active; c.classList.toggle('ready', ready > 0); });
      fab.setAttribute('aria-label', label.textContent + (active ? `, ${active} active order${active > 1 ? 's' : ''}` : '') + '. Open table service');
      if (st === 'ack' && lastState === 'pending' && !isOpen()) showToast('A waiter is on the way');
      if (st === 'idle' && lastState === 'pending') { showToast('Request cancelled'); if (isOpen()) close(); }
      clearTimeout(ackTimer);
      if (st === 'ack') ackTimer = setTimeout(() => { fab.classList.remove('is-ack'); if (!isOpen()) { const b = panel.querySelector('.as-banner'); if (b) b.remove(); } lastState = 'idle'; sync(); }, 8000);
      lastState = st; age();
      // Song tab: only while there's a request form (DJ on) or an answer to show.
      const hasMusic = !!(dj.querySelector('section') || song.querySelector('section'));
      musicTab.hidden = !hasMusic;
      if (!hasMusic && musicTab.getAttribute('aria-selected') === 'true') show('waiter');
    }
    // Orders tab bar: "Need help?" goes to the Waiter tab (pick a reason);
    // "Call waiter" calls straight away (general help) unless one's waiting.
    sheet.addEventListener('click', (e) => {
      if (e.target.closest('[data-go-waiter]')) show('waiter');
      const q = e.target.closest('[data-quick-call]');
      if (!q) return;
      if (state() === 'pending') { show('waiter'); return; }
      q.disabled = true;
      htmx.ajax('POST', '/table/' + JSON.parse(document.getElementById("pageData").textContent).table + '/assist', { target: '#assist-panel', swap: 'innerHTML', values: { reason: 'help' } })
        .then(() => show('waiter')).finally(() => { q.disabled = false; });
    });
    window.svcHub = { open, show };
    swipeToClose(sheet, { scrim: overlay, close, enabled: () => isOpen() && innerWidth < 600 });
    fab.addEventListener('click', () => isOpen() ? close() : open());
    overlay.addEventListener('click', close);
    sheet.addEventListener('click', (e) => { if (e.target.closest('[data-assist-close]')) close(); });
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && isOpen()) close(); });
    // Every update (a button's reply, or anything arriving over the sockets,
    // which fire no htmx swap event of their own) re-syncs the button.
    const mo = new MutationObserver(() => sync());
    [panel, orders, dj, song].forEach(el => mo.observe(el, { childList: true }));
    document.body.addEventListener('htmx:responseError', (e) => {
      if (panel.contains(e.detail.elt)) showToast("Couldn't reach the staff. Try again, or wave someone over.");
    });
    setInterval(age, 30000);
    sync();
    // Just placed an order (checkout lands on ?placed=1): show it straight away.
    if (new URLSearchParams(location.search).has('placed')) { history.replaceState(null, '', location.pathname); open('orders'); }
  })();

  // A menu ended or the kitchen closed: the server took those lines out of
  // the cart (see CartHandlers.Summary) and says which.
  document.body.addEventListener('cartRemoved', (e) => showToast(e.detail.value));

  // ---- cart bar + cart sheet ----
  const cartSheet = document.getElementById('cartSheet');
  const cartSheetOverlay = document.getElementById('cartSheetOverlay');
  function openCartSheet(){
    cartSheetOverlay.classList.add('show');
    cartSheet.classList.add('show');
    document.body.style.overflow = 'hidden';
  }
  function closeCartSheet(){
    cartSheetOverlay.classList.remove('show');
    cartSheet.classList.remove('show');
    if (!customizeOverlay.classList.contains('show')) document.body.style.overflow = '';
  }
  document.body.addEventListener('click', (e) => {
    if (e.target.closest('#cart-bar .cart-bar-info') || e.target.closest('#cart-bar #openCartBtn')) openCartSheet();
  });
  document.getElementById('closeCartBtn').addEventListener('click', closeCartSheet);
  swipeToClose(cartSheet, { scrim: cartSheetOverlay, close: closeCartSheet, enabled: () => cartSheet.classList.contains('show') });
  cartSheetOverlay.addEventListener('click', closeCartSheet);
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (confirmOverlay.classList.contains('show')) closeConfirmDialog(false);
    else if (customizeOverlay.classList.contains('show')) closeCustomizeModal();
    else if (cartSheet.classList.contains('show')) closeCartSheet();
  });
  // A placed order (checkout) navigates away via HX-Redirect; cancelling the
  // cart re-renders #cart-summary empty, which also hides the bar (see
  // _cart_summary.html) — close the sheet to match.
  document.body.addEventListener('htmx:afterRequest', (e) => {
    if (e.detail.successful && e.detail.requestConfig && e.detail.requestConfig.path === '/cart/clear') closeCartSheet();
  });

  // ---- DJ tip presets + custom amount. Delegated from document.body, not
  // bound directly to the tip chips/input: #dj-section is server-rendered
  // for first paint now, but a DJ-toggle mid-service still replaces it
  // wholesale via an OOB websocket push (see _dj_section.html), which would
  // orphan a direct listener bound at page-load time — binding directly
  // here would also throw on a venue with DJ requests off (no elements to
  // find yet), which (being a plain top-level <script>, no try/catch) would
  // silently abort every listener registered *after* this block too. Same
  // class of bug already documented in TODO.md for #product-list; fixed
  // the same way. ----
  function updateDjSubmitLabel(){
    const tipAmountInput = document.getElementById('tipAmountInput');
    const djSubmit = document.getElementById('djSubmit');
    if (!tipAmountInput || !djSubmit) return;
    const amount = parseFloat(tipAmountInput.value) || 0;
    djSubmit.textContent = `Send request & pay €${amount.toFixed(2)}`;
  }
  document.body.addEventListener('click', (e) => {
    const chip = e.target.closest('.tip-chip');
    if (!chip) return;
    document.querySelectorAll('.tip-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    const customTipWrap = document.getElementById('customTipWrap');
    const customTipInput = document.getElementById('customTipInput');
    const tipAmountInput = document.getElementById('tipAmountInput');
    if (chip.dataset.tip === 'custom') {
      customTipWrap.classList.add('show');
      const val = parseFloat(customTipInput.value);
      tipAmountInput.value = val > 0 ? val : 0;
      customTipInput.focus();
    } else {
      customTipWrap.classList.remove('show');
      tipAmountInput.value = chip.dataset.tip;
    }
    updateDjSubmitLabel();
  });
  document.body.addEventListener('input', (e) => {
    if (e.target.id !== 'customTipInput') return;
    const val = parseFloat(e.target.value);
    document.getElementById('tipAmountInput').value = val > 0 ? val : 0;
    updateDjSubmitLabel();
  });

  // ---- Order tip presets + custom amount — a separate chip group/ids from
  // the DJ tip block above (order-tip-chip vs. tip-chip alone) so the two
  // don't cross-talk: reused the same .tip-chip CSS, scoped the query/state
  // to .order-tip-chip so clicking one group never toggles "active" on the
  // other or overwrites the other's hidden input. Same delegated-from-body
  // reasoning as the DJ block: #cart-summary is wholesale-replaced by every
  // checkout/cancel/nudge-triggered re-render. ----
  document.body.addEventListener('click', (e) => {
    const chip = e.target.closest('.order-tip-chip');
    if (!chip) return;
    document.querySelectorAll('.order-tip-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    const wrap = document.getElementById('orderCustomTipWrap');
    const customInput = document.getElementById('orderCustomTipInput');
    const tipInput = document.getElementById('orderTipInput');
    if (chip.dataset.tip === 'custom') {
      wrap.classList.add('show');
      const val = parseFloat(customInput.value);
      tipInput.value = val > 0 ? val : 0;
      customInput.focus();
    } else {
      wrap.classList.remove('show');
      tipInput.value = chip.dataset.tip;
    }
  });
  document.body.addEventListener('input', (e) => {
    if (e.target.id !== 'orderCustomTipInput') return;
    const val = parseFloat(e.target.value);
    document.getElementById('orderTipInput').value = val > 0 ? val : 0;
  });
  // Clear the song field once a request is successfully sent (the response
  // itself, swapped into #djConfirm, carries the success/failure message).
  document.body.addEventListener('htmx:afterRequest', (e) => {
    if (e.detail.elt && e.detail.elt.id === 'djSubmit' && e.detail.successful) {
      const input = document.getElementById('songInput');
      if (input) input.value = '';
    }
  });
