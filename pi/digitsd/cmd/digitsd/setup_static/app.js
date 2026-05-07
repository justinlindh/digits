document.addEventListener('DOMContentLoaded', () => {
  const ssidSelect = document.getElementById('ssid');
  const ssidManual = document.getElementById('ssid-manual');
  const btnRefresh = document.getElementById('btn-refresh');
  const password = document.getElementById('password');
  const btnSubmit = document.getElementById('btn-submit');
  const statusEl = document.getElementById('status');
  const stepWifi = document.getElementById('step-wifi');
  const stepDone = document.getElementById('step-done');
  const stepFail = document.getElementById('step-fail');
  const failMsg = document.getElementById('fail-msg');
  const btnRetry = document.getElementById('btn-retry');

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = 'status ' + (isError ? 'error' : 'info');
  }

  async function scanNetworks() {
    ssidSelect.innerHTML = '<option value="">Scanning...</option>';
    ssidSelect.disabled = true;
    btnRefresh.disabled = true;
    try {
      const resp = await fetch('/api/networks');
      if (!resp.ok) throw new Error('Scan failed');
      const networks = await resp.json();
      ssidSelect.innerHTML = '';
      if (networks.length === 0) {
        ssidSelect.innerHTML = '<option value="">No networks found</option>';
      } else {
        networks.sort((a, b) => b.signal - a.signal);
        for (const net of networks) {
          const opt = document.createElement('option');
          opt.value = net.ssid;
          opt.textContent = net.ssid + ' (' + net.signal + ' dBm)';
          ssidSelect.appendChild(opt);
        }
      }
      const hiddenOpt = document.createElement('option');
      hiddenOpt.value = '__hidden__';
      hiddenOpt.textContent = 'Hidden network...';
      ssidSelect.appendChild(hiddenOpt);
    } catch (err) {
      ssidSelect.innerHTML = '<option value="">Scan failed</option>';
    } finally {
      ssidSelect.disabled = false;
      btnRefresh.disabled = false;
    }
  }

  btnRefresh.addEventListener('click', scanNetworks);

  ssidSelect.addEventListener('change', () => {
    if (ssidSelect.value === '__hidden__') {
      ssidManual.classList.remove('hidden');
      ssidManual.focus();
    } else {
      ssidManual.classList.add('hidden');
      ssidManual.value = '';
    }
  });

  function showStep(step) {
    stepWifi.classList.remove('active');
    stepDone.classList.remove('active');
    stepFail.classList.remove('active');
    step.classList.add('active');
  }

  function pollStatus() {
    fetch('/api/status').then(function(r) { return r.json(); }).then(function(d) {
      if (d.verifying) {
        setTimeout(pollStatus, 2000);
        return;
      }
      if (d.last_attempt && d.last_attempt.Connected && !d.last_attempt.Error) {
        showStep(stepDone);
      } else if (d.last_attempt && d.last_attempt.Error) {
        failMsg.textContent = d.last_attempt.Error;
        showStep(stepFail);
      } else {
        setTimeout(pollStatus, 2000);
      }
    }).catch(function() {
      // AP might be down during verification, keep polling
      setTimeout(pollStatus, 3000);
    });
  }

  btnSubmit.addEventListener('click', async () => {
    var ssid = ssidSelect.value;
    if (ssid === '__hidden__') {
      ssid = ssidManual.value.trim();
    }
    if (!ssid) {
      setStatus('Please select a Wi-Fi network', true);
      return;
    }

    btnSubmit.disabled = true;
    setStatus('Verifying connection... you will lose connection briefly. If this page does not return, setup succeeded.', false);

    try {
      const resp = await fetch('/api/configure', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ssid: ssid,
          password: password.value,
          hidden: ssidSelect.value === '__hidden__',
        }),
      });

      if (!resp.ok) {
        const text = await resp.text();
        throw new Error(text || 'Configuration failed');
      }

      // Start polling for verification result
      setTimeout(pollStatus, 2000);
    } catch (err) {
      setStatus(err.message, true);
      btnSubmit.disabled = false;
    }
  });

  if (btnRetry) {
    btnRetry.addEventListener('click', function() {
      showStep(stepWifi);
      btnSubmit.disabled = false;
      statusEl.textContent = '';
      statusEl.className = 'status';
      password.value = '';
      scanNetworks();
    });
  }

  // Log viewer (collapsed by default)
  var logEl = document.getElementById('log');
  var logVisible = false;
  var lastLog = '';

  window.toggleLog = function() {
    logVisible = !logVisible;
    logEl.classList.toggle('collapsed', !logVisible);
    document.getElementById('log-toggle').textContent = logVisible ? 'Hide' : 'Show';
  };

  function pollLog() {
    if (!logVisible) { setTimeout(pollLog, 2000); return; }
    fetch('/log/raw').then(function(r) { return r.text(); }).then(function(t) {
      if (t !== lastLog) {
        lastLog = t;
        var sel = window.getSelection();
        if (!sel || sel.rangeCount === 0 || !logEl.contains(sel.anchorNode)) {
          var atBottom = logEl.scrollTop + logEl.clientHeight >= logEl.scrollHeight - 30;
          logEl.textContent = t || '(empty)';
          if (atBottom) logEl.scrollTop = logEl.scrollHeight;
        }
      }
    }).catch(function() {}).finally(function() {
      setTimeout(pollLog, 2000);
    });
  }
  pollLog();

  scanNetworks();
});
