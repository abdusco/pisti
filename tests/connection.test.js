import test from 'node:test';
import assert from 'node:assert/strict';
import {LiveConnection, restoreSession} from '../assets/connection.js';
import {translations} from '../assets/translations.js';

function harness() {
  let now = 1000, nextID = 0;
  const timers = new Map(), sockets = [], states = [], readiness = [];
  let expired = 0, fetches = 0;
  class Socket {
    constructor(url) { this.url = url; this.readyState = 0; this.closed = 0; sockets.push(this); }
    close() { this.closed++; this.readyState = 3; }
    message(value) { this.onmessage?.({data:JSON.stringify(value)}); }
    state(sequence = 1) { this.readyState = 1; this.message({kind:'state',snapshot:{sequence,revision:2,hand:[],players:[]}}); }
  }
  const env = {
    Date:{now:()=>now}, Math:{random:()=>0}, navigator:{onLine:true}, document:{visibilityState:'visible'},
    location:{href:'https://example.test/?room=ABCD1234'}, WebSocket:Socket, AbortController,
    setTimeout(fn,delay) { const id=++nextID;timers.set(id,{fn,at:now+delay});return id; },
    clearTimeout(id) {timers.delete(id);}, setInterval() {return ++nextID;},clearInterval() {},
    async fetch() {fetches++;return {status:200};},
  };
  const connection = new LiveConnection({token:'secret',env,onState:s=>states.push(s),onReady:r=>readiness.push({ready:r,states:states.length}),onExpire:()=>expired++});
  function advance(ms) {
    const target = now + ms;
    while (true) {
      const entry = [...timers.entries()].filter(([,t])=>t.at<=target).sort((a,b)=>a[1].at-b[1].at)[0];
      if (!entry) break;
      const [id,timer] = entry; now=timer.at;timers.delete(id);timer.fn();
    }
    now=target;
  }
  return {connection,env,sockets,states,readiness,timers,advance,suspend:ms=>{now+=ms;},get expired(){return expired;},get fetches(){return fetches;}};
}

test('root and unmatched explicit room never restore a saved seat', () => {
  const saved = {token:'signed',room:'ABCD1234'};
  for (const [search,expected] of [['',null],['?resume=1',saved],['?room=abcd1234',saved],['?room=OTHER123',null],['?room=OTHER123&resume=1',null],['?resume=0',null],['?room=&resume=1',null]]) {
    assert.equal(restoreSession(search,saved),expected,search);
  }
  assert.equal(restoreSession('?resume=1',null),null);
});

test('initial connection is immediate and state is applied before readiness', () => {
  const h=harness();h.connection.start();
  assert.equal(h.sockets.length,1);assert.equal(h.fetches,0);assert.match(h.sockets[0].url,/^wss:/);
  h.sockets[0].state();
  assert.deepEqual(h.readiness.at(-1),{ready:true,states:1});
  h.advance(2000);assert.equal(h.sockets[0].closed,0);
});

test('open events and heartbeats do not satisfy the first-state deadline', () => {
  const h=harness();h.connection.start();h.sockets[0].readyState=1;
  h.sockets[0].message({kind:'heartbeat'});
  h.advance(1999);assert.equal(h.sockets[0].closed,0);assert.equal(h.connection.ready,false);
  h.advance(1);assert.equal(h.sockets[0].closed,1);assert.equal(h.fetches,1);
  h.advance(250);assert.equal(h.sockets.length,2);
});

test('overlapping foreground events preserve a viable two-second attempt', () => {
  const h=harness();h.connection.start();h.advance(1100);
  for(let i=0;i<5;i++)h.connection.recover();
  assert.equal(h.sockets.length,1);assert.equal(h.sockets[0].closed,0);
  h.sockets[0].state();h.advance(500);h.connection.recover();assert.equal(h.sockets.length,1);
});

test('suspended pending attempts reconnect immediately after their deadline', () => {
  const h=harness();h.connection.start();h.suspend(3000);h.connection.recover();
  assert.equal(h.sockets[0].closed,1);assert.equal(h.sockets.length,2);
  h.advance(1);assert.equal(h.sockets.length,2);
});

test('failure callbacks schedule one retry and old generations are ignored', () => {
  const h=harness();h.connection.start();const old=h.sockets[0];const failure=old.onerror;const message=old.onmessage;
  failure();failure();h.advance(250);assert.equal(h.sockets.length,2);
  message({data:JSON.stringify({kind:'state',snapshot:{sequence:999,revision:1,hand:[],players:[]}})});
  assert.equal(h.states.length,0);
  h.sockets[1].state(2);assert.equal(h.connection.ready,true);
});

test('out-of-order snapshots are ignored and heartbeat keeps a healthy socket', () => {
  const h=harness();h.connection.start();h.sockets[0].state(10);h.sockets[0].state(9);
  assert.equal(h.states.length,1);h.advance(19000);h.sockets[0].message({kind:'heartbeat'});h.suspend(3000);h.connection.recover();assert.equal(h.sockets.length,1);
  h.suspend(20000);h.connection.recover();assert.equal(h.sockets.length,2);assert.equal(h.connection.ready,false);
});

test('offline cancels all work and online recovery starts a single socket', () => {
  const h=harness();h.connection.start();h.env.navigator.onLine=false;h.connection.offline();
  assert.equal(h.connection.ready,false);h.advance(30000);assert.equal(h.sockets.length,1);
  h.env.navigator.onLine=true;h.connection.recover();h.connection.recover();assert.equal(h.sockets.length,2);
});

test('session expiry clears transport and disables retry', async () => {
  const h=harness();h.env.fetch=async()=>({status:401});h.connection.start();h.sockets[0].onerror();
  await Promise.resolve();await Promise.resolve();
  assert.equal(h.expired,1);assert.equal(h.connection.stopped,true);h.advance(30000);assert.equal(h.sockets.length,1);
});

test('network diagnostics are bounded, singular, and cannot expire a stopped session', async () => {
  const h=harness();let resolve,signal;
  h.env.fetch=(_url,options)=>{signal=options.signal;return new Promise(r=>{resolve=r;});};
  h.connection.start();h.sockets[0].onerror();const diagnostic=h.connection.diagnostic;
  h.connection.inspectSession();assert.equal(h.connection.diagnostic,diagnostic);
  h.advance(3000);assert.equal(signal.aborted,true);
  h.connection.stop();resolve({status:401});await Promise.resolve();await Promise.resolve();assert.equal(h.expired,0);
});

test('English and Turkish contain the same complete set of messages', () => {
  assert.deepEqual(Object.keys(translations.en).sort(),Object.keys(translations.tr).sort());
  for (const [lang,strings] of Object.entries(translations)) for(const [key,value] of Object.entries(strings)) assert.ok(value.length,`${lang}.${key}`);
});
