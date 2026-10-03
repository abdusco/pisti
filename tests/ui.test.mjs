import test from 'node:test';
import assert from 'node:assert/strict';
import {chromium} from 'playwright';
import {mkdir} from 'node:fs/promises';

const url = process.env.PISTI_URL || 'http://127.0.0.1:8080';
let browser;
test.before(async()=>{browser=await chromium.launch();await mkdir('test-results',{recursive:true});});
test.after(async()=>{await browser?.close();});

test('home, bot play, preferences, invitations, and mobile public information', async()=>{
  const context=await browser.newContext({locale:'en-US',viewport:{width:1280,height:1000}});
  const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(url);await page.locator('#player-name').fill('Alex');
  await page.screenshot({path:'test-results/home-desktop.png',fullPage:true});
  for (const width of [320,390,720,1000]) {
    await page.setViewportSize({width,height:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`home overflow at ${width}`);
    await page.screenshot({path:`test-results/home-${width}.png`,fullPage:true});
  }
  await page.locator('.join-card form').getByRole('button').click();
  await page.waitForFunction(()=>document.querySelector('.connection-status')?.textContent.includes('Live'));
  await page.locator('.hand-card.playable').first().waitFor();
  const before=await page.evaluate(()=>Alpine.$data(document.body).snapshot.revision);
  await page.locator('.hand-card.playable').first().click();
  await page.waitForFunction(revision=>Alpine.$data(document.body).snapshot.revision>revision,before);
  for (const width of [320,390,720,1000]) {
    await page.setViewportSize({width,height:1000});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`game overflow at ${width}`);
    for (const selector of ['.score-panel','.capture-panel','.table-rules','.history-panel']) {
      assert.ok(await page.locator(selector).isVisible(),`${selector} hidden at ${width}`);
      const bounds=await page.locator(selector).boundingBox();assert.ok(bounds.x>=0&&bounds.x+bounds.width<=width,`${selector} off screen at ${width}`);
    }
    await page.screenshot({path:`test-results/game-${width}.png`,fullPage:true});
  }
  await page.locator('.game-toolbar .secondary-button').click();await page.locator('.qr-code svg').waitFor();
  assert.match(await page.locator('.invite-link').inputValue(),/\?room=[A-Z0-9]{8}$/);
  await page.locator('.invite-modal .modal-close').click();
  await page.locator('select').selectOption('dark');assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
  await page.getByRole('button',{name:'TR',exact:true}).click();await page.locator('.connection-status').getByText('Bağlı',{exact:true}).waitFor();
  await page.reload();await page.locator('.connection-status').getByText('Bağlı',{exact:true}).waitFor();
  assert.equal(await page.locator('html').getAttribute('lang'),'tr');assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
  assert.deepEqual(errors,[]);await context.close();
});

test('a complete bot round scores all 52 cards and starts the next round or match', {timeout:45000}, async()=>{
  const context=await browser.newContext({locale:'en-US',viewport:{width:390,height:900}});const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(url);await page.locator('#player-name').fill('Round test');await page.locator('.join-card form').getByRole('button').click();await page.locator('.connection-status').getByText('Live',{exact:true}).waitFor();
  for (let i=0;i<24;i++) {
    await page.locator('.hand-card.playable').first().waitFor();
    const revision=await page.evaluate(()=>Alpine.$data(document.body).snapshot.revision);
    await page.locator('.hand-card.playable').first().click();
    await page.waitForFunction(rev=>Alpine.$data(document.body).snapshot.revision>rev,revision);
  }
  await page.locator('.round-summary').waitFor({state:'visible'});
  const snapshot=await page.evaluate(()=>Alpine.$data(document.body).snapshot);
  assert.ok(['round_over','finished'].includes(snapshot.phase));assert.equal(snapshot.results[0].cards+snapshot.results[1].cards,52);assert.equal(snapshot.history.length,48);
  assert.equal(snapshot.results[0].points+snapshot.results[1].points,13);
  await page.screenshot({path:'test-results/round-summary-mobile.png',fullPage:true});
  const nextRound=snapshot.phase==='finished'?1:2;
  await page.getByRole('button',{name:snapshot.phase==='finished'?'Play again':'Next round',exact:true}).click();await page.waitForFunction(round=>Alpine.$data(document.body).snapshot.phase==='playing'&&Alpine.$data(document.body).snapshot.round===round,nextRound);
  assert.equal(await page.evaluate(()=>Alpine.$data(document.body).snapshot.dealer),(nextRound-1)%2);await page.locator('.hand-card.playable').first().waitFor();assert.deepEqual(errors,[]);await context.close();
});

test('two human players, duplicate tabs, seat restore, root and standalone navigation', async()=>{
  const hostContext=await browser.newContext({locale:'en-US'}),guestContext=await browser.newContext({locale:'en-US'});
  const host=await hostContext.newPage(),guest=await guestContext.newPage();
  await host.goto(url);await host.locator('[data-mode=friend]').click();await host.locator('#player-name').fill('Host');await host.locator('.join-card form').getByRole('button').click();
  await host.locator('.connection-status').getByText('Live',{exact:true}).waitFor();
  const room=await host.evaluate(()=>Alpine.$data(document.body).snapshot.room);
  await guest.goto(`${url}/?room=${room.toLowerCase()}`);await guest.locator('#player-name').fill('Guest');await guest.locator('.join-card form').getByRole('button').click();
  await guest.locator('.connection-status').getByText('Live',{exact:true}).waitFor();
  await host.getByRole('button',{name:'Start the game',exact:true}).click();await guest.locator('.hand-card.playable').first().waitFor();
  const hostCards=await host.evaluate(()=>Alpine.$data(document.body).snapshot.hand.map(c=>c.id)),guestCards=await guest.evaluate(()=>Alpine.$data(document.body).snapshot.hand.map(c=>c.id));assert.ok(hostCards.every(id=>!guestCards.includes(id)));
  assert.equal(await host.locator('.hand-card.playable').count(),0);
  await guest.locator('.hand-card.playable').first().click();await host.locator('.hand-card.playable').first().waitFor();
  const duplicate=await hostContext.newPage();await duplicate.goto(`${url}/?resume=1`);await duplicate.locator('.connection-status').getByText('Live',{exact:true}).waitFor();await duplicate.close();
  await guest.waitForFunction(()=>Alpine.$data(document.body).snapshot.players[0].online===true);
  await host.reload();await host.locator('.connection-status').getByText('Live',{exact:true}).waitFor();assert.equal(await host.evaluate(()=>Alpine.$data(document.body).snapshot.seat),0);
  await host.addInitScript(()=>{const original=window.matchMedia.bind(window);window.matchMedia=q=>q==='(display-mode: standalone)'?{matches:true,addEventListener(){},removeEventListener(){}}:original(q);});
  await host.locator('.brand').click();await host.locator('.join-card').waitFor({state:'visible'});assert.equal(new URL(host.url()).search,'');
  await host.reload();await host.locator('.join-card').waitFor({state:'visible'});assert.equal(await host.evaluate(()=>Alpine.$data(document.body).token),null);
  await host.locator('#player-name').fill('New host');await host.locator('.join-card form').getByRole('button').click();await host.locator('.connection-status').getByText('Live',{exact:true}).waitFor();assert.notEqual(await host.evaluate(()=>Alpine.$data(document.body).snapshot.room),room);
  await hostContext.close();await guestContext.close();
});

test('offline shell, recovery, foreground coalescing, and expiry', async()=>{
  const context=await browser.newContext({locale:'en-US'});const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(url);await page.evaluate(()=>navigator.serviceWorker.ready);await page.locator('#player-name').fill('Reconnect');await page.locator('.join-card form').getByRole('button').click();await page.locator('.connection-status').getByText('Live',{exact:true}).waitFor();
  const room=await page.evaluate(()=>Alpine.$data(document.body).snapshot.room);
  await context.setOffline(true);await page.waitForFunction(()=>!Alpine.$data(document.body).live);assert.equal(await page.locator('.hand-card.playable').count(),0);
  await page.reload();await page.locator('.loading-screen').waitFor({state:'visible'});assert.equal(await page.locator('.brand').isVisible(),true);
  await context.setOffline(false);await page.locator('.connection-status').getByText('Live',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>Alpine.$data(document.body).snapshot.room),room);
  await page.evaluate(()=>{for(let i=0;i<4;i++){window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('online'));document.dispatchEvent(new Event('visibilitychange'));}});
  assert.equal(await page.evaluate(()=>Alpine.$data(document.body).live),true);
  await page.goto(`${url}/?room=OTHER123&resume=1`);await page.locator('.join-card').waitFor({state:'visible'});assert.equal(await page.locator('#room-code').inputValue(),'OTHER123');
  await page.evaluate(()=>localStorage.setItem('pisti.session',JSON.stringify({room:'EXPIRED1',token:'invalid.session'})));
  await page.goto(`${url}/?resume=1`);await page.getByRole('alert').getByText(/session has ended/).waitFor();assert.equal(await page.evaluate(()=>localStorage.getItem('pisti.session')),null);
  assert.deepEqual(errors,[]);await context.close();
});

test('real sockets preserve pending attempts across overlapping foreground events', async()=>{
  const context=await browser.newContext({locale:'en-US'});const page=await context.newPage();let attempts=0;
  await page.routeWebSocket(/\/api\/live/,socket=>{
    attempts++;let closed=false;const server=socket.connectToServer();
    server.onMessage(message=>setTimeout(()=>{if(!closed)socket.send(message);},1200));
    socket.onClose(()=>{closed=true;server.close();});server.onClose(()=>{closed=true;socket.close();});
  });
  await page.goto(url);await page.locator('#player-name').fill('Pending socket');await page.locator('.join-card form').getByRole('button').click();
  await page.waitForFunction(()=>Alpine.$data(document.body).token&&!Alpine.$data(document.body).live);
  await page.waitForTimeout(1050);
  await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));window.dispatchEvent(new Event('online'));window.dispatchEvent(new Event('resume'));document.dispatchEvent(new Event('visibilitychange'));});
  await page.locator('.connection-status').getByText('Live',{exact:true}).waitFor();assert.equal(attempts,1);
  await context.close();
});
