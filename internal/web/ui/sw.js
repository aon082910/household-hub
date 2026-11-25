// Household Hub service worker: keeps the app's own files for a fast start and an "installed app" feel on
// phones. Data (/api, /ics, /act, /display) always comes from the server; nothing private is cached.
const CACHE = 'hhub-shell-v1';
const SHELL = ['/', '/index.html', '/style.css', '/app.js', '/plan.js', '/biz.js', '/reports.js', '/home.js', '/security.js', '/docs.js', '/nav.js', '/winapp.js', '/money2.js', '/tax.js', '/receipt.js', '/icon.svg', '/icon-192.png', '/icon-512.png', '/manifest.webmanifest'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => Promise.all(SHELL.map((u) => c.add(u).catch(() => {})))).then(() => self.skipWaiting()));
});
self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener('fetch', (e) => {
  const u = new URL(e.request.url);
  if (e.request.method !== 'GET' || u.origin !== location.origin || /^\/(api|ics|act|display)\b/.test(u.pathname)) return;
  // network first so updates show at once; the cached copy only when offline
  e.respondWith(fetch(e.request).then((r) => {
    if (r.ok) { const copy = r.clone(); caches.open(CACHE).then((c) => c.put(e.request, copy)); }
    return r;
  }).catch(() => caches.match(e.request).then((m) => m || caches.match('/'))));
});
