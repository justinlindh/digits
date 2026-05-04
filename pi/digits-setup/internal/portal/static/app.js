document.addEventListener('DOMContentLoaded', () => {
  const ssidSelect = document.getElementById('ssid');
  const ssidManual = document.getElementById('ssid-manual');
  const btnRefresh = document.getElementById('btn-refresh');
  const password = document.getElementById('password');
  const btnSubmit = document.getElementById('btn-submit');
  const statusEl = document.getElementById('status');
  const stepWifi = document.getElementById('step-wifi');
  const stepVerifying = document.getElementById('step-verifying');
  const stepDone = document.getElementById('step-done');
  const errorBanner = document.getElementById('error-banner');
  const errorBannerText = document.getElementById('error-banner-text');

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = 'status ' + (isError ? 'error' : 'success');
  }

  async function checkStatus() {
    try {
      const resp = await fetch('/api/status');
      if (!resp.ok) return;
      const data = await resp.json();
      if (data.verifying) {
        stepWifi.classList.remove('active');
        stepVerifying.classList.add('active');
        setTimeout(() => window.location.reload(), 35000);
        return;
      }
      if (data.last_attempt && data.last_attempt.error) {
        errorBannerText.textContent = data.last_attempt.error;
        errorBanner.classList.remove('hidden');
      }
    } catch (e) {
      // Status endpoint not available
    }
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

  btnSubmit.addEventListener('click', async () => {
    let ssid = ssidSelect.value;
    if (ssid === '__hidden__') {
      ssid = ssidManual.value.trim();
    }
    if (!ssid) {
      setStatus('Please select a Wi-Fi network', true);
      return;
    }

    btnSubmit.disabled = true;
    errorBanner.classList.add('hidden');
    setStatus('', false);

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

      if (resp.status === 202) {
        stepWifi.classList.remove('active');
        stepVerifying.classList.add('active');
        setTimeout(() => window.location.reload(), 35000);
        return;
      }

      if (!resp.ok) {
        const text = await resp.text();
        throw new Error(text || 'Configuration failed');
      }

      stepWifi.classList.remove('active');
      stepDone.classList.add('active');
    } catch (err) {
      setStatus(err.message, true);
      btnSubmit.disabled = false;
    }
  });

  checkStatus();
  scanNetworks();
});
