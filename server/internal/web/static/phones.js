// phones.js: the handset pairing form. Drives the DTMF keypad, the pairing-code
// slots, live line-number availability checks, and the new-vs-existing line
// mode switch. Loaded with defer from the phones page (intercom and
// answering-machine variants share the same element IDs).
(function () {
  var pinEl = document.getElementById('pair-pin');
  if (!pinEl) return;
  var slots = pinEl.querySelectorAll('.pin__slot');
  var hidden = document.getElementById('pair-code-hidden');
  var manual = document.getElementById('pair-code-manual');
  var numberInput = document.getElementById('line-number-input');
  var numberHint = document.getElementById('number-hint');
  var nameInput = document.getElementById('line-name-input');
  var submitBtn = document.getElementById('pair-submit');
  var buttons = document.querySelectorAll('.keypad__btn');
  var code = '';
  var checkTimer = null;

  var tones = {};
  function playTone(d) {
    var a = tones[d];
    if (!a) {
      a = new Audio('/static/audio/dtmf/dtmf_' + d + '.wav');
      a.volume = 0.5;
      tones[d] = a;
    }
    a.currentTime = 0;
    a.play().catch(function () {});
  }

  function render() {
    slots.forEach(function (s, i) {
      s.textContent = code[i] || '';
      s.classList.toggle('is-filled', !!code[i]);
      s.classList.toggle('is-active', i === code.length);
    });
    hidden.value = code;
    if (document.activeElement !== manual) manual.value = code;
    updateSubmit();
  }

  function push(d) {
    if (code.length >= 6) return;
    code = code + d;
    render();
  }
  function pop() {
    if (!code.length) return;
    code = code.slice(0, -1);
    render();
  }

  function press(btn, ms) {
    btn.classList.add('is-pressed');
    setTimeout(function () { btn.classList.remove('is-pressed'); }, ms || 90);
  }
  buttons.forEach(function (b) {
    b.addEventListener('click', function () {
      press(b);
      if (b.dataset.digit) { playTone(b.dataset.digit); push(b.dataset.digit); }
      else if (b.dataset.action === 'backspace') pop();
      else if (b.dataset.action === 'clear') { code = ''; render(); }
    });
  });

  manual.addEventListener('input', function () {
    var digits = this.value.replace(/\D/g, '').slice(0, 6);
    code = digits;
    render();
    this.value = digits;
  });

  document.addEventListener('keydown', function (e) {
    if (document.activeElement === numberInput || document.activeElement === nameInput) return;
    if (e.key === 'Backspace') { e.preventDefault(); pop(); }
    else if (/^\d$/.test(e.key)) { playTone(e.key); push(e.key); }
  });

  function setNumberError() {
    numberInput.setCustomValidity('Line number is already in use');
    numberInput.classList.add('is-invalid');
    numberHint.classList.remove('hidden');
  }
  function clearNumberError() {
    numberInput.setCustomValidity('');
    numberInput.classList.remove('is-invalid');
    numberHint.classList.add('hidden');
  }
  var pairModeHidden = document.getElementById('pair-mode-hidden');
  var newLineFields = document.getElementById('new-line-fields');
  var existingLineFields = document.getElementById('existing-line-fields');
  var existingLineSelect = document.getElementById('existing-line-select');
  var pairMode = 'new';

  function setPairMode(mode) {
    pairMode = mode;
    pairModeHidden.value = mode;
    if (mode === 'existing') {
      newLineFields.classList.add('hidden');
      existingLineFields.classList.remove('hidden');
    } else {
      newLineFields.classList.remove('hidden');
      existingLineFields.classList.add('hidden');
    }
    updateSubmit();
  }

  document.querySelectorAll('input[name="pair_mode_radio"]').forEach(function (radio) {
    radio.addEventListener('change', function () {
      if (this.checked) setPairMode(this.value);
    });
  });

  function updateSubmit() {
    var hasName = nameInput.value.trim().length > 0;
    if (pairMode === 'existing') {
      var hasLine = existingLineSelect && existingLineSelect.value !== '';
      submitBtn.disabled = code.length !== 6 || !hasLine || !hasName;
    } else {
      var numDigits = numberInput.value.replace(/\D/g, '');
      submitBtn.disabled = code.length !== 6 || numDigits.length !== 7 || !hasName || numberInput.validity.customError;
    }
  }

  if (existingLineSelect) {
    existingLineSelect.addEventListener('change', updateSubmit);
  }
  function checkNumber() {
    var digits = numberInput.value.replace(/\D/g, '');
    if (digits.length !== 7) { clearNumberError(); updateSubmit(); return; }
    fetch('/api/lines/number-available?number=' + digits, { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (numberInput.value.replace(/\D/g, '') !== digits) return;
        if (data.available) clearNumberError(); else setNumberError();
        updateSubmit();
      })
      .catch(function () { clearNumberError(); updateSubmit(); });
  }
  numberInput.addEventListener('input', function () {
    var digits = this.value.replace(/\D/g, '').slice(0, 7);
    this.value = digits.length > 3 ? digits.slice(0, 3) + '-' + digits.slice(3) : digits;
    clearNumberError();
    updateSubmit();
    clearTimeout(checkTimer);
    if (digits.length === 7) checkTimer = setTimeout(checkNumber, 300);
  });
  nameInput.addEventListener('input', updateSubmit);

  // On submit, make sure the real 'code' field is present.
  document.getElementById('pair-form').addEventListener('submit', function () {
    hidden.name = 'code';
    manual.name = ''; // don't submit the manual display field
  });

  render();
})();
