// Household Hub web app. Plain JS, no build step. All HTML goes through esc() before it is inserted.
'use strict';

const S = { me: null, settings: {}, users: [], cats: [], accounts: [], view: 'dashboard', latest: 0, calMonth: null, calMode: 'month', txMonth: null, budgetMonth: null };
const $ = (sel, root = document) => root.querySelector(sel);
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const inWindowApp = () => typeof window.hhubSaveDevice === 'function';

// ---------- helpers ----------
async function api(method, path, body) {
  const opt = { method, headers: { 'X-HHub': '1' }, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch(path, opt);
  let data = null;
  try { data = await r.json(); } catch (_) { /* not JSON */ }
  if (r.status === 401 && path !== '/api/login') { showAuth(); throw new Error('Please sign in'); }
  if (!r.ok) throw new Error((data && data.error) || ('HTTP ' + r.status));
  return data;
}
function toast(msg, err) {
  const t = $('#toast'); t.textContent = msg; t.className = 'toast' + (err ? ' err' : '');
  clearTimeout(toast.t); toast.t = setTimeout(() => t.classList.add('hidden'), err ? 6000 : 2600);
}
const fail = (e) => toast(e.message || String(e), true);
function money(c, sign) {
  const cur = S.settings.currency || '$';
  const n = Math.abs(c || 0) / 100;
  const s = cur + n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  if (c < 0) return '-' + s;
  return (sign && c > 0 ? '+' : '') + s;
}
function toCents(v) {
  v = String(v || '').replace(/[$,\s]/g, '');
  if (v === '') return 0;
  const n = Math.round(parseFloat(v) * 100);
  if (isNaN(n)) throw new Error('Amount is not a number');
  return n;
}
const centsInput = (c) => (c ? (Math.abs(c) / 100).toFixed(2) : '');
const pad = (n) => String(n).padStart(2, '0');
const ymd = (d) => d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
const ym = (d) => d.getFullYear() + '-' + pad(d.getMonth() + 1);
function parseLocal(s) { // "2026-11-25" or "2026-11-25T14:00"
  const [d, t] = s.split('T'); const [y, m, dd] = d.split('-').map(Number);
  const [hh, mm] = (t || '00:00').split(':').map(Number);
  return new Date(y, m - 1, dd, hh, mm);
}
function fmtDate(s, opts) { return parseLocal(s).toLocaleDateString(undefined, opts || { weekday: 'short', month: 'short', day: 'numeric' }); }
function fmtTime(s) { return s.includes('T') ? parseLocal(s).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }) : 'All day'; }
function relDay(s) {
  const t = parseLocal(s.slice(0, 10)); const now = new Date(); now.setHours(0, 0, 0, 0);
  const d = Math.round((t - now) / 86400000);
  if (d === 0) return 'Today'; if (d === 1) return 'Tomorrow'; if (d === -1) return 'Yesterday';
  if (d < 0) return Math.abs(d) + ' days ago'; if (d < 7) return 'In ' + d + ' days';
  return fmtDate(s);
}
const userBy = (id) => S.users.find((u) => u.id === id);
const userName = (id) => (userBy(id) || {}).display_name || '?';
const catName = (id) => (S.cats.find((c) => c.id === id) || {}).name || '';
const accName = (id) => (S.accounts.find((a) => a.id === id) || {}).name || '';
const canFin = () => S.me && (S.me.role === 'admin' || S.me.role === 'adult');
const isAdmin = () => S.me && S.me.role === 'admin';
function memberColor(o) {
  if (o.color) return o.color;
  const ids = (o.members || '').split(',').filter(Boolean).map(Number);
  if (ids.length === 1 && userBy(ids[0])) return userBy(ids[0]).color;
  if (o.owner_id && o.private && userBy(o.owner_id)) return userBy(o.owner_id).color;
  return '#4f7cff';
}
function whoText(o) {
  const ids = (o.members || '').split(',').filter(Boolean).map(Number);
  if (o.private) return 'Private';
  if (!ids.length) return 'Everyone';
  return ids.map(userName).join(', ');
}
function opts(list, sel, blank) {
  return (blank !== undefined ? `<option value="">${esc(blank)}</option>` : '') +
    list.map((x) => `<option value="${esc(x[0])}"${String(x[0]) === String(sel ?? '') ? ' selected' : ''}>${esc(x[1])}</option>`).join('');
}
function formData(form) {
  const o = {};
  for (const el of form.elements) {
    if (!el.name) continue;
    if (el.type === 'checkbox') { if (el.dataset.group) { (o[el.dataset.group] = o[el.dataset.group] || []); if (el.checked) o[el.dataset.group].push(el.value); } else o[el.name] = el.checked; }
    else if (el.type === 'radio') { if (el.checked) o[el.name] = el.value; }
    else o[el.name] = el.value;
  }
  return o;
}
const idOrNull = (v) => (v ? Number(v) : null);

// ---------- modal ----------
function openModal(html, onReady) {
  $('#sheet').innerHTML = html; $('#modal').classList.remove('hidden');
  const f = $('#sheet input:not([type=hidden]), #sheet select'); if (f && window.innerWidth > 760) f.focus();
  if (onReady) onReady($('#sheet'));
}
function closeModal() { $('#modal').classList.add('hidden'); $('#sheet').innerHTML = ''; }
$('#modal').addEventListener('mousedown', (e) => { if (e.target.id === 'modal') closeModal(); });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeModal(); });

// ---------- auth ----------
function showAuth(setup) {
  $('#app').classList.add('hidden'); const a = $('#auth'); a.classList.remove('hidden');
  if (setup) {
    a.innerHTML = `<form class="card" id="setupForm"><img src="icon.svg" alt=""><h1>Welcome</h1>
      <p class="mute">Create the first account. It is the household admin and can add everyone else.</p>
      <label>Household name</label><input name="household_name" placeholder="The Smith Family" required>
      <label>Your name</label><input name="display_name" required>
      <label>Username</label><input name="username" autocomplete="username" required>
      <label>Password (8+ characters)</label><input name="password" type="password" autocomplete="new-password" minlength="8" required>
      <div class="err" id="authErr"></div><button class="primary" style="width:100%;margin-top:.6rem">Create household</button></form>`;
    $('#setupForm').addEventListener('submit', async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api('POST', '/api/setup', { Username: d.username, DisplayName: d.display_name, Password: d.password, HouseholdName: d.household_name }); boot(); }
      catch (err) { $('#authErr').textContent = err.message; }
    });
  } else {
    a.innerHTML = `<form class="card" id="loginForm"><img src="icon.svg" alt=""><h1>${esc(S.settings.household_name || 'Household Hub')}</h1>
      <label>Username</label><input name="username" autocomplete="username" required>
      <label>Password</label><input name="password" type="password" autocomplete="current-password" required>
      <div class="err" id="authErr"></div><button class="primary" style="width:100%;margin-top:.6rem">Sign in</button>
      ${inWindowApp() ? '<p style="text-align:center"><button type="button" class="link" data-action="switch-server">Change server</button></p>' : ''}</form>`;
    $('#loginForm').addEventListener('submit', async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        const r = await api('POST', '/api/login', { Username: d.username, Password: d.password });
        if (r && r.need_code) return askLoginCode(r.ticket);
        boot();
      }
      catch (err) { $('#authErr').textContent = err.message; }
    });
  }
}

async function boot() {
  let st;
  try { st = await api('GET', '/api/state'); } catch (e) { document.body.textContent = 'Cannot reach the server: ' + e.message; return; }
  S.settings = st.settings || {};
  document.title = S.settings.household_name || 'Household Hub';
  if (st.setup_needed) return showAuth(true);
  if (!st.user) return showAuth(false);
  S.me = st.user; S.latest = st.latest_notification || 0;
  $('#auth').classList.add('hidden'); $('#app').classList.remove('hidden');
  $('#hhName').textContent = S.settings.household_name || 'Household Hub';
  $('#whoName').textContent = S.me.display_name;
  document.querySelectorAll('#nav .fin').forEach((a) => a.classList.toggle('hidden', !canFin()));
  setBadge(st.unread);
  reviewBadge();
  registerWindowDevice();
  route();
  clearInterval(boot.poll); boot.poll = setInterval(poll, 30000);
}

async function loadRefs() {
  S.users = await api('GET', '/api/users');
  if (canFin()) { [S.cats, S.accounts] = await Promise.all([api('GET', '/api/categories'), api('GET', '/api/accounts')]); }
}

// The Windows app gives each PC a device token so its background notifier can show Windows toasts.
async function registerWindowDevice() {
  if (!inWindowApp()) return;
  try {
    const has = await window.hhubHasDevice(S.me.username);
    if (has) return;
    const host = (await window.hhubHostName()) || 'Windows PC';
    const d = await api('POST', '/api/me/devices', { Label: 'Windows: ' + host });
    await window.hhubSaveDevice(JSON.stringify({ token: d.token, latest: d.latest, user: S.me.username }));
  } catch (e) { console.warn('device registration', e); }
}

function setBadge(n) { const b = $('#badge'); b.textContent = n; b.classList.toggle('hidden', !n); }

// Poll for new notifications: badge + browser pop-ups (the Windows app shows toasts itself).
async function poll() {
  try {
    const st = await api('GET', '/api/state');
    if (!st.user) return;
    setBadge(st.unread);
    reviewBadge();
    if (st.latest_notification > S.latest) {
      const r = await api('GET', '/api/notifications?after=' + S.latest);
      S.latest = st.latest_notification;
      if (!inWindowApp() && 'Notification' in window && Notification.permission === 'granted') {
        for (const n of r.items) { try { new Notification(n.title, { body: n.body, icon: 'icon-192.png', tag: 'hhub-' + n.id }); } catch (_) { /* ignore */ } }
      }
      if (S.view === 'alerts') render();
    }
  } catch (_) { /* offline */ }
}

document.addEventListener('click', async (e) => {
  const a = e.target.closest('[data-action]'); if (!a) return;
  const act = a.dataset.action;
  if (act === 'logout') { await api('POST', '/api/logout').catch(() => {}); S.me = null; showAuth(false); }
  if (act === 'switch-server' && window.hhubSwitchServer) window.hhubSwitchServer();
  if (act === 'close') closeModal();
});

// ---------- routing ----------
// Later feature files add pages here: EXTRA_VIEWS[name] = fn, and money pages to FIN_VIEWS.
const EXTRA_VIEWS = {};
const FIN_VIEWS = ['bills', 'money', 'budget', 'accounts', 'import'];
const SETTINGS_TABS = []; // [key, title, fn, adminOnly]
function allViews() { return { dashboard, calendar, bills, money: moneyView, budget, accounts, alerts, settings, import: importView, ...EXTRA_VIEWS }; }
function route() {
  if (!S.me) return;
  let [v, q] = (location.hash.slice(1) || 'dashboard').split('?');
  const views = allViews();
  if (!views[v] || (!canFin() && FIN_VIEWS.includes(v))) v = 'dashboard';
  S.view = v; S.query = new URLSearchParams(q || '');
  document.querySelectorAll('#nav a').forEach((a) => a.classList.toggle('on', a.dataset.view === v));
  loadRefs().catch(() => {}).then(render); // members, categories and accounts may have changed on another device
}
async function render() {
  const fn = allViews()[S.view];
  try { await fn($('#main')); } catch (e) { $('#main').innerHTML = `<div class="card err">${esc(e.message)}</div>`; }
}
window.addEventListener('hashchange', route);

// ---------- dashboard ----------
async function dashboard(el) {
  const d = await api('GET', '/api/dashboard');
  const today = d.today;
  let html = `<h1>Hi ${esc(S.me.display_name)}</h1>`;
  if (d.summary) {
    const s = d.summary; const net = s.income_cents - s.expense_cents;
    const left = s.budget_cents ? s.budget_left_cents : null;
    html += `<div class="stats">
      <div class="stat"><small>Income this month</small><b class="good">${money(s.income_cents)}</b></div>
      <div class="stat"><small>Spent this month</small><b>${money(s.expense_cents)}</b></div>
      <div class="stat"><small>Net</small><b class="${net < 0 ? 'bad' : 'good'}">${money(net, true)}</b></div>
      ${left !== null ? `<div class="stat"><small>Budget left</small><b class="${left < 0 ? 'bad' : ''}">${money(left)}</b></div>` : ''}</div>`;
  }
  html += '<div class="grid">';
  if (d.bills) {
    html += `<div class="card"><div class="spread"><h2>Bills due (30 days)</h2><a href="#bills">All bills</a></div><ul class="list">` +
      (d.bills.length ? d.bills.map((b) => `<li><span class="grow"><b>${esc(b.name)}</b><br><small>${b.overdue ? '<span class="pill bad">Overdue</span> ' : ''}${esc(relDay(b.date))} &middot; ${esc(fmtDate(b.date))}${b.autopay ? ' &middot; autopay' : ''}</small></span>
        <span class="num">${money(b.amount_cents)}</span><button class="small" data-pay="${b.bill_id}|${b.date}">Paid</button></li>`).join('') : '<li class="empty">Nothing due. Nice.</li>') + '</ul></div>';
  }
  html += `<div class="card"><div class="spread"><h2>Next 7 days</h2><button class="small primary" id="dashAdd">+ Event</button></div><ul class="list">` +
    (d.events.length ? d.events.map((o) => `<li ${o.source === 'feed' ? '' : `data-occ="${esc(o.id)}" class="click"`}><span class="dot" style="background:${esc(memberColor(o))}"></span>
      <span class="grow"><b>${esc(o.title)}</b><br><small>${esc(o.start.slice(0, 10) === today ? 'Today' : relDay(o.start))} &middot; ${esc(fmtTime(o.start))} &middot; ${esc(whoText(o))}${o.location ? ' &middot; ' + esc(o.location) : ''}</small></span></li>`).join('') : '<li class="empty">Nothing planned.</li>') + '</ul></div>';
  if (d.accounts) {
    html += `<div class="card"><div class="spread"><h2>Accounts</h2><a href="#accounts">Manage</a></div><ul class="list">` +
      (d.accounts.filter((a) => !a.archived).map((a) => `<li><span class="grow">${esc(a.name)} ${a.private ? '<span class="pill">private</span>' : ''}<br><small>${esc(a.type)}</small></span><b class="${a.balance_cents < 0 ? 'bad' : ''}">${money(a.balance_cents)}</b></li>`).join('') || '<li class="empty">No accounts yet.</li>') + '</ul></div>';
  }
  html += '</div>';
  el.innerHTML = html;
  $('#dashAdd').onclick = () => eventForm({ start: today });
  el.querySelectorAll('[data-occ]').forEach((li) => (li.onclick = async () => eventForm(await api('GET', '/api/events/' + li.dataset.occ))));
  el.querySelectorAll('[data-pay]').forEach((b) => (b.onclick = () => { const [id, occ] = b.dataset.pay.split('|'); payForm(Number(id), occ); }));
  if (typeof dashboardExtras === 'function') dashboardExtras(el);
}

// ---------- calendar ----------
async function calendar(el) {
  const qd = S.query.get('d');
  if (qd) S.calMonth = parseLocal(qd.slice(0, 7) + '-01');
  if (!S.calMonth) { const n = new Date(); S.calMonth = new Date(n.getFullYear(), n.getMonth(), 1); }
  const m = S.calMonth; const ws = Number(S.settings.week_start || 0);
  const first = new Date(m.getFullYear(), m.getMonth(), 1);
  const start = new Date(first); start.setDate(1 - ((first.getDay() - ws + 7) % 7));
  const end = new Date(start); end.setDate(start.getDate() + 41);
  let from = start, to = end;
  if (S.calMode === 'agenda') { from = new Date(); from.setHours(0, 0, 0, 0); to = new Date(from); to.setDate(to.getDate() + 60); }
  const occ = await api('GET', `/api/calendar?from=${ymd(from)}&to=${ymd(to)}`);
  const head = `<div class="spread"><h1>${S.calMode === 'agenda' ? 'Next 60 days' : esc(m.toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</h1>
    <div class="row">${S.calMode === 'month' ? '<button id="calPrev">&lsaquo;</button><button id="calToday">Today</button><button id="calNext">&rsaquo;</button>' : ''}
    <button id="calMode">${S.calMode === 'month' ? 'List' : 'Month'}</button><button class="primary" id="calAdd">+ Event</button></div></div>`;
  if (S.calMode === 'agenda') {
    let html = head + '<div class="card"><ul class="list">'; let last = '';
    for (const o of occ) {
      const day = o.start.slice(0, 10);
      if (day !== last) { html += `<li><h3 style="margin:.3rem 0 0">${esc(relDay(day))} &middot; ${esc(fmtDate(day))}</h3></li>`; last = day; }
      html += occRow(o);
    }
    el.innerHTML = html + (occ.length ? '' : '<li class="empty">Nothing in the next 60 days.</li>') + '</ul></div>';
  } else {
    const byDay = {};
    for (const o of occ) {
      const s = parseLocal(o.start.slice(0, 10)); const e = o.end ? parseLocal(o.end.slice(0, 10)) : s;
      for (let d = new Date(s); d <= e; d.setDate(d.getDate() + 1)) (byDay[ymd(d)] = byDay[ymd(d)] || []).push(o);
    }
    const names = []; for (let i = 0; i < 7; i++) { const d = new Date(start); d.setDate(start.getDate() + i); names.push(d.toLocaleDateString(undefined, { weekday: 'short' })); }
    let html = head + '<div class="cal">' + names.map((n) => `<div class="hd">${esc(n)}</div>`).join('');
    const todayS = ymd(new Date());
    for (let i = 0; i < 42; i++) {
      const d = new Date(start); d.setDate(start.getDate() + i); const k = ymd(d); const list = byDay[k] || [];
      html += `<div class="day${d.getMonth() !== m.getMonth() ? ' other' : ''}${k === todayS ? ' today' : ''}" data-day="${k}"><span class="n">${d.getDate()}</span>` +
        list.slice(0, 3).map((o) => `<div class="ev${o.source === 'bill' ? ' bill' : ''}${o.paid ? ' paid' : ''}${o.overdue ? ' overdue' : ''}" ${o.source === 'bill' ? '' : `style="background:${esc(memberColor(o))}"`} title="${esc(o.title)}">${o.all_day ? '' : esc(fmtTime(o.start).replace(/:00| /g, '').toLowerCase()) + ' '}${esc(o.title)}</div>`).join('') +
        (list.length > 3 ? `<div class="more">+${list.length - 3} more</div>` : '') + '</div>';
    }
    el.innerHTML = html + '</div>';
    el.querySelectorAll('[data-day]').forEach((c) => (c.onclick = () => dayPanel(c.dataset.day, byDay[c.dataset.day] || [])));
    $('#calPrev').onclick = () => { S.calMonth = new Date(m.getFullYear(), m.getMonth() - 1, 1); history.replaceState(null, '', '#calendar'); calendar(el); };
    $('#calNext').onclick = () => { S.calMonth = new Date(m.getFullYear(), m.getMonth() + 1, 1); history.replaceState(null, '', '#calendar'); calendar(el); };
    $('#calToday').onclick = () => { S.calMonth = null; history.replaceState(null, '', '#calendar'); S.query = new URLSearchParams(); calendar(el); };
  }
  $('#calMode').onclick = () => { S.calMode = S.calMode === 'month' ? 'agenda' : 'month'; calendar(el); };
  $('#calAdd').onclick = () => eventForm({ start: ymd(new Date()) });
  bindOccRows(el);
}
function occRow(o) {
  if (o.source === 'bill') {
    return `<li class="click" data-bill="${o.id}|${esc(o.start)}"><span class="dot" style="background:var(--bill)"></span><span class="grow"><b>${esc(o.title)}</b> ${o.paid ? '<span class="pill good">paid</span>' : o.overdue ? '<span class="pill bad">overdue</span>' : ''}<br><small>Bill &middot; ${money(o.amount_cents)}</small></span></li>`;
  }
  if (o.source === 'feed') {
    return `<li><span class="dot" style="background:${esc(memberColor(o))}"></span><span class="grow"><b>${esc(o.title)}</b> <span class="pill">${esc(o.kind)}</span><br><small>${esc(fmtTime(o.start))}${o.end && o.end.includes('T') ? ' - ' + esc(fmtTime(o.end)) : ''}${o.location ? ' &middot; ' + esc(o.location) : ''}</small></span></li>`;
  }
  return `<li class="click" data-ev="${o.id}"><span class="dot" style="background:${esc(memberColor(o))}"></span><span class="grow"><b>${esc(o.title)}</b>${o.repeats ? ' <small>&#8635;</small>' : ''}<br><small>${esc(fmtTime(o.start))}${o.end && o.end.includes('T') ? ' - ' + esc(fmtTime(o.end)) : ''} &middot; ${esc(whoText(o))}${o.location ? ' &middot; ' + esc(o.location) : ''}</small></span></li>`;
}
function bindOccRows(root) {
  root.querySelectorAll('[data-ev]').forEach((li) => (li.onclick = async () => eventForm(await api('GET', '/api/events/' + li.dataset.ev).catch(fail))));
  root.querySelectorAll('[data-bill]').forEach((li) => (li.onclick = () => { const [id, occ] = li.dataset.bill.split('|'); payForm(Number(id), occ); }));
}
function dayPanel(day, list) {
  openModal(`<div class="spread"><h2>${esc(fmtDate(day, { weekday: 'long', month: 'long', day: 'numeric' }))}</h2><button class="small" data-action="close">&times;</button></div>
    <ul class="list">${list.map(occRow).join('') || '<li class="empty">Nothing on this day.</li>'}</ul>
    <div class="actions"><button class="primary" id="dpAdd">+ Event on this day</button></div>`, (sh) => {
    bindOccRows(sh); $('#dpAdd', sh).onclick = () => eventForm({ start: day });
  });
}

const REMIND = [[0, 'At start'], [15, '15 min'], [60, '1 hour'], [180, '3 hours'], [1440, '1 day'], [2880, '2 days'], [10080, '1 week']];
const KINDS = [['appointment', 'Appointment'], ['due', 'Due date / deadline'], ['school', 'School'], ['work', 'Work'], ['birthday', 'Birthday'], ['other', 'Other']];
function freqOpts(sel, bill) {
  return opts([[bill ? 'once' : 'none', bill ? 'One time' : 'Does not repeat'], ['daily', 'Daily'], ['weekly', 'Weekly'], ['monthly', 'Monthly'], ['yearly', 'Yearly']], sel || (bill ? 'monthly' : 'none'));
}

function eventForm(e) {
  if (!e) return;
  const isNew = !e.id; const allDay = e.all_day ?? !String(e.start || '').includes('T');
  const sd = (e.start || ymd(new Date())).slice(0, 10); const stime = (e.start || '').includes('T') ? e.start.slice(11, 16) : '09:00';
  const ed = (e.end || '').slice(0, 10); const etime = (e.end || '').includes('T') ? e.end.slice(11, 16) : '';
  const rem = (e.remind_minutes ?? (isNew ? '60' : '')).split(',').filter((x) => x !== '').map(Number);
  const mem = (e.members || '').split(',').filter(Boolean).map(Number);
  const extra = rem.filter((r) => !REMIND.some((x) => x[0] === r));
  const mine = isNew || e.owner_id === S.me.id || !e.private;
  openModal(`<form id="evForm"><h2>${isNew ? 'New event' : 'Edit event'}</h2>
    <div class="form2">
    <div class="full"><label>Title</label><input name="title" value="${esc(e.title)}" required placeholder="Dentist, soccer practice, car registration due..."></div>
    <div><label>Type</label><select name="kind">${opts(KINDS, e.kind || 'appointment')}</select></div>
    <div><label>&nbsp;</label><label class="inline"><input type="checkbox" name="all_day" ${allDay ? 'checked' : ''}> All day</label></div>
    <div><label>Date</label><input type="date" name="sd" value="${esc(sd)}" required></div>
    <div class="tm"><label>Time</label><input type="time" name="st" value="${esc(stime)}"></div>
    <div><label>End date (optional)</label><input type="date" name="ed" value="${esc(ed)}"></div>
    <div class="tm"><label>End time</label><input type="time" name="et" value="${esc(etime)}"></div>
    <div><label>Repeats</label><select name="freq">${freqOpts(e.freq)}</select></div>
    <div><label>Every</label><input type="number" name="interval" min="1" value="${esc(e.interval || 1)}"></div>
    <div class="full"><label>Repeat until (optional)</label><input type="date" name="until" value="${esc(e.until)}"></div>
    <div class="full"><label>Remind</label>${REMIND.map(([v, t]) => `<label class="inline"><input type="checkbox" data-group="rem" name="rem" value="${v}" ${rem.includes(v) ? 'checked' : ''}>${t}</label>`).join('')}
      <input name="rem_extra" placeholder="Other minutes before, e.g. 30, 4320" value="${esc(extra.join(', '))}" style="margin-top:.3rem"></div>
    <div class="full"><label>Who is it for? (none ticked = everyone; reminders go to these people)</label>
      ${S.users.filter((u) => u.active).map((u) => `<label class="inline"><input type="checkbox" data-group="mem" name="mem" value="${u.id}" ${mem.includes(u.id) ? 'checked' : ''}><span class="dot" style="background:${esc(u.color)}"></span>${esc(u.display_name)}</label>`).join('')}</div>
    <div class="full"><label>Location</label><input name="location" value="${esc(e.location)}"></div>
    <div class="full"><label>Notes</label><textarea name="notes" rows="2">${esc(e.notes)}</textarea></div>
    <div><label class="inline"><input type="checkbox" name="private" ${e.private ? 'checked' : ''}> Private (only me)</label></div>
    <div><label>Color (optional)</label><input type="color" name="color" value="${esc(e.color || '#4f7cff')}"><label class="inline"><input type="checkbox" name="usecolor" ${e.color ? 'checked' : ''}> use</label></div>
    </div>
    <div class="actions">${!isNew && mine ? '<button type="button" class="danger left" id="evDel">Delete</button>' : ''}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    const f = $('#evForm', sh);
    const sync = () => sh.querySelectorAll('.tm').forEach((x) => x.classList.toggle('hidden', f.all_day.checked));
    f.all_day.onchange = sync; sync();
    f.onsubmit = async (ev) => {
      ev.preventDefault(); const d = formData(f);
      const rems = d.rem.map(Number).concat(String(d.rem_extra).split(',').map((x) => parseInt(x, 10)).filter((x) => !isNaN(x) && x >= 0));
      const body = { title: d.title, kind: d.kind, all_day: d.all_day, start: d.all_day ? d.sd : d.sd + 'T' + (d.st || '09:00'), end: '',
        freq: d.freq, interval: Number(d.interval) || 1, until: d.until, remind_minutes: [...new Set(rems)].join(','), members: d.mem.join(','),
        location: d.location, notes: d.notes, private: d.private, color: d.usecolor ? d.color : '' };
      if (d.ed || d.et) body.end = d.all_day ? (d.ed || '') : (d.ed || d.sd) + 'T' + (d.et || d.st || '09:00');
      try { await api(isNew ? 'POST' : 'PUT', '/api/events' + (isNew ? '' : '/' + e.id), body); closeModal(); toast('Saved'); render(); } catch (err) { fail(err); }
    };
    const del = $('#evDel', sh);
    if (del) del.onclick = async () => {
      if (!confirm('Delete "' + e.title + '"' + (e.freq && e.freq !== 'none' ? ' and all its repeats' : '') + '?')) return;
      try { await api('DELETE', '/api/events/' + e.id); closeModal(); toast('Deleted'); render(); } catch (err) { fail(err); }
    };
  });
}

// ---------- bills ----------
async function bills(el) {
  const now = new Date(); const from = new Date(now); from.setDate(from.getDate() - 60); const to = new Date(now); to.setDate(to.getDate() + 60);
  const [list, dues] = await Promise.all([api('GET', '/api/bills'), api('GET', `/api/bills/dues?from=${ymd(from)}&to=${ymd(to)}`)]);
  const todayS = ymd(now);
  const open = dues.filter((d) => !d.paid && (d.overdue || d.date >= todayS));
  const recent = dues.filter((d) => d.paid).reverse().slice(0, 15);
  const monthly = list.filter((b) => b.active).reduce((s, b) => s + monthlyCost(b), 0);
  el.innerHTML = `<div class="spread"><h1>Bills</h1><button class="primary" id="billAdd">+ Bill</button></div>
    <div class="stats"><div class="stat"><small>Unpaid (next 60 days)</small><b>${money(open.reduce((s, d) => s + d.amount_cents, 0))}</b></div>
      <div class="stat"><small>Overdue</small><b class="${open.some((d) => d.overdue) ? 'bad' : ''}">${open.filter((d) => d.overdue).length}</b></div>
      <div class="stat"><small>Bills per month (about)</small><b>${money(Math.round(monthly))}</b></div></div>
    <div class="grid"><div class="card"><h2>Coming up</h2><ul class="list">${open.map((d) => `<li><span class="grow"><b>${esc(d.name)}</b> ${d.overdue ? '<span class="pill bad">overdue</span>' : ''}${d.autopay ? ' <span class="pill">autopay</span>' : ''}<br>
      <small>${esc(relDay(d.date))} &middot; ${esc(fmtDate(d.date))}${d.assigned_to ? ' &middot; ' + esc(userName(d.assigned_to)) : ''}</small></span><span class="num">${money(d.amount_cents)}</span>
      ${d.url ? `<a class="btn small" href="${esc(safeUrl(d.url))}" target="_blank" rel="noopener">Pay site</a>` : ''}<button class="small primary" data-pay="${d.bill_id}|${d.date}">Paid</button></li>`).join('') || '<li class="empty">All caught up.</li>'}</ul></div>
    <div class="card"><h2>Recently paid</h2><ul class="list">${recent.map((d) => `<li><span class="grow">${esc(d.name)}<br><small>due ${esc(fmtDate(d.date))}, paid ${esc(fmtDate(d.paid_date))}</small></span><span class="num">${money(d.paid_cents)}</span><button class="small" data-unpay="${d.bill_id}|${d.date}">Undo</button></li>`).join('') || '<li class="empty">No payments yet.</li>'}</ul></div></div>
    <div class="card scroll"><h2>All bills</h2><table><tr><th>Name</th><th>Repeats</th><th>Next due</th><th>For</th><th class="num">Amount</th></tr>
    ${list.map((b) => { const nx = dues.find((d) => d.bill_id === b.id && !d.paid && d.date >= todayS); return `<tr class="click" data-edit="${b.id}"><td>${esc(b.name)} ${b.private ? '<span class="pill">private</span>' : ''}${!b.active ? ' <span class="pill">paused</span>' : ''}</td><td>${esc(freqText(b))}</td><td>${nx ? esc(fmtDate(nx.date)) : '-'}</td><td>${b.assigned_to ? esc(userName(b.assigned_to)) : 'Adults'}</td><td class="num">${money(b.amount_cents)}</td></tr>`; }).join('') || '<tr><td colspan="5" class="empty">No bills yet. Add rent, utilities, phone, insurance, subscriptions...</td></tr>'}</table></div>`;
  $('#billAdd').onclick = () => billForm({});
  el.querySelectorAll('[data-edit]').forEach((r) => (r.onclick = () => billForm(list.find((b) => b.id === Number(r.dataset.edit)))));
  el.querySelectorAll('[data-pay]').forEach((b) => (b.onclick = () => { const [id, occ] = b.dataset.pay.split('|'); payForm(Number(id), occ); }));
  el.querySelectorAll('[data-unpay]').forEach((b) => (b.onclick = async () => {
    const [id, occ] = b.dataset.unpay.split('|');
    if (!confirm('Mark this as not paid? The expense it recorded is removed too.')) return;
    try { await api('POST', `/api/bills/${id}/unpay`, { Occ: occ }); toast('Marked unpaid'); render(); } catch (e) { fail(e); }
  }));
}
function safeUrl(u) { return /^https?:\/\//i.test(u) ? u : 'https://' + u; }
function monthlyCost(b) {
  const n = b.interval || 1;
  return { daily: b.amount_cents * 30.44 / n, weekly: b.amount_cents * 4.345 / n, monthly: b.amount_cents / n, yearly: b.amount_cents / 12 / n, once: 0 }[b.freq] || 0;
}
function freqText(b) {
  const n = b.interval || 1; const unit = { daily: 'day', weekly: 'week', monthly: 'month', yearly: 'year' }[b.freq];
  if (!unit) return 'One time';
  return n === 1 ? { daily: 'Daily', weekly: 'Weekly', monthly: 'Monthly', yearly: 'Yearly' }[b.freq] : `Every ${n} ${unit}s`;
}
function billForm(b) {
  const isNew = !b.id; const rd = (b.remind_days ?? '3,0').split(',').filter((x) => x !== '').map(Number);
  const adults = S.users.filter((u) => u.active && u.role !== 'kid');
  openModal(`<form id="billForm"><h2>${isNew ? 'New bill' : 'Edit bill'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="name" value="${esc(b.name)}" required placeholder="Electric, rent, car insurance..."></div>
    <div><label>Amount (${esc(S.settings.currency || '$')})</label><input name="amount" inputmode="decimal" value="${esc(centsInput(b.amount_cents))}" placeholder="0.00"></div>
    <div><label>${isNew ? 'Next due date' : 'First due date'}</label><input type="date" name="start_date" value="${esc(b.start_date || ymd(new Date()))}" required></div>
    <div><label>Repeats</label><select name="freq">${freqOpts(b.freq, true)}</select></div>
    <div><label>Every</label><input type="number" min="1" name="interval" value="${esc(b.interval || 1)}"></div>
    <div><label>Ends (optional)</label><input type="date" name="until" value="${esc(b.until)}"></div>
    <div><label>Who pays / gets reminders</label><select name="assigned_to">${opts(adults.map((u) => [u.id, u.display_name]), b.assigned_to, 'All adults')}</select></div>
    <div class="full"><label>Remind</label>${[[7, '1 week before'], [3, '3 days before'], [1, '1 day before'], [0, 'On the due day']].map(([v, t]) => `<label class="inline"><input type="checkbox" data-group="rd" name="rd" value="${v}" ${rd.includes(v) ? 'checked' : ''}>${t}</label>`).join('')}</div>
    <div><label>Category</label><select name="category_id">${opts(S.cats.filter((c) => c.kind === 'expense').map((c) => [c.id, c.name]), b.category_id, '-')}</select></div>
    <div><label>Paid from account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), b.account_id, '-')}</select></div>
    <div class="full"><label>Payment website (optional)</label><input name="url" value="${esc(b.url)}" placeholder="https://..."></div>
    <div class="full"><label>Notes</label><textarea name="notes" rows="2">${esc(b.notes)}</textarea></div>
    <div class="full"><label class="inline"><input type="checkbox" name="autopay" ${b.autopay ? 'checked' : ''}> Autopay (marked paid by itself on the due day)</label>
    <label class="inline"><input type="checkbox" name="private" ${b.private ? 'checked' : ''}> Private (only me)</label>
    ${isNew ? '' : `<label class="inline"><input type="checkbox" name="active" ${b.active ? 'checked' : ''}> Active</label>`}</div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="billDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    const f = $('#billForm', sh);
    f.onsubmit = async (ev) => {
      ev.preventDefault(); const d = formData(f);
      try {
        const body = { name: d.name, amount_cents: toCents(d.amount), start_date: d.start_date, freq: d.freq, interval: Number(d.interval) || 1, until: d.until,
          assigned_to: idOrNull(d.assigned_to), remind_days: d.rd.join(','), category_id: idOrNull(d.category_id), account_id: idOrNull(d.account_id),
          url: d.url, notes: d.notes, autopay: d.autopay, private: d.private, active: isNew ? true : d.active };
        await api(isNew ? 'POST' : 'PUT', '/api/bills' + (isNew ? '' : '/' + b.id), body); closeModal(); toast('Saved'); render();
      } catch (err) { fail(err); }
    };
    const del = $('#billDel', sh);
    if (del) del.onclick = async () => {
      if (!confirm('Delete bill "' + b.name + '" and its payment history? Recorded expenses stay.')) return;
      try { await api('DELETE', '/api/bills/' + b.id); closeModal(); toast('Deleted'); render(); } catch (err) { fail(err); }
    };
  });
}
async function payForm(id, occ) {
  let dues;
  try { dues = await api('GET', `/api/bills/dues?from=${occ}&to=${occ}`); } catch (e) { return fail(e); }
  const d = dues.find((x) => x.bill_id === id) || { name: 'Bill', amount_cents: 0 };
  if (d.paid) {
    openModal(`<h2>${esc(d.name)}</h2><p>Due ${esc(fmtDate(occ))}. <span class="pill good">Paid</span> ${esc(fmtDate(d.paid_date))}, ${money(d.paid_cents)}.</p>
      <div class="actions"><button class="danger left" id="unpay">Mark not paid</button><button data-action="close">Close</button></div>`, (sh) => {
      $('#unpay', sh).onclick = async () => { try { await api('POST', `/api/bills/${id}/unpay`, { Occ: occ }); closeModal(); render(); } catch (e) { fail(e); } };
    });
    return;
  }
  openModal(`<form id="payForm"><h2>Pay ${esc(d.name)}</h2><p class="mute">Due ${esc(fmtDate(occ))}</p><div class="form2">
    <div><label>Amount paid</label><input name="amount" inputmode="decimal" value="${esc(centsInput(d.amount_cents))}"></div>
    <div><label>Paid on</label><input type="date" name="paid_date" value="${ymd(new Date())}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="record" checked> Also record it as an expense</label></div></div>
    <div class="actions"><button type="button" data-action="close">Cancel</button><button class="primary">Mark paid</button></div></form>`, (sh) => {
    $('#payForm', sh).onsubmit = async (ev) => {
      ev.preventDefault(); const f = formData(ev.target);
      try { await api('POST', `/api/bills/${id}/pay`, { occ, paid_date: f.paid_date, amount_cents: toCents(f.amount), record: f.record }); closeModal(); toast('Marked paid'); S.accounts = await api('GET', '/api/accounts'); render(); } catch (e) { fail(e); }
    };
  });
}

// ---------- money (transactions) ----------
function monthBounds(m) { const s = new Date(m.getFullYear(), m.getMonth(), 1); const e = new Date(m.getFullYear(), m.getMonth() + 1, 0); return [ymd(s), ymd(e)]; }
async function moneyView(el) {
  if (!S.txMonth) S.txMonth = new Date();
  const f = S.txFilter || (S.txFilter = { account: '', category: '', q: '' });
  const [from, to] = monthBounds(S.txMonth);
  const qs = new URLSearchParams({ from, to, account: f.account, category: f.category, q: f.q });
  const txs = await api('GET', '/api/transactions?' + qs);
  const inc = txs.filter((t) => t.amount_cents > 0).reduce((s, t) => s + t.amount_cents, 0);
  const out = txs.filter((t) => t.amount_cents < 0).reduce((s, t) => s - t.amount_cents, 0);
  el.innerHTML = `<div class="spread"><h1>Money</h1><div class="row"><button id="mPrev">&lsaquo;</button><b>${esc(S.txMonth.toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</b><button id="mNext">&rsaquo;</button>
    <button class="primary" id="txAdd">+ Add</button></div></div>
    <div class="card"><div class="row"><select id="fAcc" style="width:auto">${opts(S.accounts.map((a) => [a.id, a.name]), f.account, 'All accounts')}</select>
      <select id="fCat" style="width:auto">${opts(S.cats.map((c) => [c.id, c.name]), f.category, 'All categories')}</select>
      <input id="fQ" placeholder="Search payee or notes" value="${esc(f.q)}" style="width:auto;flex:1;min-width:140px">
      <a class="btn" href="/api/transactions.csv?${qs}" download>Export CSV</a></div></div>
    <div class="stats"><div class="stat"><small>In</small><b class="good">${money(inc)}</b></div><div class="stat"><small>Out</small><b>${money(out)}</b></div><div class="stat"><small>Net</small><b class="${inc - out < 0 ? 'bad' : 'good'}">${money(inc - out, true)}</b></div></div>
    <div id="bulkBar"></div>
    <div class="card scroll"><table><tr><th><input type="checkbox" id="selAll" title="Select all"></th><th>Date</th><th>Payee</th><th>Category</th><th>Account</th><th>By</th><th class="num">Amount</th></tr>
    ${txs.map((t) => `<tr class="click" data-tx="${t.id}"><td><input type="checkbox" data-sel="${t.id}"></td><td>${esc(fmtDate(t.date, { month: 'short', day: 'numeric' }))}</td><td>${esc(t.payee)} ${t.private ? '<span class="pill">private</span>' : ''}${t.business ? ' <span class="pill">business</span>' : ''}${t.split ? ' <span class="pill">split</span>' : ''}${t.receipts ? ' <span class="pill">&#128206; ' + t.receipts + '</span>' : ''}${t.bill_id ? ' <span class="pill">bill</span>' : ''}${t.transfer ? ' <span class="pill">transfer</span>' : ''}${t.source && t.source !== 'manual' ? ` <span class="pill">${esc(t.source)}</span>` : ''}<br><small>${esc(t.notes)}</small></td>
      <td>${t.split ? '<i>split</i>' : esc(catName(t.category_id))}</td><td>${esc(accName(t.account_id))}</td><td>${esc(userName(t.user_id))}</td><td class="num ${t.amount_cents > 0 ? 'good' : ''}">${money(t.amount_cents, true)}</td></tr>`).join('') || '<tr><td colspan="7" class="empty">No transactions this month.</td></tr>'}</table></div>`;
  const shift = (n) => { S.txMonth = new Date(S.txMonth.getFullYear(), S.txMonth.getMonth() + n, 1); moneyView(el); };
  $('#mPrev').onclick = () => shift(-1); $('#mNext').onclick = () => shift(1);
  $('#txAdd').onclick = () => txForm({});
  $('#fAcc').onchange = (e) => { f.account = e.target.value; moneyView(el); };
  $('#fCat').onchange = (e) => { f.category = e.target.value; moneyView(el); };
  $('#fQ').onchange = (e) => { f.q = e.target.value; moneyView(el); };
  el.querySelectorAll('[data-tx]').forEach((r) => (r.onclick = (e) => { if (e.target.closest('[data-sel]')) return; txForm(txs.find((t) => t.id === Number(r.dataset.tx))); }));
  if (typeof bulkBar === 'function') bulkBar(el, () => moneyView(el));
}
function txForm(t) {
  const isNew = !t.id; const type = t.amount_cents > 0 ? 'income' : 'expense';
  openModal(`<form id="txForm"><h2>${isNew ? 'Add transaction' : 'Edit transaction'}</h2>
    <div class="tabs"><label class="inline"><input type="radio" name="type" value="expense" ${type === 'expense' ? 'checked' : ''}> Money out</label><label class="inline"><input type="radio" name="type" value="income" ${type === 'income' ? 'checked' : ''}> Money in</label></div>
    <div class="form2"><div><label>Amount</label><input name="amount" inputmode="decimal" value="${esc(centsInput(t.amount_cents))}" required></div>
    <div><label>Date</label><input type="date" name="date" value="${esc(t.date || ymd(new Date()))}" required></div>
    <div class="full"><label>Payee / source</label><input name="payee" value="${esc(t.payee)}" list="payees"></div>
    <div><label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name + (c.kind === 'income' ? ' (income)' : '')]), t.category_id, '-')}</select></div>
    <div><label>Account</label><select name="account_id">${opts(S.accounts.filter((a) => !a.archived || a.id === t.account_id).map((a) => [a.id, a.name]), t.account_id ?? (S.accounts[0] || {}).id, '-')}</select></div>
    <div class="full"><label>Notes</label><input name="notes" value="${esc(t.notes)}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="private" ${t.private ? 'checked' : ''}> Private (only me)</label>
    <label class="inline"><input type="checkbox" name="transfer" ${t.transfer ? 'checked' : ''}> Transfer between my own accounts (not income or spending)</label>
    <label class="inline"><input type="checkbox" name="business" ${t.business ? 'checked' : ''}> Business</label></div></div>
    <div id="txExtra"></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="txDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#txForm', sh).onsubmit = async (ev) => {
      ev.preventDefault(); const d = formData(ev.target);
      try {
        let c = Math.abs(toCents(d.amount)); if (d.type === 'expense') c = -c;
        await api(isNew ? 'POST' : 'PUT', '/api/transactions' + (isNew ? '' : '/' + t.id), { date: d.date, amount_cents: c, payee: d.payee, category_id: idOrNull(d.category_id), account_id: idOrNull(d.account_id), notes: d.notes, private: d.private, transfer: d.transfer, business: d.business });
        closeModal(); toast('Saved'); S.accounts = await api('GET', '/api/accounts'); render();
      } catch (err) { fail(err); }
    };
    if (!isNew && typeof txFormExtras === 'function') txFormExtras(t, $('#txExtra', sh));
    const del = $('#txDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete this transaction?')) return; try { await api('DELETE', '/api/transactions/' + t.id); closeModal(); render(); } catch (e) { fail(e); } };
  });
}

// ---------- budget ----------
async function budget(el) {
  if (!S.budgetMonth) S.budgetMonth = new Date();
  const s = await api('GET', '/api/summary?month=' + ym(S.budgetMonth));
  const exp = s.categories.filter((c) => c.kind === 'expense'); const inc = s.categories.filter((c) => c.kind === 'income');
  const row = (c) => {
    const pct = c.budget_cents ? Math.min(100, Math.round((c.spent_cents / c.budget_cents) * 100)) : 0;
    const cls = c.budget_cents && c.spent_cents > c.budget_cents ? 'over' : pct >= 85 ? 'near' : '';
    return `<tr><td><b>${esc(c.name)}</b>${c.budget_cents ? `<div class="bar ${cls}"><i style="width:${pct}%"></i></div>` : ''}</td><td class="num">${money(c.spent_cents)}</td>
      <td class="num"><input data-budget="${c.id}" value="${esc(centsInput(c.budget_cents))}" placeholder="no budget" inputmode="decimal" style="width:7.5rem;text-align:right"></td>
      <td class="num ${c.budget_cents && c.budget_cents - c.spent_cents < 0 ? 'bad' : ''}">${c.budget_cents ? money(c.budget_cents - c.spent_cents) : ''}</td><td><button class="small" data-cat="${c.id}">&#9998;</button></td></tr>`;
  };
  el.innerHTML = `<div class="spread"><h1>Budget</h1><div class="row"><button id="bPrev">&lsaquo;</button><b>${esc(S.budgetMonth.toLocaleDateString(undefined, { month: 'long', year: 'numeric' }))}</b><button id="bNext">&rsaquo;</button><button id="catAdd">+ Category</button></div></div>
    <div class="stats"><div class="stat"><small>Budgeted</small><b>${money(s.budget_cents)}</b></div><div class="stat"><small>Spent in budgeted categories</small><b>${money(s.budget_spent_cents)}</b></div>
      <div class="stat"><small>Budget left</small><b class="${s.budget_left_cents < 0 ? 'bad' : 'good'}">${money(s.budget_left_cents)}</b></div><div class="stat"><small>All spending</small><b>${money(s.expense_cents)}</b></div><div class="stat"><small>Income</small><b class="good">${money(s.income_cents)}</b></div></div>
    <div class="card scroll"><h2>Spending</h2><p class="mute">Type a monthly budget and press Enter or leave the box. It applies to every month.</p><table><tr><th>Category</th><th class="num">Spent</th><th class="num">Budget / month</th><th class="num">Left</th><th></th></tr>${exp.map(row).join('')}
    ${s.uncategorised_cents ? `<tr><td class="mute">No category</td><td class="num">${money(-s.uncategorised_cents)}</td><td></td><td></td><td></td></tr>` : ''}</table></div>
    <div class="card"><h2>Income</h2><ul class="list">${inc.map((c) => `<li><span class="grow">${esc(c.name)}</span><b class="good">${money(c.spent_cents)}</b><button class="small" data-cat="${c.id}">&#9998;</button></li>`).join('')}</ul></div>`;
  const shift = (n) => { S.budgetMonth = new Date(S.budgetMonth.getFullYear(), S.budgetMonth.getMonth() + n, 1); budget(el); };
  $('#bPrev').onclick = () => shift(-1); $('#bNext').onclick = () => shift(1);
  $('#catAdd').onclick = () => catForm({ kind: 'expense' });
  el.querySelectorAll('[data-budget]').forEach((inp) => (inp.onchange = async () => {
    const c = S.cats.find((x) => x.id === Number(inp.dataset.budget));
    try { await api('PUT', '/api/categories/' + c.id, { ...c, budget_cents: toCents(inp.value) }); S.cats = await api('GET', '/api/categories'); toast('Budget saved'); budget(el); } catch (e) { fail(e); }
  }));
  el.querySelectorAll('[data-cat]').forEach((b) => (b.onclick = () => catForm(S.cats.find((x) => x.id === Number(b.dataset.cat)))));
}
function catForm(c) {
  const isNew = !c.id;
  openModal(`<form id="catForm"><h2>${isNew ? 'New category' : 'Edit category'}</h2><label>Name</label><input name="name" value="${esc(c.name)}" required>
    <label>Kind</label><select name="kind">${opts([['expense', 'Spending'], ['income', 'Income']], c.kind)}</select>
    <label>Monthly budget</label><input name="budget" inputmode="decimal" value="${esc(centsInput(c.budget_cents))}">
    <label>Tax line (business categories)</label><select name="tax_line">${opts((typeof TAX_LINES !== 'undefined' ? TAX_LINES : []).map((x) => [x, x]), c.tax_line, '(none)')}</select>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="catDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#catForm', sh).onsubmit = async (ev) => {
      ev.preventDefault(); const d = formData(ev.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/categories' + (isNew ? '' : '/' + c.id), { name: d.name, kind: d.kind, budget_cents: toCents(d.budget), color: c.color || '', tax_line: d.tax_line || '' }); S.cats = await api('GET', '/api/categories'); closeModal(); render(); } catch (e) { fail(e); }
    };
    const del = $('#catDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete category "' + c.name + '"? Its transactions keep no category.')) return; try { await api('DELETE', '/api/categories/' + c.id); S.cats = await api('GET', '/api/categories'); closeModal(); render(); } catch (e) { fail(e); } };
  });
}

// ---------- accounts ----------
const ACC_TYPES = [['checking', 'Checking'], ['savings', 'Savings'], ['credit', 'Credit card'], ['cash', 'Cash'], ['loan', 'Loan'], ['investment', 'Investment'], ['other', 'Other']];
async function accounts(el) {
  S.accounts = await api('GET', '/api/accounts');
  const total = S.accounts.filter((a) => !a.archived).reduce((s, a) => s + a.balance_cents, 0);
  el.innerHTML = `<div class="spread"><h1>Accounts</h1><button class="primary" id="accAdd">+ Account</button></div>
    <div class="stats"><div class="stat"><small>Total of visible accounts</small><b class="${total < 0 ? 'bad' : ''}">${money(total)}</b></div></div>
    <div class="card"><p class="mute">Balances are the opening balance plus every transaction recorded here. Credit cards and loans: enter what you owe as a negative opening balance.</p><ul class="list">
    ${S.accounts.map((a) => `<li class="click" data-acc="${a.id}"><span class="grow"><b>${esc(a.name)}</b> ${a.private ? '<span class="pill">private</span>' : ''}${a.archived ? ' <span class="pill">archived</span>' : ''}<br><small>${esc((ACC_TYPES.find((t) => t[0] === a.type) || [, a.type])[1])} &middot; ${esc(userName(a.owner_id))}</small></span><b class="${a.balance_cents < 0 ? 'bad' : ''}">${money(a.balance_cents)}</b></li>`).join('') || '<li class="empty">Add your checking, savings and cards.</li>'}</ul></div>`;
  $('#accAdd').onclick = () => accForm({});
  el.querySelectorAll('[data-acc]').forEach((r) => (r.onclick = () => accForm(S.accounts.find((a) => a.id === Number(r.dataset.acc)))));
}
function accForm(a) {
  const isNew = !a.id;
  openModal(`<form id="accForm"><h2>${isNew ? 'New account' : 'Edit account'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="name" value="${esc(a.name)}" required placeholder="Joint checking"></div>
    <div><label>Type</label><select name="type">${opts(ACC_TYPES, a.type || 'checking')}</select></div>
    <div><label>Opening balance</label><input name="opening" inputmode="decimal" value="${a.opening_cents ? esc((a.opening_cents / 100).toFixed(2)) : ''}" placeholder="0.00"></div>
    <div><label>Interest rate % (cards, loans)</label><input name="apr" inputmode="decimal" value="${a.apr_bps ? esc((a.apr_bps / 100).toFixed(2)) : ''}" placeholder="19.99"></div>
    <div><label>Minimum payment / month</label><input name="minpay" inputmode="decimal" value="${esc(centsInput(a.min_payment_cents))}"></div>
    <div class="full"><label>Warn me when the forecast drops below (empty = no warning)</label><input name="low" inputmode="decimal" value="${a.low_balance_cents != null ? esc((a.low_balance_cents / 100).toFixed(2)) : ''}" placeholder="e.g. 200.00"></div>
    <div class="full"><label>Notes</label><input name="notes" value="${esc(a.notes)}" placeholder="No full account numbers here, please"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="private" ${a.private ? 'checked' : ''}> Private (only me)</label><label class="inline"><input type="checkbox" name="archived" ${a.archived ? 'checked' : ''}> Archived</label><label class="inline"><input type="checkbox" name="business" ${a.business ? 'checked' : ''}> Business account (imports marked business)</label></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="accDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#accForm', sh).onsubmit = async (ev) => {
      ev.preventDefault(); const d = formData(ev.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/accounts' + (isNew ? '' : '/' + a.id), { name: d.name, type: d.type, opening_cents: toCents(d.opening), notes: d.notes, private: d.private, archived: d.archived, low_balance_cents: String(d.low).trim() === '' ? null : toCents(d.low), business: d.business, apr_bps: Math.round((parseFloat(d.apr) || 0) * 100), min_payment_cents: Math.abs(toCents(d.minpay)) }); closeModal(); render(); } catch (e) { fail(e); }
    };
    const del = $('#accDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete account "' + a.name + '"? Its transactions stay, with no account.')) return; try { await api('DELETE', '/api/accounts/' + a.id); closeModal(); render(); } catch (e) { fail(e); } };
  });
}

// ---------- alerts ----------
async function alerts(el) {
  const r = await api('GET', '/api/notifications?limit=200');
  el.innerHTML = `<div class="spread"><h1>Alerts</h1><div class="row"><button id="nRead">Mark all read</button><button id="nClear">Clear read</button></div></div>
    <div class="card"><ul class="list">${r.items.map((n) => `<li><span class="dot" style="background:${n.read ? 'var(--line)' : 'var(--accent)'}"></span><span class="grow"><b>${esc(n.title)}</b><br><small>${esc(n.body)}</small><br><small>${esc(new Date(n.created).toLocaleString())}${n.delivery ? ' &middot; sent: ' + esc(n.delivery) : ''}</small></span>${n.read ? '' : `<button class="small" data-read="${n.id}">Read</button>`}</li>`).join('') || '<li class="empty">No alerts yet. Reminders for bills and appointments show up here.</li>'}</ul></div>`;
  $('#nRead').onclick = async () => { await api('POST', '/api/notifications/read', { ID: 0 }); setBadge(0); alerts(el); };
  $('#nClear').onclick = async () => { await api('POST', '/api/notifications/clear'); alerts(el); };
  el.querySelectorAll('[data-read]').forEach((b) => (b.onclick = async () => { await api('POST', '/api/notifications/read', { ID: Number(b.dataset.read) }); setBadge(Math.max(0, r.unread - 1)); alerts(el); }));
}

// ---------- settings ----------
async function settings(el) {
  const tab = S.settingsTab || 'me';
  const extra = SETTINGS_TABS.filter((t) => !t[3] || isAdmin());
  const tabs = [['me', 'My profile'], ['notify', 'My notifications']].concat(extra.map((t) => [t[0], t[1]])).concat(isAdmin() ? [['members', 'Members'], ['household', 'Household'], ['email', 'Auto import'], ['backup', 'Backup']] : []);
  el.innerHTML = `<h1>Settings</h1><div class="tabs">${tabs.map(([k, t]) => `<button data-tab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="setBody"></div>`;
  el.querySelectorAll('[data-tab]').forEach((b) => (b.onclick = () => { S.settingsTab = b.dataset.tab; settings(el); }));
  const body = $('#setBody');
  const fns = { me: setMe, notify: setNotify, members: setMembers, household: setHousehold, email: setEmail, backup: typeof setBackupAuto === 'function' ? setBackupAuto : setBackup };
  for (const t of extra) fns[t[0]] = t[2];
  await (fns[tab] || setMe)(body);
}
async function setMe(el) {
  const me = await api('GET', '/api/me');
  el.innerHTML = `<div class="grid"><form class="card" id="meForm"><h2>Profile</h2><label>Name</label><input name="display_name" value="${esc(me.display_name)}" required>
    <label>My color on the calendar</label><input type="color" name="color" value="${esc(me.color)}"><p class="mute">Signed in as <b>${esc(me.username)}</b> (${esc(me.role)})</p><button class="primary">Save</button></form>
    <form class="card" id="pwForm"><h2>Password</h2><label>Current password</label><input type="password" name="cur" autocomplete="current-password" required>
    <label>New password (8+ characters)</label><input type="password" name="new" autocomplete="new-password" minlength="8" required><button class="primary" style="margin-top:.6rem">Change password</button></form>
    ${inWindowApp() ? '<div class="card"><h2>This Windows app</h2><p class="mute">Switch between hosting the data on this PC and connecting to the Unraid server.</p><button data-action="switch-server">Change server</button></div>' : ''}</div>`;
  $('#meForm').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { S.me = await api('PUT', '/api/me', { ...me, display_name: d.display_name, color: d.color }); $('#whoName').textContent = S.me.display_name; S.users = await api('GET', '/api/users'); toast('Saved'); } catch (err) { fail(err); }
  };
  $('#pwForm').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { await api('POST', '/api/me/password', { Current: d.cur, New: d.new }); e.target.reset(); toast('Password changed'); } catch (err) { fail(err); }
  };
}
async function setNotify(el) {
  const [me, devices, ics] = await Promise.all([api('GET', '/api/me'), api('GET', '/api/me/devices'), api('GET', '/api/me/ics')]);
  const ch = (me.channels || '').split(',');
  const icsURL = location.origin + ics.path;
  const secure = window.isSecureContext;
  el.innerHTML = `<form class="card" id="nForm"><h2>Where should my reminders go?</h2>
    <label class="inline"><input type="checkbox" data-group="ch" name="ch" value="inapp" checked disabled> Bell in this app (always on)</label><br>
    <label class="inline"><input type="checkbox" data-group="ch" name="ch" value="device" ${ch.includes('device') ? 'checked' : ''}> Windows pop-ups and browser pop-ups</label>
    <div class="form2">
    <div class="full"><label class="inline"><input type="checkbox" data-group="ch" name="ch" value="ntfy" ${ch.includes('ntfy') ? 'checked' : ''}> <b>Phone push with ntfy</b> (free)</label>
      <input name="ntfy_topic" value="${esc(me.ntfy_topic)}" placeholder="a hard-to-guess topic, e.g. smith-mom-7f3k2">
      <small>Install the ntfy app (Android or iPhone), tap +, and subscribe to this same topic. Anyone who knows a topic on ntfy.sh can read it, so make it hard to guess.</small></div>
    <div class="full"><label class="inline"><input type="checkbox" data-group="ch" name="ch" value="email" ${ch.includes('email') ? 'checked' : ''}> <b>Email</b></label><input name="email" type="email" value="${esc(me.email)}" placeholder="you@example.com"></div>
    <div class="full"><label class="inline"><input type="checkbox" data-group="ch" name="ch" value="sms" ${ch.includes('sms') ? 'checked' : ''}> <b>Text message</b></label>
      <div class="form2"><div><label>Mobile number (for Twilio, +15551234567)</label><input name="phone" value="${esc(me.phone)}"></div>
      <div><label>or carrier email-to-text address</label><input name="sms_gateway" value="${esc(me.sms_gateway)}" placeholder="5551234567@vtext.com"></div></div>
      <small>Texts need either Twilio (paid, set up by the admin) or a carrier email-to-text address plus email set up by the admin. AT&amp;T and T-Mobile have shut email-to-text down; Verizon's (vtext.com) ends March 31, 2027. ntfy is the reliable free choice.</small></div>
    <div class="full"><label class="inline"><input type="checkbox" data-group="ch" name="ch" value="webhook" ${ch.includes('webhook') ? 'checked' : ''}> <b>Discord or other webhook</b></label><input name="webhook_url" value="${esc(me.webhook_url)}" placeholder="https://discord.com/api/webhooks/..."></div>
    </div><div class="actions"><button type="button" id="nTest">Send me a test</button><button class="primary">Save</button></div></form>
    <div class="grid"><div class="card"><h2>Browser pop-ups</h2>${inWindowApp() ? '<p>This Windows app shows Windows notifications by itself, even when the window is closed.</p>' :
      secure ? `<p>Status: <b>${'Notification' in window ? esc(Notification.permission) : 'not supported'}</b>. Pop-ups show while this page is open in a tab.</p><button id="nPerm">Allow pop-ups in this browser</button>` :
      '<p class="warn">Browsers only allow pop-ups on https:// pages (or localhost). Open Household Hub through an HTTPS reverse proxy to use them here, or use ntfy / the Windows app.</p>'}</div>
    <div class="card"><h2>Windows PCs getting my pop-ups</h2><ul class="list">${devices.map((d) => `<li><span class="grow">${esc(d.label)}<br><small>last seen ${esc(d.last_seen ? new Date(d.last_seen).toLocaleString() : 'never')}</small></span><button class="small danger" data-dev="${esc(d.id)}">Remove</button></li>`).join('') || '<li class="empty">None. Install the Windows app and sign in to add this PC.</li>'}</ul></div>
    <div class="card"><h2>Calendar feed</h2><p class="mute">Subscribe from your phone or Outlook calendar to see events and bill dates there (read-only). Keep this link private.</p><code>${esc(icsURL)}</code>
      <div class="actions"><button id="icsCopy">Copy link</button><button id="icsRot" class="danger">New link</button></div></div></div>`;
  $('#nForm').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { S.me = await api('PUT', '/api/me', { ...me, channels: ['inapp'].concat(d.ch).join(','), ntfy_topic: d.ntfy_topic, email: d.email, phone: d.phone, sms_gateway: d.sms_gateway, webhook_url: d.webhook_url }); toast('Saved'); } catch (err) { fail(err); }
  };
  $('#nTest').onclick = async () => { try { const r = await api('POST', '/api/me/test-notify'); toast('Test sent: ' + r.result, /:(?! ok)/.test(r.result)); poll(); } catch (e) { fail(e); } };
  const perm = $('#nPerm'); if (perm) perm.onclick = async () => { await Notification.requestPermission(); setNotify(el); };
  el.querySelectorAll('[data-dev]').forEach((b) => (b.onclick = async () => { if (!confirm('Stop pop-ups on this PC?')) return; await api('DELETE', '/api/me/devices/' + b.dataset.dev); setNotify(el); }));
  $('#icsCopy').onclick = () => navigator.clipboard.writeText(icsURL).then(() => toast('Copied'), () => toast('Copy it by hand', true));
  $('#icsRot').onclick = async () => { if (!confirm('Make a new link? Calendars using the old one stop updating.')) return; await api('POST', '/api/me/ics/rotate'); setNotify(el); };
}
async function setMembers(el) {
  const users = await api('GET', '/api/users');
  const roles = [['admin', 'Admin (everything)'], ['adult', 'Adult (money + calendar)'], ['kid', 'Kid (calendar only)']];
  el.innerHTML = `<div class="grid"><div class="card"><h2>Household members</h2><ul class="list">${users.map((u) => `<li><span class="dot" style="background:${esc(u.color)}"></span><span class="grow"><b>${esc(u.display_name)}</b> <small>${esc(u.username)}</small><br>
      <select data-role="${u.id}" style="width:auto">${opts(roles, u.role)}</select> <label class="inline"><input type="checkbox" data-active="${u.id}" ${u.active ? 'checked' : ''}> active</label></span><button class="small" data-pw="${u.id}">Reset password</button><button class="small" data-r2fa="${u.id}" title="For someone who lost their phone and recovery codes">Reset 2-step</button></li>`).join('')}</ul></div>
    <form class="card" id="addUser"><h2>Add a member</h2><label>Name</label><input name="display_name" required><label>Username</label><input name="username" required autocomplete="off">
      <label>Starting password (8+; they can change it)</label><input name="password" type="password" minlength="8" required autocomplete="new-password">
      <label>Role</label><select name="role">${opts(roles, 'adult')}</select><label>Color</label><input type="color" name="color" value="#2bb673"><button class="primary" style="margin-top:.6rem">Add</button></form></div>`;
  const save = async (id) => {
    const role = $(`[data-role="${id}"]`).value; const active = $(`[data-active="${id}"]`).checked;
    try { await api('PUT', '/api/users/' + id, { Role: role, Active: active }); toast('Saved'); S.users = await api('GET', '/api/users'); } catch (e) { fail(e); setMembers(el); }
  };
  el.querySelectorAll('[data-role]').forEach((s) => (s.onchange = () => save(s.dataset.role)));
  el.querySelectorAll('[data-active]').forEach((s) => (s.onchange = () => save(s.dataset.active)));
  el.querySelectorAll('[data-r2fa]').forEach((b) => (b.onclick = async () => {
    const u = users.find((x) => x.id === Number(b.dataset.r2fa));
    if (!confirm(`Turn off two-step sign-in for ${u.display_name}? They can sign in with only their password until they turn it on again.`)) return;
    try { await api('PUT', '/api/users/' + u.id, { Role: u.role, Active: u.active, Reset2FA: true }); toast('Two-step sign-in reset'); } catch (e) { fail(e); }
  }));
  el.querySelectorAll('[data-pw]').forEach((b) => (b.onclick = () => {
    const u = users.find((x) => x.id === Number(b.dataset.pw));
    openModal(`<form id="rpw"><h2>New password for ${esc(u.display_name)}</h2><input type="password" name="pw" minlength="8" required autocomplete="new-password"><div class="actions"><button type="button" data-action="close">Cancel</button><button class="primary">Set</button></div></form>`, (sh) => {
      $('#rpw', sh).onsubmit = async (e) => { e.preventDefault(); try { await api('PUT', '/api/users/' + u.id, { Role: u.role, Active: u.active, Password: e.target.pw.value }); closeModal(); toast('Password set'); } catch (err) { fail(err); } };
    });
  }));
  $('#addUser').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { await api('POST', '/api/users', { Username: d.username, DisplayName: d.display_name, Password: d.password, Role: d.role, Color: d.color }); S.users = await api('GET', '/api/users'); toast('Added'); setMembers(el); } catch (err) { fail(err); }
  };
}
async function setHousehold(el) {
  const s = await api('GET', '/api/settings');
  const f = (k, label, type, ph) => `<div><label>${label}</label><input name="${k}" type="${type || 'text'}" value="${esc(s[k])}" placeholder="${esc(ph || '')}" autocomplete="off"></div>`;
  el.innerHTML = `<form id="hhForm"><div class="card"><h2>General</h2><div class="form2">${f('household_name', 'Household name')}${f('currency', 'Currency symbol')}
    <div><label>Week starts on</label><select name="week_start">${opts([['0', 'Sunday'], ['1', 'Monday']], s.week_start)}</select></div>
    ${f('bill_remind_hour', 'Bill reminders go out at (hour, 0-23)', 'number')}${f('allday_remind_hour', 'All-day event reminders at (hour)', 'number')}
    <div><label>Overdue bill reminders</label><select name="overdue_reminders">${opts([['1', 'On (day after due)'], ['0', 'Off']], s.overdue_reminders)}</select></div>
    <div class="full">${f('base_url', 'Address people use to open this app (for links in messages)', 'url', 'http://192.168.1.144:8095')}</div></div></div>
    <div class="card"><h2>ntfy (phone push)</h2><div class="form2">${f('ntfy_server', 'ntfy server', 'url', 'https://ntfy.sh')}${f('ntfy_token', 'Access token (only for a protected server)', 'password')}</div></div>
    <div class="card"><h2>Email (SMTP)</h2><p class="mute">For Gmail use smtp.gmail.com, port 587, your address, and an App Password (Google account, Security, App passwords). Passwords are stored on this server and never shown again.</p><div class="form2">
    ${f('smtp_host', 'SMTP server', 'text', 'smtp.gmail.com')}${f('smtp_port', 'Port (587 or 465)', 'number')}${f('smtp_user', 'Username')}${f('smtp_pass', 'Password / app password', 'password')}${f('smtp_from', 'From address')}</div></div>
    <div class="card"><h2>Text messages (Twilio, optional)</h2><p class="mute">Twilio charges per text and US numbers must be registered (toll-free verification or A2P 10DLC) before texts are delivered. Leave empty to use carrier email-to-text instead.</p><div class="form2">
    ${f('twilio_sid', 'Account SID')}${f('twilio_token', 'Auth token', 'password')}${f('twilio_from', 'From number', 'text', '+15551234567')}</div></div>
    <div class="card"><h2>Household webhook (optional)</h2><p class="mute">Every shared reminder is also posted here once, for example a family Discord channel.</p>${f('household_webhook', 'Webhook URL', 'password', 'https://discord.com/api/webhooks/...')}</div>
    <div class="row end"><button class="primary">Save household settings</button></div></form>`;
  $('#hhForm').onsubmit = async (e) => {
    e.preventDefault();
    try { const r = await api('PUT', '/api/settings', formData(e.target)); S.settings = { ...S.settings, household_name: r.household_name, currency: r.currency, week_start: r.week_start }; $('#hhName').textContent = r.household_name; toast('Saved'); } catch (err) { fail(err); }
  };
}
async function setBackup(el) {
  el.innerHTML = `<div class="card"><h2>Backup</h2><p>Download a copy of everything (one SQLite file). On Unraid the data also lives in the app's appdata folder, which your usual appdata backup covers.</p>
    <a class="btn primary" href="/api/backup" download>Download backup</a><h3>Restore</h3><p class="mute">Stop the app, replace <code>household.db</code> in the data folder with the backup file (delete any household.db-wal and -shm files next to it), then start the app.</p></div>`;
}

// ---------- import: statements + email review ----------
async function reviewBadge() {
  if (!canFin()) return;
  try {
    const items = await api('GET', '/api/email-items?status=review');
    const b = $('#ibadge'); b.textContent = items.length; b.classList.toggle('hidden', !items.length);
  } catch (_) { /* ignore */ }
}
const KIND_TEXT = { paypal: 'PayPal', transaction: 'Bank / card', bill_notice: 'Bill amount', bill_paid: 'Bill paid', ignore: 'Ignore' };
const STATUS_PILL = { review: 'warn', preview: '', added: 'good', matched: 'good', duplicate: '', applied: 'good', ignored: '' };

async function importView(el) {
  const tab = S.importTab || 'file';
  el.innerHTML = `<h1>Import</h1><div class="tabs">${[['file', 'Statement file'], ['review', 'Email review'], ['history', 'Email history']].map(([k, t]) => `<button data-itab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="impBody"></div>`;
  el.querySelectorAll('[data-itab]').forEach((b) => (b.onclick = () => { S.importTab = b.dataset.itab; importView(el); }));
  const body = $('#impBody');
  if (tab === 'file') return importFile(body);
  return emailItems(body, tab === 'review' ? 'open' : '');
}

function importFile(el) {
  const accs = S.accounts.filter((a) => !a.archived);
  el.innerHTML = `<div class="card"><h2>Upload a statement</h2>
    <p class="mute">Download a CSV, OFX or QFX file from your bank or PayPal (PayPal: Activity &gt; Statements &gt; Activity report, CSV). Rows you already have are skipped, so importing the same file twice is safe.</p>
    <div class="form2"><div><label>Into account</label><select id="impAcc">${opts(accs.map((a) => [a.id, a.name]), S.impAcc || (accs[0] || {}).id)}</select></div>
    <div><label>File</label><input type="file" id="impFile" accept=".csv,.ofx,.qfx,.txt"></div></div>
    <div class="actions"><button class="primary" id="impPrev">Preview</button></div></div><div id="impResult"></div>`;
  if (!accs.length) { $('#impResult').innerHTML = '<div class="card warn">Add an account first (Accounts page), for example "PayPal 1", "PayPal 2" and "Relay".</div>'; return; }
  $('#impAcc').onchange = (e) => (S.impAcc = e.target.value);
  $('#impPrev').onclick = () => runImport(false);
}

async function sendImport(commit, mapping) {
  const f = $('#impFile').files[0]; if (!f) throw new Error('Choose a file');
  const fd = new FormData(); fd.append('file', f); fd.append('account_id', $('#impAcc').value);
  if (mapping) fd.append('mapping', JSON.stringify(mapping));
  const r = await fetch('/api/import/' + (commit ? 'commit' : 'preview'), { method: 'POST', headers: { 'X-HHub': '1' }, body: fd, credentials: 'same-origin' });
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(d.error || 'HTTP ' + r.status);
  return d;
}

async function runImport(commit, mapping) {
  const out = $('#impResult');
  try {
    if (commit) {
      const d = await sendImport(true, mapping);
      const c = d.counts || {};
      out.innerHTML = `<div class="card"><h2>Imported</h2><p><b>${c.added || 0}</b> added, ${c.matched || 0} matched to entries you already had, ${c.duplicate || 0} already imported, ${c.skip || 0} skipped.</p><a href="#money">See them in Money</a></div>`;
      S.accounts = await api('GET', '/api/accounts');
      return;
    }
    const d = await sendImport(false, mapping);
    S.impMapping = d.mapping;
    const c = d.counts || {};
    const col = (k) => (d.format === 'csv' ? `<div><label>${k[1]}</label><select data-map="${k[0]}">${opts([[-1, '(none)']].concat((d.headers || []).map((h, i) => [i, h || 'column ' + (i + 1)])), d.mapping[k[0]])}</select></div>` : '');
    out.innerHTML = `<div class="card"><div class="spread"><h2>Preview: ${d.total} rows (${esc(d.format.toUpperCase())}${d.mapping && d.mapping.profile === 'paypal' ? ', PayPal' : ''})</h2>
      <div class="row"><span class="pill good">${c.new || 0} new</span><span class="pill">${c.duplicate || 0} already there</span><span class="pill">${c.match || 0} match existing</span><span class="pill">${c.skip || 0} skipped</span></div></div>
      ${d.format === 'csv' ? `<details><summary>Columns (fix them if the preview looks wrong)</summary><div class="form2">
        ${[['date', 'Date'], ['amount', 'Amount (+/-)'], ['debit', 'Money out'], ['credit', 'Money in'], ['payee', 'Payee / name'], ['description', 'Notes'], ['id', 'Transaction ID'], ['status', 'Status'], ['type', 'Type']].map(col).join('')}
        <div><label>&nbsp;</label><label class="inline"><input type="checkbox" id="mapInv" ${d.mapping.invert ? 'checked' : ''}> Spending shows as positive numbers</label></div></div>
        <div class="actions"><button id="mapApply">Use these columns</button></div></details>` : ''}
      <div class="scroll" style="max-height:55vh;overflow:auto"><table><tr><th>Date</th><th>Payee</th><th class="num">Amount</th><th></th></tr>
      ${d.rows.map((r) => `<tr><td>${esc(r.date)}</td><td>${esc(r.payee)}<br><small>${esc(r.notes)}</small></td><td class="num ${r.amount_cents > 0 ? 'good' : ''}">${money(r.amount_cents, true)}</td>
        <td>${r.skip ? `<span class="pill">skip: ${esc(r.skip)}</span>` : r.status === 'new' ? '<span class="pill good">new</span>' : `<span class="pill">${esc(r.status)}</span>`}${r.transfer ? ' <span class="pill">transfer</span>' : ''}</td></tr>`).join('')}</table></div>
      <div class="actions"><button class="primary" id="impGo" ${c.new ? '' : 'disabled'}>Import ${c.new || 0} new</button></div></div>`;
    const ap = $('#mapApply');
    if (ap) ap.onclick = () => {
      const m = { ...S.impMapping };
      out.querySelectorAll('[data-map]').forEach((s) => (m[s.dataset.map] = Number(s.value)));
      m.invert = $('#mapInv').checked;
      runImport(false, m);
    };
    $('#impGo').onclick = () => runImport(true, d.format === 'csv' ? S.impMapping : undefined);
  } catch (e) { out.innerHTML = `<div class="card err">${esc(e.message)}</div>`; }
}

async function emailItems(el, status) {
  const items = await api('GET', '/api/email-items?status=' + status);
  el.innerHTML = `<div class="card">${status === 'open'
    ? '<p class="mute">Emails the reader was not sure about (or mailboxes still in preview mode). Fix and add them, or ignore them. Admins set up mailboxes and rules in Settings &gt; Email reader.</p>'
    : '<p class="mute">Last 300 money emails and what the reader did with them.</p>'}
    <div class="scroll"><table><tr><th>Received</th><th>Email</th><th>Found</th><th class="num">Amount</th><th></th></tr>
    ${items.map((it) => `<tr><td>${esc(new Date(it.received).toLocaleDateString())}</td><td><b>${esc(it.subject)}</b><br><small>${esc(it.sender)}${it.recipient ? ' &rarr; ' + esc(it.recipient) : ''}</small></td>
      <td>${esc(KIND_TEXT[it.kind] || it.kind)}: ${esc(it.payee || '?')}${it.account_id ? ' &middot; ' + esc(accName(it.account_id)) : ''}${it.due_date ? ' &middot; due ' + esc(it.due_date) : ''}<br><small>${esc(it.note)}</small></td>
      <td class="num">${it.amount_cents ? money(it.amount_cents, it.kind === 'paypal' || it.kind === 'transaction') : ''}</td>
      <td><span class="pill ${STATUS_PILL[it.status] || ''}">${esc(it.status)}</span>${it.status === 'review' || it.status === 'preview' ? `<br><button class="small primary" data-fix="${it.id}">Add</button> <button class="small" data-ign="${it.id}">Ignore</button>` : ''}</td></tr>`).join('') || `<tr><td colspan="5" class="empty">${status === 'open' ? 'Nothing waiting.' : 'No money emails read yet.'}</td></tr>`}</table></div></div>`;
  el.querySelectorAll('[data-ign]').forEach((b) => (b.onclick = async () => { try { await api('POST', '/api/email-items/' + b.dataset.ign, { action: 'ignore' }); reviewBadge(); emailItems(el, status); } catch (e) { fail(e); } }));
  el.querySelectorAll('[data-fix]').forEach((b) => (b.onclick = () => fixItem(items.find((x) => x.id === Number(b.dataset.fix)), () => emailItems(el, status))));
}

async function fixItem(it, done) {
  const bills = await api('GET', '/api/bills');
  const isTx = (k) => k === 'paypal' || k === 'transaction';
  openModal(`<form id="fixForm"><h2>Add from email</h2><p class="mute">${esc(it.subject)}</p><div class="form2">
    <div><label>What is it?</label><select name="kind">${opts(Object.entries(KIND_TEXT).filter(([k]) => k !== 'ignore'), it.kind)}</select></div>
    <div><label>Amount (negative = money out)</label><input name="amount" value="${esc(it.amount_cents ? (it.amount_cents / 100).toFixed(2) : '')}"></div>
    <div class="txf"><label>Payee</label><input name="payee" value="${esc(it.payee)}"></div>
    <div class="txf"><label>Account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), it.account_id, '-')}</select></div>
    <div class="txf"><label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name]), '', '(learn from payee)')}</select></div>
    <div class="bf"><label>Bill</label><select name="bill_id">${opts(bills.map((b) => [b.id, b.name]), it.bill_id, '-')}</select></div></div>
    <p class="mute">Tip: to handle emails like this by themselves next time, add a rule in Settings &gt; Email reader.</p>
    <div class="actions"><button type="button" data-action="close">Cancel</button><button class="primary">Add</button></div></form>`, (sh) => {
    const f = $('#fixForm', sh);
    const sync = () => { const tx = isTx(f.kind.value); sh.querySelectorAll('.txf').forEach((x) => x.classList.toggle('hidden', !tx)); sh.querySelectorAll('.bf').forEach((x) => x.classList.toggle('hidden', tx)); };
    f.kind.onchange = sync; sync();
    f.onsubmit = async (e) => {
      e.preventDefault(); const d = formData(f);
      try {
        let amt = toCents(d.amount);
        if (!isTx(d.kind)) amt = Math.abs(amt);
        await api('POST', '/api/email-items/' + it.id, { action: 'apply', kind: d.kind, amount_cents: amt, payee: d.payee, account_id: idOrNull(d.account_id), category_id: idOrNull(d.category_id), bill_id: idOrNull(d.bill_id) });
        closeModal(); toast('Added'); reviewBadge(); done();
      } catch (err) { fail(err); }
    };
  });
}

// ---------- settings: email reader ----------
const MAIL_HOSTS = [['imap.gmail.com', 'Gmail'], ['imap.mail.yahoo.com', 'Yahoo'], ['imap.aol.com', 'AOL'], ['imap.mail.me.com', 'iCloud']];
async function setEmail(el) {
  const [mbs, rules, bills, wf] = await Promise.all([api('GET', '/api/mailboxes'), api('GET', '/api/email-rules'), api('GET', '/api/bills'), api('GET', '/api/watch-folders')]);
  el.innerHTML = `<div class="card"><div class="spread"><h2>Statement folders</h2><button class="primary" id="wfAdd">+ Folder</button></div>
    <p class="mute">Save statement downloads (CSV, OFX, QFX) into a folder and they import into its account by themselves every 10 minutes. Files are only read, never moved or deleted, and each file is imported once. Subfolders (2 levels) are included. ${wf.root ? `On this server the folders live inside <code>${esc(wf.root)}</code> (the "Statements folder" of the container, on Unraid normally <code>/mnt/user/appdata/household-hub/statements</code>); type a name like <code>paypal-aon</code>.` : 'Type the full folder path on this PC.'}</p>
    <ul class="list">${wf.folders.map((w) => `<li><span class="grow"><b>${esc(w.label)}</b> &rarr; ${esc(accName(w.account_id))} ${w.enabled ? '' : '<span class="pill">off</span>'}<br>
      <small>${esc(w.path)} &middot; ${w.files} file(s) seen &middot; ${w.last_scan ? 'checked ' + esc(new Date(w.last_scan).toLocaleString()) : 'not checked yet'}${w.last_error ? ' &middot; <span class="bad">' + esc(w.last_error) + '</span>' : ''}</small></span>
      <button class="small" data-wscan="${w.id}">Check now</button><button class="small" data-wfiles="${w.id}">Files</button><button class="small" data-wf="${w.id}">Edit</button></li>`).join('') || '<li class="empty">No statement folder yet.</li>'}</ul></div>
    <div class="card"><div class="spread"><h2>Mailboxes</h2><button class="primary" id="mbAdd">+ Mailbox</button></div>
    <p class="mute">The reader checks these every 10 minutes, read-only: nothing is moved, deleted or marked read. Use an <b>app password</b> (Gmail: Google Account &gt; Security &gt; 2-Step Verification &gt; App passwords). Outlook/Hotmail no longer allows app passwords: set Outlook to forward bill and bank emails to your Gmail bills address.</p>
    <ul class="list">${mbs.map((m) => `<li><span class="grow"><b>${esc(m.label)}</b> ${m.enabled ? '' : '<span class="pill">off</span>'}${m.preview ? ' <span class="pill warn">preview</span>' : ''}<br>
      <small>${esc(m.username)} @ ${esc(m.host)} &middot; ${esc(m.folder)} &middot; ${m.last_check ? 'checked ' + esc(new Date(m.last_check).toLocaleString()) : 'not checked yet'}${m.last_error ? ' &middot; <span class="bad">' + esc(m.last_error) + '</span>' : ''}</small></span>
      <button class="small" data-scan="${m.id}">Check now</button><button class="small" data-mb="${m.id}">Edit</button></li>`).join('') || '<li class="empty">No mailbox yet.</li>'}</ul></div>
    <div class="card"><div class="spread"><h2>Rules</h2><button class="primary" id="ruAdd">+ Rule</button></div>
    <p class="mute">Rules are tried top to bottom (lowest number first). Without a rule, PayPal emails and emails naming one of your bills (GEICO, Straight Talk...) are still recognised, but they wait in Import &gt; Email review until they know the account. Make one PayPal rule per PayPal address: "From contains" <code>paypal.com</code>, "To contains" that PayPal's email address, account = that PayPal account. Several words: separate with <code>|</code>.</p>
    <div class="scroll"><table><tr><th>#</th><th>Name</th><th>When</th><th>Does</th><th></th></tr>${rules.map((r) => `<tr><td>${r.priority}</td><td>${esc(r.name)}${r.enabled ? '' : ' <span class="pill">off</span>'}</td>
      <td><small>${[['from', r.from_contains], ['to', r.to_contains], ['subject', r.subject_contains], ['body', r.body_contains]].filter((x) => x[1]).map((x) => esc(x[0] + ' has "' + x[1] + '"')).join(', ')}</small></td>
      <td><small>${esc(KIND_TEXT[r.kind])}${r.account_id ? ' &rarr; ' + esc(accName(r.account_id)) : ''}${r.bill_id ? ' &rarr; ' + esc((bills.find((b) => b.id === r.bill_id) || {}).name || '') : ''}</small></td><td><button class="small" data-ru="${r.id}">Edit</button></td></tr>`).join('') || '<tr><td colspan="5" class="empty">No rules yet.</td></tr>'}</table></div></div>
    <form class="card" id="ruTest"><h2>Test with an email</h2><p class="mute">Paste an email you got (or type a made-up one) to see what the reader would do. Nothing is saved.</p><div class="form2">
      <div><label>From</label><input name="From" placeholder="service@paypal.com"></div><div><label>To</label><input name="To"></div>
      <div class="full"><label>Subject</label><input name="Subject"></div><div class="full"><label>Body (text or HTML)</label><textarea name="Body" rows="5"></textarea></div></div>
      <div class="actions"><button class="primary">Test</button></div><pre id="ruOut" style="white-space:pre-wrap"></pre></form>`;
  $('#wfAdd').onclick = () => watchForm({ enabled: true, account_id: (S.accounts[0] || {}).id }, wf.root, () => setEmail(el));
  el.querySelectorAll('[data-wf]').forEach((b) => (b.onclick = () => watchForm(wf.folders.find((w) => w.id === Number(b.dataset.wf)), wf.root, () => setEmail(el))));
  el.querySelectorAll('[data-wscan]').forEach((b) => (b.onclick = async () => {
    b.disabled = true; b.textContent = 'Checking...';
    try { const r = await api('POST', `/api/watch-folders/${b.dataset.wscan}/scan`); toast(`Done: ${r.added} new transaction(s)`); } catch (e) { fail(e); }
    setEmail(el);
  }));
  el.querySelectorAll('[data-wfiles]').forEach((b) => (b.onclick = async () => {
    const files = await api('GET', `/api/watch-folders/${b.dataset.wfiles}/files`).catch(fail);
    openModal(`<div class="spread"><h2>Files seen</h2><button class="small" data-action="close">&times;</button></div><ul class="list">${(files || []).map((f) => `<li><span class="grow">${esc(f.name)}<br><small>${esc(new Date(f.seen).toLocaleString())} &middot; ${esc(f.result)}</small></span></li>`).join('') || '<li class="empty">No files yet.</li>'}</ul>`);
  }));
  $('#mbAdd').onclick = () => mailboxForm({ host: 'imap.gmail.com', port: 993, folder: 'INBOX', enabled: true, preview: true, since_days: 60 }, () => setEmail(el));
  el.querySelectorAll('[data-mb]').forEach((b) => (b.onclick = () => mailboxForm(mbs.find((m) => m.id === Number(b.dataset.mb)), () => setEmail(el))));
  el.querySelectorAll('[data-scan]').forEach((b) => (b.onclick = async () => {
    b.disabled = true; b.textContent = 'Checking...';
    try { const r = await api('POST', `/api/mailboxes/${b.dataset.scan}/scan`); toast(`Done: ${r.stored} money email(s) found`); reviewBadge(); } catch (e) { fail(e); }
    setEmail(el);
  }));
  $('#ruAdd').onclick = () => ruleForm({ kind: 'paypal', from_contains: 'paypal.com', direction: 'auto', priority: 100, enabled: true }, bills, () => setEmail(el));
  el.querySelectorAll('[data-ru]').forEach((b) => (b.onclick = () => ruleForm(rules.find((r) => r.id === Number(b.dataset.ru)), bills, () => setEmail(el))));
  $('#ruTest').onsubmit = async (e) => {
    e.preventDefault();
    try { const r = await api('POST', '/api/email-rules/test', formData(e.target)); $('#ruOut').textContent = JSON.stringify(r, null, 2); } catch (err) { fail(err); }
  };
}

function mailboxForm(m, done) {
  const isNew = !m.id;
  openModal(`<form id="mbForm"><h2>${isNew ? 'Add mailbox' : 'Edit mailbox'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="label" value="${esc(m.label)}" placeholder="Bills Gmail"></div>
    <div><label>IMAP server</label><input name="host" value="${esc(m.host)}" list="mailHosts" required><datalist id="mailHosts">${MAIL_HOSTS.map((h) => `<option value="${h[0]}">${h[1]}</option>`).join('')}</datalist></div>
    <div><label>Port</label><input name="port" type="number" value="${esc(m.port || 993)}"></div>
    <div><label>Email / username</label><input name="username" value="${esc(m.username)}" autocomplete="off" required></div>
    <div><label>App password ${isNew ? '' : '(leave empty to keep)'}</label><input name="secret" type="password" autocomplete="new-password" ${isNew ? 'required' : ''} placeholder="${m.has_secret ? 'saved' : ''}"></div>
    <div class="full"><label>Folders to read (one per line or comma separated; Gmail labels count as folders)</label><textarea name="folder" rows="3">${esc((m.folder || 'INBOX').split(/\s*,\s*/).join('\n'))}</textarea>
      ${isNew ? '<small>Save first, then use "Pick folders" to choose from a list.</small>' : '<button type="button" class="small" id="mbFolders">Pick folders</button>'}<div id="mbFolderList"></div></div>
    <div><label>First check looks back (days)</label><input name="since_days" type="number" value="${esc(m.since_days || 60)}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="enabled" ${m.enabled ? 'checked' : ''}> Check this mailbox</label>
    <label class="inline"><input type="checkbox" name="preview" ${m.preview ? 'checked' : ''}> Preview only (list what it finds in Import &gt; Email review, change nothing)</label></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="mbDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    const pick = $('#mbFolders', sh);
    if (pick) pick.onclick = async () => {
      pick.disabled = true; pick.textContent = 'Loading...';
      try {
        const list = await api('GET', `/api/mailboxes/${m.id}/folders`);
        const cur = $('#mbForm', sh).folder.value.split(/[\n,]/).map((x) => x.trim().toLowerCase()).filter(Boolean);
        $('#mbFolderList', sh).innerHTML = list.map((f) => `<label class="inline"><input type="checkbox" data-fold="${esc(f)}" ${cur.includes(f.toLowerCase()) ? 'checked' : ''}>${esc(f)}</label>`).join('');
        sh.querySelectorAll('[data-fold]').forEach((c) => (c.onchange = () => {
          $('#mbForm', sh).folder.value = [...sh.querySelectorAll('[data-fold]:checked')].map((x) => x.dataset.fold).join('\n');
        }));
      } catch (e) { fail(e); }
      pick.disabled = false; pick.textContent = 'Pick folders';
    };
    $('#mbForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/mailboxes' + (isNew ? '' : '/' + m.id), { ...d, port: Number(d.port) || 993, since_days: Number(d.since_days) || 60, auth_type: 'password' }); closeModal(); toast('Saved'); done(); } catch (err) { fail(err); }
    };
    const del = $('#mbDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Remove this mailbox? Its saved app password is deleted; what it already added stays.')) return; try { await api('DELETE', '/api/mailboxes/' + m.id); closeModal(); done(); } catch (e) { fail(e); } };
  });
}

function ruleForm(r, bills, done) {
  const isNew = !r.id;
  openModal(`<form id="ruForm"><h2>${isNew ? 'New rule' : 'Edit rule'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="name" value="${esc(r.name)}" required placeholder="PayPal 1, Relay alerts, GEICO..."></div>
    <div><label>From contains</label><input name="from_contains" value="${esc(r.from_contains)}"></div>
    <div><label>To contains</label><input name="to_contains" value="${esc(r.to_contains)}" placeholder="the PayPal account's email"></div>
    <div><label>Subject contains</label><input name="subject_contains" value="${esc(r.subject_contains)}"></div>
    <div><label>Body contains</label><input name="body_contains" value="${esc(r.body_contains)}"></div>
    <div><label>What it is</label><select name="kind">${opts(Object.entries(KIND_TEXT), r.kind)}</select></div>
    <div><label>Money direction</label><select name="direction">${opts([['auto', 'Work it out'], ['out', 'Always money out'], ['in', 'Always money in']], r.direction)}</select></div>
    <div><label>Account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), r.account_id, '-')}</select></div>
    <div><label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name]), r.category_id, '(learn from payee)')}</select></div>
    <div><label>Bill (for bill emails)</label><select name="bill_id">${opts(bills.map((b) => [b.id, b.name]), r.bill_id, '(match by name)')}</select></div>
    <div><label>Order (lower runs first)</label><input name="priority" type="number" value="${esc(r.priority || 100)}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="enabled" ${r.enabled ? 'checked' : ''}> On</label></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="ruDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#ruForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        await api(isNew ? 'POST' : 'PUT', '/api/email-rules' + (isNew ? '' : '/' + r.id), { ...d, priority: Number(d.priority) || 100, account_id: idOrNull(d.account_id), category_id: idOrNull(d.category_id), bill_id: idOrNull(d.bill_id) });
        closeModal(); toast('Saved'); done();
      } catch (err) { fail(err); }
    };
    const del = $('#ruDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete rule "' + r.name + '"?')) return; try { await api('DELETE', '/api/email-rules/' + r.id); closeModal(); done(); } catch (e) { fail(e); } };
  });
}

function watchForm(w, root, done) {
  const isNew = !w.id;
  openModal(`<form id="wfForm"><h2>${isNew ? 'Add statement folder' : 'Edit statement folder'}</h2><div class="form2">
    <div class="full"><label>Folder ${root ? '(inside ' + esc(root) + ')' : '(full path)'}</label><input name="path" value="${esc(w.path)}" required placeholder="${root ? 'paypal-aon' : 'D:\\Statements\\PayPal AON'}"></div>
    <div><label>Name</label><input name="label" value="${esc(w.label)}"></div>
    <div><label>Import into account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), w.account_id)}</select></div>
    <div class="full"><label class="inline"><input type="checkbox" name="enabled" ${w.enabled ? 'checked' : ''}> Check this folder</label></div></div>
    <p class="mute">Tip: import one file by hand first (Import page) if the columns need fixing; the folder then uses the same columns.</p>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="wfDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#wfForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/watch-folders' + (isNew ? '' : '/' + w.id), { ...d, account_id: Number(d.account_id) }); closeModal(); toast('Saved'); done(); } catch (err) { fail(err); }
    };
    const del = $('#wfDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Stop watching this folder? Files and what they added stay.')) return; try { await api('DELETE', '/api/watch-folders/' + w.id); closeModal(); done(); } catch (e) { fail(e); } };
  });
}

boot();
