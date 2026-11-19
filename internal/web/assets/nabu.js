// NABU pages: buttons that act on the drive, and live progress for
// downloads and for preparing new dumps. Plain ES5 for old browsers.
(function () {
  'use strict';

  function post(action, body, done) {
    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/nabu/api/' + action);
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.setRequestHeader('X-Nabu', '1');
    xhr.onload = function () {
      var res = {};
      try { res = JSON.parse(xhr.responseText); } catch (e) { res = { error: xhr.responseText }; }
      if (xhr.status >= 400) { alert(res.error || ('NABU could not do that (' + xhr.status + ').')); }
      if (done) done(xhr.status < 400, res);
    };
    xhr.onerror = function () { if (done) done(false, {}); };
    xhr.send(JSON.stringify(body || {}));
  }

  function human(n) {
    if (!n || n < 0) return '0 B';
    var u = ['B', 'KB', 'MB', 'GB', 'TB'], i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)) + ' ' + u[i];
  }
  function duration(s) {
    if (!s) return '';
    if (s < 90) return Math.round(s) + ' s';
    if (s < 5400) return Math.round(s / 60) + ' min';
    if (s < 172800) return (s / 3600).toFixed(1) + ' h';
    return (s / 86400).toFixed(1) + ' days';
  }

  var labels = {
    starting: 'Contacting Wikimedia…',
    checking: 'Checking the part already downloaded…',
    downloading: 'Downloading',
    retrying: 'Connection lost, retrying soon…',
    finishing: 'Installing…',
    done: 'Downloaded and verified. NABU is now preparing it for reading.',
    failed: 'Download stopped',
    cancelled: 'Paused. You can resume later; nothing is lost.'
  };

  var box = document.getElementById('dl');
  var preps = document.querySelectorAll('[data-prep]');
  var ftPreps = document.querySelectorAll('[data-prep-ft]');
  var ftBefore = {};
  var wasActive = false, wasChecking = false;
  var readyBefore = {};

  function poll() {
    var xhr = new XMLHttpRequest();
    xhr.open('GET', '/nabu/api/status');
    xhr.onload = function () {
      var st;
      try { st = JSON.parse(xhr.responseText); } catch (e) { return; }
      var d = st.download;
      var active = ['starting', 'checking', 'downloading', 'retrying', 'finishing'].indexOf(d.state) >= 0;
      if (box) {
        box.hidden = d.state === 'idle';
        box.querySelector('[data-file]').textContent = d.release || '';
        box.querySelector('[data-part]').textContent = active ? (d.file || '') : '';
        var pct = d.total > 0 ? Math.min(100, 100 * d.done / d.total) : 0;
        box.querySelector('.progress > div').style.width = pct.toFixed(1) + '%';
        var line = labels[d.state] || d.state;
        if (d.state === 'downloading' || d.state === 'checking') {
          line += ' · ' + human(d.done) + (d.total ? ' of ' + human(d.total) + ' (' + pct.toFixed(1) + '%)' : '');
          if (d.speed > 0 && d.state === 'downloading') line += ' · ' + human(d.speed) + '/s';
          if (d.eta) line += ' · about ' + duration(d.eta) + ' left';
        }
        box.querySelector('[data-line]').textContent = line;
        box.querySelector('[data-error]').textContent = d.error && (d.state === 'failed' || d.state === 'retrying') ? d.error : '';
        box.querySelector('[data-cancel]').hidden = !active;
        box.className = 'notice' + (d.state === 'failed' ? ' bad' : d.state === 'done' ? ' ok' : '');
      }
      var preparing = false;
      for (var i = 0; i < (st.books || []).length; i++) {
        var b = st.books[i];
        if (b.state === 'preparing' || b.state === 'waiting') preparing = true;
        var el = document.querySelector('[data-prep="' + b.id + '"] .progress > div');
        if (el && b.total > 0) el.style.width = (100 * b.done / b.total).toFixed(1) + '%';
        if (readyBefore[b.id] === false && b.state === 'ready' && preps.length) { location.reload(); return; }
        readyBefore[b.id] = b.state === 'ready';
        var ftBusy = b.ft === 'building' || b.ft === 'queued';
        if (ftBusy) preparing = true;
        var fe = document.querySelectorAll('[data-prep-ft="' + b.id + '"] .progress > *');
        for (var f = 0; f < fe.length; f++) if (b.ft_total > 0) fe[f].style.width = (100 * b.ft_done / b.ft_total).toFixed(1) + '%';
        if (ftBefore[b.id] && !ftBusy && ftPreps.length) { location.reload(); return; }
        ftBefore[b.id] = ftBusy;
      }
      // Offline pictures: live progress; the buttons change with the state.
      for (var p = 0; p < (st.pictures || []).length; p++) {
        var pc = st.pictures[p];
        var row = document.querySelector('[data-pics="' + pc.wiki + '"]');
        if (!row) continue;
        var was = row.getAttribute('data-pics-state');
        var busy = pc.state === 'running' || pc.state === 'queued';
        if ((was === 'running' || was === 'queued') !== busy) { location.reload(); return; }
        if (busy) preparing = true;
        var ln = row.querySelector('[data-pics-line]');
        if (ln) ln.textContent = pc.line;
        var bar = row.querySelector('[data-pics-bar]');
        if (bar && pc.total > 0) bar.style.width = (100 * pc.done / pc.total).toFixed(1) + '%';
      }
      var checking = document.querySelector('[data-checking]');
      if (checking) checking.hidden = !st.checking;
      if ((wasActive && !active) || (wasChecking && !st.checking)) { setTimeout(function () { location.reload(); }, 600); return; }
      wasActive = active;
      wasChecking = st.checking;
      setTimeout(poll, active || st.checking || preparing ? 1000 : 5000);
    };
    xhr.onerror = function () { setTimeout(poll, 5000); };
    xhr.send();
  }
  if (box || preps.length || ftPreps.length || document.querySelector('[data-checking]')) poll();

  // Buttons: <button data-action="download" data-wiki="enwiki">.
  document.addEventListener('click', function (ev) {
    var b = ev.target.closest ? ev.target.closest('[data-action]') : null;
    if (!b) return;
    ev.preventDefault();
    var action = b.getAttribute('data-action');
    var confirmText = b.getAttribute('data-confirm');
    if (confirmText && !confirm(confirmText)) return;
    var promptText = b.getAttribute('data-prompt'), answer = '';
    if (promptText) {
      answer = window.prompt(promptText, b.getAttribute('data-value') || '');
      if (answer === null || !answer.replace(/\s/g, '')) return;
    }
    var body = {
      slug: b.getAttribute('data-slug') || '',
      name: answer,
      wiki: b.getAttribute('data-wiki') || '',
      key: b.getAttribute('data-key') || '',
      book: b.getAttribute('data-book') || '',
      title: b.getAttribute('data-title') || '',
      mode: b.getAttribute('data-mode') || ''
    };
    b.disabled = true;
    post(action, body, function (ok) {
      b.disabled = false;
      if (!ok) return;
      if (action === 'shutdown') {
        document.body.innerHTML = '<div style="text-align:center;padding:80px 16px;font-family:system-ui,sans-serif"><img src="/nabu/static/nabu.svg" width="72" height="72" alt=""><h1>NABU has stopped.</h1><p>It is now safe to close this tab and remove the drive.</p></div>';
        return;
      }
      if (action === 'check') {
        wasChecking = true;
        var c = document.querySelector('[data-checking]');
        if (c) c.hidden = false;
        setTimeout(poll, 800);
        return;
      }
      if (b.getAttribute('data-goto')) { location.href = b.getAttribute('data-goto'); return; }
      if (action === 'collection-refresh') { alert('Copies refreshed from the newest dumps on the drive.'); }
      if (action === 'unsave' || action === 'forget' || action === 'collection-remove') {
        var li = b.closest ? b.closest('li') : null;
        if (li) { li.parentNode.removeChild(li); return; }
      }
      if (action === 'download' || action === 'resume') window.scrollTo(0, 0);
      if (action === 'fulltext') { setTimeout(function () { location.reload(); }, 300); return; }
      location.reload();
    });
  });

  // New collection (Saved page).
  var cf = document.querySelector('[data-create-collection]');
  if (cf) cf.addEventListener('submit', function (ev) {
    ev.preventDefault();
    var name = cf.querySelector('input').value;
    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/nabu/api/collection-create');
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.setRequestHeader('X-Nabu', '1');
    xhr.onload = function () {
      var res = {};
      try { res = JSON.parse(xhr.responseText); } catch (e) {}
      if (xhr.status >= 400) { alert(res.error || 'Could not create the collection.'); return; }
      location.href = '/nabu/collections/' + encodeURIComponent(res.result.slug) + '/';
    };
    xhr.send(JSON.stringify({ name: name }));
  });

  // Settings switches: <input type=checkbox data-setting="pictures">.
  var switches = document.querySelectorAll('[data-setting]');
  for (var i = 0; i < switches.length; i++) {
    switches[i].addEventListener('change', function (ev) {
      var body = {};
      var v = ev.target.type === 'number' ? parseInt(ev.target.value, 10) : ev.target.checked;
      if (v !== v || v < 0) return; // not a number
      body[ev.target.getAttribute('data-setting')] = v;
      post('settings', body);
    });
  }
})();

// "Make a copy" page: live total, destination buttons, progress.
(function () {
  var form = document.querySelector('[data-copy-form]');
  if (!form) return;
  function human(n) {
    if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GB';
    if (n >= 1048576) return Math.round(n / 1048576) + ' MB';
    return Math.max(1, Math.round(n / 1024)) + ' KB';
  }
  function checked(name) { var e = form.querySelector('[name="' + name + '"]'); return e && e.checked; }
  function total() {
    var sum = 0, i, wikis = form.querySelectorAll('[name="wiki"]');
    for (i = 0; i < wikis.length; i++) {
      if (!wikis[i].checked) continue;
      sum += +wikis[i].getAttribute('data-size');
      var cells = form.querySelectorAll('[data-wiki="' + wikis[i].value + '"]');
      for (var c = 0; c < cells.length; c++) if (checked(cells[c].getAttribute('data-part'))) sum += +cells[c].getAttribute('data-size');
    }
    var more = form.querySelectorAll('[name="system"],[name="collections"],[name="personal"],[name="source"],[name="pictures"]');
    for (i = 0; i < more.length; i++) if (more[i].checked) sum += +(more[i].getAttribute('data-size') || 0);
    form.querySelector('[data-total]').textContent = human(sum);
  }
  form.addEventListener('change', total);
  total();
  var dests = form.querySelectorAll('[data-dest]');
  for (var i = 0; i < dests.length; i++) dests[i].addEventListener('click', function (ev) {
    form.querySelector('[name="to"]').value = ev.currentTarget.getAttribute('data-dest');
  });
  var box = document.getElementById('copybox');
  function poll() {
    var xhr = new XMLHttpRequest();
    xhr.open('GET', '/nabu/api/status');
    xhr.onload = function () {
      var st; try { st = JSON.parse(xhr.responseText); } catch (e) { return; }
      var c = st.copy;
      if (!c) return;
      box.hidden = false;
      var pct = c.total > 0 ? 100 * c.done / c.total : 0;
      var line = {copying: 'Copying… ', done: 'Copy finished ✓ ', failed: 'Copy stopped. ', cancelled: 'Copy stopped; start again to go on. '}[c.state] || '';
      line += human(c.done) + ' of ' + human(c.total) + ' (' + pct.toFixed(0) + '%) to ' + c.dest;
      if (c.state === 'copying' && c.eta > 0) line += ' · about ' + Math.ceil(c.eta / 6e10) + ' min left';
      box.querySelector('[data-copy-line]').textContent = line;
      box.querySelector('.progress > div').style.width = pct.toFixed(1) + '%';
      box.querySelector('[data-copy-file]').textContent = c.state === 'copying' ? c.file : (c.state === 'done' ? 'It is ready: eject the drive and give it away. It starts like this one.' : '');
      box.querySelector('[data-copy-error]').textContent = c.error || '';
      box.querySelector('[data-copy-cancel]').hidden = c.state !== 'copying';
      box.className = 'notice' + (c.state === 'failed' ? ' bad' : c.state === 'done' ? ' ok' : '');
      if (c.state === 'copying') setTimeout(poll, 1000);
    };
    xhr.send();
  }
  poll();
  form.addEventListener('submit', function (ev) {
    ev.preventDefault();
    var vals = function (n) { var r = [], e = form.querySelectorAll('[name="' + n + '"]'); for (var i = 0; i < e.length; i++) if (e[i].checked) r.push(e[i].value); return r; };
    var body = {
      to: form.querySelector('[name="to"]').value,
      copy: { wikis: vals('wiki'), systems: vals('system'), indexes: checked('indexes'), search: checked('search'),
        pictures: checked('pictures'), collections: checked('collections'), personal: checked('personal'), source: checked('source') }
    };
    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/nabu/api/copy-start');
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.setRequestHeader('X-Nabu', '1');
    xhr.onload = function () {
      if (xhr.status !== 200) { var m = xhr.responseText; try { m = JSON.parse(m).error || m; } catch (e) {} alert(m); return; }
      window.scrollTo(0, 0);
      poll();
    };
    xhr.send(JSON.stringify(body));
  });
})();
