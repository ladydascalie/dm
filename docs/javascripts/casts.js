// casts.js mounts an asciinema player on every element that carries data-cast.
//
// The staging tree mirrors the repo, so a page names a cast by the path the
// file has in the repo, "docs/casts/split.cast". This file resolves that path
// against the site root, which it derives from its own URL, so a page at any
// depth names the same file. The site root is what makes the same markdown
// work in the tree and on the published site.
//
// The cast is the source of truth for the colours it recorded. The theme here
// only sets the cells the recording left alone, and it follows the palette
// toggle, so a light page does not frame a dark terminal.
(function () {
  var self = document.currentScript || document.querySelector('script[src*="javascripts/casts.js"]');
  var root = new URL('../../', self.src);

  function theme() {
    return document.body.getAttribute('data-md-color-scheme') === 'slate' ? 'asciinema' : 'tango';
  }

  function options(el) {
    return {
      cols: Number(el.dataset.cols || 100),
      rows: Number(el.dataset.rows || 26),
      theme: theme(),
      fit: 'width',
      autoPlay: false,
      preload: true,
      poster: 'npt:0:01',
      controls: true,
    };
  }

  function castURL(el) {
    return new URL(el.dataset.cast, root).href;
  }

  function create(el) {
    return AsciinemaPlayer.create(castURL(el), el, options(el));
  }

  if (typeof AsciinemaPlayer === 'undefined') {
    document.querySelectorAll('[data-cast]').forEach(function (el) {
      var a = document.createElement('a');
      a.href = castURL(el);
      a.textContent = 'Watch ' + el.dataset.cast;
      el.replaceChildren(a);
    });
    return;
  }

  document.querySelectorAll('[data-cast]').forEach(function (el) {
    el.castPlayer = create(el);
  });

  document.addEventListener('change', function (ev) {
    if (!ev.target.matches('input[name="__palette"]')) return;
    document.querySelectorAll('[data-cast]').forEach(function (el) {
      el.castPlayer.dispose();
      el.castPlayer = create(el);
    });
  });
})();
