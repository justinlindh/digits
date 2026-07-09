// settings.js: section scrollspy for the settings page. Highlights the
// .settings-nav item whose target section sits under a focus line about 25%
// down the viewport, and smooth-scrolls on nav clicks. Works for both the
// intercom/answering-machine window-scroll layout and the dialup .dialup-main
// inner scroller. Loaded with defer from the settings page.
(function () {
  var nav = document.querySelector('.settings-nav');
  if (!nav) return;
  var links = nav.querySelectorAll('.settings-nav__item');
  if (!links.length) return;

  var byId = {};
  var sections = [];
  links.forEach(function (link) {
    var hash = link.getAttribute('href') || '';
    if (hash.charAt(0) !== '#') return;
    var id = hash.slice(1);
    var sec = document.getElementById(id);
    if (!sec) return;
    byId[id] = link;
    sections.push(sec);
  });
  if (!sections.length) return;

  function setActive(id) {
    links.forEach(function (l) { l.classList.remove('is-active'); });
    if (byId[id]) byId[id].classList.add('is-active');
  }

  // Resolve the real scroll container. For either dialup variant
  // (CRT-all or non-CRT), the innermost scroll container is
  // .dialup-main -- both have overflow: auto set on it, and the
  // window / bezel above just frames it without scrolling. Intercom
  // falls through to window-level scrolling.
  var body = document.body;
  var scroller = body.classList.contains('dialup')
    ? document.querySelector('.dialup-main')
    : null;
  function scrollMetrics() {
    if (scroller) {
      return {
        scrollTop: scroller.scrollTop,
        viewHeight: scroller.clientHeight,
        pageHeight: scroller.scrollHeight,
      };
    }
    var doc = document.documentElement;
    return {
      scrollTop: window.scrollY || doc.scrollTop,
      viewHeight: window.innerHeight || doc.clientHeight,
      pageHeight: Math.max(doc.scrollHeight, document.body.scrollHeight),
    };
  }

  function sectionTopRelative(sec) {
    // Position of section top within the scroller's coordinate system.
    if (scroller) {
      return sec.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop;
    }
    return sec.getBoundingClientRect().top + (window.scrollY || document.documentElement.scrollTop);
  }

  function update() {
    var m = scrollMetrics();
    var viewTop = m.scrollTop;
    var viewBottom = viewTop + m.viewHeight;

    // Focus line slides with scroll progress so every section can
    // reach it AND each section gets a wide-enough active window.
    // Starting lower (0.10) at the top keeps the first section
    // highlighted until the user has scrolled well past it; ending
    // higher (0.90) at the bottom lets the last section activate
    // before we run out of scroll room on a short final section.
    var maxScroll = Math.max(0, m.pageHeight - m.viewHeight);
    var progress = maxScroll > 0 ? Math.min(1, viewTop / maxScroll) : 0;
    var factor = 0.10 + 0.80 * progress;
    var focusLine = viewTop + m.viewHeight * factor;

    var chosen = sections[0];
    for (var i = 0; i < sections.length; i++) {
      if (sectionTopRelative(sections[i]) <= focusLine) chosen = sections[i];
      else break;
    }

    // Safety net: if the chosen section is entirely outside the
    // viewport (e.g., because the final section is smaller than
    // what remains of the page between it and the focus line), fall
    // back to whichever section has the most visible pixels. Keeps
    // the highlight on something the user can actually see.
    var chosenTop = sectionTopRelative(chosen);
    var chosenBottom = chosenTop + chosen.offsetHeight;
    if (chosenBottom <= viewTop || chosenTop >= viewBottom) {
      var bestSec = chosen;
      var bestVisible = -1;
      for (var j = 0; j < sections.length; j++) {
        var top = sectionTopRelative(sections[j]);
        var bottom = top + sections[j].offsetHeight;
        var visible = Math.max(0, Math.min(bottom, viewBottom) - Math.max(top, viewTop));
        if (visible > bestVisible) { bestVisible = visible; bestSec = sections[j]; }
      }
      chosen = bestSec;
    }

    setActive(chosen.id);
  }

  // When the user clicks a nav item we want the clicked section to
  // stay highlighted across the smooth-scroll. Without this, the
  // scroll animation triggers scroll events -> spy re-runs -> the
  // section currently under the focus line (typically the NEXT one)
  // wins, so the highlight jumps away from what the user clicked.
  // spyLockedUntil suspends the spy until the smooth-scroll settles.
  var spyLockedUntil = 0;

  // rAF-throttle so rapid scroll events don't thrash the DOM.
  var scheduled = false;
  function onScroll() {
    if (scheduled) return;
    scheduled = true;
    window.requestAnimationFrame(function () {
      scheduled = false;
      if (Date.now() < spyLockedUntil) return;
      update();
    });
  }

  var scrollTarget = scroller || window;
  scrollTarget.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('resize', onScroll);

  // Run once now and again after the page has settled. Section top
  // positions depend on final layout (font loading, any late-loaded
  // chrome in the window frame), so the very first call can run
  // against preliminary geometry -- belt-and-suspenders so the
  // highlight lands on the right item on first paint.
  update();
  window.addEventListener('load', update);
  if (document.fonts && document.fonts.ready && typeof document.fonts.ready.then === 'function') {
    document.fonts.ready.then(update);
  }

  links.forEach(function (link) {
    link.addEventListener('click', function (e) {
      // Let modifier-clicks (new tab, new window) keep native behavior.
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
      var hash = link.getAttribute('href') || '';
      if (hash.charAt(0) !== '#') return;
      var id = hash.slice(1);
      var target = document.getElementById(id);
      if (!target) return;

      e.preventDefault();
      setActive(id);
      // Lock spy across the smooth-scroll so the click-chosen item
      // doesn't get overridden by the scroll events the animation
      // generates. Primary unlock signal is the scrollend event
      // (Chrome/Firefox/Edge); the 800ms timeout is a fallback for
      // older browsers and a ceiling if the animation stalls.
      spyLockedUntil = Date.now() + 800;
      scrollTarget.addEventListener('scrollend', function () {
        spyLockedUntil = 0;
      }, { once: true });

      var top = sectionTopRelative(target);
      if (scroller) {
        scroller.scrollTo({ top: top, behavior: 'smooth' });
      } else {
        window.scrollTo({ top: top, behavior: 'smooth' });
      }

      // Update the URL hash without triggering a second browser jump.
      if (history.replaceState) history.replaceState(null, '', hash);
    });
  });
})();
