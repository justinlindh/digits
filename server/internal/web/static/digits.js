// digits.js: global, CSP-safe UI behaviors wired via event delegation.
// Loaded from every layout <head> with defer so it runs once the DOM has
// parsed. Everything here is delegated off document, so it also covers markup
// that htmx swaps in after the initial render.
(function () {
  'use strict';

  // Stateless click delegations in one handler: generic <dialog>
  // open/close/backdrop dismiss, banner dismiss, and the intercom rail /
  // answering-machine drawer nav toggles. Elements opt in via
  // data-dialog-open="<id>", data-dialog-close, data-backdrop-close, or
  // data-dismiss.
  document.addEventListener('click', function (e) {
    var opener = e.target.closest('[data-dialog-open]');
    if (opener) {
      var dlg = document.getElementById(opener.getAttribute('data-dialog-open'));
      if (dlg && typeof dlg.showModal === 'function') dlg.showModal();
      return;
    }
    var closer = e.target.closest('[data-dialog-close]');
    if (closer) {
      var owner = closer.closest('dialog');
      if (owner) owner.close();
      return;
    }
    if (e.target.matches && e.target.matches('dialog[data-backdrop-close]')) {
      e.target.close();
      return;
    }
    var dismiss = e.target.closest('[data-dismiss]');
    if (dismiss && dismiss.parentElement) {
      dismiss.parentElement.remove();
      return;
    }
    if (e.target.closest('.rail__toggle')) {
      var nav = document.querySelector('.rail__nav');
      if (nav) nav.classList.toggle('is-open');
      return;
    }
    var amToggle = e.target.closest('#am-nav-toggle');
    if (amToggle) {
      var drawer = document.getElementById('am-nav-drawer');
      if (drawer) {
        var open = drawer.classList.toggle('is-open');
        amToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      }
    }
  });

  // Open any dialog flagged to auto-open on load (server-side form errors that
  // want the modal back up so the user can fix and resubmit).
  document.querySelectorAll('dialog[data-open-on-load]').forEach(function (d) {
    if (typeof d.showModal === 'function') d.showModal();
  });

  // Auto-submit a form when a control inside it changes. Used by toggle
  // switches and radio groups. requestSubmit fires a submit event, so htmx
  // forms are intercepted and plain forms navigate as normal.
  document.addEventListener('change', function (e) {
    var el = e.target.closest('[data-autosubmit]');
    if (el && el.form) el.form.requestSubmit();
  });

  // Native confirm guard: a form carrying data-confirm-message asks before it
  // submits. Capture phase so we can cancel before htmx or the browser act.
  document.addEventListener('submit', function (e) {
    var form = e.target;
    var msg = form.getAttribute && form.getAttribute('data-confirm-message');
    if (msg && !window.confirm(msg)) e.preventDefault();
  }, true);

  // Themed confirm dialog: [data-confirm] triggers populate and open the
  // shared #confirm-dialog, which then POSTs to data-confirm-action.
  (function () {
    var dialog = document.getElementById('confirm-dialog');
    if (!dialog || typeof dialog.showModal !== 'function') return;
    var form = document.getElementById('confirm-dialog__form');
    var title = document.getElementById('confirm-dialog__title');
    var body = document.getElementById('confirm-dialog__body');
    var submit = document.getElementById('confirm-dialog__submit');
    document.addEventListener('click', function (e) {
      var trigger = e.target.closest('[data-confirm]');
      if (!trigger) return;
      e.preventDefault();
      form.action = trigger.dataset.confirmAction || '';
      title.textContent = trigger.dataset.confirmTitle || 'Are you sure?';
      body.textContent = trigger.dataset.confirmBody || '';
      submit.textContent = trigger.dataset.confirmSubmit || 'Confirm';
      dialog.showModal();
    });
  })();

  // Changelog: the footer "What's new" button opens the dialog and lazy-loads
  // its content; tabs switch panels; each release row plays its audio notes.
  var activeAudio = null;
  var activeBtn = null;
  function stopChangelogAudio() {
    if (activeAudio) activeAudio.pause();
    if (activeBtn) activeBtn.innerHTML = '&#9654; Listen';
    activeAudio = null;
    activeBtn = null;
  }
  document.addEventListener('click', function (e) {
    var opener = e.target.closest('[data-changelog-open]');
    if (opener) {
      var d = document.getElementById('changelog-dialog');
      if (d && typeof d.showModal === 'function') {
        d.showModal();
        if (!d.dataset.loaded && window.htmx) {
          window.htmx.ajax('GET', '/changelog', '#changelog-body');
          d.dataset.loaded = '1';
        }
      }
      return;
    }
    var tab = e.target.closest('.changelog-tab');
    if (tab) {
      var target = tab.getAttribute('data-tab');
      document.querySelectorAll('.changelog-tab').forEach(function (t) {
        t.classList.remove('changelog-tab--active');
        t.setAttribute('aria-selected', 'false');
      });
      document.querySelectorAll('.changelog-panel').forEach(function (p) {
        p.classList.remove('changelog-panel--active');
      });
      tab.classList.add('changelog-tab--active');
      tab.setAttribute('aria-selected', 'true');
      var panel = document.querySelector('[data-panel="' + target + '"]');
      if (panel) panel.classList.add('changelog-panel--active');
      return;
    }
    var playBtn = e.target.closest('.changelog__play');
    if (playBtn) {
      var wasActive = activeBtn === playBtn;
      stopChangelogAudio();
      if (wasActive) return;
      var audio = new Audio(playBtn.dataset.audioUrl);
      audio.addEventListener('ended', stopChangelogAudio);
      audio.play().catch(stopChangelogAudio);
      playBtn.innerHTML = '&#9724; Stop';
      activeAudio = audio;
      activeBtn = playBtn;
    }
  });

  // Dialup welcome chime. /connecting redirects here with ?welcome=1 after the
  // modem intro; the earlier Connect click unlocked audio for this origin, so
  // autoplay succeeds. Strip the flag from the URL either way.
  (function () {
    var params = new URLSearchParams(window.location.search);
    if (params.get('welcome') !== '1') return;
    params.delete('welcome');
    var q = params.toString();
    var clean = window.location.pathname + (q ? '?' + q : '') + window.location.hash;
    window.history.replaceState({}, '', clean);
    var audio = document.getElementById('dialup-welcome');
    if (audio) audio.play().catch(function () {});
  })();
})();
