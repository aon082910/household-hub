// Round 2: business vs personal, tax lines, receipts, splits, bulk edit, category rules. Loaded after app.js.
'use strict';

// IRS Schedule C lines most small online sellers use (labels only; not tax advice).
const TAX_LINES = ['1 Gross receipts or sales', '2 Returns and allowances', '6 Other income', '8 Advertising', '9 Car and truck expenses', '10 Commissions and fees',
  '11 Contract labor', '13 Depreciation', '15 Insurance (other than health)', '16b Interest (other)', '17 Legal and professional services', '18 Office expense',
  '20a Rent: vehicles, machinery, equipment', '20b Rent: other business property', '21 Repairs and maintenance', '22 Supplies', '23 Taxes and licenses',
  '24a Travel', '24b Deductible meals', '25 Utilities', '26 Wages', '27a Other expenses', '30 Business use of home', 'Part III Cost of goods sold'];

// ---- bulk edit on the Money page ----
function bulkBar(el, refresh) {
  const bar = $('#bulkBar', el);
  const sel = () => [...el.querySelectorAll('[data-sel]:checked')].map((c) => Number(c.dataset.sel));
  const draw = () => {
    const ids = sel();
    if (!ids.length) { bar.innerHTML = ''; return; }
    bar.innerHTML = `<div class="card row"><b>${ids.length} selected</b>
      <select id="bkCat" style="width:auto">${opts(S.cats.map((c) => [c.id, c.name]), '', 'Set category...')}</select>
      <button class="small" data-bk="biz1">Business</button><button class="small" data-bk="biz0">Personal</button>
      <button class="small" data-bk="tr1">Transfer</button><button class="small" data-bk="tr0">Not transfer</button></div>`;
    const send = async (change) => {
      try { const r = await api('POST', '/api/transactions/bulk', { ids: sel(), change }); toast(`${r.changed} changed`); refresh(); } catch (e) { fail(e); }
    };
    $('#bkCat', bar).onchange = (e) => e.target.value && send({ category_id: Number(e.target.value) });
    bar.querySelectorAll('[data-bk]').forEach((b) => (b.onclick = () => send({
      biz1: { business: true }, biz0: { business: false }, tr1: { transfer: true }, tr0: { transfer: false } }[b.dataset.bk])));
  };
  el.querySelectorAll('[data-sel]').forEach((c) => (c.onchange = draw));
  const all = $('#selAll', el);
  if (all) all.onchange = () => { el.querySelectorAll('[data-sel]').forEach((c) => (c.checked = all.checked)); draw(); };
}

// ---- receipts and split parts inside the transaction form ----
async function txFormExtras(t, box) {
  box.innerHTML = `<h3>Receipts</h3><div id="rcList" class="row"></div>
    <label class="inline"><input type="file" id="rcFile" accept="image/*,application/pdf" capture="environment" style="width:auto"></label>
    <h3>Split into categories</h3><div id="spBox"></div>`;
  const listR = async () => {
    const rs = await api('GET', `/api/transactions/${t.id}/receipts`).catch(() => []);
    $('#rcList', box).innerHTML = rs.map((r) => `<span class="pill">${r.mime.startsWith('image/') ? `<a href="/api/files/${r.id}" target="_blank" rel="noopener"><img src="/api/files/${r.id}" alt="" style="height:48px;vertical-align:middle;border-radius:4px"></a>` : `<a href="/api/files/${r.id}" target="_blank" rel="noopener">${esc(r.name)}</a>`}
      <button type="button" class="link" data-rdel="${r.id}">&times;</button></span>`).join('') || '<small class="mute">None. Add a photo or PDF.</small>';
    box.querySelectorAll('[data-rdel]').forEach((b) => (b.onclick = async () => { if (!confirm('Delete this receipt?')) return; await api('DELETE', '/api/files/' + b.dataset.rdel).catch(fail); listR(); }));
  };
  $('#rcFile', box).onchange = async (e) => {
    const f = e.target.files[0]; if (!f) return;
    const fd = new FormData(); fd.append('file', f);
    const r = await fetch(`/api/transactions/${t.id}/receipts`, { method: 'POST', headers: { 'X-HHub': '1' }, body: fd });
    const d = await r.json().catch(() => ({}));
    if (!r.ok) fail(new Error(d.error || 'upload failed')); else toast('Receipt saved');
    e.target.value = ''; listR();
  };
  listR();
  const parts = await api('GET', `/api/transactions/${t.id}/splits`).catch(() => []);
  const sp = $('#spBox', box);
  const sign = t.amount_cents < 0 ? -1 : 1;
  const row = (p) => `<div class="row sprow"><input data-sa placeholder="0.00" inputmode="decimal" value="${p ? esc((Math.abs(p.amount_cents) / 100).toFixed(2)) : ''}" style="width:7rem">
    <select data-sc style="width:auto">${opts(S.cats.map((c) => [c.id, c.name]), p && p.category_id, '-')}</select>
    <input data-sn placeholder="note" value="${esc(p ? p.notes : '')}" style="width:auto;flex:1">
    <label class="inline"><input type="checkbox" data-sb ${p && p.business ? 'checked' : ''}> business</label><button type="button" class="link" data-sx>&times;</button></div>`;
  const draw = (list) => {
    sp.innerHTML = `<small class="mute">Total ${money(t.amount_cents)}. Parts must add up to it. ${list.length ? '' : 'Not split.'}</small><div id="spRows">${list.map(row).join('')}</div>
      <div class="row"><button type="button" class="small" id="spAdd">+ Part</button>${list.length ? '<button type="button" class="small" id="spClear">Remove split</button>' : ''}<button type="button" class="small primary" id="spSave">Save split</button><span id="spLeft" class="mute"></span></div>`;
    const left = () => {
      const sum = [...sp.querySelectorAll('[data-sa]')].reduce((a, i) => a + Math.round((parseFloat(i.value) || 0) * 100), 0);
      $('#spLeft', sp).textContent = 'left: ' + money(Math.abs(t.amount_cents) - sum);
    };
    sp.querySelectorAll('[data-sa]').forEach((i) => (i.oninput = left));
    sp.querySelectorAll('[data-sx]').forEach((b) => (b.onclick = () => { b.closest('.sprow').remove(); left(); }));
    $('#spAdd', sp).onclick = () => { $('#spRows', sp).insertAdjacentHTML('beforeend', row(null)); draw(collect()); };
    const clr = $('#spClear', sp);
    if (clr) clr.onclick = async () => { try { await api('PUT', `/api/transactions/${t.id}/splits`, { parts: [] }); toast('Split removed'); draw([]); } catch (e) { fail(e); } };
    $('#spSave', sp).onclick = async () => {
      try { await api('PUT', `/api/transactions/${t.id}/splits`, { parts: collect() }); toast('Split saved'); render(); } catch (e) { fail(e); }
    };
    left();
  };
  const collect = () => [...sp.querySelectorAll('.sprow')].map((r) => ({
    amount_cents: sign * Math.abs(Math.round((parseFloat($('[data-sa]', r).value) || 0) * 100)),
    category_id: idOrNull($('[data-sc]', r).value), notes: $('[data-sn]', r).value, business: $('[data-sb]', r).checked,
  }));
  draw(parts.length ? parts : []);
}

// ---- Business page: tax summary and category rules ----
async function businessView(el) {
  const tab = S.bizTab || 'tax';
  el.innerHTML = `<h1>Business</h1><div class="tabs">${[['tax', 'Tax summary'], ['rules', 'Category rules']].map(([k, t]) => `<button data-btab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="bizBody"></div>`;
  el.querySelectorAll('[data-btab]').forEach((b) => (b.onclick = () => { S.bizTab = b.dataset.btab; businessView(el); }));
  await (tab === 'tax' ? taxTab : rulesTab)($('#bizBody'));
}

async function taxTab(el) {
  const y = S.taxYear || new Date().getFullYear();
  const r = await api('GET', '/api/tax/report?year=' + y);
  el.innerHTML = `<div class="spread"><p class="mute" style="margin:0">Business transactions only (marked "business", or imported into a business account). A summary to hand to your tax preparer; not tax advice.</p>
    <div class="row"><select id="taxY" style="width:auto">${opts([0, 1, 2, 3].map((i) => [y - i + (i ? 0 : 0), String(y - i)]).concat([[y + 1, String(y + 1)]]).sort((a, b) => b[0] - a[0]), y)}</select>
    <a class="btn" href="/api/tax/report.csv?year=${y}" download>Download CSV</a></div></div>
    <div class="stats"><div class="stat"><small>Business income</small><b class="good">${money(r.income_cents)}</b></div><div class="stat"><small>Business expenses</small><b>${money(r.expense_cents)}</b></div>
      <div class="stat"><small>Net</small><b class="${r.income_cents - r.expense_cents < 0 ? 'bad' : 'good'}">${money(r.income_cents - r.expense_cents, true)}</b></div></div>
    ${r.uncategorised ? `<div class="card warn">${r.uncategorised} business transaction(s) have no category. Give them one (Money page, or a category rule) so they land on the right line.</div>` : ''}
    ${r.no_receipt ? `<div class="card warn">${r.no_receipt} business expense(s) of $75 or more have no receipt attached.</div>` : ''}
    <div class="card scroll"><table><tr><th>Schedule C line</th><th>Category</th><th class="num">Count</th><th class="num">Amount</th></tr>
    ${r.lines.map((l) => `<tr><td>${esc(l.line)}</td><td>${esc(l.category)} <small>${esc(l.kind)}</small></td><td class="num">${l.count}</td><td class="num ${l.kind === 'income' ? 'good' : ''}">${money(l.amount_cents)}</td></tr>`).join('') || '<tr><td colspan="4" class="empty">No business transactions this year.</td></tr>'}</table></div>
    <p class="mute">Set a category's tax line on the Budget page (pencil button). Business categories starting with "Business:" are already set up.</p>`;
  $('#taxY').onchange = (e) => { S.taxYear = Number(e.target.value); taxTab(el); };
}

async function rulesTab(el) {
  const rules = await api('GET', '/api/category-rules');
  const flag = (v, yes, no) => (v === 1 ? yes : v === 0 ? no : '');
  el.innerHTML = `<div class="card"><div class="spread"><h2>Category rules</h2><div class="row"><button id="crApply">Run on uncategorised</button><button class="primary" id="crAdd">+ Rule</button></div></div>
    <p class="mute">New imported and emailed transactions whose payee or notes contain the words get this category and flags. Rules run top to bottom; the first match wins. Several words: separate with <code>|</code>.</p>
    <ul class="list">${rules.map((r) => `<li class="click" data-cr="${r.id}"><span class="grow"><b>${esc(r.contains)}</b> &rarr; ${esc(catName(r.category_id) || '(category unchanged)')} ${flag(r.business, '<span class="pill">business</span>', '<span class="pill">personal</span>')} ${flag(r.transfer, '<span class="pill">transfer</span>', '')}<br><small>order ${r.priority}</small></span></li>`).join('') || '<li class="empty">No rules yet. Example: "canva|adobe" &rarr; Business: Office &amp; software, business.</li>'}</ul></div>`;
  $('#crAdd').onclick = () => crForm({ business: -1, transfer: -1, priority: 100 }, () => rulesTab(el));
  $('#crApply').onclick = async () => { try { const x = await api('POST', '/api/category-rules/apply'); toast(`${x.changed} transaction(s) updated`); } catch (e) { fail(e); } };
  el.querySelectorAll('[data-cr]').forEach((li) => (li.onclick = () => crForm(rules.find((r) => r.id === Number(li.dataset.cr)), () => rulesTab(el))));
}

function crForm(r, done) {
  const isNew = !r.id;
  openModal(`<form id="crForm"><h2>${isNew ? 'New category rule' : 'Edit category rule'}</h2><div class="form2">
    <div class="full"><label>Payee or notes contain</label><input name="contains" value="${esc(r.contains)}" required placeholder="canva|adobe"></div>
    <div><label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name]), r.category_id, '(leave as is)')}</select></div>
    <div><label>Business or personal</label><select name="business">${opts([[-1, 'Leave as is'], [1, 'Business'], [0, 'Personal']], r.business)}</select></div>
    <div><label>Transfer</label><select name="transfer">${opts([[-1, 'Leave as is'], [1, 'Transfer (not income/spending)'], [0, 'Not a transfer']], r.transfer)}</select></div>
    <div><label>Order (lower first)</label><input type="number" name="priority" value="${esc(r.priority || 100)}"></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="crDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#crForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/category-rules' + (isNew ? '' : '/' + r.id), { contains: d.contains, category_id: idOrNull(d.category_id), business: Number(d.business), transfer: Number(d.transfer), priority: Number(d.priority) || 100 }); closeModal(); done(); } catch (err) { fail(err); }
    };
    const del = $('#crDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete this rule?')) return; try { await api('DELETE', '/api/category-rules/' + r.id); closeModal(); done(); } catch (err) { fail(err); } };
  });
}

EXTRA_VIEWS.business = businessView;
FIN_VIEWS.push('business');
