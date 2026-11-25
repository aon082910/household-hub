// Round 4: Family page (lists, chores, meals, allowance). Kids can use it. Loaded after app.js.
'use strict';

async function familyView(el) {
  const tab = S.famTab || 'lists';
  const tabs = [['lists', 'Lists'], ['chores', 'Chores'], ['meals', 'Meals'], ['allowance', 'Allowance']];
  el.innerHTML = `<h1>Family</h1><div class="tabs">${tabs.map(([k, t]) => `<button data-ftab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="famBody"></div>`;
  el.querySelectorAll('[data-ftab]').forEach((b) => (b.onclick = () => { S.famTab = b.dataset.ftab; familyView(el); }));
  await ({ lists: listsTab, chores: choresTab, meals: mealsTab, allowance: allowanceTab }[tab])($('#famBody'));
}

// ---- lists ----
async function listsTab(el) {
  const lists = await api('GET', '/api/lists');
  if (!S.listId || !lists.some((l) => l.id === S.listId)) S.listId = (lists[0] || {}).id;
  el.innerHTML = `<div class="row">${lists.map((l) => `<button data-list="${l.id}" class="${l.id === S.listId ? 'primary' : ''}">${esc(l.name)} ${l.open ? `<span class="pill">${l.open}</span>` : ''}</button>`).join('')}<button id="lNew">+ List</button></div><div id="lBody" style="margin-top:1rem"></div>`;
  el.querySelectorAll('[data-list]').forEach((b) => (b.onclick = () => { S.listId = Number(b.dataset.list); listsTab(el); }));
  $('#lNew').onclick = () => listForm({ kind: 'shopping' }, () => listsTab(el));
  if (!S.listId) { $('#lBody').innerHTML = '<div class="card empty">Make a list: groceries, Costco run, to-dos, packing.</div>'; return; }
  const l = await api('GET', '/api/lists/' + S.listId);
  const body = $('#lBody');
  body.innerHTML = `<div class="card"><div class="spread"><h2>${esc(l.name)} ${l.private ? '<span class="pill">private</span>' : ''}</h2><div class="row"><button class="small" id="lClear">Clear ticked</button><button class="small" id="lEdit">Edit</button></div></div>
    <form id="lAdd" class="row"><textarea name="text" rows="1" placeholder="Add items (one per line, e.g. 2 milk)" style="flex:1"></textarea>
    ${l.kind === 'todo' ? `<select name="assigned_to" style="width:auto">${opts(S.users.filter((u) => u.active).map((u) => [u.id, u.display_name]), '', 'Anyone')}</select><input type="date" name="due_date" style="width:auto">` : ''}<button class="primary">Add</button></form>
    <ul class="list">${l.items.map((it) => `<li><input type="checkbox" data-it="${it.id}" ${it.done ? 'checked' : ''}><span class="grow" style="${it.done ? 'text-decoration:line-through;opacity:.55' : ''}">${it.qty ? `<b>${esc(it.qty)}</b> ` : ''}${esc(it.text)}
      ${it.assigned_to ? `<small> &middot; ${esc(userName(it.assigned_to))}</small>` : ''}${it.due_date ? `<small> &middot; ${esc(fmtDate(it.due_date))}</small>` : ''}</span><button class="link" data-idel="${it.id}">&times;</button></li>`).join('') || '<li class="empty">Empty.</li>'}</ul></div>`;
  $('#lAdd').onsubmit = async (e) => {
    e.preventDefault(); const d = formData(e.target);
    try { await api('POST', `/api/lists/${l.id}/items`, { text: d.text, assigned_to: idOrNull(d.assigned_to), due_date: d.due_date || '' }); listsTab(el); } catch (err) { fail(err); }
  };
  $('#lAdd').text.onkeydown = (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); $('#lAdd').requestSubmit(); } };
  body.querySelectorAll('[data-it]').forEach((c) => (c.onchange = async () => { await api('PUT', '/api/items/' + c.dataset.it, { done: c.checked }).catch(fail); listsTab(el); }));
  body.querySelectorAll('[data-idel]').forEach((b) => (b.onclick = async () => { await api('DELETE', '/api/items/' + b.dataset.idel).catch(fail); listsTab(el); }));
  $('#lClear').onclick = async () => { await api('POST', `/api/lists/${l.id}/clear`).catch(fail); listsTab(el); };
  $('#lEdit').onclick = () => listForm(l, () => listsTab(el));
}
function listForm(l, done) {
  const isNew = !l.id;
  openModal(`<form id="lf"><h2>${isNew ? 'New list' : 'Edit list'}</h2><label>Name</label><input name="name" value="${esc(l.name)}" required>
    <label>Kind</label><select name="kind">${opts([['shopping', 'Shopping'], ['todo', 'To-do (with who and when)']], l.kind)}</select>
    <label class="inline"><input type="checkbox" name="private" ${l.private ? 'checked' : ''}> Private (only me)</label>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="lfDel">Delete list</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#lf', sh).onsubmit = async (e) => { e.preventDefault(); const d = formData(e.target); try { const r = await api(isNew ? 'POST' : 'PUT', '/api/lists' + (isNew ? '' : '/' + l.id), d); S.listId = r.id || l.id; closeModal(); done(); } catch (err) { fail(err); } };
    const del = $('#lfDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete list "' + l.name + '" and its items?')) return; await api('DELETE', '/api/lists/' + l.id).catch(fail); S.listId = null; closeModal(); done(); };
  });
}

// ---- chores ----
async function choresTab(el) {
  const [due, chores] = await Promise.all([api('GET', '/api/chores/due?days=7'), api('GET', '/api/chores')]);
  const adult = canFin();
  const today = ymd(new Date());
  const mine = due.filter((d) => !d.assigned_to || d.assigned_to === S.me.id || adult);
  const waiting = due.filter((d) => d.status === 'waiting');
  const row = (d) => `<li><span class="dot" style="background:${esc((userBy(d.assigned_to) || {}).color || '#8892a6')}"></span><span class="grow"><b>${esc(d.name)}</b>${d.reward_cents ? ` <span class="pill">${money(d.reward_cents)}</span>` : ''}${d.late ? ' <span class="pill bad">late</span>' : ''}<br>
    <small>${esc(d.date === today ? 'Today' : relDay(d.date))} &middot; ${esc(d.assigned_to ? userName(d.assigned_to) : 'Anyone')}${d.done_by ? ' &middot; done by ' + esc(userName(d.done_by)) : ''}</small></span>
    ${d.status === 'todo' ? `<button class="small primary" data-cdone="${d.chore_id}|${d.date}">Done</button>` : `<span class="pill ${d.status === 'approved' ? 'good' : d.status === 'rejected' ? 'bad' : 'warn'}">${esc(d.status === 'waiting' ? 'waiting for OK' : d.status)}</span>`}
    ${d.status === 'waiting' && adult ? `<button class="small" data-cok="${d.done_id}">OK</button><button class="small" data-cno="${d.done_id}">Not done</button>` : ''}
    ${d.status !== 'todo' && (adult || (d.done_by === S.me.id && d.status === 'waiting')) ? `<button class="link" data-cundo="${d.done_id}">undo</button>` : ''}</li>`;
  el.innerHTML = `${adult && waiting.length ? `<div class="card"><h2>Waiting for your OK</h2><ul class="list">${waiting.map(row).join('')}</ul></div>` : ''}
    <div class="card"><h2>This week</h2><ul class="list">${mine.filter((d) => d.status !== 'waiting' || !adult).map(row).join('') || '<li class="empty">No chores this week.</li>'}</ul></div>
    ${adult ? `<div class="card"><div class="spread"><h2>All chores</h2><button class="primary" id="chAdd">+ Chore</button></div><ul class="list">${chores.map((c) => `<li class="click" data-ch="${c.id}"><span class="grow"><b>${esc(c.name)}</b> ${c.active ? '' : '<span class="pill">paused</span>'}<br><small>${esc(freqText(c))} &middot; ${esc(c.assigned_to ? userName(c.assigned_to) : 'Anyone')}${c.reward_cents ? ' &middot; earns ' + money(c.reward_cents) : ''}</small></span></li>`).join('') || '<li class="empty">No chores yet.</li>'}</ul></div>` : ''}`;
  const act = async (fn) => { try { await fn(); choresTab(el); } catch (e) { fail(e); } };
  el.querySelectorAll('[data-cdone]').forEach((b) => (b.onclick = () => { const [id, date] = b.dataset.cdone.split('|'); act(() => api('POST', `/api/chores/${id}/done`, { date })); }));
  el.querySelectorAll('[data-cok]').forEach((b) => (b.onclick = () => act(() => api('POST', `/api/chore-done/${b.dataset.cok}/decide`, { approve: true }))));
  el.querySelectorAll('[data-cno]').forEach((b) => (b.onclick = () => act(() => api('POST', `/api/chore-done/${b.dataset.cno}/decide`, { approve: false }))));
  el.querySelectorAll('[data-cundo]').forEach((b) => (b.onclick = () => act(() => api('DELETE', `/api/chore-done/${b.dataset.cundo}`))));
  const add = $('#chAdd');
  if (add) add.onclick = () => choreForm({ freq: 'weekly', interval: 1, start_date: ymd(new Date()), active: true }, () => choresTab(el));
  el.querySelectorAll('[data-ch]').forEach((li) => (li.onclick = () => choreForm(chores.find((c) => c.id === Number(li.dataset.ch)), () => choresTab(el))));
}
function choreForm(c, done) {
  const isNew = !c.id;
  openModal(`<form id="cf"><h2>${isNew ? 'New chore' : 'Edit chore'}</h2><div class="form2">
    <div class="full"><label>Chore</label><input name="name" value="${esc(c.name)}" required placeholder="Take out trash, feed the dog..."></div>
    <div><label>Who</label><select name="assigned_to">${opts(S.users.filter((u) => u.active).map((u) => [u.id, u.display_name]), c.assigned_to, 'Anyone')}</select></div>
    <div><label>Earns (allowance)</label><input name="reward" inputmode="decimal" value="${esc(centsInput(c.reward_cents))}" placeholder="0.00"></div>
    <div><label>First day</label><input type="date" name="start_date" value="${esc(c.start_date)}" required></div>
    <div><label>Repeats</label><select name="freq">${freqOpts(c.freq, true)}</select></div>
    <div><label>Every</label><input type="number" min="1" name="interval" value="${esc(c.interval || 1)}"></div>
    ${isNew ? '' : `<div><label>&nbsp;</label><label class="inline"><input type="checkbox" name="active" ${c.active ? 'checked' : ''}> Active</label></div>`}</div>
    <p class="mute">Kids tick their chores; an adult says OK, and then the reward goes to their allowance.</p>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="cfDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#cf', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/chores' + (isNew ? '' : '/' + c.id), { name: d.name, assigned_to: idOrNull(d.assigned_to), reward_cents: Math.abs(toCents(d.reward)), start_date: d.start_date, freq: d.freq, interval: Number(d.interval) || 1, active: isNew ? true : d.active }); closeModal(); done(); } catch (err) { fail(err); }
    };
    const del = $('#cfDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete chore "' + c.name + '"?')) return; await api('DELETE', '/api/chores/' + c.id).catch(fail); closeModal(); done(); };
  });
}

// ---- meals ----
async function mealsTab(el) {
  const start = S.mealWeek || (() => { const d = new Date(); d.setDate(d.getDate() - ((d.getDay() - Number(S.settings.week_start || 0) + 7) % 7)); return d; })();
  S.mealWeek = start;
  const end = new Date(start); end.setDate(end.getDate() + 6);
  const [meals, lists] = await Promise.all([api('GET', `/api/meals?from=${ymd(start)}&to=${ymd(end)}`), api('GET', '/api/lists')]);
  const shopping = lists.filter((l) => l.kind === 'shopping');
  let days = '';
  for (let i = 0; i < 7; i++) {
    const d = new Date(start); d.setDate(start.getDate() + i); const k = ymd(d);
    const ms = meals.filter((m) => m.date === k);
    days += `<div class="card"><div class="spread"><b>${esc(fmtDate(k))}</b><button class="small" data-madd="${k}">+</button></div>
      <ul class="list">${ms.map((m) => `<li class="click" data-meal="${m.id}"><span class="grow"><small>${esc(m.slot)}</small><br>${esc(m.title)}</span></li>`).join('') || '<li class="empty">-</li>'}</ul></div>`;
  }
  el.innerHTML = `<div class="spread"><div class="row"><button id="mwPrev">&lsaquo;</button><b>${esc(fmtDate(ymd(start)))} - ${esc(fmtDate(ymd(end)))}</b><button id="mwNext">&rsaquo;</button></div>
    ${shopping.length ? `<div class="row"><select id="mwList" style="width:auto">${opts(shopping.map((l) => [l.id, l.name]), shopping[0].id)}</select><button id="mwShop">Add ingredients to list</button></div>` : '<small class="mute">Make a shopping list to send ingredients to it.</small>'}</div>
    <div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(170px,1fr));margin-top:1rem">${days}</div>`;
  const shift = (n) => { const d = new Date(start); d.setDate(d.getDate() + n); S.mealWeek = d; mealsTab(el); };
  $('#mwPrev').onclick = () => shift(-7); $('#mwNext').onclick = () => shift(7);
  el.querySelectorAll('[data-madd]').forEach((b) => (b.onclick = () => mealForm({ date: b.dataset.madd, slot: 'dinner' }, () => mealsTab(el))));
  el.querySelectorAll('[data-meal]').forEach((li) => (li.onclick = () => mealForm(meals.find((m) => m.id === Number(li.dataset.meal)), () => mealsTab(el))));
  const shop = $('#mwShop');
  if (shop) shop.onclick = async () => { try { const r = await api('POST', '/api/meals/to-list', { list_id: Number($('#mwList').value), from: ymd(start), to: ymd(end) }); toast(`${r.added} item(s) added`); } catch (e) { fail(e); } };
}
function mealForm(m, done) {
  const isNew = !m.id;
  openModal(`<form id="mf"><h2>${isNew ? 'Plan a meal' : 'Edit meal'}</h2><div class="form2">
    <div><label>Day</label><input type="date" name="date" value="${esc(m.date)}" required></div>
    <div><label>Meal</label><select name="slot">${opts([['breakfast', 'Breakfast'], ['lunch', 'Lunch'], ['dinner', 'Dinner'], ['snack', 'Snack']], m.slot)}</select></div>
    <div class="full"><label>What</label><input name="title" value="${esc(m.title)}" required placeholder="Tacos"></div>
    <div class="full"><label>Ingredients to buy (one per line)</label><textarea name="ingredients" rows="4">${esc(m.ingredients)}</textarea></div>
    <div class="full"><label>Notes / recipe link</label><input name="notes" value="${esc(m.notes)}"></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="mfDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#mf', sh).onsubmit = async (e) => { e.preventDefault(); const d = formData(e.target); try { await api(isNew ? 'POST' : 'PUT', '/api/meals' + (isNew ? '' : '/' + m.id), d); closeModal(); done(); } catch (err) { fail(err); } };
    const del = $('#mfDel', sh);
    if (del) del.onclick = async () => { await api('DELETE', '/api/meals/' + m.id).catch(fail); closeModal(); done(); };
  });
}

// ---- allowance ----
async function allowanceTab(el) {
  const kids = S.me.role === 'kid' ? [S.me] : S.users.filter((u) => u.active && u.role === 'kid');
  if (!kids.length) { el.innerHTML = '<div class="card empty">Add kids under Settings &gt; Members (role "Kid") to track allowance.</div>'; return; }
  const data = await Promise.all(kids.map((k) => api('GET', '/api/allowance/' + k.id)));
  el.innerHTML = `<p class="mute">Allowance is kept here, separate from the bank accounts. Approved chores add to it; adults record payouts and bonuses.</p><div class="grid">${kids.map((k, i) => `<div class="card"><div class="spread"><h2>${esc(k.display_name)}</h2><b class="good" style="font-size:1.3rem">${money(data[i].balance_cents)}</b></div>
    ${canFin() ? `<div class="row"><button class="small" data-pay="${k.id}">Pay out</button><button class="small" data-bonus="${k.id}">Add money</button></div>` : ''}
    <ul class="list">${data[i].entries.slice(0, 15).map((e) => `<li><span class="grow">${esc(e.note || (e.cents < 0 ? 'Paid out' : 'Added'))}<br><small>${esc(fmtDate(e.date))}</small></span><span class="num ${e.cents > 0 ? 'good' : ''}">${money(e.cents, true)}</span></li>`).join('') || '<li class="empty">Nothing yet.</li>'}</ul></div>`).join('')}</div>`;
  const ask = async (id, sign, label) => {
    const v = prompt(label + ' (amount):'); if (!v) return;
    const note = prompt('Note (optional):', sign < 0 ? 'Paid out' : 'Weekly allowance') || '';
    try { await api('POST', '/api/allowance/' + id, { cents: sign * Math.abs(toCents(v)), note }); allowanceTab(el); } catch (e) { fail(e); }
  };
  el.querySelectorAll('[data-pay]').forEach((b) => (b.onclick = () => ask(b.dataset.pay, -1, 'Pay out')));
  el.querySelectorAll('[data-bonus]').forEach((b) => (b.onclick = () => ask(b.dataset.bonus, 1, 'Add money')));
}

// ---- dashboard card: today's chores and open list items ----
async function dashboardExtras(el) {
  try {
    const [due, lists] = await Promise.all([api('GET', '/api/chores/due?days=1'), api('GET', '/api/lists')]);
    const today = ymd(new Date());
    const mine = due.filter((d) => d.status === 'todo' && (!d.assigned_to || d.assigned_to === S.me.id) && d.date <= today);
    const open = lists.reduce((a, l) => a + l.open, 0);
    const grid = el.querySelector('.grid');
    if (!grid || (!mine.length && !open)) return;
    grid.insertAdjacentHTML('afterbegin', `<div class="card"><div class="spread"><h2>Family</h2><a href="#family">Open</a></div><ul class="list">
      ${mine.map((d) => `<li><span class="grow">${esc(d.name)}${d.late ? ' <span class="pill bad">late</span>' : ''}</span><small>chore</small></li>`).join('')}
      ${lists.filter((l) => l.open).map((l) => `<li><span class="grow">${esc(l.name)}</span><small>${l.open} item(s)</small></li>`).join('')}</ul></div>`);
  } catch (_) { /* ignore */ }
}

EXTRA_VIEWS.family = familyView;
