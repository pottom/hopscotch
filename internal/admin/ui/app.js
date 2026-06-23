// ── Helpers (global so Alpine templates can call them) ───────────────────────

window.fmtBytes = function(n) {
  if (!n) return '0 B';
  if (n < 1024)    return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1048576).toFixed(2) + ' MB';
};

window.fmtUptime = function(sec) {
  if (!sec) return '—';
  sec = Math.floor(sec);
  if (sec < 60)   return sec + 's';
  if (sec < 3600) return Math.floor(sec / 60) + 'm ' + (sec % 60) + 's';
  return Math.floor(sec / 3600) + 'h ' + Math.floor((sec % 3600) / 60) + 'm';
};

// ── Chart management ─────────────────────────────────────────────────────────

const PALETTE = ['#38bdf8', '#818cf8', '#34d399', '#f59e0b', '#f87171'];
const DIRECT_COLOR = '#64748b';
const WINDOW_SIZE = 60;
const charts = {};

window.initChart = function(name, el) {
  if (charts[name]) return;
  const canvas = el.querySelector('.tc-canvas');
  if (!canvas) return;

  const names = Object.keys(Alpine.store('hop').tunnels).sort();
  const color = name === 'direct' ? DIRECT_COLOR : PALETTE[names.indexOf(name) % PALETTE.length];

  charts[name] = new Chart(canvas, {
    type: 'line',
    data: {
      labels:   Array(WINDOW_SIZE).fill(''),
      datasets: [{
        data:            Array(WINDOW_SIZE).fill(null),
        borderColor:     color,
        backgroundColor: color + '18',
        borderWidth:     1.5,
        pointRadius:     0,
        tension:         0.4,
        fill:            true,
      }],
    },
    options: {
      animation:           false,
      responsive:          true,
      maintainAspectRatio: false,
      scales: {
        x: { display: false },
        y: {
          display:     true,
          beginAtZero: true,
          grid:        { color: 'rgba(26,37,53,0.8)' },
          ticks: {
            color:         '#475569',
            maxTicksLimit: 3,
            font:          { size: 9, family: "'JetBrains Mono', monospace" },
            callback:      v => fmtBytes(v) + '/s',
          },
        },
      },
      plugins: {
        legend:  { display: false },
        tooltip: { enabled: false },
      },
    },
  });
};

function pushChart(name, value) {
  const c = charts[name];
  if (!c) return;
  c.data.labels.push('');
  c.data.labels.shift();
  c.data.datasets[0].data.push(value);
  c.data.datasets[0].data.shift();
  c.update('none');
}

// ── Alpine store ─────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('hop', {
    tunnels: {},
    direct:  { bps_in: 0, bps_out: 0, active: 0 },
    meta:    { version: '…', pid: 0, uptime: '…', proxy_port: 0, admin_port: 0, status: '…' },

    tunnelList() {
      return Object.keys(this.tunnels).sort();
    },

    // Returns the effective visual state for a tunnel — mirrors TUI renderStatus logic.
    // connected + keepalive_failures > 0 → 'warning' (amber, same as connecting visuals)
    visualStatus(name) {
      const t = this.tunnels[name];
      if (!t) return '';
      if (t.status === 'connected' && t.keepalive_failures > 0) return 'warning';
      return t.status;
    },

    totalStats() {
      let bpsIn = this.direct.bps_in || 0;
      let bpsOut = this.direct.bps_out || 0;
      let active = this.direct.active || 0;
      for (const t of Object.values(this.tunnels)) {
        bpsIn  += t.bps_in  || 0;
        bpsOut += t.bps_out || 0;
        active += t.active  || 0;
      }
      return { bpsIn, bpsOut, active };
    },
  });
});

// ── Status polling ────────────────────────────────────────────────────────────

async function refreshStatus() {
  try {
    const st = await fetch('/status').then(r => r.json());
    const store = Alpine.store('hop');

    store.meta = {
      version:    st.version,
      pid:        st.pid,
      uptime:     st.uptime,
      proxy_port: st.proxy_port,
      admin_port: st.admin_port,
      status:     st.status,
    };

    // Rebuild tunnel map, preserving live bps/active values from SSE.
    const next = {};
    for (const [name, t] of Object.entries(st.tunnels || {})) {
      const prev = store.tunnels[name] || {};
      next[name] = {
        status:             t.status,
        host:               t.host,
        local_port:         t.local_port,
        uptime_seconds:     t.uptime_seconds,
        reconnect_count:    t.reconnect_count,
        keepalive_failures: t.keepalive_failures || 0,
        bps_in:             prev.bps_in       ?? 0,
        bps_out:            prev.bps_out      ?? 0,
        active:             prev.active       ?? 0,
        reconnect_in:       prev.reconnect_in ?? null,
      };
    }
    store.tunnels = next;

    document.title = `hopscotch ${st.version}`;
  } catch {
    Alpine.store('hop').meta.status = 'offline';
  }
}

// ── SSE traffic stream ────────────────────────────────────────────────────────

function connectSSE() {
  const es = new EventSource('/traffic/stream');

  es.onmessage = e => {
    const d = JSON.parse(e.data);
    const store = Alpine.store('hop');

    for (const [name, t] of Object.entries(d.tunnels || {})) {
      if (store.tunnels[name]) {
        store.tunnels[name].bps_in       = t.bps_in;
        store.tunnels[name].bps_out      = t.bps_out;
        store.tunnels[name].active       = t.active;
        store.tunnels[name].reconnect_in = t.reconnect_in ?? null;
      }
      pushChart(name, t.bps_in + t.bps_out);
    }

    store.direct = {
      bps_in:  d.direct?.bps_in  ?? 0,
      bps_out: d.direct?.bps_out ?? 0,
      active:  d.direct?.active  ?? 0,
    };
    pushChart('direct', (d.direct?.bps_in ?? 0) + (d.direct?.bps_out ?? 0));
  };

  es.onerror = () => { es.close(); setTimeout(connectSSE, 3000); };
}

// Start polling/SSE only after Alpine has fully initialized the store.
document.addEventListener('alpine:initialized', () => {
  refreshStatus();
  setInterval(refreshStatus, 5000);
  connectSSE();

  // Footer clock
  const tsEl = document.getElementById('footer-ts');
  if (tsEl) {
    const tick = () => { tsEl.textContent = new Date().toLocaleTimeString(); };
    tick();
    setInterval(tick, 1000);
  }
});

// ── Tab switching ─────────────────────────────────────────────────────────────

const TAB_INIT = {};

window.switchTab = function(name) {
  document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
  document.querySelectorAll('#tab-bar a').forEach(a => {
    a.classList.toggle('active', a.dataset.tab === name);
  });
  document.getElementById('panel-' + name).classList.add('active');

  if (!TAB_INIT[name]) {
    TAB_INIT[name] = true;
    if (name === 'logs')  initLogStream();
    if (name === 'docs')  initDocs();
  }
};

// ── Log SSE stream ────────────────────────────────────────────────────────────

const MAX_LOG_LINES = 500;
let logLineCount = 0;
let ansiUp = null;

function getAnsiUp() {
  if (!ansiUp && window.AnsiUp) {
    ansiUp = new AnsiUp();
    ansiUp.use_classes = false;
  }
  return ansiUp;
}

function appendLogLine(scroll, raw) {
  const atBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 40;
  const au = getAnsiUp();
  const html = au ? au.ansi_to_html(raw) : raw.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  const div = document.createElement('div');
  div.className = 'log-line';
  div.innerHTML = html;
  scroll.appendChild(div);
  logLineCount++;
  if (logLineCount > MAX_LOG_LINES) {
    scroll.firstElementChild?.remove();
    logLineCount--;
  }
  if (atBottom) scroll.scrollTop = scroll.scrollHeight;
}

function initLogStream() {
  const scroll = document.getElementById('log-scroll');
  if (!scroll) return;

  const cursor = document.createElement('div');
  cursor.innerHTML = '<span class="log-cursor">▌</span>';

  const es = new EventSource('/logs/stream');
  es.onmessage = e => {
    cursor.remove();
    appendLogLine(scroll, e.data);
    scroll.appendChild(cursor);
    scroll.scrollTop = scroll.scrollHeight;
  };
  es.onerror = () => { es.close(); setTimeout(initLogStream, 3000); };

  scroll.appendChild(cursor);
}

// ── Docs tab ──────────────────────────────────────────────────────────────────

function initDocs() {
  const el = document.getElementById('docs-content');
  if (!el) return;
  fetch('/readme')
    .then(r => r.text())
    .then(md => { el.innerHTML = marked.parse(md); })
    .catch(() => { el.innerHTML = '<p style="color:var(--muted)">Could not load documentation.</p>'; });
}

// ── Logo animation ────────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
  const dot = document.getElementById('hop-dot');
  if (!dot) return;
  let t = 0, dir = 1;
  setInterval(() => {
    t += 0.018 * dir;
    if (t >= 1) { t = 1; dir = -1; }
    if (t <= 0) { t = 0; dir =  1; }
    const x = (1-t)*(1-t)*11 + 2*(1-t)*t*18 + t*t*25;
    const y = (1-t)*(1-t)*26 + 2*(1-t)*t*7  + t*t*26;
    dot.setAttribute('cx', x.toFixed(2));
    dot.setAttribute('cy', y.toFixed(2));
  }, 30);
});
