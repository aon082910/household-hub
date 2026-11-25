// Round 3: Reports page (trends, net worth, year in review, goals, debt payoff). Loaded after plan.js.
'use strict';

// stacked bar chart: rows = [{label, parts:[{value,color}]}]
function barChart(rows, line) {
  const W = 640, H = 210, P = 38, n = rows.length || 1;
  const max = Math.max(1, ...rows.map((r) => r.parts.reduce((a, p) => a + p.value, 0)), ...(line || []).map((v) => Math.abs(v)));
  const bw = (W - P - 10) / n;
  const y = (v) => H - 24 - (v * (H - 40)) / max;
  let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" style="width:100%;height:auto"><line x1="${P}" x2="${W - 6}" y1="${y(0)}" y2="${y(0)}" stroke="var(--line)"/>`;
  svg += `<text x="2" y="${y(max) + 4}" font-size="10" fill="var(--mute)">${esc(money(max).replace(/\.\d\d$/, ''))}</text>`;
  rows.forEach((r, i) => {
    let base = 0;
    for (const p of r.parts) {
      if (p.value <= 0) continue;
      svg += `<rect x="${(P + i * bw + 3).toFixed(1)}" y="${y(base + p.value).toFixed(1)}" width="${Math.max(2, bw - 6).toFixed(1)}" height="${(y(base) - y(base + p.value)).toFixed(1)}" fill="${esc(p.color)}"><title>${esc(r.label)}: ${esc(money(p.value))}</title></rect>`;
      base += p.value;
    }
    if (n <= 24 && (n <= 12 || i % 2 === 0)) svg += `<text x="${(P + i * bw + bw / 2).toFixed(1)}" y="${H - 8}" font-size="9" fill="var(--mute)" text-anchor="middle">${esc(r.label)}</text>`;
  });
  if (line) {
    const d = line.map((v, i) => `${i ? 'L' : 'M'}${(P + i * bw + bw / 2).toFixed(1)},${y(Math.max(0, v)).toFixed(1)}`).join('');
    svg += `<path d="${d}" fill="none" stroke="var(--good)" stroke-width="2"/>`;
  }
  return svg + '</svg>';
}
const monthLabel = (m) => new Date(m + '-15').toLocaleDateString(undefined, { month: 'short' });

async function reportsView(el) {
  const tab = S.repTab || 'trends';
  const tabs = [['trends', 'Spending trends'], ['networth', 'Net worth'], ['year', 'Year in review'], ['goals', 'Savings goals'], ['debt', 'Debt payoff']];
  el.innerHTML = `<h1>Reports</h1><div class="tabs">${tabs.map(([k, t]) => `<button data-rtab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="repBody"></div>`;
  el.querySelectorAll('[data-rtab]').forEach((b) => (b.onclick = () => { S.repTab = b.dataset.rtab; reportsView(el); }));
  await ({ trends: trendsTab, networth: netTab, year: yearTab, goals: goalsTab, debt: debtTab }[tab])($('#repBody'));
}

async function trendsTab(el) {
  const rows = await api('GET', '/api/reports/trends?months=12');
  const totals = {};
  rows.forEach((r) => Object.entries(r.by_category).forEach(([k, v]) => (totals[k] = (totals[k] || 0) + v)));
  const top = Object.entries(totals).sort((a, b) => b[1] - a[1]).slice(0, 6).map((x) => x[0]);
  const color = (k) => (top.includes(k) ? COLORS[top.indexOf(k) % COLORS.length] : '#8892a6');
  const chart = barChart(rows.map((r) => {
    const other = Object.entries(r.by_category).filter(([k]) => !top.includes(k)).reduce((a, [, v]) => a + v, 0);
    return { label: monthLabel(r.month), parts: top.map((k) => ({ value: r.by_category[k] || 0, color: color(k) })).concat([{ value: other, color: '#8892a6' }]) };
  }), rows.map((r) => r.income_cents));
  const avg = rows.reduce((a, r) => a + r.expense_cents, 0) / rows.length;
  el.innerHTML = `<div class="stats"><div class="stat"><small>Average spending / month</small><b>${money(Math.round(avg))}</b></div>
    <div class="stat"><small>Average income / month</small><b class="good">${money(Math.round(rows.reduce((a, r) => a + r.income_cents, 0) / rows.length))}</b></div></div>
    <div class="card"><h2>Last 12 months</h2>${chart}<div class="row">${top.map((k) => `<span><span class="dot" style="background:${color(k)}"></span> ${esc(k)}</span>`).join('')}<span><span class="dot" style="background:#8892a6"></span> Other</span><span style="color:var(--good)">&#9135; Income</span></div></div>
    <div class="card scroll"><table><tr><th>Category</th>${rows.map((r) => `<th class="num">${esc(monthLabel(r.month))}</th>`).join('')}</tr>
    ${Object.entries(totals).sort((a, b) => b[1] - a[1]).map(([k]) => `<tr><td>${esc(k)}</td>${rows.map((r) => `<td class="num">${r.by_category[k] ? esc(money(r.by_category[k]).replace(/\.\d\d$/, '')) : ''}</td>`).join('')}</tr>`).join('')}</table></div>`;
}

async function netTab(el) {
  const pts = await api('GET', '/api/reports/networth?months=24');
  const last = pts[pts.length - 1] || { net_cents: 0, assets_cents: 0, debt_cents: 0 };
  const yearAgo = pts[pts.length - 13];
  el.innerHTML = `<div class="stats"><div class="stat"><small>Net worth now</small><b class="${last.net_cents < 0 ? 'bad' : 'good'}">${money(last.net_cents)}</b></div>
    <div class="stat"><small>Have</small><b>${money(last.assets_cents)}</b></div><div class="stat"><small>Owe</small><b class="bad">${money(last.debt_cents)}</b></div>
    ${yearAgo ? `<div class="stat"><small>Change in 12 months</small><b>${money(last.net_cents - yearAgo.net_cents, true)}</b></div>` : ''}</div>
    <div class="card"><h2>Last 24 months</h2>${lineChart([{ name: 'Net worth', color: '#4f7cff', points: pts.map((p) => ({ date: p.month + '-28', balance_cents: p.net_cents })) }], null)}
    <p class="mute">From the accounts in this app (opening balances plus every transaction). Add savings, retirement and loan accounts to make it complete.</p></div>`;
}

async function yearTab(el) {
  const yr = S.repYear || new Date().getFullYear();
  const y = await api('GET', '/api/reports/year?year=' + yr);
  const list = (l) => `<ul class="list">${l.map((x) => `<li><span class="grow">${esc(x.name)}${x.count > 1 ? ` <small>${x.count}x</small>` : ''}</span><b>${money(x.amount_cents)}</b></li>`).join('') || '<li class="empty">Nothing yet.</li>'}</ul>`;
  el.innerHTML = `<div class="spread"><h2>${yr}</h2><select id="ryr" style="width:auto">${opts([0, 1, 2, 3].map((i) => [new Date().getFullYear() - i, String(new Date().getFullYear() - i)]), yr)}</select></div>
    <div class="stats"><div class="stat"><small>Income</small><b class="good">${money(y.income_cents)}</b></div><div class="stat"><small>Spent</small><b>${money(y.expense_cents)}</b></div>
    <div class="stat"><small>Kept</small><b>${y.savings_rate_pct}%</b></div><div class="stat"><small>Average month</small><b>${money(y.monthly_avg_expense_cents)}</b></div>
    <div class="stat"><small>Subscriptions / year</small><b>${money(y.subscriptions_yearly_cents)}</b></div></div>
    <div class="grid"><div class="card"><h2>Top categories</h2>${list(y.top_categories)}</div><div class="card"><h2>Top places</h2>${list(y.top_payees)}</div><div class="card"><h2>Biggest single costs</h2>${list(y.biggest)}</div></div>`;
  $('#ryr').onchange = (e) => { S.repYear = Number(e.target.value); yearTab(el); };
}

async function goalsTab(el) {
  const gs = await api('GET', '/api/goals');
  el.innerHTML = `<div class="spread"><p class="mute" style="margin:0">Set money aside for big costs: a 6-month insurance premium, Christmas, the car registration. A goal can follow a savings account's balance, or you add money to it by hand.</p><button class="primary" id="gAdd">+ Goal</button></div>
    <div class="grid" style="margin-top:1rem">${gs.map((g) => `<div class="card"><div class="spread"><h2>${esc(g.name)}</h2>${g.done ? '<span class="pill good">done</span>' : ''}</div>
      <div class="bar ${g.progress_pct >= 100 ? '' : 'near'}"><i style="width:${g.progress_pct}%;background:${esc(g.color || 'var(--accent)')}"></i></div>
      <p><b>${money(g.current_cents)}</b> of ${money(g.target_cents)} (${g.progress_pct}%)${g.target_date ? ` &middot; by ${esc(fmtDate(g.target_date))}` : ''}</p>
      ${g.per_month_cents ? `<p class="mute">Put aside about <b>${money(g.per_month_cents)}</b> a month for ${g.months_left} month(s).</p>` : ''}
      <small>${g.account_id ? 'Follows ' + esc(accName(g.account_id)) : 'Added by hand'}</small>
      <div class="actions">${g.account_id ? '' : `<button class="small" data-gadd="${g.id}">+ Add money</button>`}<button class="small" data-gedit="${g.id}">Edit</button></div></div>`).join('') || '<div class="card empty">No goals yet.</div>'}</div>`;
  $('#gAdd').onclick = () => goalForm({}, () => goalsTab(el));
  el.querySelectorAll('[data-gedit]').forEach((b) => (b.onclick = () => goalForm(gs.find((g) => g.id === Number(b.dataset.gedit)), () => goalsTab(el))));
  el.querySelectorAll('[data-gadd]').forEach((b) => (b.onclick = async () => {
    const v = prompt('Amount to add (use - to take out):'); if (!v) return;
    try { await api('POST', `/api/goals/${b.dataset.gadd}/add`, { cents: toCents(v) }); goalsTab(el); } catch (e) { fail(e); }
  }));
}

function goalForm(g, done) {
  const isNew = !g.id;
  openModal(`<form id="gForm"><h2>${isNew ? 'New goal' : 'Edit goal'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="name" value="${esc(g.name)}" required></div>
    <div><label>Target amount</label><input name="target" inputmode="decimal" value="${esc(centsInput(g.target_cents))}" required></div>
    <div><label>Need it by (optional)</label><input type="date" name="target_date" value="${esc(g.target_date)}"></div>
    <div><label>Follow a savings account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), g.account_id, '(no, add by hand)')}</select></div>
    <div><label>Color</label><input type="color" name="color" value="${esc(g.color || '#4f7cff')}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="private" ${g.private ? 'checked' : ''}> Private</label>${isNew ? '' : `<label class="inline"><input type="checkbox" name="done" ${g.done ? 'checked' : ''}> Done</label>`}</div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="gDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#gForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/goals' + (isNew ? '' : '/' + g.id), { name: d.name, target_cents: Math.abs(toCents(d.target)), target_date: d.target_date, account_id: idOrNull(d.account_id), color: d.color, private: d.private, done: !!d.done }); closeModal(); done(); } catch (err) { fail(err); }
    };
    const del = $('#gDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete goal "' + g.name + '"?')) return; try { await api('DELETE', '/api/goals/' + g.id); closeModal(); done(); } catch (err) { fail(err); } };
  });
}

async function debtTab(el) {
  const extra = S.debtExtra || '100';
  const r = await api('GET', '/api/debts?extra=' + encodeURIComponent(extra));
  const yrs = (m) => (m >= 12 ? `${Math.floor(m / 12)} yr ${m % 12} mo` : `${m} mo`);
  const plan = (p, title, what) => `<div class="card"><h2>${title}</h2><p class="mute">${what}</p>
    ${p.stuck ? '<p class="bad">The payments do not cover the interest. Raise the extra amount or the minimums.</p>' : `<div class="stats"><div class="stat"><small>Debt-free in</small><b>${yrs(p.months)}</b></div><div class="stat"><small>Interest paid</small><b>${money(p.total_interest_cents)}</b></div></div>`}
    <ol>${p.order.map((d) => `<li>${esc(d.name)} <small>${money(d.balance_cents)} at ${(d.apr_bps / 100).toFixed(2)}% &middot; paid off in ${d.paid_off_month ? yrs(d.paid_off_month) : '?'}</small></li>`).join('')}</ol></div>`;
  el.innerHTML = r.debts.length ? `<div class="card row"><label>Extra per month on top of the minimums</label><input id="dExtra" value="${esc(extra)}" inputmode="decimal" style="width:8rem"><button id="dGo">Update</button></div>
    ${r.debts.some((d) => !d.apr_bps || !d.min_payment_cents) ? '<div class="card warn">Some cards or loans have no interest rate or minimum payment. Add them on the Accounts page for a correct plan.</div>' : ''}
    <div class="grid">${plan(r.snowball, 'Snowball', 'Smallest balance first. Quick wins keep you going.')}${plan(r.avalanche, 'Avalanche', 'Highest interest first. Pays the least interest.')}</div>
    <p class="mute">An estimate with today's balances and rates; real statements will differ a little.</p>`
    : '<div class="card empty">No debts found. Cards and loans show up here when their account type is "Credit card" or "Loan" and the balance is below zero.</div>';
  const go = $('#dGo');
  if (go) go.onclick = () => { S.debtExtra = $('#dExtra').value; debtTab(el); };
}

EXTRA_VIEWS.reports = reportsView;
FIN_VIEWS.push('reports');
