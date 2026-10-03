import Alpine from './vendor/alpine-3.15.0.js';
import { LiveConnection, restoreSession } from './connection.js';
import { translations } from './translations.js';

const read = (key, fallback) => { try { return localStorage.getItem(`pisti.${key}`) ?? fallback; } catch { return fallback; } };
const save = (key, value) => { try { localStorage.setItem(`pisti.${key}`, value); } catch { /* Private browsing. */ } };
let connection, installPrompt, audio;
let soundedTurn = '';
let audioUnlocked = false;
function chime() {
  if (!audioUnlocked || document.hidden) return;
  audio ||= new (window.AudioContext || window.webkitAudioContext)();
  void audio.resume();
  const oscillator = audio.createOscillator(), gain = audio.createGain();
  oscillator.type = 'sine'; oscillator.frequency.setValueAtTime(660, audio.currentTime);
  oscillator.frequency.setValueAtTime(880, audio.currentTime + .08);
  gain.gain.setValueAtTime(.0001, audio.currentTime); gain.gain.exponentialRampToValueAtTime(.055, audio.currentTime + .01); gain.gain.exponentialRampToValueAtTime(.0001, audio.currentTime + .2);
  oscillator.connect(gain); gain.connect(audio.destination); oscillator.start(); oscillator.stop(audio.currentTime + .22);
}
window.addEventListener('pointerdown', () => { audioUnlocked = true; }, {once: true});
window.addEventListener('keydown', () => { audioUnlocked = true; }, {once: true});
window.addEventListener('beforeinstallprompt', event => { event.preventDefault(); installPrompt = event; window.dispatchEvent(new Event('pisti-install')); });

Alpine.data('pisti', () => ({
  lang: read('language', navigator.language.startsWith('tr') ? 'tr' : 'en'), theme: read('theme', 'system'), sound: read('sound', 'on') === 'on',
  name: read('name', ''), mode: 'bot', code: '', token: null, snapshot: null, live: false, busy: false, pending: false, error: '', copied: false,
  canInstall: Boolean(installPrompt), networkOnline: navigator.onLine,
  init() {
    if (!translations[this.lang]) this.lang = 'en';
    const params = new URLSearchParams(location.search);
    this.code = (params.get('room') || '').toUpperCase();
    if (this.code) this.mode = 'join';
    let saved; try { saved = JSON.parse(read('session', 'null')); } catch { saved = null; }
    const session = restoreSession(location.search, saved);
    if (session) this.connect(session);
    const recovery = () => { this.networkOnline = navigator.onLine; connection?.recover(); };
    window.addEventListener('online', recovery);
    window.addEventListener('offline', () => { this.networkOnline = false; connection?.offline(); });
    window.addEventListener('focus', recovery);
    window.addEventListener('resume', recovery);
    window.addEventListener('pageshow', event => { if (event.persisted) recovery(); });
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) recovery(); else void audio?.suspend();
    });
    window.addEventListener('pisti-install', () => { this.canInstall = true; });
    window.addEventListener('appinstalled', () => { this.canInstall = false; installPrompt = null; });
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => this.applyTheme());
    if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
  },
  t(key) { return translations[this.lang][key] || key; },
  setLanguage(value) { this.lang = value; save('language', value); document.documentElement.lang = value; },
  applyTheme() { document.documentElement.dataset.theme = this.theme === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : this.theme; save('theme', this.theme); },
  toggleSound() { this.sound = !this.sound; save('sound', this.sound ? 'on' : 'off'); },
  rank(card) { return ({1:'A',11:'J',12:'Q',13:'K'})[card?.rank] || card?.rank; },
  suit(card) { return ({clubs:'♣', diamonds:'♦', hearts:'♥', spades:'♠'})[card?.suit] || ''; },
  red(card) { return card?.suit === 'hearts' || card?.suit === 'diamonds'; },
  cardName(card) { return card ? `${this.t(`rank${card.rank}`) === `rank${card.rank}` ? card.rank : this.t(`rank${card.rank}`)} · ${this.t(card.suit)}` : ''; },
  get me() { return this.snapshot?.players[this.snapshot.seat]; },
  get opponent() { return this.snapshot?.players[1-this.snapshot.seat]; },
  get turnText() {
    if (!this.live) return this.t(this.networkOnline ? 'reconnecting' : 'offline');
    if (this.snapshot?.phase !== 'playing') return '';
    return this.t(this.snapshot.turn === this.snapshot.seat ? 'yourTurn' : this.opponent?.bot ? 'thinking' : 'theirTurn');
  },
  get inviteURL() { return `${location.origin}/?room=${this.snapshot?.room || ''}`; },
  async join() {
    if (this.busy) return;
    this.busy = true; this.error = '';
    try {
      const response = await fetch('/api/join', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name:this.name.trim(), room:this.mode === 'join' ? this.code.trim() : '', bot:this.mode === 'bot'}), signal:AbortSignal.timeout(5000)});
      const result = await response.json();
      if (!response.ok) { this.error = result.error; return; }
      save('name', this.name.trim()); save('session', JSON.stringify(result));
      this.connect(result);
    } catch { this.error = 'network'; }
    finally { this.busy = false; }
  },
  connect(session) {
    connection?.stop();
    this.token = session.token; this.snapshot = null; this.live = false;
    soundedTurn = '';
    history.replaceState(null, '', `/?room=${session.room}`);
    connection = new LiveConnection({token:session.token,
      onState: snapshot => {
        this.snapshot = snapshot;
        const key = `${snapshot.room}:${snapshot.revision}:${snapshot.turn}`;
        if (snapshot.phase === 'playing' && snapshot.turn === snapshot.seat && soundedTurn !== key) {
          soundedTurn = key;
          if (this.sound) chime();
        }
      },
      onReady: ready => { this.live = ready; },
      onExpire: () => { try { localStorage.removeItem('pisti.session'); } catch {} this.home(); this.error = 'expired'; },
    });
    connection.start();
  },
  home() {
    connection?.stop(); connection = null;
    this.token = null; this.snapshot = null; this.live = false; this.pending = false; this.error = ''; this.code = ''; this.mode = 'bot';
    history.replaceState(null, '', '/');
  },
  async action(kind, card = 0) {
    if (!this.live || this.pending || !this.snapshot) return;
    if (kind === 'play' && !this.snapshot.legal.includes(card)) return;
    this.pending = true; this.error = '';
    const token = this.token;
    try {
      const r = await fetch('/api/action', {method:'POST', headers:{'Content-Type':'application/json', Authorization:`Bearer ${token}`}, body:JSON.stringify({kind, card, revision:this.snapshot.revision}), signal:AbortSignal.timeout(5000)});
      const result = await r.json();
      if (this.token !== token) return;
      if (!r.ok) {
        if (r.status === 401) { connection?.stop(); try { localStorage.removeItem('pisti.session'); } catch {} this.home(); }
        this.error = result.error;
      }
    } catch { if (this.token === token) this.error = 'network'; }
    finally { if (this.token === token) this.pending = false; }
  },
  invite() {
    this.copied = false;
    const qr = window.qrcode(0, 'M'); qr.addData(this.inviteURL); qr.make();
    this.$refs.qr.innerHTML = qr.createSvgTag({cellSize:4, margin:16, scalable:true});
    this.$refs.invitation.showModal();
  },
  async copyInvite() {
    try { await navigator.clipboard.writeText(this.inviteURL); this.copied = true; }
    catch { this.$refs.inviteLink.select(); }
  },
  async shareInvite() {
    if (navigator.share) { try { await navigator.share({title:'Pişti',url:this.inviteURL}); } catch {} }
    else await this.copyInvite();
  },
  async install() { if (!installPrompt) return; await installPrompt.prompt(); installPrompt = null; this.canInstall = false; },
}));
window.Alpine = Alpine;
Alpine.start();
