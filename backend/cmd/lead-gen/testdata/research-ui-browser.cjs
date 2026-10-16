// Живой Chromium проверяет серверную страницу и её polling, используя CDP.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');
const config = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const userData = fs.mkdtempSync(path.join(os.tmpdir(), 'research-ui-chromium-'));
const chrome = spawn(config.chromium, ['--headless', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0', '--user-data-dir=' + userData, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
(async () => {
 let socket;
 try {
  const endpoint = await new Promise((resolve, reject) => {
   const timeout = setTimeout(() => reject(new Error('Chromium startup timeout')), 15000);
   chrome.stderr.on('data', bytes => { const match = bytes.toString().match(/DevTools listening on (ws:\/\/\S+)/); if (match) { clearTimeout(timeout); resolve(match[1]); } });
   chrome.once('error', reject); chrome.once('exit', code => reject(new Error('Chromium exited: ' + code)));
  });
  socket = new WebSocket(endpoint); await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let counter = 0; const pending = new Map(); const errors = [];
  socket.onmessage = event => { const message = JSON.parse(event.data); if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.text); if (!message.id) return; const call = pending.get(message.id); if (!call) return; pending.delete(message.id); clearTimeout(call.timer); if (message.error) call.reject(new Error(message.error.message)); else call.resolve(message.result); };
  function send(method, params, sessionId) { return new Promise((resolve, reject) => { const id = ++counter; const timer = setTimeout(() => { pending.delete(id); reject(new Error(method + ' timeout')); }, 15000); pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params: params || {}, sessionId })); }); }
  const target = await send('Target.createTarget', { url: 'about:blank' }); const attached = await send('Target.attachToTarget', { targetId: target.targetId, flatten: true }); const session = attached.sessionId;
  await send('Network.enable', {}, session); await send('Runtime.enable', {}, session); await send('Page.enable', {}, session);
  await send('Network.setCookies', { cookies: [{ name: 'session', value: config.session, domain: new URL(config.url).hostname, path: '/', httpOnly: true, sameSite: 'Lax' }] }, session);
  await send('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'reduce'}]},session);
  await send('Emulation.setDeviceMetricsOverride', { width: 1365, height: 1000, deviceScaleFactor: 1, mobile: false }, session);
  await send('Page.navigate', { url: config.url }, session);
  let ready = false;
  for (let attempt = 0; attempt < 60; attempt++) { const result = await send('Runtime.evaluate', { expression: 'document.readyState === "complete" && !!document.querySelector("[data-research-snapshot]")', returnByValue: true }, session); if (result.result.value) { ready = true; break; } await new Promise(resolve => setTimeout(resolve, 250)); }
  if (!ready) throw new Error('research page missing');
  await new Promise(resolve => setTimeout(resolve, 5500));
  const result = await send('Runtime.evaluate', { expression: 'JSON.stringify({rows:document.querySelectorAll("[data-entity-id]").length,status:document.querySelector("[data-research-refresh-status]").textContent,heading:document.querySelector("h1").textContent})', returnByValue: true }, session);
  const state = JSON.parse(result.result.value); if (state.rows < 1 || errors.length || state.status.includes('недоступно')) throw new Error('browser page/polling failed: ' + JSON.stringify({state, errors}));
  const screenshot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true }, session); fs.writeFileSync(path.join(config.directory, 'research-live.png'), Buffer.from(screenshot.data, 'base64'));
  const pages = {};
  for (const name of ['dossier','queue','execution']) {
   if (!config[name]) continue;
   await send('Page.navigate', { url: config[name] }, session);
   let loaded=false;
   for(let attempt=0;attempt<60;attempt++) {
    const value=await send('Runtime.evaluate',{expression:'document.readyState === "complete" && document.location.href === '+JSON.stringify(config[name]),returnByValue:true},session);
    if(value.result.value){loaded=true;break;} await new Promise(resolve=>setTimeout(resolve,100));
   }
   if(!loaded)throw new Error('navigation timeout: '+name);
   await new Promise(resolve=>setTimeout(resolve,350));
   const page=await send('Runtime.evaluate',{expression:'JSON.stringify({heading:document.querySelector("h1")?.textContent,rows:document.querySelectorAll("tbody tr").length,assertions:document.querySelectorAll("[data-research-assertion]").length,width:document.documentElement.scrollWidth,viewport:window.innerWidth})',returnByValue:true},session);
   pages[name]=JSON.parse(page.result.value);
   if(!pages[name].heading || pages[name].rows+pages[name].assertions<1 || pages[name].width>pages[name].viewport)throw new Error('empty page: '+name);
   const shot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},session);
   fs.writeFileSync(path.join(config.directory,'research-'+name+'.png'),Buffer.from(shot.data,'base64'));
  }
  if(errors.length)throw new Error('browser exceptions after navigation');
  fs.writeFileSync(path.join(config.directory, 'research-browser.json'), JSON.stringify({ state, pages, exceptions: errors }, null, 2));
 } finally { if (socket) socket.close(); chrome.kill('SIGTERM'); }
})().catch(error => { process.stderr.write(error.message + '\n'); process.exitCode = 1; });
