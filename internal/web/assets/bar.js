// Search-as-you-type for NABU. Used both in the bar on article pages and
// on NABU's own pages. Plain ES5 so it runs in old browsers too.
(function () {
  'use strict';

  function debounce(fn, ms) {
    var t;
    return function () {
      var args = arguments, self = this;
      clearTimeout(t);
      t = setTimeout(function () { fn.apply(self, args); }, ms);
    };
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text) e.textContent = text;
    return e;
  }

  function attach(form) {
    var input = form.querySelector('input[type=search]');
    if (!input || form.getAttribute('data-nabu-ready')) return;
    form.setAttribute('data-nabu-ready', '1');
    var bookField = form.querySelector('[name=book]');
    var list = el('ul', form.closest('#nabu-bar') ? 'nabu-suggest' : 'suggest');
    list.hidden = true;
    list.setAttribute('role', 'listbox');
    form.appendChild(list);
    var active = -1, lastQ = '', seq = 0;

    function items() { return list.querySelectorAll('a'); }
    function setActive(i) {
      var a = items();
      for (var k = 0; k < a.length; k++) a[k].className = k === i ? 'active' : '';
      active = i;
    }
    function hide() { list.hidden = true; active = -1; }

    var fetchSuggestions = debounce(function () {
      var q = input.value.trim();
      if (q === lastQ) return;
      lastQ = q;
      if (!q) { hide(); return; }
      var my = ++seq;
      var book = bookField ? bookField.value : '';
      var xhr = new XMLHttpRequest();
      xhr.open('GET', '/suggest?book=' + encodeURIComponent(book) + '&q=' + encodeURIComponent(q));
      xhr.onload = function () {
        if (my !== seq || xhr.status !== 200) return;
        var res;
        try { res = JSON.parse(xhr.responseText); } catch (e) { return; }
        list.innerHTML = '';
        for (var i = 0; i < res.length; i++) {
          var li = el('li'), a = el('a', '', res[i].title);
          a.href = res[i].url;
          a.setAttribute('role', 'option');
          if (res[i].via) a.appendChild(el('small', '', '← ' + res[i].via));
          li.appendChild(a);
          list.appendChild(li);
        }
        var more = el('li'), m = el('a', '', 'Search for “' + q + '” …');
        m.href = '/search?list=1&book=' + encodeURIComponent(book) + '&q=' + encodeURIComponent(q);
        m.setAttribute('dir', 'auto');
        more.appendChild(m);
        list.appendChild(more);
        active = -1;
        list.hidden = false;
      };
      xhr.send();
    }, 90);

    input.addEventListener('input', fetchSuggestions);
    input.addEventListener('focus', function () { if (list.children.length && input.value.trim()) list.hidden = false; });
    input.addEventListener('keydown', function (ev) {
      var a = items();
      if (list.hidden || !a.length) return;
      if (ev.key === 'ArrowDown') { ev.preventDefault(); setActive((active + 1) % a.length); }
      else if (ev.key === 'ArrowUp') { ev.preventDefault(); setActive(active <= 0 ? a.length - 1 : active - 1); }
      else if (ev.key === 'Enter' && active >= 0) { ev.preventDefault(); window.location.href = a[active].href; }
      else if (ev.key === 'Escape') { hide(); }
    });
    document.addEventListener('click', function (ev) { if (!form.contains(ev.target)) hide(); });
  }

  function init() {
    var forms = document.querySelectorAll('form.nabu-search, .searchbox form');
    for (var i = 0; i < forms.length; i++) attach(forms[i]);
    // Press "/" anywhere to jump to search, as on Wikipedia.
    document.addEventListener('keydown', function (ev) {
      if (ev.key !== '/' || ev.ctrlKey || ev.metaKey || ev.altKey) return;
      var t = ev.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
      var q = document.querySelector('.nabu-q, .searchbox input[type=search]');
      if (q) { ev.preventDefault(); q.focus(); q.select(); }
    });
  }

  // Pictures come from Wikimedia when online. When one cannot load
  // (offline, blocked), show its labelled frame instead.
  document.addEventListener('error', function (ev) {
    var t = ev.target;
    if (t && t.tagName === 'IMG' && t.parentNode && t.parentNode.className === 'pic') {
      t.parentNode.className = 'pic broken';
    }
  }, true);

  if (!Element.prototype.closest) {
    Element.prototype.closest = function (s) {
      var e = this;
      while (e && e.nodeType === 1) { if (e.matches ? e.matches(s) : e.msMatchesSelector(s)) return e; e = e.parentNode; }
      return null;
    };
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
