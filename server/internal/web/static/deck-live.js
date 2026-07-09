// deck-live.js: the live-session "deck" pages (single call and 3-way
// conference). Runs the elapsed-time clock, watches for a stalled SSE stream,
// and reloads the page when the session ends so the server can re-render it in
// postmortem mode. Shared by call-live-detail and conference-live-detail;
// each part self-guards on the elements it needs. Loaded with defer.
(function () {
  var el = document.querySelector('.deck-duration');
  if (!el) return;
  var since = parseInt(el.getAttribute('data-duration-since'), 10);
  if (!since) return;

  function pad(n) { return n < 10 ? '0' + n : String(n); }
  function tick() {
    if (!document.body.contains(el)) return;
    var s = Math.max(0, Math.floor((Date.now() - since) / 1000));
    var h = Math.floor(s / 3600);
    var m = Math.floor((s % 3600) / 60);
    var sec = s % 60;
    el.textContent = (h > 0 ? h + ':' : '') + pad(m) + ':' + pad(sec);
  }
  tick();
  var iv = setInterval(tick, 1000);

  // Single-call deck: surface a "connection lost" banner if the SSE link goes
  // quiet for 30s. The conference deck has no such panel and skips this.
  var lastEvent = Date.now();
  var panel = document.getElementById('call-live-panel');
  if (panel) {
    panel.addEventListener('htmx:sseMessage', function () { lastEvent = Date.now(); });
    setInterval(function () {
      if (Date.now() - lastEvent > 30000 && !document.getElementById('deck-stale-banner')) {
        var banner = document.createElement('div');
        banner.id = 'deck-stale-banner';
        banner.className = 'deck-stale';
        banner.appendChild(document.createTextNode('Connection lost. '));
        var refresh = document.createElement('button');
        refresh.type = 'button';
        refresh.textContent = 'Refresh';
        refresh.addEventListener('click', function () { location.reload(); });
        banner.appendChild(refresh);
        var deck = document.querySelector('.deck');
        if (deck) deck.prepend(banner);
      }
    }, 5000);
  }

  document.body.addEventListener('htmx:sseMessage', function (ev) {
    if (ev.detail && (ev.detail.type === 'ended' || ev.detail.type === 'disconnect')) {
      clearInterval(iv);
      // Refresh after a short delay so the server re-renders the page in
      // postmortem mode (End-call button hidden, terminal chip in the header).
      // The inline swap handles the panel; the reload handles the chrome.
      setTimeout(function () { location.reload(); }, 800);
    }
  });
})();
