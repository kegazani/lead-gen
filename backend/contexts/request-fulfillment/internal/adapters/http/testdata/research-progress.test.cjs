// Проверяем клиентский автомат реальным JavaScript: запоздалый ответ не меняет
// снимок и ошибка транспорта сохраняет последнее подтверждённое состояние.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const script = fs.readFileSync(path.join(__dirname, '../static/research_progress.js'), 'utf8');

function harness() {
  let current = snapshot(5);
  const status = { textContent: '' };
  const login = { hidden: true };
  const timers = [];
  const replies = [];
  const calls = [];
  const page = {
    getAttribute(name) { return ({ 'data-tenant-id': 'tenant', 'data-request-id': 'request', 'data-refresh-error': 'refresh-error', 'data-refresh-updated': 'updated', 'data-session-expired': 'expired' })[name]; },
    querySelector(selector) { return ({ '[data-research-snapshot]': current, '[data-research-refresh-status]': status, '[data-research-login]': login })[selector]; }
  };
  function snapshot(revision, tenant = 'tenant', mission = 'mission') {
    return {
      revision,
      getAttribute(name) { return ({ 'data-revision': String(revision), 'data-mission-id': mission, 'data-tenant-id': tenant, 'data-request-id': 'request' })[name]; },
      contains() { return false; },
      replaceWith(next) { current = next; }
    };
  }
  const sandbox = {
    document: { querySelector() { return page; }, hidden: false, activeElement: {} },
    window: { location: { href: 'http://localhost/intake/requests/request/research?tenant_id=tenant&filter=accepted' } },
    URL, AbortController,
    setTimeout(fn, delay) { const timer = { fn, delay }; timers.push(timer); return timer; },
    clearTimeout(timer) { const index = timers.indexOf(timer); if (index >= 0) timers.splice(index, 1); },
    fetch: async (url, options) => {
      calls.push({ url, options });
      const response = replies.shift();
      if (response instanceof Error) throw response;
      return { ok: response.status === 200, status: response.status, headers: { get: () => '"revision-' + response.revision + '"' }, text: async () => response };
    },
    DOMParser: class { parseFromString(body) { return { querySelector: () => snapshot(body.revision, body.tenant, body.mission) }; } }
  };
  vm.runInNewContext(script, sandbox);
  return {
    replies, status, login, calls,
    current: () => current,
    pending: () => timers.filter(timer => timer.delay === 5000).length,
    async poll() { const index = timers.findIndex(timer => timer.delay === 5000); assert.notEqual(index, -1); const [timer] = timers.splice(index, 1); await timer.fn(); }
  };
}

test('новый снимок заменяется полностью, дубликат и старый не откатывают версию', async () => {
  const h = harness();
  h.replies.push({ status: 200, revision: 8 }); await h.poll();
  assert.equal(h.current().revision, 8);
  h.replies.push({ status: 200, revision: 6 }); await h.poll();
  assert.equal(h.current().revision, 8);
  h.replies.push({ status: 200, revision: 8 }); await h.poll();
  assert.equal(h.current().revision, 8);
  assert.match(h.calls[0].url, /\/intake\/requests\/request\/research\/progress\?/);
  assert.match(h.calls[0].url, /filter=accepted/);
  assert.equal(h.calls[1].options.headers['If-None-Match'], '"revision-8"');
});

test('ошибка, другая область и другая миссия сохраняют последний снимок', async () => {
  const h = harness();
  for (const response of [{ status: 503 }, { status: 200, revision: 6, tenant: 'other' }, { status: 200, revision: 7, mission: 'other' }, new Error('network')]) {
    h.replies.push(response); await h.poll();
    assert.equal(h.current().revision, 5);
    assert.equal(h.status.textContent, 'refresh-error');
  }
});

test('истёкшая сессия останавливает опрос и предлагает вход', async () => {
  const h = harness(); h.replies.push({ status: 401 }); await h.poll();
  assert.equal(h.current().revision, 5);
  assert.equal(h.status.textContent, 'expired');
  assert.equal(h.login.hidden, false);
  assert.equal(h.pending(), 0);
});
