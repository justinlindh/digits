// voicemail-form.js
//
// Mirrors the enabled-checkbox state into the field group's disabled +
// opacity attributes so the form gives instant feedback when the user
// flips voicemail on or off, without waiting for the htmx round-trip.
//
// Bound at the document level via event delegation so a single load on
// phone-detail.html covers both the intercom and answering-machine
// partials, and survives the htmx outerHTML swap on Save (which would
// orphan a per-form listener bound at partial-render time).
(function () {
  document.addEventListener('change', function (e) {
    var box = e.target;
    if (!box || !box.matches || !box.matches('[data-voicemail-enabled]')) {
      return;
    }
    var form = box.closest('[data-voicemail-form]');
    if (!form) {
      return;
    }
    var fieldsWrap = form.querySelector('[data-voicemail-fields]');
    if (!fieldsWrap) {
      return;
    }
    var on = box.checked;
    fieldsWrap.style.opacity = on ? '' : '0.55';
    var inputs = fieldsWrap.querySelectorAll('input');
    for (var i = 0; i < inputs.length; i++) {
      inputs[i].disabled = !on;
    }
  });
})();
