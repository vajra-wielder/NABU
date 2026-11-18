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
  if (box || preps.length || document.querySelector('[data-checking]')) poll();

  // Buttons: <button data-action="download" data-wiki="enwiki">.
  document.addEventListener('click', function (ev) {
    var b = ev.target.closest ? ev.target.closest('[data-action]') : null;
    if (!b) return;
    ev.preventDefault();
    var action = b.getAttribute('data-action');
    var confirmText = b.getAttribute('data-confirm');
    if (confirmText && !confirm(confirmText)) return;
    var body = {
      wiki: b.getAttribute('data-wiki') || '',
      key: b.getAttribute('data-key') || '',
      book: b.getAttribute('data-book') || ''
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
      if (action === 'download' || action === 'resume') window.scrollTo(0, 0);
      location.reload();
    });
  });

  // Settings switches: <input type=checkbox data-setting="pictures">.
  var switches = document.querySelectorAll('[data-setting]');
  for (var i = 0; i < switches.length; i++) {
    switches[i].addEventListener('change', function (ev) {
      var body = {};
      body[ev.target.getAttribute('data-setting')] = ev.target.checked;
      post('settings', body);
    });
  }
})();
