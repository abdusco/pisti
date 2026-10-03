export function restoreSession(search, saved) {
  if (!saved?.token || !saved?.room) return null;
  const params = new URLSearchParams(search);
  if (params.has('room')) return params.get('room').toUpperCase() === saved.room.toUpperCase() ? saved : null;
  return params.get('resume') === '1' ? saved : null;
}

// One attempt, one deadline, one retry. All callbacks belong to a generation.
export class LiveConnection {
  constructor({token, onState, onReady, onExpire, env = globalThis}) {
    Object.assign(this, {token, onState, onReady, onExpire, env});
    this.generation = 0;
    this.backoff = 250;
    this.sequence = -1;
    this.ready = false;
    this.stopped = false;
    this.lastMessage = 0;
    this.connectStarted = 0;
    this.socket = this.retry = this.deadline = this.diagnostic = null;
  }
  now() { return this.env.Date.now(); }
  online() { return this.env.navigator.onLine !== false; }
  setReady(value) { this.ready = value; this.onReady(value); }
  start() {
    this.watch = this.env.setInterval(() => {
      if (this.env.document.visibilityState === 'visible') this.recover();
    }, 5000);
    this.connect();
  }
  clearAttempt() {
    this.generation++;
    this.env.clearTimeout(this.deadline);
    this.deadline = null;
    const socket = this.socket;
    this.socket = null;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      socket.close();
    }
  }
  connect() {
    if (this.stopped || !this.online() || this.socket) return;
    this.env.clearTimeout(this.retry);
    this.retry = null;
    const generation = ++this.generation;
    this.connectStarted = this.now();
    this.setReady(false);
    const url = new URL('/api/live', this.env.location.href);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('token', this.token);
    let socket;
    try { socket = this.socket = new this.env.WebSocket(url.href); }
    catch { this.fail(generation); return; }
    this.deadline = this.env.setTimeout(() => this.fail(generation), 2000);
    socket.onmessage = event => {
      if (generation !== this.generation || this.stopped) return;
      let msg;
      try { msg = JSON.parse(event.data); } catch { return; }
      if (msg.kind === 'heartbeat') { this.lastMessage = this.now(); return; }
      const s = msg.snapshot;
      if (msg.kind !== 'state' || !s || !Number.isFinite(s.sequence) || !Number.isFinite(s.revision) || !Array.isArray(s.hand) || !Array.isArray(s.players) || s.sequence < this.sequence) return;
      this.lastMessage = this.now();
      this.sequence = s.sequence;
      this.onState(s); // Apply before exposing readiness.
      this.env.clearTimeout(this.deadline);
      this.deadline = null;
      this.backoff = 250;
      this.setReady(true);
    };
    socket.onerror = socket.onclose = () => this.fail(generation);
  }
  fail(generation) {
    if (generation !== this.generation || this.stopped) return;
    this.clearAttempt();
    this.setReady(false);
    this.inspectSession();
    if (!this.online()) return;
    const delay = this.backoff + this.env.Math.random() * 250;
    this.backoff = Math.min(this.backoff * 2, 10000);
    if (!this.retry) this.retry = this.env.setTimeout(() => { this.retry = null; this.connect(); }, delay);
  }
  async inspectSession() {
    if (this.diagnostic || this.stopped || !this.online()) return;
    const token = this.token;
    const controller = new this.env.AbortController();
    this.diagnostic = controller;
    const timer = this.env.setTimeout(() => controller.abort(), 3000);
    try {
      const r = await this.env.fetch('/api/session', {headers: {Authorization: `Bearer ${token}`}, signal: controller.signal, cache: 'no-store'});
      if (r.status === 401 && !this.stopped && this.token === token && this.diagnostic === controller) {
        this.stop();
        this.onExpire();
      }
    } catch { /* Network failures leave saved credentials intact. */ }
    finally { this.env.clearTimeout(timer); if (this.diagnostic === controller) this.diagnostic = null; }
  }
  recover() {
    if (this.stopped || !this.online()) return;
    if (this.socket) {
      if (!this.ready && this.now() - this.connectStarted < 2000) return;
      if (this.ready && this.socket.readyState === 1 && this.now() - this.lastMessage < 20000) return;
      this.clearAttempt();
      this.setReady(false);
    }
    this.env.clearTimeout(this.retry);
    this.retry = null;
    this.backoff = 250;
    this.connect();
  }
  offline() {
    this.clearAttempt();
    this.env.clearTimeout(this.retry);
    this.retry = null;
    this.diagnostic?.abort();
    this.diagnostic = null;
    this.setReady(false);
  }
  stop() {
    this.stopped = true;
    this.offline();
    this.env.clearInterval(this.watch);
  }
}
