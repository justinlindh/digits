// AOL-era JavaScript. Keep it simple.
function openModal(id) {
  var el = document.getElementById(id);
  if (el) el.classList.add('open');
  return false;
}
function closeModal(id) {
  var el = document.getElementById(id);
  if (el) el.classList.remove('open');
  return false;
}
function togglePanel(id) {
  var el = document.getElementById(id);
  if (!el) return false;
  el.style.display = (el.style.display === 'none' || !el.style.display) ? 'block' : 'none';
  return false;
}
