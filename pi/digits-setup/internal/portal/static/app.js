document.addEventListener('DOMContentLoaded', () => {
  const ssidSelect = document.getElementById('ssid');
  const ssidManual = document.getElementById('ssid-manual');
  const btnRefresh = document.getElementById('btn-refresh');
  const password = document.getElementById('password');
  const btnSubmit = document.getElementById('btn-submit');
  const statusEl = document.getElementById('status');
  const stepWifi = document.getElementById('step-wifi');
  const stepDone = document.getElementById('step-done');

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = 'status ' + (isError ? 'error' : 'success');
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
          opt.textContent = `${net.ssid} (${net.signal} dBm)`;
          ssidSelect.appendChild(opt);
        }
      }
      // Always add hidden network option at the end
      const hiddenOpt = document.createElement('option');
      hiddenOpt.value = '__hidden__';
      hiddenOpt.textContent = 'Hidden network...';
      ssidSelect.appendChild(hiddenOpt);
    } catch (err) {
      ssidSelect.innerHTML = '<option value="">Scan failed — tap ↻</option>';
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
    setStatus('Configuring...', false);

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

      // Success — show done screen
      stepWifi.classList.remove('active');
      stepDone.classList.add('active');
    } catch (err) {
      setStatus(err.message, true);
      btnSubmit.disabled = false;
    }
  });

  // Initial scan
  scanNetworks();
});
