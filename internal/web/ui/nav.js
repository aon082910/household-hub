// Round 7: phone menu with "More", search everything, recently deleted, quiet hours and daily summary.
'use strict';

// ---- phone menu: 4 main buttons + More ----
const MAIN_PHONE = ['dashboard', 'calendar', 'family', 'money'];
(function phoneMenu() {
  const nav = $('#nav');
  const more = document.createElement('a');
  more.href = '#'; more.className = 'more-btn'; more.dataset.view = 'more';
  more.innerHTML = '<span class="ic">&#9776;</span><span>More <b id="mbadge" class="badge hidden"></b></span>';
  nav.insertBefore(more, nav.querySelector('.who'));
  const search = document.createElement('a');
  search.href = '#search'; search.dataset.view = 'search';
  search.innerHTML = '<span class="ic">&#128269;</span><span>Search</span>';
  nav.insertBefore(search, nav.querySelector('a[data-view="dashboard"]').nextSibling);
  nav.querySelectorAll('a[data-view]').forEach((a) => { if (!MAIN_PHONE.includes(a.dataset.view) && a !== more) a.classList.add('phone-more'); });
  more.onclick = (e) => {
    e.preventDefault();
    const links = [...nav.querySelectorAll('a.phone-more')].filter((a) => !a.classList.contains('hidden'));
    openModal(`<div class="spread"><h2>Menu</h2><button class="small" data-action="close">&times;</button></div>
      <div class="grid" style="grid-template-columns:repeat(3,1fr);gap:.5rem">${links.map((a) => `<a class="btn" style="text-align:center" href="${esc(a.getAttribute('href'))}" data-go>${a.innerHTML}</a>`).join('')}</div>
      <div class="actions"><button class="link" data-action="logout">Sign out</button></div>`, (sh) => {
      sh.querySelectorAll('[data-go]').forEach((x) => (x.onclick = () => closeModal()));
    });
  };
  // mirror the alerts count on the More button
  const obs = new MutationObserver(() => { const b = $('#badge'); const m = $('#mbadge'); m.textContent = b.textContent; m.classList.toggle('hidden', b.classList.contains('hidden')); });
  obs.observe($('#badge'), { attributes: true, childList: true });
  const st = document.createElement('style');
  st.textContent = `@media (min-width: 761px) { #nav .more-btn { display: none; } }
    @media (max-width: 760px) { #nav a.phone-more { display: none; } #nav { overflow-x: hidden; } }`;
  document.head.appendChild(st);
})();

// ---- search ----
async function searchView(el) {
  const q = S.query.get('q') || S.lastSearch || '';
  el.innerHTML = `<h1>Search</h1><form id="sForm" class="row"><input name="q" value="${esc(q)}" placeholder="Payee, event, bill, document, list item..." autofocus style="flex:1"><button class="primary">Search</button></form><div id="sRes" style="margin-top:1rem"></div>`;
  const run = async (text) => {
    S.lastSearch = text;
    if (text.trim().length < 2) { $('#sRes').innerHTML = '<p class="mute">Type at least 2 letters.</p>'; return; }
    const hits = await api('GET', '/api/search?q=' + encodeURIComponent(text));
    const groups = {};
    hits.forEach((h) => (groups[h.kind] = groups[h.kind] || []).push(h));
    $('#sRes').innerHTML = Object.entries(groups).map(([k, list]) => `<div class="card"><h2>${esc(k[0].toUpperCase() + k.slice(1))}s</h2><ul class="list">${list.map((h) => `<li><span class="grow"><a href="${esc(h.link)}">${esc(h.title)}</a><br><small>${esc(h.sub || '')}${h.date ? ' &middot; ' + esc(h.date) : ''}</small></span></li>`).join('')}</ul></div>`).join('') || '<div class="card empty">Nothing found.</div>';
  };
  $('#sForm').onsubmit = (e) => { e.preventDefault(); run(e.target.q.value); };
  if (q) run(q);
}

// ---- Settings > Recently deleted ----
async function setTrash(el) {
  if (!canFin()) { el.innerHTML = '<div class="card empty">Ask an adult to bring back something that was deleted.</div>'; return; }
  const items = await api('GET', '/api/trash');
  el.innerHTML = `<div class="card"><h2>Recently deleted</h2><p class="mute">Deleted transactions, bills, events, lists, goals, chores, meals, documents and files stay here for 30 days.</p>
    <ul class="list">${items.map((t) => `<li><span class="grow"><b>${esc(t.label)}</b> <span class="pill">${esc(t.kind)}</span><br><small>deleted ${esc(new Date(t.deleted_at).toLocaleString())} by ${esc(userName(t.deleted_by))}</small></span><button class="small primary" data-rs="${t.id}">Restore</button></li>`).join('') || '<li class="empty">Nothing deleted lately.</li>'}</ul></div>`;
  el.querySelectorAll('[data-rs]').forEach((b) => (b.onclick = async () => { try { const r = await api('POST', `/api/trash/${b.dataset.rs}/restore`); toast(`Restored (${r.kind})`); setTrash(el); } catch (e) { fail(e); } }));
}

// ---- Settings > Quiet hours & summary ----
async function setQuiet(el) {
  const me = await api('GET', '/api/me');
  const hours = [[-1, 'Off']].concat([...Array(24).keys()].map((h) => [h, new Date(2000, 0, 1, h).toLocaleTimeString([], { hour: 'numeric' })]));
  el.innerHTML = `<div class="grid"><form class="card" id="qForm"><h2>Quiet hours</h2><p class="mute">Reminders that come up during these hours wait and arrive when they end (bell, phone, email, text and Windows pop-ups).</p>
    <div class="form2"><div><label>From</label><select name="quiet_start">${opts(hours, me.quiet_start)}</select></div><div><label>Until</label><select name="quiet_end">${opts(hours.slice(1), me.quiet_end < 0 ? 7 : me.quiet_end)}</select></div></div>
    <h2 style="margin-top:1rem">Daily summary</h2><p class="mute">One message each morning: today's appointments, bills due soon or overdue, chores, and low-balance warnings.</p>
    <label>Send at</label><select name="digest_hour">${opts(hours, me.digest_hour)}</select>
    <div class="actions"><button type="button" id="qPrev">Preview</button><button class="primary">Save</button></div><pre id="qOut" style="white-space:pre-wrap"></pre></form></div>`;
  $('#qForm').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { await api('PUT', '/api/me', { ...me, quiet_start: Number(d.quiet_start), quiet_end: Number(d.quiet_start) < 0 ? -1 : Number(d.quiet_end), digest_hour: Number(d.digest_hour) }); toast('Saved'); } catch (err) { fail(err); }
  };
  $('#qPrev').onclick = async () => { const m = await api('GET', '/api/me/digest').catch(fail); if (m) $('#qOut').textContent = m.title + '\n' + m.body; };
}

EXTRA_VIEWS.search = searchView;
SETTINGS_TABS.push(['quiet', 'Quiet hours & summary', setQuiet, false]);
SETTINGS_TABS.push(['trash', 'Recently deleted', setTrash, false]);
