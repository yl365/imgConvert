/* imgConvert site — interactions */
(function () {
  'use strict';

  /* ---------- Sticky header state ---------- */
  var header = document.getElementById('siteHeader');
  if (header) {
    var onScroll = function () {
      header.classList.toggle('is-stuck', window.scrollY > 12);
    };
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
  }

  /* ---------- Scroll reveal ---------- */
  var items = document.querySelectorAll('.reveal');
  if (!('IntersectionObserver' in window)) {
    Array.prototype.forEach.call(items, function (el) { el.classList.add('in'); });
  } else {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('in');
          io.unobserve(entry.target);
        }
      });
    }, { rootMargin: '0px 0px -12% 0px', threshold: 0.12 });

    Array.prototype.forEach.call(items, function (el) { io.observe(el); });
  }

  /* ---------- Copy to clipboard ---------- */
  function legacyCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-1000px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    return ok;
  }

  function copy(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).then(function () { return true; },
        function () { return legacyCopy(text); });
    }
    return Promise.resolve(legacyCopy(text));
  }

  Array.prototype.forEach.call(document.querySelectorAll('.copy-btn'), function (btn) {
    var label = btn.textContent;
    var timer = null;

    btn.addEventListener('click', function () {
      var value = btn.getAttribute('data-copy') || '';
      copy(value).then(function () {
        btn.textContent = '已复制 ✓';
        btn.classList.add('is-copied');
        clearTimeout(timer);
        timer = setTimeout(function () {
          btn.textContent = label;
          btn.classList.remove('is-copied');
        }, 1800);
      });
    });
  });
})();
