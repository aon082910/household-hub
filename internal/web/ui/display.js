// Wall display: read-only week view for a kitchen tablet. The token in the address is the only key.
'use strict';
(function () {
  const token = location.pathname.split('/').pop();
  const $ = (s) => document.querySelector(s);
  const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const pad = (n) => String(n).padStart(2, '0');
  const ymd = (d) => d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  const parse = (s) => { const [d, t] = s.split('T'); const [y, m, dd] = d.split('-').map(Number); const [h, mi] = (t || '00:00').split(':').map(Number); return new Date(y, m - 1, dd, h, mi); };
  let members = {};

  function clock() {
    const n = new Date();
    $('#dClock').textContent = n.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    $('#dDate').textContent = n.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' });
  }

  function color(o) {
    if (o.color) return o.color;
    const ids = (o.members || '').split(',').filter(Boolean);
    return ids.length === 1 && members[ids[0]] ? members[ids[0]].color : '#4f7cff';
  }

  async function load() {
    try {
      const r = await fetch('/api/display/' + encodeURIComponent(token), { cache: 'no-store' });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error || 'error');
      $('#dErr').textContent = '';
      $('#dName').textContent = d.household || 'Household Hub';
      members = {}; (d.members || []).forEach((m) => (members[m.id] = m));
      const today = new Date(); today.setHours(0, 0, 0, 0);
      let html = '';
      for (let i = 0; i < 7; i++) {
        const day = new Date(today); day.setDate(today.getDate() + i); const k = ymd(day);
        const evs = d.events.filter((o) => o.start.slice(0, 10) <= k && (o.end ? o.end.slice(0, 10) : o.start.slice(0, 10)) >= k);
        html += `<div class="day${i === 0 ? ' today' : ''}"><h3>${esc(i === 0 ? 'Today' : day.toLocaleDateString([], { weekday: 'short', day: 'numeric' }))}</h3>` +
          evs.map((o) => `<div class="ev" style="border-color:${esc(color(o))}">${esc(o.title)}<small>${o.all_day ? 'All day' : esc(parse(o.start).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }))}${o.location ? ' &middot; ' + esc(o.location) : ''}</small></div>`).join('') + '</div>';
      }
      $('#dDays').innerHTML = html;
      $('#dChores').innerHTML = d.chores.filter((c) => c.status === 'todo').map((c) => `<li class="${c.late ? 'late' : ''}">${c.assigned_to && members[c.assigned_to] ? `<span class="dot" style="background:${esc(members[c.assigned_to].color)}"></span>${esc(members[c.assigned_to].name)}: ` : ''}${esc(c.name)}</li>`).join('') || '<li>All done</li>';
      $('#dMeals').innerHTML = d.meals.map((m) => `<li>${esc(m.date === ymd(today) ? 'Today' : 'Tomorrow')} ${esc(m.slot)}: ${esc(m.title)}</li>`).join('') || '<li>Nothing planned</li>';
      $('#dLists').innerHTML = d.lists.filter((l) => l.items.length).map((l) => `<b>${esc(l.name)}</b><ul>${l.items.slice(0, 12).map((it) => `<li>${it.qty ? esc(it.qty) + ' ' : ''}${esc(it.text)}</li>`).join('')}${l.items.length > 12 ? `<li>+${l.items.length - 12} more</li>` : ''}</ul>`).join('') || 'All clear';
    } catch (e) {
      $('#dErr').textContent = 'Cannot update: ' + e.message;
    }
  }
  clock(); setInterval(clock, 10000);
  load(); setInterval(load, 120000);
})();
