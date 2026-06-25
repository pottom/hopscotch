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

const WINDOW_SIZE = 60;
const charts = {};

window.initChart = function(name, el) {
  if (charts[name]) return;
  const canvas = el.querySelector('.tc-canvas');
  if (!canvas) return;

  charts[name] = new Chart(canvas, {
    type: 'line',
    data: {
      labels:   Array(WINDOW_SIZE).fill(''),
      datasets: [
        {
          // Download (↓) — positive, fills upward
          data:            Array(WINDOW_SIZE).fill(null),
          borderColor:     '#38bdf8',
          backgroundColor: '#38bdf814',
          borderWidth:     1.5,
          pointRadius:     0,
          tension:         0.3,
          fill:            'origin',
        },
        {
          // Upload (↑) — stored as negative, fills downward
          data:            Array(WINDOW_SIZE).fill(null),
          borderColor:     '#818cf8',
          backgroundColor: '#818cf814',
          borderWidth:     1.5,
          pointRadius:     0,
          tension:         0.3,
          fill:            'origin',
        },
      ],
    },
    options: {
      animation:           false,
      responsive:          true,
      maintainAspectRatio: false,
      scales: {
        x: { display: false },
        y: {
          display: true,
          grid:    { color: 'rgba(26,37,53,0.8)' },
          ticks: {
            color:         '#475569',
            maxTicksLimit: 3,
            font:          { size: 9, family: "'JetBrains Mono', monospace" },
            callback:      v => v === 0 ? '' : fmtBytes(Math.abs(v)) + '/s',
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

function pushChart(name, bpsIn, bpsOut) {
  const c = charts[name];
  if (!c) return;
  c.data.labels.push('');
  c.data.labels.shift();
  c.data.datasets[0].data.push(bpsIn  ?? null);
  c.data.datasets[0].data.shift();
  c.data.datasets[1].data.push(bpsOut != null ? -bpsOut : null);
  c.data.datasets[1].data.shift();
  c.update('none');
}

// ── Alpine store ─────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('hop', {
    tunnels: {},
    vpns:    {},
    direct:  { bps_in: 0, bps_out: 0, active: 0 },
    routes:  [],
    meta:    { version: '…', pid: 0, uptime: '…', proxy_port: 0, admin_port: 0, status: '…' },

    tunnelList() {
      return Object.keys(this.tunnels).sort();
    },

    vpnList() {
      return Object.keys(this.vpns).sort();
    },

    // Returns the effective visual state for a tunnel — mirrors TUI renderStatus logic.
    visualStatus(name) {
      const t = this.tunnels[name];
      if (!t) return '';
      if (t.status === 'connected' && t.keepalive_failures > 0) return 'warning';
      if (t.last_error?.startsWith('waiting for VPN') || t.last_error?.startsWith('waiting for network')) return 'connecting';
      return t.status;
    },

    // Returns the display text for a tunnel's status label.
    tunnelStatusText(name) {
      const t = this.tunnels[name];
      if (!t) return '…';
      if (t.last_error?.startsWith('waiting for VPN') || t.last_error?.startsWith('waiting for network')) return 'pending';
      return t.status || '…';
    },

    vpnVisualStatus(name) {
      const v = this.vpns[name];
      if (!v) return '';
      return v.state; // 'connected' | 'connecting' | 'disconnected'
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
      version:        st.version,
      latest_version: st.latest_version || '',
      pid:            st.pid,
      uptime:         st.uptime,
      proxy_port:     st.proxy_port,
      admin_port:     st.admin_port,
      status:         st.status,
      uplink:         st.uplink ?? true,
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
        last_error:         t.last_error || '',
        bps_in:             prev.bps_in       ?? 0,
        bps_out:            prev.bps_out      ?? 0,
        active:             prev.active       ?? 0,
        reconnect_in:       prev.reconnect_in ?? null,
      };
    }
    store.tunnels = next;

    // Rebuild VPN map, preserving live reconnect_in from SSE.
    const nextVpns = {};
    for (const [name, v] of Object.entries(st.vpns || {})) {
      const prev = store.vpns[name] || {};
      nextVpns[name] = {
        state:          v.state,
        host:           v.host || '',
        reconnects:     v.reconnects || 0,
        uptime_seconds: v.uptime_seconds || 0,
        last_error:     v.last_error || '',
        reconnect_in:   prev.reconnect_in ?? null,
      };
    }
    store.vpns = nextVpns;

    store.routes = st.routes || [];

    if (TAB_INIT['patterns']) {
      const testerVal = document.getElementById('routes-tester-input')?.value || '';
      renderRoutesTable(testerVal ? findMatchIdx(testerVal) : undefined);
    }

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
      pushChart(name, t.bps_in, t.bps_out);
    }

    for (const [name, v] of Object.entries(d.vpns || {})) {
      if (store.vpns[name]) {
        store.vpns[name].reconnect_in = v.reconnect_in ?? null;
      }
    }

    store.direct = {
      bps_in:  d.direct?.bps_in  ?? 0,
      bps_out: d.direct?.bps_out ?? 0,
      active:  d.direct?.active  ?? 0,
    };
    pushChart('direct', d.direct?.bps_in ?? 0, d.direct?.bps_out ?? 0);
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
    if (name === 'logs')   initLogStream();
    if (name === 'patterns') initRoutes();
    if (name === 'docs')   initDocs();
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

// ── Routes tab ────────────────────────────────────────────────────────────────

function matchPattern(pattern, host) {
  if (pattern === '*') return true;
  if (pattern.endsWith('.*')) {
    return host.startsWith(pattern.slice(0, -1));
  }
  if (pattern.startsWith('*.')) {
    const suffix = pattern.slice(1);
    return host.endsWith(suffix) || host === pattern.slice(2);
  }
  return pattern === host;
}

function extractHostname(input) {
  const s = input.trim();
  if (!s) return '';
  try {
    return new URL(s.includes('://') ? s : 'https://' + s).hostname;
  } catch {
    return s;
  }
}

function findMatchIdx(input) {
  const host = extractHostname(input);
  if (!host) return undefined;
  const routes = Alpine.store('hop').routes || [];
  for (let i = 0; i < routes.length; i++) {
    if (matchPattern(routes[i].pattern, host)) return i;
  }
  return undefined;
}

function tunnelVisualStatus(t) {
  if (!t) return '';
  if (t.status === 'connected' && t.keepalive_failures > 0) return 'warning';
  return t.status;
}

function renderRoutesTable(highlightIdx) {
  const tbody = document.getElementById('routes-tbody');
  if (!tbody) return;
  const routes = Alpine.store('hop').routes || [];
  const tunnels = Alpine.store('hop').tunnels || {};

  if (routes.length === 0) {
    tbody.innerHTML = '<tr><td colspan="4" class="routes-empty">No routing rules configured.</td></tr>';
    return;
  }

  tbody.innerHTML = '';
  routes.forEach((r, i) => {
    const via = r.tunnel || r.via || 'direct';
    const t = tunnels[via];
    const vs = tunnelVisualStatus(t);

    const viaHtml = (via === 'direct' || !r.tunnel)
      ? `<span class="routes-via-direct">${via}</span>`
      : `<span class="routes-via-tunnel">${via}</span>`;

    let statusHtml = '';
    if (t) {
      statusHtml = `<span class="dot ${vs}" style="display:inline-block;margin-right:.4em;vertical-align:middle"></span><span class="status-label ${vs}">${t.status}</span>`;
    } else if (via === 'direct') {
      statusHtml = '<span class="routes-via-direct">—</span>';
    }

    const tr = document.createElement('tr');
    if (highlightIdx === i) tr.className = 'routes-match';
    tr.innerHTML = `<td class="routes-num">${i + 1}</td><td class="routes-pattern">${r.pattern}</td><td>${viaHtml}</td><td>${statusHtml}</td>`;
    tbody.appendChild(tr);
  });
}

function initRoutes() {
  renderRoutesTable();
}

window.routesTesterUpdate = function(input) {
  const result = document.getElementById('routes-tester-result');
  const routes = Alpine.store('hop').routes || [];
  const host = extractHostname(input);

  if (!host) {
    if (result) result.innerHTML = '';
    renderRoutesTable();
    return;
  }

  let matchIdx = findMatchIdx(input);
  if (result) {
    if (matchIdx !== undefined) {
      const via = routes[matchIdx].tunnel || routes[matchIdx].via || 'direct';
      result.innerHTML = `<span class="routes-tester-match">✓ rule ${matchIdx + 1} matched &rarr; <strong>${via}</strong></span>`;
    } else {
      result.innerHTML = `<span class="routes-tester-nomatch">no rule matched &rarr; direct (fallback)</span>`;
    }
  }
  renderRoutesTable(matchIdx);
};

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
