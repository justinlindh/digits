(function () {
  'use strict';

  // --- DOM -----------------------------------------------------------------
  const body = document.body;
  const dialer = document.getElementById('dialer');
  const connectBtn = document.getElementById('connect-btn');
  const skipBtn = document.getElementById('skip-btn');
  const statusText = document.getElementById('status-text');
  const spinner = document.getElementById('spinner');
  const progress = document.getElementById('progress');
  const logEl = document.getElementById('log');
  const hintEl = document.getElementById('hint');
  const sbDot = document.getElementById('sb-dot');
  const sbConn = document.getElementById('sb-conn');
  const sbBps = document.getElementById('sb-bps');
  const audioEl = document.getElementById('dialup-audio');

  const ledNames = ['pwr', 'rdy', 'txd', 'rxd', 'cd', 'oh'];
  const leds = {};
  ledNames.forEach(function (n) {
    leds[n] = document.querySelector('[data-led="' + n + '"]');
  });

  const PROG_CELLS = 24;
  for (let i = 0; i < PROG_CELLS; i++) {
    const c = document.createElement('div');
    c.className = 'dialer__progress-cell';
    progress.appendChild(c);
  }
  const progCells = progress.children;

  // --- Config --------------------------------------------------------------
  const HOUSEHOLD = body.dataset.household || '';
  const PHONE = '555-6390';
  // Redirect backstop: the dialup.m4a is ~15.5s; 18s ensures redirect even
  // if the audio 'ended' event never fires (muted autoplay, decode error).
  const BACKSTOP_MS = 18000;
  const prefersReduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // --- Helpers -------------------------------------------------------------
  // appendLog writes to innerHTML so log templates can carry <span> tags
  // for colour. Values that originate from user input (e.g. household name)
  // must be escaped before interpolation to prevent stored XSS.
  function escapeHTML(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
  function setStatus(html) { statusText.innerHTML = html; }
  function setProgressPct(pct) {
    const n = Math.round((pct / 100) * PROG_CELLS);
    for (let i = 0; i < PROG_CELLS; i++) {
      progCells[i].classList.toggle('is-filled', i < n);
    }
  }
  function setLed(name, on, color) {
    const el = leds[name];
    if (!el) return;
    el.classList.remove('is-on', 'is-on--grn');
    if (on) el.classList.add(color === 'grn' ? 'is-on--grn' : 'is-on');
  }
  function flashLed(name) {
    setLed(name, true);
    setTimeout(function () { setLed(name, false); }, 110);
  }

  let logLines = [];
  let placeholderCleared = false;
  function clearPlaceholder() {
    if (placeholderCleared) return;
    placeholderCleared = true;
    const ph = document.getElementById('log-placeholder');
    if (ph) ph.remove();
  }
  function appendLog(html) {
    clearPlaceholder();
    const li = document.createElement('li');
    li.innerHTML = html + ' <span class="caret"></span>';
    const prev = logEl.querySelector('li .caret');
    if (prev) prev.remove();
    logEl.appendChild(li);
    requestAnimationFrame(function () { li.classList.add('is-shown'); });
    logLines.push(li);
    while (logLines.length > 6) {
      const gone = logLines.shift();
      gone.remove();
    }
  }

  let redirected = false;
  function goToDashboard() {
    if (redirected) return;
    redirected = true;
    try { audioEl.pause(); } catch (e) { /* ignore */ }
    // ?welcome=1 signals the dashboard to play the welcome chime. The Connect
    // click unlocked audio for this origin, so autoplay there should succeed.
    window.location.assign('/?welcome=1');
  }

  // --- Reduced-motion fast path --------------------------------------------
  function runReduced() {
    dialer.dataset.state = 'connecting';
    connectBtn.disabled = true;
    setLed('pwr', true, 'grn');
    setLed('rdy', true, 'grn');
    setLed('cd', true, 'grn');
    setStatus('Connecting&hellip;');
    appendLog('<span class="tag">[pppd]</span> Fast-connect (reduced motion)');
    setProgressPct(100);
    sbDot.classList.add('on');
    sbConn.innerHTML = 'Connection: <b>Direct</b>';
    sbBps.textContent = '56 000 bps';
    // Play audio if possible (user gesture IS present), but don't wait for it.
    const p = audioEl.play();
    if (p && typeof p.catch === 'function') p.catch(function () { /* ignore */ });
    setTimeout(goToDashboard, 400);
  }

  // --- Full sequence -------------------------------------------------------
  // A scripted schedule of events, each expressed as an offset in ms from
  // the moment audio playback begins. Timings are tuned to feel "about right"
  // against the recording; exact sync isn't necessary or desired.
  function runSequence() {
    dialer.dataset.state = 'connecting';
    connectBtn.disabled = true;
    hintEl.textContent = 'Holding the line. Please do not pick up the phone.';

    const start = audioEl.play();
    if (start && typeof start.catch === 'function') {
      start.catch(function () {
        // Audio blocked despite the gesture (rare on mobile Safari with low
        // volume). Run the sequence anyway against wall-clock time.
      });
    }

    const t0 = performance.now();
    function at(ms, fn) {
      setTimeout(fn, Math.max(0, ms - (performance.now() - t0)));
    }

    // Phase 1: modem init (0 - 0.8s)
    at(0, function () {
      setLed('pwr', true, 'grn');
      setStatus('Initializing modem&hellip;');
      appendLog('<span class="tag">[modem]</span> ATZ - OK');
      setProgressPct(3);
    });
    at(400, function () {
      setLed('rdy', true, 'grn');
      appendLog('<span class="tag">[modem]</span> AT&amp;F1 - OK');
      setProgressPct(8);
    });

    // Phase 2: dial tone + dialing (0.8 - 3s)
    at(800, function () {
      setLed('oh', true);
      setStatus('Picking up line&hellip;');
      appendLog('<span class="tag">[modem]</span> Off-hook &middot; detecting dial tone');
      setProgressPct(14);
    });
    at(1400, function () {
      setStatus('Dialing <b>' + PHONE + '</b>&hellip;');
      appendLog('<span class="tag">[dial]</span> ATDT' + PHONE.replace(/-/g, '') + '&hellip;');
      flashLed('txd');
      setProgressPct(24);
    });

    // Phase 3: ringing + answer tone (3 - 6s)
    at(3000, function () {
      setStatus('Ringing&hellip;');
      appendLog('<span class="tag">[line]</span> RING');
      setProgressPct(36);
    });
    at(4500, function () {
      appendLog('<span class="tag">[v.25]</span> Answer tone (CED) - 2100 Hz');
      setLed('cd', true, 'grn');
      setProgressPct(48);
    });

    // Phase 4: handshake + training (6 - 12s)
    at(6000, function () {
      appendLog('<span class="tag">[v.8bis]</span> Capability exchange');
      flashLed('txd'); flashLed('rxd');
      setProgressPct(58);
    });
    at(7500, function () {
      appendLog('<span class="tag">[probe]</span> Line probing - L1/L2 tones');
      setProgressPct(66);
    });
    at(9000, function () {
      appendLog('<span class="tag">[train]</span> Equalizer training - <b>28.8 kbps</b>');
      flashLed('rxd');
      setProgressPct(74);
    });
    at(10500, function () {
      appendLog('<span class="tag">[train]</span> V.34 probe - <b>33.6 kbps</b>');
      flashLed('txd');
      setProgressPct(80);
    });
    at(11500, function () {
      appendLog('<span class="tag">[v.90]</span> Digital Impairment Learning');
      setProgressPct(86);
    });

    // Phase 5: carrier lock + PPP (12 - 15s)
    at(13000, function () {
      appendLog('<span class="tag">[v.90]</span> Carrier locked - <b>56.0 kbps</b>');
      setProgressPct(93);
    });
    at(14000, function () {
      appendLog('<span class="tag">[pppd]</span> LCP up &middot; CHAP accepted');
      setProgressPct(97);
    });
    at(14800, function () {
      appendLog('<span class="ok">[ok]</span> Welcome' + (HOUSEHOLD ? ', <b>' + escapeHTML(HOUSEHOLD) + '</b>' : '') + '!');
      setStatus('Connected.');
      spinner.classList.add('dialer__spinner--done');
      setLed('txd', true, 'grn');
      setLed('rxd', true, 'grn');
      sbDot.classList.add('on');
      sbConn.innerHTML = 'Connection: <b>Direct</b>';
      sbBps.textContent = '56 000 bps';
      setProgressPct(100);
      dialer.dataset.state = 'connected';
      hintEl.textContent = 'Loading your dashboard.';
    });
  }

  // --- Wiring --------------------------------------------------------------
  connectBtn.addEventListener('click', function () {
    if (dialer.dataset.state !== 'idle') return;
    if (prefersReduced) {
      runReduced();
    } else {
      runSequence();
      audioEl.addEventListener('ended', goToDashboard, { once: true });
      setTimeout(goToDashboard, BACKSTOP_MS);
    }
  });

  // Skip is a plain anchor with href="/"; pausing here keeps the audio from
  // playing one buffered chunk past the navigation on slow browsers.
  skipBtn.addEventListener('click', function () {
    try { audioEl.pause(); } catch (e) { /* ignore */ }
  });

  setProgressPct(0);
})();
