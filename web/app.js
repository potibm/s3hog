const tbody = document.querySelector('#objects tbody');
const bucketFilter = document.getElementById('bucketFilter');
const search = document.getElementById('search');
const status = document.getElementById('status');
const empty = document.getElementById('empty');

let objects = [];

function fmtSize(n) {
  if (n < 1024) return n + ' B';
  if (n < 1024*1024) return (n/1024).toFixed(1) + ' KB';
  if (n < 1024*1024*1024) return (n/1024/1024).toFixed(1) + ' MB';
  return (n/1024/1024/1024).toFixed(1) + ' GB';
}

function fmtDate(s) {
  const d = new Date(s);
  return d.toLocaleString();
}

function render() {
  const q = search.value.trim().toLowerCase();
  const bf = bucketFilter.value;
  const filtered = objects.filter(o => {
    if (bf && o.bucket !== bf) return false;
    if (q && !o.key.toLowerCase().includes(q) && !o.bucket.toLowerCase().includes(q)) return false;
    return true;
  });
  tbody.innerHTML = '';
  for (const o of filtered) {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td>${escape(o.bucket)}</td>
      <td>${escape(o.key)}</td>
      <td>${fmtSize(o.size)}</td>
      <td>${fmtDate(o.lastModified)}</td>
      <td>
        <a href="/download?bucket=${encodeURIComponent(o.bucket)}&key=${encodeURIComponent(o.key)}" download>Download</a>
      </td>`;
    tbody.appendChild(tr);
  }
  empty.hidden = filtered.length !== 0;
  // buckets dropdown
  const buckets = [...new Set(objects.map(o => o.bucket))];
  const cur = bucketFilter.value;
  bucketFilter.innerHTML = '<option value="">All buckets</option>';
  for (const b of buckets.sort()) {
    const opt = document.createElement('option');
    opt.value = b; opt.textContent = b;
    if (b === cur) opt.selected = true;
    bucketFilter.appendChild(opt);
  }
}

function escape(s){ return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function escapeAttr(s){ return String(s).replace(/"/g,'&quot;'); }

async function load() {
  const res = await fetch('/api/objects');
  objects = await res.json();
  render();
}

document.getElementById('refresh').addEventListener('click', load);
bucketFilter.addEventListener('change', render);
search.addEventListener('input', render);

const es = new EventSource('/events');
es.addEventListener('change', () => load());
es.onopen = () => { status.textContent = 'live'; status.classList.add('ok'); };
es.onerror = () => { status.textContent = 'reconnecting…'; status.classList.remove('ok'); };

load();
setInterval(load, 30000);
