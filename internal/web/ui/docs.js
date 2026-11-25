// Round 6: document vault, calendar subscriptions, wall display links. Loaded after app.js.
'use strict';

const DOC_KINDS = [['insurance', 'Insurance'], ['id', 'ID / license / passport'], ['vehicle', 'Vehicle / registration'], ['warranty', 'Warranty / receipt'], ['medical', 'Medical'], ['legal', 'Legal'], ['financial', 'Financial'], ['other', 'Other']];

async function documentsView(el) {
  const docs = await api('GET', '/api/documents');
  const soon = docs.filter((d) => d.days_left != null && d.days_left <= 30);
  el.innerHTML = `<div class="spread"><h1>Documents</h1><button class="primary" id="docAdd">+ Document</button></div>
    <p class="mute">Insurance cards, licenses, registrations, warranties. Private to you unless you share them with the adults of the house; kids never see this page. Expiry reminders go out like bill reminders. Avoid storing full Social Security numbers.</p>
    ${soon.length ? `<div class="card warn">${soon.map((d) => `${esc(d.title)} ${d.days_left < 0 ? 'expired' : 'expires in ' + d.days_left + ' day(s)'}`).join(' &middot; ')}</div>` : ''}
    <div class="grid">${docs.map((d) => `<div class="card click" data-doc="${d.id}"><div class="spread"><h2>${esc(d.title)}</h2>${d.private ? '<span class="pill">private</span>' : '<span class="pill">shared</span>'}</div>
      <small>${esc((DOC_KINDS.find((k) => k[0] === d.kind) || [, d.kind])[1])}${d.expires ? ` &middot; expires ${esc(fmtDate(d.expires))}` : ''} &middot; ${d.files} file(s)</small>
      ${d.days_left != null && d.days_left <= 30 ? `<p class="${d.days_left < 0 ? 'bad' : 'warn'}">${d.days_left < 0 ? 'Expired' : d.days_left + ' day(s) left'}</p>` : ''}</div>`).join('') || '<div class="card empty">Nothing yet.</div>'}</div>`;
  $('#docAdd').onclick = () => docForm({ kind: 'insurance', remind_days: '30,7', private: true }, () => documentsView(el));
  el.querySelectorAll('[data-doc]').forEach((c) => (c.onclick = () => docForm(docs.find((d) => d.id === Number(c.dataset.doc)), () => documentsView(el))));
}

function docForm(d, done) {
  const isNew = !d.id;
  openModal(`<form id="dForm"><h2>${isNew ? 'New document' : esc(d.title)}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="title" value="${esc(d.title)}" required placeholder="Car insurance card"></div>
    <div><label>Type</label><select name="kind">${opts(DOC_KINDS, d.kind)}</select></div>
    <div><label>Expires (optional)</label><input type="date" name="expires" value="${esc(d.expires)}"></div>
    <div class="full"><label>Remind me (days before, comma separated)</label><input name="remind_days" value="${esc(d.remind_days)}"></div>
    <div class="full"><label>Notes (policy number, phone)</label><textarea name="notes" rows="2">${esc(d.notes)}</textarea></div>
    <div class="full"><label class="inline"><input type="checkbox" name="private" ${d.private ? 'checked' : ''}> Private (only me); untick to share with the adults</label></div></div>
    ${isNew ? '<p class="mute">Save first, then add photos or PDFs.</p>' : '<h3>Files</h3><div id="dFiles" class="row"></div><input type="file" id="dUp" accept="image/*,application/pdf" style="width:auto">'}
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="dDel">Delete</button>'}<button type="button" data-action="close">Close</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#dForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const f = formData(e.target);
      try { const r = await api(isNew ? 'POST' : 'PUT', '/api/documents' + (isNew ? '' : '/' + d.id), f); toast('Saved'); if (isNew) { closeModal(); done(); docForm({ ...r, files: 0 }, done); } else { closeModal(); done(); } } catch (err) { fail(err); }
    };
    if (isNew) return;
    const list = async () => {
      const fs = await api('GET', `/api/documents/${d.id}/files`).catch(() => []);
      $('#dFiles', sh).innerHTML = fs.map((x) => `<span class="pill"><a href="/api/files/${x.id}" target="_blank" rel="noopener">${esc(x.name)}</a> <button type="button" class="link" data-fdel="${x.id}">&times;</button></span>`).join('') || '<small class="mute">No files.</small>';
      sh.querySelectorAll('[data-fdel]').forEach((b) => (b.onclick = async () => { if (!confirm('Delete this file?')) return; await api('DELETE', '/api/files/' + b.dataset.fdel).catch(fail); list(); }));
    };
    $('#dUp', sh).onchange = async (e) => {
      const file = e.target.files[0]; if (!file) return;
      const fd = new FormData(); fd.append('file', file);
      const r = await fetch(`/api/documents/${d.id}/files`, { method: 'POST', headers: { 'X-HHub': '1' }, body: fd });
      const x = await r.json().catch(() => ({}));
      if (!r.ok) fail(new Error(x.error || 'upload failed')); else toast('File saved');
      e.target.value = ''; list();
    };
    list();
    $('#dDel', sh).onclick = async () => { if (!confirm('Delete "' + d.title + '" and its files?')) return; try { await api('DELETE', '/api/documents/' + d.id); closeModal(); done(); } catch (err) { fail(err); } };
  });
}

// ---- Settings > Calendars (admin) ----
async function setCalendars(el) {
  const feeds = await api('GET', '/api/feeds');
  el.innerHTML = `<div class="card"><div class="spread"><h2>Other calendars</h2><button class="primary" id="fdAdd">+ Calendar</button></div>
    <p class="mute">Show a school, team, work or Google/Outlook calendar in the family calendar. Paste its .ics address (Google: calendar settings, "Secret address in iCal format"; Outlook: Settings, Shared calendars, Publish, ICS). It refreshes every hour. This is one-way: events stay in their own calendar and are edited there.</p>
    <ul class="list">${feeds.map((f) => `<li><span class="dot" style="background:${esc(f.color)}"></span><span class="grow"><b>${esc(f.name)}</b> ${f.enabled ? '' : '<span class="pill">off</span>'}<br>
      <small>${f.items} event(s)${f.skipped ? ` &middot; ${f.skipped} repeating event(s) with a rule this app cannot read were left out` : ''} &middot; ${f.last_fetch ? 'updated ' + esc(new Date(f.last_fetch).toLocaleString()) : 'not loaded yet'}${f.last_error ? ' &middot; <span class="bad">' + esc(f.last_error) + '</span>' : ''}</small></span>
      <button class="small" data-fdr="${f.id}">Refresh</button><button class="small" data-fd="${f.id}">Edit</button></li>`).join('') || '<li class="empty">None yet.</li>'}</ul></div>`;
  $('#fdAdd').onclick = () => feedForm({ enabled: true, color: '#8a63d2' }, () => setCalendars(el));
  el.querySelectorAll('[data-fd]').forEach((b) => (b.onclick = () => feedForm(feeds.find((f) => f.id === Number(b.dataset.fd)), () => setCalendars(el))));
  el.querySelectorAll('[data-fdr]').forEach((b) => (b.onclick = async () => { try { const r = await api('POST', `/api/feeds/${b.dataset.fdr}/fetch`); toast(`${r.items} event(s) loaded`); } catch (e) { fail(e); } setCalendars(el); }));
}
function feedForm(f, done) {
  const isNew = !f.id;
  openModal(`<form id="fdF"><h2>${isNew ? 'Add a calendar' : 'Edit calendar'}</h2>
    <label>Name</label><input name="name" value="${esc(f.name)}" required placeholder="School, soccer team...">
    <label>Calendar address (.ics)</label><input name="url" value="${esc(f.url)}" required placeholder="https://... or webcal://...">
    <label>Color</label><input type="color" name="color" value="${esc(f.color || '#8a63d2')}">
    <label class="inline"><input type="checkbox" name="enabled" ${f.enabled ? 'checked' : ''}> Show it</label>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="fdDel">Remove</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save and load</button></div></form>`, (sh) => {
    $('#fdF', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { const r = await api(isNew ? 'POST' : 'PUT', '/api/feeds' + (isNew ? '' : '/' + f.id), d); if (r.error) fail(new Error('Saved, but loading failed: ' + r.error)); else toast(`${r.items} event(s) loaded`); closeModal(); done(); } catch (err) { fail(err); }
    };
    const del = $('#fdDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Remove "' + f.name + '"?')) return; await api('DELETE', '/api/feeds/' + f.id).catch(fail); closeModal(); done(); };
  });
}

// ---- Settings > Wall display (admin) ----
async function setDisplays(el) {
  const list = await api('GET', '/api/displays');
  el.innerHTML = `<div class="card"><div class="spread"><h2>Wall display</h2><button class="primary" id="dpAdd">+ Display link</button></div>
    <p class="mute">A full-screen week view for a tablet in the kitchen: shared events, today's chores, meals and lists. No money and nothing private, and no sign-in on the tablet. Open the link once on the tablet and add it to its home screen. Remove the link here if the tablet is lost.</p>
    <div id="dpNew"></div>
    <ul class="list">${list.map((d) => `<li><span class="grow"><b>${esc(d.label)}</b><br><small>made ${esc(new Date(d.created).toLocaleDateString())} &middot; ${d.last_seen ? 'last used ' + esc(new Date(d.last_seen).toLocaleString()) : 'not used yet'}</small></span><button class="small danger" data-dprm="${esc(d.id)}">Remove</button></li>`).join('') || '<li class="empty">No display links.</li>'}</ul></div>`;
  $('#dpAdd').onclick = async () => {
    const label = prompt('Name for this display:', 'Kitchen tablet'); if (label === null) return;
    try {
      const r = await api('POST', '/api/displays', { Label: label });
      const link = location.origin + r.path;
      $('#dpNew').innerHTML = `<div class="card"><p><b>Open this on the tablet</b> (shown only now):</p><code>${esc(link)}</code><div class="actions"><button id="dpCopy">Copy</button><a class="btn" href="${esc(r.path)}" target="_blank" rel="noopener">Preview</a></div></div>`;
      $('#dpCopy').onclick = () => navigator.clipboard.writeText(link).then(() => toast('Copied'));
    } catch (e) { fail(e); }
  };
  el.querySelectorAll('[data-dprm]').forEach((b) => (b.onclick = async () => { if (!confirm('Remove this display link? The tablet stops updating.')) return; await api('DELETE', '/api/displays/' + b.dataset.dprm).catch(fail); setDisplays(el); }));
}

EXTRA_VIEWS.documents = documentsView;
FIN_VIEWS.push('documents');
SETTINGS_TABS.push(['calendars', 'Other calendars', setCalendars, true]);
SETTINGS_TABS.push(['displays', 'Wall display', setDisplays, true]);
