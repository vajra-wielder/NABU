// Article pages: save for later, link previews, "you are here" in the
// contents sidebar, printing. Plain ES5, and every feature checks that
// the browser supports what it needs.
(function () {
  'use strict';
  var body = document.body;
  var book = body.getAttribute('data-book'), title = body.getAttribute('data-title');

  function post(action, data, done) {
    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/nabu/api/' + action);
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.setRequestHeader('X-Nabu', '1');
    xhr.onload = function () { done(xhr.status < 400); };
    xhr.onerror = function () { done(false); };
    xhr.send(JSON.stringify(data));
  }

  // Save / unsave (kept on the drive).
  var saveBtns = document.querySelectorAll('[data-save]');
  function setSaved(on) {
    for (var i = 0; i < saveBtns.length; i++) {
      var b = saveBtns[i];
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      var label = b.querySelector('.label');
      if (label) label.textContent = on ? 'Saved' : 'Save';
      else b.textContent = on ? '★ Saved' : '☆ Save article';
    }
  }
  for (var i = 0; i < saveBtns.length; i++) {
    saveBtns[i].addEventListener('click', function () {
      var on = this.getAttribute('aria-pressed') !== 'true';
      setSaved(on);
      post('save', { book: book, title: title, saved: on }, function (ok) {
        if (!ok) { setSaved(!on); alert('Saving is only possible on the computer running NABU.'); }
      });
    });
  }

  var printBtn = document.querySelector('[data-print]');
  if (printBtn) printBtn.addEventListener('click', function () { window.print(); });

  // Contents sidebar: highlight the section being read, i.e. the last
  // heading above the top of the window (works however far one scrolls).
  var side = document.querySelector('.side-toc');
  if (side) {
    var links = {}, as = side.querySelectorAll('a'), current = null, pending = false;
    for (var j = 0; j < as.length; j++) {
      try { links[decodeURIComponent(as[j].getAttribute('href').slice(1))] = as[j]; } catch (e) { /* odd id */ }
    }
    var heads = document.querySelectorAll('.mw-body h2[id], .mw-body h3[id], .mw-body h4[id]');
    var spy = function () {
      pending = false;
      var here = null;
      var atEnd = window.innerHeight + window.pageYOffset >= document.documentElement.scrollHeight - 4;
      var line = atEnd ? window.innerHeight : 120; // at the very end, short last sections count
      for (var h = 0; h < heads.length; h++) {
        if (heads[h].getBoundingClientRect().top < line) here = heads[h];
        else break;
      }
      var a = here ? links[here.id] : null;
      if (a === current) return;
      if (current) current.className = '';
      current = a;
      if (!a) return;
      a.className = 'here';
      var box = side.parentNode;
      if (a.offsetTop < box.scrollTop + 40 || a.offsetTop > box.scrollTop + box.clientHeight - 60) {
        box.scrollTop = a.offsetTop - 80;
      }
    };
    window.addEventListener('scroll', function () {
      if (!pending) { pending = true; setTimeout(spy, 80); }
    });
    spy();
  }

  // Link previews: hover an article link for a moment to see how it begins.
  var article = document.querySelector('.mw-body');
  var canHover = !window.matchMedia || window.matchMedia('(hover: hover)').matches;
  if (!article || !canHover || !window.JSON) return;
  var prefix = '/' + book + '/wiki/';
  var card = null, timer = null, hideTimer = null, cache = {};

  function hide() {
    clearTimeout(timer);
    if (card) { card.parentNode.removeChild(card); card = null; }
  }
  function show(a, p) {
    hide();
    card = document.createElement('div');
    card.className = 'preview';
    card.setAttribute('role', 'tooltip');
    if (p.image) {
      var img = document.createElement('img');
      img.className = 'pv-img';
      img.alt = '';
      img.src = p.image;
      img.onerror = function () { this.parentNode && this.parentNode.removeChild(this); };
      card.appendChild(img);
    }
    var h = document.createElement('h3');
    h.textContent = p.title;
    h.setAttribute('dir', 'auto');
    var t = document.createElement('p');
    t.textContent = p.extract || 'This page has no introduction yet.';
    t.setAttribute('dir', 'auto');
    card.appendChild(h);
    card.appendChild(t);
    card.addEventListener('mouseenter', function () { clearTimeout(hideTimer); });
    card.addEventListener('mouseleave', function () { hideTimer = setTimeout(hide, 250); });
    document.body.appendChild(card);
    var r = a.getBoundingClientRect();
    var x = r.left + window.pageXOffset;
    var maxX = window.pageXOffset + document.documentElement.clientWidth - card.offsetWidth - 10;
    card.style.left = Math.max(10, Math.min(x, maxX)) + 'px';
    var below = r.bottom + window.pageYOffset + 8;
    if (r.bottom + card.offsetHeight + 16 > window.innerHeight) below = r.top + window.pageYOffset - card.offsetHeight - 8;
    card.style.top = below + 'px';
  }
  function linkTitle(a) {
    var href = a.getAttribute('href') || '';
    if (href.indexOf(prefix) !== 0 || a.closest('.side, .toc, sup.reference')) return null;
    href = href.slice(prefix.length).split('#')[0].split('?')[0];
    try { return decodeURIComponent(href).replace(/_/g, ' '); } catch (e) { return null; }
  }
  article.addEventListener('mouseover', function (ev) {
    var a = ev.target.closest ? ev.target.closest('a') : null;
    if (!a) return;
    var t = linkTitle(a);
    if (!t) return;
    clearTimeout(timer);
    clearTimeout(hideTimer);
    timer = setTimeout(function () {
      if (cache[t]) { show(a, cache[t]); return; }
      var xhr = new XMLHttpRequest();
      xhr.open('GET', '/nabu/api/preview?book=' + encodeURIComponent(book) + '&title=' + encodeURIComponent(t));
      xhr.onload = function () {
        if (xhr.status !== 200) return;
        try { cache[t] = JSON.parse(xhr.responseText); } catch (e) { return; }
        show(a, cache[t]);
      };
      xhr.send();
    }, 450);
  });
  article.addEventListener('mouseout', function (ev) {
    var a = ev.target.closest ? ev.target.closest('a') : null;
    if (!a) return;
    clearTimeout(timer);
    hideTimer = setTimeout(hide, 250);
  });
  window.addEventListener('scroll', function () { if (card) hide(); }, { passive: true });
})();
