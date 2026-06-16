'use strict';
// rgdevenv dashboard. Vanilla JS, no build step. All data is fetched client-side
// from /api/v1/* with a bearer token kept ONLY in sessionStorage. The CSP forbids
// inline scripts, so every event listener is attached programmatically here.

const POLL_MS = 5000;
const TOKEN_KEY = 'rgdevenv.token';
const AUTH = Symbol('auth-required'); // thrown by api() on 401; action handlers ignore it

// UI-only module state (never the source of truth — the API is).
const expanded = new Set();     // lb names whose mapping panel is open
const labelEditing = new Set(); // lb names whose label is being edited
let addingLB = false;           // the "add load balancer" form is open
let cas = [];                   // CA names for the upstream-TLS dropdown
let pollTimer = null;
let polling = false;            // in-flight guard so slow polls can't overlap

// ---- tiny DOM helpers ------------------------------------------------------
function byId(id) { return document.getElementById(id); }
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

// el(tag, props, ...children): props supports class, text, onX listeners, style
// (applied via CSSOM so it is CSP-safe), boolean and string attributes.
function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v == null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
      else if (k === 'style') n.style.cssText = v;
      else if (v === true) n.setAttribute(k, '');
      else n.setAttribute(k, v);
    }
  }
  for (const kid of kids) {
    if (kid == null) continue;
    n.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return n;
}

function swallow(e) { if (e !== AUTH) showError((e && e.message) || String(e)); }

// ---- session + fetch -------------------------------------------------------
function getToken() { return sessionStorage.getItem(TOKEN_KEY); }
function setToken(t) { sessionStorage.setItem(TOKEN_KEY, t); }
function clearToken() { sessionStorage.removeItem(TOKEN_KEY); }

async function api(method, path, body) {
  const opts = { method, headers: {} };
  const tok = getToken();
  if (tok) opts.headers['Authorization'] = 'Bearer ' + tok;
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 401) { handleUnauthorized(); throw AUTH; }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch (_) { /* non-JSON body */ } }
  if (!res.ok) {
    throw new Error((data && data.error) || (method + ' ' + path + ' failed (' + res.status + ')'));
  }
  return data;
}

function handleUnauthorized() {
  clearToken();
  stopPolling();
  settleConfirm(false); // a destructive-action dialog must not survive into the login view
  showLogin();
}

// ---- views -----------------------------------------------------------------
function showLogin() {
  byId('dashboard').hidden = true;
  byId('login').hidden = false;
  byId('token').value = '';
  byId('token').focus();
}
function showDashboard() {
  byId('login').hidden = true;
  byId('dashboard').hidden = false;
}

// ---- error banner ----------------------------------------------------------
function showError(msg) { byId('error-text').textContent = msg; byId('error-banner').hidden = false; }
function clearError() { byId('error-banner').hidden = true; }

// ---- reusable confirm dialog -----------------------------------------------
let confirmResolver = null;
function confirmAction(message) {
  if (confirmResolver) { const prev = confirmResolver; confirmResolver = null; prev(false); }
  byId('confirm-message').textContent = message;
  byId('confirm-overlay').hidden = false;
  return new Promise((resolve) => { confirmResolver = resolve; });
}
function settleConfirm(ok) {
  byId('confirm-overlay').hidden = true;
  const r = confirmResolver;
  confirmResolver = null;
  if (r) r(ok);
}

// ---- polling (suspended while a field is focused or dirty) ------------------
function fieldFocused() {
  const a = document.activeElement;
  return !!a && (a.tagName === 'INPUT' || a.tagName === 'SELECT' || a.tagName === 'TEXTAREA');
}
// AIDEV-NOTE: "dirty" means a field has been CHANGED FROM ITS DEFAULT, not merely
// non-empty. A freshly-rendered mapping form has a <select> ("mode") sitting at its
// default "verify" and the label editor pre-fills the current label; treating those
// as dirty would suspend the 5s health poll the whole time any panel is open. Compare
// each field to its default so only genuine in-progress edits pause polling.
function fieldDirty(i) {
  if (i.type === 'checkbox' || i.type === 'radio') return i.checked !== i.defaultChecked;
  if (i.tagName === 'SELECT') {
    const def = Array.from(i.options).find((o) => o.defaultSelected) || i.options[0];
    return !!def && i.value !== def.value;
  }
  return i.value !== i.defaultValue;
}
function hasDirtyField() {
  for (const i of document.querySelectorAll('#dashboard input, #dashboard select')) {
    if (fieldDirty(i)) return true;
  }
  return false;
}
// busy() is stateless (reads the DOM) so it can never leak a stuck "editing" flag:
// a background poll is skipped only while the user is actually mid-input.
function busy() { return fieldFocused() || hasDirtyField(); }

function startPolling() { stopPolling(); pollTimer = setInterval(poll, POLL_MS); }
function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
// AIDEV-NOTE: the `polling` guard stops a slow refresh() from overlapping the next
// interval tick (out-of-order renders, piled-up requests). stopPolling() clears the
// timer; an already-in-flight refresh still settles and resets the flag in finally.
async function poll() {
  if (polling || busy()) return;
  polling = true;
  try { await refresh(); } catch (e) { if (e !== AUTH) setRunDot(false); } finally { polling = false; }
}

async function refresh() {
  const [lbs, ports, status] = await Promise.all([
    api('GET', '/api/v1/lbs'),
    api('GET', '/api/v1/ports'),
    api('GET', '/api/v1/status'),
  ]);
  setRunDot(true);
  renderHeader(status);
  renderLBs(lbs || []);
  renderPorts(ports);
}

function setRunDot(up) { byId('run-dot').className = 'dot ' + (up ? 'dot-up' : 'dot-down'); }

// ---- header ----------------------------------------------------------------
function renderHeader(status) {
  byId('version').textContent = status && status.version ? 'v' + status.version : '';
  const ls = ((status && status.active_listeners) || []).map((p) => ':' + p).join(' · ');
  byId('listeners').textContent = ls || '—';
}

// ---- health + link helpers -------------------------------------------------
function worstHealth(maps) {
  let any = false;
  let worst = 'up';
  for (const m of maps || []) {
    any = true;
    const h = m.health || 'unknown';
    if (h === 'down') return 'down';
    if (h === 'unknown') worst = 'unknown';
  }
  return any ? worst : 'unknown';
}
function launchURL(name, m) {
  const scheme = m.listen_tls ? 'https' : 'http';
  const def = m.listen_tls ? 443 : 80;
  const port = (m.listen_port && m.listen_port !== def) ? ':' + m.listen_port : '';
  return scheme + '://' + name + port;
}
function sortedMappings(lb) {
  return (lb.mappings || []).slice().sort((a, b) => a.listen_port - b.listen_port);
}

// ---- load balancers (left column) ------------------------------------------
function renderLBs(lbs) {
  const list = byId('lb-list');
  clear(list);
  if (addingLB) list.append(renderAddLBForm());
  if (!lbs.length && !addingLB) {
    list.append(el('p', { class: 'muted empty' }, 'No load balancers yet.'));
    return;
  }
  for (const lb of lbs.slice().sort((a, b) => a.name.localeCompare(b.name))) {
    list.append(renderLBRow(lb));
  }
}

function renderLBRow(lb) {
  if (labelEditing.has(lb.name)) return renderLabelEditor(lb);
  const maps = sortedMappings(lb);
  const worst = worstHealth(maps);
  const open = expanded.has(lb.name);

  const name = maps.length
    ? el('a', { class: 'lb-name', href: launchURL(lb.name, maps[0]), target: '_blank', rel: 'noopener' }, lb.name + ' ↗')
    : el('span', { class: 'lb-name' }, lb.name);

  const head = el('div', { class: 'lb-head' },
    el('span', { class: 'dot dot-' + worst, title: worst }),
    name,
    lb.label ? el('span', { class: 'muted' }, lb.label) : null,
    el('span', { class: 'spacer' }),
    el('button', { class: 'link', type: 'button', title: 'mappings', onclick: () => toggleExpand(lb.name) }, open ? '− mappings' : '+ map'),
    el('button', { class: 'link', type: 'button', title: 'edit label', onclick: () => startLabelEdit(lb.name) }, '✎'),
    el('button', { class: 'link danger', type: 'button', title: 'delete', onclick: () => deleteLB(lb) }, '🗑'),
  );

  const chips = el('div', { class: 'chips' });
  for (const m of maps) chips.append(renderChip(lb.name, m));

  const row = el('div', { class: 'lb-row' + (open ? ' open' : '') }, head, chips);
  if (open) row.append(renderMappingsPanel(lb, maps));
  return row;
}

function renderChip(name, m) {
  const badge = (m.upstream && m.upstream.scheme === 'https')
    ? el('span', { class: 'badge' }, '🔒 ' + (m.upstream.tls ? (m.upstream.tls.mode || 'verify') : 'verify'))
    : null;
  const label = m.listen_port + ' → ' + (m.upstream ? m.upstream.host + ':' + m.upstream.port : '—');
  return el('a', { class: 'chip', href: launchURL(name, m), target: '_blank', rel: 'noopener' }, label, badge);
}

function toggleExpand(name) {
  if (expanded.has(name)) expanded.delete(name); else expanded.add(name);
  refresh().catch(swallow);
}

// ---- add / edit-label / delete load balancer -------------------------------
function renderAddLBForm() {
  const name = el('input', { type: 'text', placeholder: 'hostname (app.dev.example.com)' });
  const label = el('input', { type: 'text', placeholder: 'label (optional)' });
  const form = el('form', { class: 'inline-form add-lb' },
    name, label,
    el('button', { class: 'btn-primary', type: 'submit' }, 'Create'),
    el('button', { class: 'btn-ghost', type: 'button', onclick: () => { addingLB = false; refresh().catch(swallow); } }, 'Cancel'),
  );
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await api('POST', '/api/v1/lbs', { name: name.value.trim(), label: label.value.trim() });
      addingLB = false;
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });
  setTimeout(() => name.focus(), 0);
  return form;
}

function startLabelEdit(name) { labelEditing.add(name); refresh().catch(swallow); }

function renderLabelEditor(lb) {
  const input = el('input', { type: 'text', value: lb.label || '', placeholder: 'label' });
  const form = el('form', { class: 'inline-form label-edit' },
    el('span', { class: 'dot dot-' + worstHealth(sortedMappings(lb)) }),
    el('span', { class: 'lb-name' }, lb.name),
    input,
    el('button', { class: 'btn-primary', type: 'submit' }, 'Save'),
    el('button', { class: 'btn-ghost', type: 'button', onclick: () => { labelEditing.delete(lb.name); refresh().catch(swallow); } }, 'Cancel'),
  );
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await api('PATCH', '/api/v1/lbs/' + encodeURIComponent(lb.name), { label: input.value.trim() });
      labelEditing.delete(lb.name);
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });
  setTimeout(() => input.focus(), 0);
  return el('div', { class: 'lb-row' }, form);
}

async function deleteLB(lb) {
  const ok = await confirmAction('Delete load balancer "' + lb.name + '"? Its mappings and any auto-allocated ports will be removed.');
  if (!ok) return;
  try {
    await api('DELETE', '/api/v1/lbs/' + encodeURIComponent(lb.name));
    expanded.delete(lb.name);
    labelEditing.delete(lb.name);
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- mappings panel + add/replace/delete form ------------------------------
function mapURL(name, port) {
  const base = '/api/v1/lbs/' + encodeURIComponent(name) + '/mappings';
  return port != null ? base + '/' + port : base;
}

function renderMappingsPanel(lb, maps) {
  const form = renderMappingForm(lb); // built first so row ✎ buttons can prefill it
  const tbody = el('tbody');
  for (const m of maps) {
    tbody.append(el('tr', null,
      el('td', null, m.listen_port + ' ', el('span', { class: 'muted' }, m.listen_tls ? 'https' : 'http')),
      el('td', null, m.upstream ? m.upstream.host + ':' + m.upstream.port : '—',
        m.auto_allocated ? el('span', { class: 'badge' }, 'allocated') : null),
      el('td', { class: 'muted' }, m.upstream && m.upstream.scheme === 'https' ? (m.upstream.tls.mode || 'verify') : '—'),
      el('td', null, el('span', { class: 'health health-' + (m.health || 'unknown') }, '● ' + (m.health || 'unknown'))),
      el('td', { class: 'right' },
        m.auto_allocated ? null : el('button', { class: 'link', type: 'button', title: 'replace', onclick: () => form._fill(m) }, '✎'),
        el('button', { class: 'link danger', type: 'button', title: 'delete', onclick: () => deleteMapping(lb, m) }, '🗑')),
    ));
  }
  const table = el('table', { class: 'grid' },
    el('thead', null, el('tr', null,
      el('th', null, 'Listen'), el('th', null, 'Upstream'), el('th', null, 'TLS'),
      el('th', null, 'Health'), el('th', null, ''))),
    tbody);
  return el('div', { class: 'panel' }, table, form);
}

function renderMappingForm(lb) {
  const port = el('input', { type: 'number', min: '1', max: '65535', placeholder: 'listen port', class: 'w-port' });
  // Listen-side scheme → listen_tls. https (the server/CLI default) = TLS; http is
  // the equivalent of the CLI's `--no-tls`. Applies to allocated mappings too.
  const listenScheme = el('select', { title: 'listen port scheme' },
    el('option', { value: 'https' }, 'https'),
    el('option', { value: 'http' }, 'http'));
  const url = el('input', { type: 'text', placeholder: 'upstream URL (http://localhost:3000)', class: 'grow' });
  const mode = el('select', null,
    el('option', { value: 'verify' }, 'verify'),
    el('option', { value: 'ca' }, 'ca'),
    el('option', { value: 'skip' }, 'skip'));
  const ca = el('select', null, el('option', { value: '' }, 'CA: none'));
  for (const c of cas) ca.append(el('option', { value: c }, c));
  const allocate = el('input', { type: 'checkbox' });
  const submit = el('button', { class: 'btn-primary', type: 'submit' }, 'Add');

  const form = el('form', { class: 'inline-form map-form' },
    port, listenScheme, url, mode, ca,
    el('label', { class: 'chk' }, allocate, ' allocate'),
    submit);
  form._editPort = null; // non-null → replace (PUT) that listen port

  allocate.addEventListener('change', () => { url.disabled = allocate.checked; });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const body = buildMappingBody(port.value, url.value, listenScheme.value === 'https', mode.value, ca.value, allocate.checked);
      if (form._editPort != null) await api('PUT', mapURL(lb.name, form._editPort), body);
      else await api('POST', mapURL(lb.name), body);
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });

  // _fill prefills this panel's form from an existing mapping for a replace (PUT).
  form._fill = (m) => {
    form._editPort = m.listen_port;
    port.value = m.listen_port;
    port.readOnly = true;
    listenScheme.value = m.listen_tls ? 'https' : 'http';
    url.value = m.upstream ? (m.upstream.scheme + '://' + m.upstream.host + ':' + m.upstream.port) : '';
    mode.value = (m.upstream && m.upstream.tls && m.upstream.tls.mode) || 'verify';
    ca.value = (m.upstream && m.upstream.tls && m.upstream.tls.ca_name) || '';
    allocate.checked = !!m.auto_allocated;
    url.disabled = allocate.checked;
    submit.textContent = 'Save';
    url.focus();
  };
  return form;
}

function buildMappingBody(portStr, urlStr, listenTls, mode, caName, allocate) {
  const body = { listen_tls: listenTls };
  if (String(portStr).trim() !== '') body.listen_port = parseInt(portStr, 10);
  if (allocate) {
    body.allocate = true;
  } else {
    const up = parseUpstream(urlStr);
    body.upstream = { scheme: up.scheme, host: up.host, port: up.port, tls: { mode: mode, ca_name: caName } };
  }
  return body;
}

function parseUpstream(raw) {
  let s = (raw || '').trim();
  if (!s) throw new Error('upstream URL is required (or check "allocate")');
  if (!/^https?:\/\//i.test(s)) s = 'http://' + s;
  let u;
  try { u = new URL(s); } catch (_) { throw new Error('invalid upstream URL: ' + raw); }
  // AIDEV-NOTE: reject (don't silently drop) URL parts the server's upstream has no
  // field for — mirrors client.ParseUpstreamURL so the UI and CLI agree on what a
  // valid upstream is. The scheme-prefix and default-port conveniences above are
  // intentional UI-only sugar and are deliberately kept.
  if (u.username || u.password) throw new Error('upstream URL must not contain userinfo: ' + raw);
  if (u.pathname && u.pathname !== '/') throw new Error('upstream URL must not contain a path: ' + raw);
  if (u.search || u.hash) throw new Error('upstream URL must not contain a query or fragment: ' + raw);
  const scheme = u.protocol.replace(':', '');
  const port = u.port ? parseInt(u.port, 10) : (scheme === 'https' ? 443 : 80);
  return { scheme, host: u.hostname, port };
}

async function deleteMapping(lb, m) {
  const tgt = m.upstream ? m.upstream.host + ':' + m.upstream.port : '';
  const ok = await confirmAction('Delete mapping ' + m.listen_port + ' → ' + tgt + '?');
  if (!ok) return;
  try {
    await api('DELETE', mapURL(lb.name, m.listen_port));
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- port pool (right column) ----------------------------------------------
function renderPorts(pool) {
  const card = byId('port-pool');
  clear(card);
  if (!pool) return;
  const total = (pool.used || 0) + (pool.free || 0);
  const pct = total > 0 ? Math.round((pool.used / total) * 100) : 0;
  card.append(
    el('div', { class: 'muted' }, 'Range ', el('strong', null, pool.start + '–' + pool.end)),
    el('div', { class: 'bar' }, el('div', { class: 'bar-fill', style: 'width:' + pct + '%' })),
    el('div', { class: 'muted small' }, (pool.used || 0) + ' used · ' + (pool.free || 0) + ' free'),
  );
  const tbody = el('tbody');
  for (const a of (pool.allocations || []).slice().sort((x, y) => x.port - y.port)) {
    tbody.append(el('tr', null,
      el('td', null, String(a.port)),
      el('td', { class: a.owner ? '' : 'muted' }, a.owner || '—'),
      el('td', { class: 'muted' }, a.label || '—', a.auto ? el('span', { class: 'badge' }, 'auto') : null),
      el('td', { class: 'right' }, el('button', { class: 'link', type: 'button', onclick: () => returnPort(a) }, 'return')),
    ));
  }
  card.append(el('table', { class: 'grid' },
    el('thead', null, el('tr', null,
      el('th', null, 'Port'), el('th', null, 'Owner'), el('th', null, 'Label'), el('th', null, ''))),
    tbody));
  card.append(renderAllocateControl());
}

function renderAllocateControl() {
  const wrap = el('div', { class: 'alloc' });
  const open = el('button', { class: 'btn-outline', type: 'button' }, 'Allocate port');
  open.addEventListener('click', () => {
    const owner = el('input', { type: 'text', placeholder: 'owner (optional)' });
    const label = el('input', { type: 'text', placeholder: 'label (optional)' });
    const form = el('form', { class: 'inline-form alloc-form' },
      owner, label,
      el('button', { class: 'btn-primary', type: 'submit' }, 'Allocate'),
      el('button', { class: 'btn-ghost', type: 'button', onclick: () => refresh().catch(swallow) }, 'Cancel'));
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        await api('POST', '/api/v1/ports/allocate', { owner: owner.value.trim(), label: label.value.trim() });
        clearError();
        await refresh();
      } catch (err) { swallow(err); }
    });
    clear(wrap);
    wrap.append(form);
    owner.focus();
  });
  wrap.append(open);
  return wrap;
}

async function returnPort(a) {
  const ok = await confirmAction('Return port ' + a.port + '? Any mapping using it will be affected.');
  if (!ok) return;
  try {
    await api('DELETE', '/api/v1/ports/' + a.port);
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- bootstrap -------------------------------------------------------------
function init() {
  byId('login-form').addEventListener('submit', onLogin);
  byId('logout').addEventListener('click', onLogout);
  byId('refresh').addEventListener('click', () => refresh().catch(swallow));
  byId('add-lb').addEventListener('click', () => { addingLB = !addingLB; refresh().catch(swallow); });
  byId('error-dismiss').addEventListener('click', clearError);
  byId('confirm-ok').addEventListener('click', () => settleConfirm(true));
  byId('confirm-cancel').addEventListener('click', () => settleConfirm(false));
  byId('confirm-overlay').addEventListener('click', (e) => { if (e.target === byId('confirm-overlay')) settleConfirm(false); });
  boot();
}

async function boot() {
  if (!getToken()) { showLogin(); return; }
  try {
    await api('GET', '/api/v1/status'); // verify the stored token
    await startDashboard();
  } catch (e) {
    if (e !== AUTH) showLogin(); // network error → login (can't verify)
  }
}

async function onLogin(e) {
  e.preventDefault();
  const t = byId('token').value.trim();
  if (!t) return;
  setToken(t);
  byId('login-error').hidden = true;
  try {
    await api('GET', '/api/v1/status');
    await startDashboard();
  } catch (err) {
    const msg = err === AUTH ? 'Invalid or expired token' : ((err && err.message) || 'Connection failed');
    byId('login-error').textContent = msg;
    byId('login-error').hidden = false;
  }
}

function onLogout() {
  stopPolling();
  settleConfirm(false); // resolve any open confirm promise and hide its overlay
  clearToken();
  expanded.clear();
  labelEditing.clear();
  addingLB = false;
  showLogin();
}

async function startDashboard() {
  showDashboard();
  try { cas = (await api('GET', '/api/v1/cas')) || []; } catch (e) { if (e === AUTH) return; cas = []; }
  try { await refresh(); } catch (e) { if (e === AUTH) return; swallow(e); }
  startPolling();
}

// The <script> tag is deferred, so the DOM is parsed before this runs.
init();
