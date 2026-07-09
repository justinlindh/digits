// phone-detail.js: the phone detail page. Covers the shared chrome (online
// status badge, line-editor mode switch) plus the two operator panels. Both
// the intercom and answering-machine operator panels ship on this page under
// different element IDs, so each panel's wiring self-guards on the elements it
// owns and only one is ever present per render. Loaded with defer.
//
// The line number is read from [data-phone-number] on the page wrapper, which
// survives htmx swaps of #operator-panel. Buttons declare their action via
// data-op (and data-prefix / data-param / data-mode) and are handled by a
// single delegated click listener, so swapped-in operator markup works too.
(function () {
  'use strict';

  function phoneNumber() {
    var el = document.querySelector('[data-phone-number]');
    return el ? el.getAttribute('data-phone-number') : '';
  }

  function selectedHardwareID() {
    var p = document.getElementById('operator-panel');
    return p ? (p.getAttribute('data-hardware-id') || '') : '';
  }

  function postForm(path, params) {
    return fetch('/phones/' + phoneNumber() + path, {
      method: 'POST',
      headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
      body: params.toString(),
    });
  }

  // --- Shared chrome: online status badge -------------------------------
  // Reload the page when the handset flips between online and offline so the
  // whole detail view re-renders against the new state.
  (function () {
    var badge = document.getElementById('status-badge');
    if (!badge) return;
    badge.addEventListener('htmx:afterSwap', function (evt) {
      var marker = evt.target.querySelector('[data-online]');
      if (!marker) return;
      var newOnline = marker.getAttribute('data-online') === 'true';
      var prev = badge.dataset.prevOnline === 'true';
      if (badge.dataset.prevOnline !== undefined && newOnline !== prev) { location.reload(); return; }
      badge.dataset.prevOnline = newOnline;
    });
    var span = badge.querySelector('[data-online]');
    if (span) badge.dataset.prevOnline = span.getAttribute('data-online');
  })();

  // --- Shared chrome: line-editor mode switch ---------------------------
  document.addEventListener('change', function (e) {
    var radio = e.target.closest('[data-line-edit-mode]');
    if (!radio) return;
    var mode = radio.getAttribute('data-line-edit-mode');
    var numberEl = document.getElementById('line-edit-number');
    var moveEl = document.getElementById('line-edit-move');
    if (numberEl) numberEl.classList.toggle('hidden', mode !== 'number');
    if (moveEl) moveEl.classList.toggle('hidden', mode !== 'move');
  });

  // --- Intercom operator panel ------------------------------------------
  (function () {
    if (!document.getElementById('ring-test-btn')) return;

    var piPollTimer = null;
    var fwPollTimer = null;
    var restartPollTimer = null;
    var devModePollTimer = null;
    var restartMode = null;

    function doRingTest() {
      var btn = document.getElementById('ring-test-btn');
      var status = document.getElementById('ring-test-status');
      var spinner = document.getElementById('ring-test-spinner');
      var text = document.getElementById('ring-test-text');
      btn.disabled = true;
      status.classList.remove('hidden');
      spinner.classList.remove('hidden');
      text.textContent = 'Ringing...';
      text.style.color = 'var(--ink)';
      var body = new URLSearchParams();
      body.append('hardware_id', selectedHardwareID());
      postForm('/ring-test', body)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          spinner.classList.add('hidden');
          if (data.error) {
            text.textContent = 'Failed: ' + data.error;
            text.style.color = 'var(--rust)';
            btn.disabled = false;
          } else {
            text.textContent = 'Done';
            text.style.color = 'var(--forest)';
            setTimeout(function () { status.classList.add('hidden'); btn.disabled = false; }, 2000);
          }
        })
        .catch(function (err) {
          spinner.classList.add('hidden');
          text.textContent = 'Error: ' + err.message;
          text.style.color = 'var(--rust)';
          btn.disabled = false;
        });
    }

    function triggerComponentUpdate(prefix, paramName) {
      var btn = document.getElementById(prefix + '-install-btn');
      var radio = document.querySelector('input[name="' + paramName + '"]:checked');
      var body = new URLSearchParams();
      if (radio) body.append(paramName, radio.value);
      body.append('hardware_id', selectedHardwareID());
      startUpdate(prefix, btn, body);
    }

    function startUpdate(prefix, btn, body) {
      var progress = document.getElementById(prefix + '-update-progress');
      var progressText = document.getElementById(prefix + '-progress-text');
      if (btn) { btn.disabled = true; btn.classList.add('is-disabled'); }
      if (progress) progress.classList.remove('hidden');
      if (progressText) progressText.textContent = 'Sending update command...';

      postForm('/update', body)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (data.error) { showResult(prefix, 'failed', 'Failed to send: ' + data.error); return; }
          if (progressText) progressText.textContent = 'Update triggered: waiting for device...';
          startPolling(prefix);
        })
        .catch(function (err) { showResult(prefix, 'failed', 'Network error: ' + err.message); });
    }

    function startPolling(prefix) {
      var timerVar = prefix === 'pi' ? 'piPollTimer' : 'fwPollTimer';
      if (window[timerVar]) clearInterval(window[timerVar]);
      var attempts = 0;
      window[timerVar] = setInterval(function () {
        attempts++;
        if (attempts > 120) { clearInterval(window[timerVar]); showResult(prefix, 'failed', 'Timed out; please retry'); return; }
        fetch('/phones/' + phoneNumber() + '/update-status?hardware_id=' + encodeURIComponent(selectedHardwareID()))
          .then(function (r) { return r.json(); })
          .then(function (data) {
            if (!data.status) return;
            var progressText = document.getElementById(prefix + '-progress-text');
            switch (data.status) {
              case 'downloading': if (progressText) progressText.textContent = 'Downloading update... ' + (data.detail || ''); break;
              case 'applying': if (progressText) progressText.textContent = 'Applying update... ' + (data.detail || ''); break;
              case 'rebooting': if (progressText) progressText.textContent = 'Rebooting device...'; break;
              case 'success': clearInterval(window[timerVar]); showResult(prefix, 'success', data.detail || 'Update installed successfully'); setTimeout(function () { location.reload(); }, 2000); break;
              case 'up_to_date': clearInterval(window[timerVar]); showResult(prefix, 'success', data.detail || 'Already up to date'); break;
              case 'failed': clearInterval(window[timerVar]); showResult(prefix, 'failed', data.detail || 'Update failed'); break;
            }
          }).catch(function () {});
      }, 1000);
    }

    function showResult(prefix, type, message) {
      var progress = document.getElementById(prefix + '-update-progress');
      var spinner = document.getElementById(prefix + '-progress-spinner');
      var progressText = document.getElementById(prefix + '-progress-text');
      var btn = document.getElementById(prefix + '-install-btn');
      if (spinner) spinner.classList.add('hidden');
      if (progress) progress.classList.remove('hidden');
      if (progressText) {
        progressText.textContent = message;
        progressText.style.color = (type === 'success') ? 'var(--forest)' : 'var(--rust)';
      }
      if (type !== 'success' && btn) { btn.disabled = false; }
    }

    function showRestartConfirm(mode) {
      restartMode = mode;
      document.getElementById('restart-buttons').classList.add('hidden');
      var c = document.getElementById('restart-confirm');
      c.classList.remove('hidden');
      var text = document.getElementById('restart-confirm-text');
      var btn = document.getElementById('restart-confirm-btn');
      if (mode === 'reboot') {
        text.textContent = 'Reboot the entire device? This will take about 30 seconds.';
        btn.className = 'btn btn--danger';
      } else {
        text.textContent = 'Restart the service on this device? It will reconnect in a few seconds.';
        btn.className = 'btn btn--primary';
      }
    }
    function cancelRestartConfirm() {
      restartMode = null;
      document.getElementById('restart-confirm').classList.add('hidden');
      document.getElementById('restart-buttons').classList.remove('hidden');
    }
    function doRestart() {
      var mode = restartMode;
      document.getElementById('restart-confirm').classList.add('hidden');
      document.getElementById('restart-progress').classList.remove('hidden');
      var status = document.getElementById('restart-status');
      status.textContent = mode === 'reboot' ? 'Sending reboot command...' : 'Sending restart command...';
      status.style.color = 'var(--ink)';
      document.getElementById('restart-spinner').classList.remove('hidden');
      var body = new URLSearchParams({ mode: mode });
      body.append('hardware_id', selectedHardwareID());
      postForm('/restart', body)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (data.error) { showRestartResult('failed', 'Failed: ' + data.error); return; }
          var maxAttempts = mode === 'reboot' ? 90 : 15;
          status.textContent = mode === 'reboot' ? 'Rebooting device...' : 'Restarting service...';
          pollOnline(maxAttempts);
        })
        .catch(function (err) { showRestartResult('failed', 'Network error: ' + err.message); });
    }
    function pollOnline(maxAttempts) {
      if (restartPollTimer) clearInterval(restartPollTimer);
      var attempts = 0;
      var sawOffline = false;
      setTimeout(function () {
        restartPollTimer = setInterval(function () {
          attempts++;
          if (attempts > maxAttempts) { clearInterval(restartPollTimer); showRestartResult('failed', 'Timed out waiting for device to come back online'); return; }
          fetch('/phones/' + phoneNumber() + '/online?hardware_id=' + encodeURIComponent(selectedHardwareID()))
            .then(function (r) { return r.json(); })
            .then(function (data) {
              if (!data.online) { sawOffline = true; document.getElementById('restart-status').textContent = 'Waiting for device to reconnect...'; }
              else if (sawOffline) { clearInterval(restartPollTimer); showRestartResult('success', 'Device is back online'); setTimeout(function () { location.reload(); }, 1500); }
            }).catch(function () {});
        }, 1000);
      }, 2000);
    }
    function showRestartResult(t, msg) {
      document.getElementById('restart-spinner').classList.add('hidden');
      var status = document.getElementById('restart-status');
      status.textContent = msg;
      status.style.color = (t === 'success') ? 'var(--forest)' : 'var(--rust)';
    }

    function showFactoryResetConfirm() {
      document.getElementById('factory-reset-default').classList.add('hidden');
      document.getElementById('factory-reset-confirm').classList.remove('hidden');
    }
    function cancelFactoryReset() {
      document.getElementById('factory-reset-confirm').classList.add('hidden');
      document.getElementById('factory-reset-default').classList.remove('hidden');
    }
    function doFactoryReset() {
      document.getElementById('factory-reset-confirm').classList.add('hidden');
      document.getElementById('factory-reset-progress').classList.remove('hidden');
      var frBody = new URLSearchParams();
      frBody.append('hardware_id', selectedHardwareID());
      postForm('/factory-reset', frBody)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          var spinner = document.getElementById('factory-reset-spinner');
          var text = document.getElementById('factory-reset-text');
          spinner.classList.add('hidden');
          if (data.error) { text.textContent = 'Failed: ' + data.error; text.style.color = 'var(--rust)'; }
          else { text.textContent = 'Device is rebooting into recovery mode. Connect to the Digits-Recovery Wi-Fi network to continue.'; text.style.color = 'var(--ochre)'; }
        })
        .catch(function (err) {
          var spinner = document.getElementById('factory-reset-spinner');
          var text = document.getElementById('factory-reset-text');
          spinner.classList.add('hidden');
          text.textContent = 'Error: ' + err.message;
          text.style.color = 'var(--rust)';
        });
    }

    function showDevModeEnableForm() {
      document.getElementById('devmode-off-default').classList.add('hidden');
      document.getElementById('devmode-enable-form').classList.remove('hidden');
      document.getElementById('devmode-password').focus();
    }
    function cancelDevModeEnable() {
      document.getElementById('devmode-enable-form').classList.add('hidden');
      document.getElementById('devmode-off-default').classList.remove('hidden');
      clearDevModePwError();
    }
    function clearDevModePwError() {
      document.getElementById('devmode-pw-error').classList.add('hidden');
    }
    function showDevModeDisableConfirm() {
      document.getElementById('devmode-on-default').classList.add('hidden');
      document.getElementById('devmode-disable-confirm').classList.remove('hidden');
    }
    function cancelDevModeDisable() {
      document.getElementById('devmode-disable-confirm').classList.add('hidden');
      document.getElementById('devmode-on-default').classList.remove('hidden');
    }
    function devModeProgress(msg) {
      var p = document.getElementById('devmode-progress');
      if (p) p.classList.remove('hidden');
      var t = document.getElementById('devmode-text');
      if (t) { t.textContent = msg; t.style.color = 'var(--ink)'; }
    }
    function devModeError(msg) {
      var spinner = document.getElementById('devmode-spinner');
      if (spinner) spinner.classList.add('hidden');
      var t = document.getElementById('devmode-text');
      if (t) { t.textContent = msg; t.style.color = 'var(--rust)'; }
    }
    function doDevModeEnable() {
      var pw = document.getElementById('devmode-password').value;
      if (pw.length < 8 || pw.length > 72) {
        document.getElementById('devmode-pw-error').classList.remove('hidden');
        document.getElementById('devmode-password').focus();
        return;
      }
      document.getElementById('devmode-enable-form').classList.add('hidden');
      devModeProgress('Enabling developer mode...');
      sendDevMode(new URLSearchParams({ enabled: 'true', password: pw }), true);
    }
    function doDevModeDisable() {
      document.getElementById('devmode-disable-confirm').classList.add('hidden');
      devModeProgress('Disabling developer mode...');
      sendDevMode(new URLSearchParams({ enabled: 'false' }), false);
    }
    function sendDevMode(body, target) {
      body.append('hardware_id', selectedHardwareID());
      postForm('/dev-mode', body)
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, data: d }; }); })
        .then(function (res) {
          if (!res.ok) { devModeError('Failed: ' + (res.data.error || 'request rejected')); return; }
          devModeProgress(target ? 'Waiting for device to enable SSH...' : 'Waiting for device to disable SSH...');
          pollDevMode(target);
        })
        .catch(function (err) { devModeError('Network error: ' + err.message); });
    }
    function pollDevMode(target) {
      if (devModePollTimer) clearInterval(devModePollTimer);
      var attempts = 0;
      devModePollTimer = setInterval(function () {
        attempts++;
        if (attempts > 15) { clearInterval(devModePollTimer); devModeError('Timed out waiting for the device to apply the change.'); return; }
        fetch('/phones/' + phoneNumber() + '/dev-mode-status?hardware_id=' + encodeURIComponent(selectedHardwareID()))
          .then(function (r) { return r.json(); })
          .then(function (d) {
            if (d.enabled === target) {
              clearInterval(devModePollTimer);
              devModeProgress('Done. Reloading...');
              setTimeout(function () { location.reload(); }, 1000);
            }
          }).catch(function () {});
      }, 2000);
    }

    var pwInput = document.getElementById('devmode-password');
    if (pwInput) pwInput.addEventListener('input', clearDevModePwError);

    document.addEventListener('click', function (e) {
      var el = e.target.closest('[data-op]');
      if (!el) return;
      switch (el.getAttribute('data-op')) {
        case 'ring-test': doRingTest(); break;
        case 'update': triggerComponentUpdate(el.getAttribute('data-prefix'), el.getAttribute('data-param')); break;
        case 'restart-confirm': showRestartConfirm(el.getAttribute('data-mode')); break;
        case 'restart-do': doRestart(); break;
        case 'restart-cancel': cancelRestartConfirm(); break;
        case 'factory-show': showFactoryResetConfirm(); break;
        case 'factory-do': doFactoryReset(); break;
        case 'factory-cancel': cancelFactoryReset(); break;
        case 'devmode-enable-show': showDevModeEnableForm(); break;
        case 'devmode-enable-do': doDevModeEnable(); break;
        case 'devmode-enable-cancel': cancelDevModeEnable(); break;
        case 'devmode-disable-show': showDevModeDisableConfirm(); break;
        case 'devmode-disable-do': doDevModeDisable(); break;
        case 'devmode-disable-cancel': cancelDevModeDisable(); break;
      }
    });

    window.addEventListener('pagehide', function () {
      if (piPollTimer) clearInterval(piPollTimer);
      if (fwPollTimer) clearInterval(fwPollTimer);
      if (restartPollTimer) clearInterval(restartPollTimer);
      if (devModePollTimer) clearInterval(devModePollTimer);
    });
  })();

  // --- Answering-machine operator panel ---------------------------------
  (function () {
    if (!document.getElementById('am-ring-test-btn')) return;

    var pollers = { pi: null, fw: null };
    var amRestartMode = null;
    var amRestartPollTimer = null;
    var amDevModePollTimer = null;

    function amTriggerComponentUpdate(prefix, paramName) {
      var btn = document.getElementById(prefix + '-install-btn');
      var radio = document.querySelector('input[name="' + paramName + '"]:checked');
      var body = new URLSearchParams();
      if (radio) body.append(paramName, radio.value);
      body.append('hardware_id', selectedHardwareID());
      amStartUpdate(prefix, btn, body);
    }

    function amStartUpdate(prefix, btn, body) {
      var progress = document.getElementById(prefix + '-update-progress');
      var lamp = document.getElementById(prefix + '-progress-lamp');
      var text = document.getElementById(prefix + '-progress-text');
      if (btn) btn.disabled = true;
      if (progress) progress.classList.remove('hidden');
      if (lamp) { lamp.classList.remove('am-lamp--on-green', 'am-lamp--on-red'); lamp.classList.add('am-lamp--on-amber'); }
      if (text) { text.classList.remove('am-led--green', 'am-led--red', 'am-led--dim'); text.textContent = 'Sending update command'; }

      postForm('/update', body)
        .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
        .then(function (data) {
          if (data.error) { amShowResult(prefix, 'failed', 'Failed: ' + data.error); return; }
          if (text) text.textContent = 'Triggered, awaiting handset';
          amStartPolling(prefix);
        })
        .catch(function (err) { amShowResult(prefix, 'failed', 'Network error: ' + err.message); });
    }

    function amStartPolling(prefix) {
      if (pollers[prefix]) clearInterval(pollers[prefix]);
      var attempts = 0;
      pollers[prefix] = setInterval(function () {
        attempts++;
        if (attempts > 120) { clearInterval(pollers[prefix]); amShowResult(prefix, 'failed', 'Timed out, please retry'); return; }
        fetch('/phones/' + phoneNumber() + '/update-status?hardware_id=' + encodeURIComponent(selectedHardwareID()))
          .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
          .then(function (data) {
            if (!data.status) return;
            var text = document.getElementById(prefix + '-progress-text');
            switch (data.status) {
              case 'downloading': if (text) text.textContent = ('Downloading ' + (data.detail || '')).trim(); break;
              case 'applying': if (text) text.textContent = ('Applying ' + (data.detail || '')).trim(); break;
              case 'rebooting': if (text) text.textContent = 'Rebooting handset'; break;
              case 'success': clearInterval(pollers[prefix]); amShowResult(prefix, 'success', data.detail || 'Update installed'); setTimeout(function () { location.reload(); }, 2000); break;
              case 'up_to_date': clearInterval(pollers[prefix]); amShowResult(prefix, 'success', data.detail || 'Already up to date'); break;
              case 'failed': clearInterval(pollers[prefix]); amShowResult(prefix, 'failed', data.detail || 'Update failed'); break;
            }
          }).catch(function () {});
      }, 1000);
    }

    function amShowResult(prefix, kind, message) {
      var lamp = document.getElementById(prefix + '-progress-lamp');
      var text = document.getElementById(prefix + '-progress-text');
      var btn = document.getElementById(prefix + '-install-btn');
      if (lamp) {
        lamp.classList.remove('am-lamp--on-amber', 'am-lamp--on-green', 'am-lamp--on-red');
        lamp.classList.add(kind === 'success' ? 'am-lamp--on-green' : 'am-lamp--on-red');
      }
      if (text) {
        text.classList.remove('am-led--green', 'am-led--red');
        text.classList.add(kind === 'success' ? 'am-led--green' : 'am-led--red');
        text.textContent = message;
      }
      if (kind !== 'success' && btn) btn.disabled = false;
    }

    function amDoRingTest() {
      var btn = document.getElementById('am-ring-test-btn');
      var status = document.getElementById('am-ring-test-status');
      var lamp = document.getElementById('am-ring-test-lamp');
      var text = document.getElementById('am-ring-test-text');
      btn.disabled = true;
      status.classList.remove('hidden');
      lamp.classList.remove('am-lamp--on-green', 'am-lamp--on-red');
      lamp.classList.add('am-lamp--on-amber');
      text.classList.remove('am-led--green', 'am-led--red');
      text.textContent = 'Ringing...';
      var rtBody = new URLSearchParams();
      rtBody.append('hardware_id', selectedHardwareID());
      postForm('/ring-test', rtBody)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (data.error) {
            lamp.classList.remove('am-lamp--on-amber');
            lamp.classList.add('am-lamp--on-red');
            text.classList.add('am-led--red');
            text.textContent = 'Failed: ' + data.error;
            btn.disabled = false;
          } else {
            lamp.classList.remove('am-lamp--on-amber');
            lamp.classList.add('am-lamp--on-green');
            text.classList.add('am-led--green');
            text.textContent = 'Done';
            setTimeout(function () { status.classList.add('hidden'); btn.disabled = false; }, 2000);
          }
        })
        .catch(function (err) {
          lamp.classList.remove('am-lamp--on-amber');
          lamp.classList.add('am-lamp--on-red');
          text.classList.add('am-led--red');
          text.textContent = 'Error: ' + err.message;
          btn.disabled = false;
        });
    }

    function amShowRestartConfirm(mode) {
      amRestartMode = mode;
      document.getElementById('am-ctrl-buttons').classList.add('hidden');
      var c = document.getElementById('am-restart-confirm');
      c.classList.remove('hidden');
      var text = document.getElementById('am-restart-confirm-text');
      if (mode === 'reboot') {
        text.textContent = 'Reboot the entire device? This will take about 30 seconds.';
      } else {
        text.textContent = 'Restart the service on this device? It will reconnect in a few seconds.';
      }
    }
    function amCancelRestartConfirm() {
      amRestartMode = null;
      document.getElementById('am-restart-confirm').classList.add('hidden');
      document.getElementById('am-ctrl-buttons').classList.remove('hidden');
    }
    function amDoRestart() {
      var mode = amRestartMode;
      document.getElementById('am-restart-confirm').classList.add('hidden');
      var progress = document.getElementById('am-restart-progress');
      var lamp = document.getElementById('am-restart-lamp');
      var text = document.getElementById('am-restart-text');
      progress.classList.remove('hidden');
      lamp.classList.remove('am-lamp--on-green', 'am-lamp--on-red');
      lamp.classList.add('am-lamp--on-amber');
      text.classList.remove('am-led--green', 'am-led--red');
      text.textContent = mode === 'reboot' ? 'Sending reboot command...' : 'Sending restart command...';
      var body = new URLSearchParams({ mode: mode });
      body.append('hardware_id', selectedHardwareID());
      postForm('/restart', body)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (data.error) { amShowRestartResult('failed', 'Failed: ' + data.error); return; }
          var maxAttempts = mode === 'reboot' ? 90 : 15;
          text.textContent = mode === 'reboot' ? 'Rebooting device...' : 'Restarting service...';
          amPollOnline(maxAttempts);
        })
        .catch(function (err) { amShowRestartResult('failed', 'Network error: ' + err.message); });
    }
    function amPollOnline(maxAttempts) {
      if (amRestartPollTimer) clearInterval(amRestartPollTimer);
      var attempts = 0;
      var sawOffline = false;
      setTimeout(function () {
        amRestartPollTimer = setInterval(function () {
          attempts++;
          if (attempts > maxAttempts) { clearInterval(amRestartPollTimer); amShowRestartResult('failed', 'Timed out waiting for device to come back online'); return; }
          fetch('/phones/' + phoneNumber() + '/online?hardware_id=' + encodeURIComponent(selectedHardwareID()))
            .then(function (r) { return r.json(); })
            .then(function (data) {
              if (!data.online) { sawOffline = true; document.getElementById('am-restart-text').textContent = 'Waiting for device to reconnect...'; }
              else if (sawOffline) { clearInterval(amRestartPollTimer); amShowRestartResult('success', 'Device is back online'); setTimeout(function () { location.reload(); }, 1500); }
            }).catch(function () {});
        }, 1000);
      }, 2000);
    }
    function amShowRestartResult(kind, msg) {
      var lamp = document.getElementById('am-restart-lamp');
      var text = document.getElementById('am-restart-text');
      lamp.classList.remove('am-lamp--on-amber', 'am-lamp--on-green', 'am-lamp--on-red');
      lamp.classList.add(kind === 'success' ? 'am-lamp--on-green' : 'am-lamp--on-red');
      text.classList.remove('am-led--green', 'am-led--red');
      text.classList.add(kind === 'success' ? 'am-led--green' : 'am-led--red');
      text.textContent = msg;
    }

    function amShowRecoveryConfirm() {
      document.getElementById('am-recovery-default').classList.add('hidden');
      document.getElementById('am-recovery-confirm').classList.remove('hidden');
    }
    function amCancelRecovery() {
      document.getElementById('am-recovery-confirm').classList.add('hidden');
      document.getElementById('am-recovery-default').classList.remove('hidden');
    }
    function amDoRecovery() {
      document.getElementById('am-recovery-confirm').classList.add('hidden');
      var progress = document.getElementById('am-recovery-progress');
      var lamp = document.getElementById('am-recovery-lamp');
      var text = document.getElementById('am-recovery-text');
      progress.classList.remove('hidden');
      var frBody = new URLSearchParams();
      frBody.append('hardware_id', selectedHardwareID());
      postForm('/factory-reset', frBody)
        .then(function (r) { return r.json(); })
        .then(function (data) {
          lamp.classList.remove('am-lamp--on-amber');
          if (data.error) {
            lamp.classList.add('am-lamp--on-red');
            text.classList.add('am-led--red');
            text.textContent = 'Failed: ' + data.error;
          } else {
            lamp.classList.add('am-lamp--on-amber');
            text.textContent = 'Device is rebooting into recovery mode. Connect to the Digits-Recovery Wi-Fi network to continue.';
          }
        })
        .catch(function (err) {
          lamp.classList.remove('am-lamp--on-amber');
          lamp.classList.add('am-lamp--on-red');
          text.classList.add('am-led--red');
          text.textContent = 'Error: ' + err.message;
        });
    }

    function amShowDevModeEnable() {
      document.getElementById('am-devmode-off-default').classList.add('hidden');
      document.getElementById('am-devmode-enable-form').classList.remove('hidden');
      document.getElementById('am-devmode-password').focus();
    }
    function amCancelDevModeEnable() {
      document.getElementById('am-devmode-enable-form').classList.add('hidden');
      document.getElementById('am-devmode-off-default').classList.remove('hidden');
      amClearDevModePwError();
    }
    function amClearDevModePwError() {
      document.getElementById('am-devmode-pw-error').classList.add('hidden');
    }
    function amShowDevModeDisable() {
      document.getElementById('am-devmode-on-default').classList.add('hidden');
      document.getElementById('am-devmode-disable-confirm').classList.remove('hidden');
    }
    function amCancelDevModeDisable() {
      document.getElementById('am-devmode-disable-confirm').classList.add('hidden');
      document.getElementById('am-devmode-on-default').classList.remove('hidden');
    }
    function amDevModeProgress(msg) {
      document.getElementById('am-devmode-progress').classList.remove('hidden');
      var lamp = document.getElementById('am-devmode-lamp');
      lamp.classList.remove('am-lamp--on-red');
      lamp.classList.add('am-lamp--on-amber');
      var t = document.getElementById('am-devmode-text');
      t.classList.remove('am-led--red');
      t.textContent = msg;
    }
    function amDevModeError(msg) {
      var lamp = document.getElementById('am-devmode-lamp');
      lamp.classList.remove('am-lamp--on-amber');
      lamp.classList.add('am-lamp--on-red');
      var t = document.getElementById('am-devmode-text');
      t.classList.add('am-led--red');
      t.textContent = msg;
    }
    function amDoDevModeEnable() {
      var pw = document.getElementById('am-devmode-password').value;
      if (pw.length < 8 || pw.length > 72) {
        document.getElementById('am-devmode-pw-error').classList.remove('hidden');
        document.getElementById('am-devmode-password').focus();
        return;
      }
      document.getElementById('am-devmode-enable-form').classList.add('hidden');
      amDevModeProgress('Enabling developer mode...');
      amSendDevMode(new URLSearchParams({ enabled: 'true', password: pw }), true);
    }
    function amDoDevModeDisable() {
      document.getElementById('am-devmode-disable-confirm').classList.add('hidden');
      amDevModeProgress('Disabling developer mode...');
      amSendDevMode(new URLSearchParams({ enabled: 'false' }), false);
    }
    function amSendDevMode(body, target) {
      body.append('hardware_id', selectedHardwareID());
      postForm('/dev-mode', body)
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, data: d }; }); })
        .then(function (res) {
          if (!res.ok) { amDevModeError('Failed: ' + (res.data.error || 'request rejected')); return; }
          amDevModeProgress(target ? 'Waiting for device to enable SSH...' : 'Waiting for device to disable SSH...');
          amPollDevMode(target);
        })
        .catch(function (err) { amDevModeError('Network error: ' + err.message); });
    }
    function amPollDevMode(target) {
      if (amDevModePollTimer) clearInterval(amDevModePollTimer);
      var attempts = 0;
      amDevModePollTimer = setInterval(function () {
        attempts++;
        if (attempts > 15) { clearInterval(amDevModePollTimer); amDevModeError('Timed out waiting for the device to apply the change.'); return; }
        fetch('/phones/' + phoneNumber() + '/dev-mode-status?hardware_id=' + encodeURIComponent(selectedHardwareID()))
          .then(function (r) { return r.json(); })
          .then(function (d) {
            if (d.enabled === target) {
              clearInterval(amDevModePollTimer);
              amDevModeProgress('Done. Reloading...');
              setTimeout(function () { location.reload(); }, 1000);
            }
          }).catch(function () {});
      }, 2000);
    }

    var pwInput = document.getElementById('am-devmode-password');
    if (pwInput) pwInput.addEventListener('input', amClearDevModePwError);

    document.addEventListener('click', function (e) {
      var el = e.target.closest('[data-op]');
      if (!el) return;
      switch (el.getAttribute('data-op')) {
        case 'am-ring-test': amDoRingTest(); break;
        case 'am-update': amTriggerComponentUpdate(el.getAttribute('data-prefix'), el.getAttribute('data-param')); break;
        case 'am-restart-confirm': amShowRestartConfirm(el.getAttribute('data-mode')); break;
        case 'am-restart-do': amDoRestart(); break;
        case 'am-restart-cancel': amCancelRestartConfirm(); break;
        case 'am-recovery-show': amShowRecoveryConfirm(); break;
        case 'am-recovery-do': amDoRecovery(); break;
        case 'am-recovery-cancel': amCancelRecovery(); break;
        case 'am-devmode-enable-show': amShowDevModeEnable(); break;
        case 'am-devmode-enable-do': amDoDevModeEnable(); break;
        case 'am-devmode-enable-cancel': amCancelDevModeEnable(); break;
        case 'am-devmode-disable-show': amShowDevModeDisable(); break;
        case 'am-devmode-disable-do': amDoDevModeDisable(); break;
        case 'am-devmode-disable-cancel': amCancelDevModeDisable(); break;
      }
    });

    window.addEventListener('pagehide', function () {
      if (amRestartPollTimer) clearInterval(amRestartPollTimer);
      if (amDevModePollTimer) clearInterval(amDevModePollTimer);
      Object.keys(pollers).forEach(function (p) { if (pollers[p]) clearInterval(pollers[p]); });
    });
  })();
})();
