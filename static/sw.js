// Service worker for BiteBox Worker's phone alerts — a new order or a
// ready-for-pickup handoff shows up as a real, lock-screen-visible
// notification, the same way an SMS or a chat app's push does, even with
// the browser closed. Served at the origin root (see handlers.ServiceWorker)
// so its scope covers the whole app, not just /static/.
//
// Deliberately has no "fetch" handler — this isn't an offline cache, just
// a push/notification endpoint, so there's nothing else for it to do.

self.addEventListener('install', () => {
  self.skipWaiting(); // don't make staff close every open tab to pick up a sw.js update
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; }
  catch (e) { data = { title: 'BiteBox', body: event.data ? event.data.text() : '' }; }

  const title = data.title || 'BiteBox';
  const options = {
    body: data.body || '',
    icon: '/static/icons/icon-192.png',
    badge: '/static/icons/icon-192.png',
    tag: data.tag || 'bitebox-order',   // same tag replaces a still-unread notification for the same order instead of stacking duplicates
    renotify: true,                     // ...but still re-alerts (sound/vibrate) when that happens
    vibrate: [200, 100, 200],
    data: { url: data.url || '/worker' },
  };

  // Skip the OS notification entirely if this device already has the app
  // open and visible — someone actively looking at the live dashboard
  // already sees the order land in real time over the websocket; a
  // lock-screen-style popup on top of that tells them nothing they don't
  // already know, it's just noise.
  //
  // Checks both `focused` and `visibilityState`, not just `focused` alone
  // — iOS Safari has documented gaps in Service Worker Clients API fields,
  // and this app's own live testing shows the notification still firing
  // with the app on screen even with this check in place, so something
  // here isn't matching reality yet. The fetch() below is a TEMPORARY
  // diagnostic (see handlers.DebugPushClientState) reporting exactly what
  // this device saw at push time, straight to the server log — once
  // that's confirmed matching what actually happened on screen, both this
  // fetch() and the debug endpoint come back out.
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windowClients) => {
      const clientStates = windowClients.map((c) => ({
        url: c.url, focused: c.focused, visibilityState: c.visibilityState,
      }));
      fetch('/debug/push-client-state', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tag: data.tag, clientCount: windowClients.length, clients: clientStates }),
      }).catch(() => {}); // diagnostic only — never let this block real notification delivery

      const appIsOnScreen = windowClients.some(
        (client) => client.focused || client.visibilityState === 'visible'
      );
      if (appIsOnScreen) return;
      return self.registration.showNotification(title, options);
    })
  );
});

// Tapping the notification focuses an already-open dashboard tab if there
// is one, otherwise opens a fresh one — either way landing on /worker.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/worker';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windowClients) => {
      for (const client of windowClients) {
        if (client.url.includes(url) && 'focus' in client) return client.focus();
      }
      if (self.clients.openWindow) return self.clients.openWindow(url);
    })
  );
});
