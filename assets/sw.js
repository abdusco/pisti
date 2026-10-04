const CACHE = 'pisti-shell-v5';
const SHELL = ['/', '/style.css', '/theme.js', '/app.js', '/connection.js', '/translations.js', '/vendor/alpine-3.15.0.js', '/vendor/qrcode-1.4.4.js', '/manifest.webmanifest', '/icons/icon.svg', '/icons/icon-maskable.svg', '/icons/icon-192.png', '/icons/icon-512.png', '/icons/icon-1024.png'];
self.addEventListener('install', event => event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(SHELL)).then(() => self.skipWaiting())));
self.addEventListener('activate', event => event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key.startsWith('pisti-shell-') && key !== CACHE).map(key => caches.delete(key)))).then(() => self.clients.claim())));
self.addEventListener('fetch', event => {
  const {request} = event, url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin || url.pathname.startsWith('/api/') || url.pathname === '/healthz') return;
  if (request.mode === 'navigate') { event.respondWith(caches.open(CACHE).then(async cache => (await cache.match('/')) || fetch(request))); return; }
  if (SHELL.includes(url.pathname)) event.respondWith(caches.open(CACHE).then(async cache => (await cache.match(url.pathname)) || fetch(request)));
});
