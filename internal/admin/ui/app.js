// ── Helpers (global so Alpine templates can call them) ───────────────────────

// Mirrors isVPNProgressMsg in tui/model.go — informational connecting-phase messages.
window.isVPNProgressMsg = function(msg) {
  if (!msg) return false;
  return msg.startsWith('resolving ') ||
    msg.startsWith('DNS retry: ') ||
    msg.startsWith('pre_connect: ') ||
    msg.startsWith('probing ') ||
    msg === 'openconnect starting' ||
    msg === 'waiting for VPN tunnel' ||
    msg === 'waiting for network';
};

// For tunnel messages — waiting for VPN or network is informational, not an error.
window.isTunnelProgressMsg = function(msg) {
  if (!msg) return false;
  return msg.startsWith('waiting for VPN') || msg === 'waiting for network';
};

window.fmtBytes = function(n) {
  if (!n) return '0 B/s';
  if (n < 1024)    return n + ' B/s';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB/s';
  return (n / 1048576).toFixed(2) + ' MB/s';
};

window.fmtUptime = function(sec) {
  if (!sec) return '—';
  sec = Math.floor(sec);
  if (sec < 60)   return sec + 's';
  if (sec < 3600) return Math.floor(sec / 60) + 'm' + (sec % 60) + 's';
  return Math.floor(sec / 3600) + 'h' + Math.floor((sec % 3600) / 60) + 'm';
};

// ── Chart management ─────────────────────────────────────────────────────────

const WINDOW_SIZE = 60;
const charts = {};

window.initChart = function(name, canvas) {
  if (charts[name]) return;
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
            callback:      v => v === 0 ? '' : fmtBytes(Math.abs(v)),
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

// ── Table rendering ───────────────────────────────────────────────────────────

function escHtml(s) {
  return !s ? '' : String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function vpnStatusHtml(state, reconnectIn) {
  if (state === 'connected') return '<span class="st-connected">● connected</span>';
  if (state === 'connecting' || state === 'disconnected') {
    if (reconnectIn != null && reconnectIn >= 0) return `<span class="st-connecting">○ next try: ${reconnectIn}s</span>`;
    if (state === 'connecting') return '<span class="st-connecting">connecting</span>';
    return '<span class="st-disconnected">disconnected</span>';
  }
  return `<span class="st-muted">${escHtml(state || '…')}</span>`;
}

function tunnelStatusHtml(t) {
  if (!t) return '<span class="st-muted">—</span>';
  if (t.last_error?.startsWith('waiting for VPN') || t.last_error === 'waiting for network') {
    return '<span class="st-connecting">◌ pending</span>';
  }
  const s = t.status, ri = t.reconnect_in;
  if (s === 'connected') {
    return t.keepalive_failures > 0
      ? `<span class="st-warning">● connected ⚠${t.keepalive_failures}</span>`
      : '<span class="st-connected">● connected</span>';
  }
  if (s === 'connecting' || s === 'disconnected') {
    if (ri != null && ri >= 0) return `<span class="st-connecting">○ next try: ${ri}s</span>`;
    return s === 'connecting'
      ? '<span class="st-connecting">connecting</span>'
      : '<span class="st-disconnected">disconnected</span>';
  }
  return `<span class="st-muted">${escHtml(s || '…')}</span>`;
}

function vpnDepHtml(vpn, state) {
  if (!vpn) return '<span class="st-muted">—</span>';
  const cls = state === 'connected' ? 'vdep-connected' : state === 'connecting' ? 'vdep-connecting' : 'vdep-disconnected';
  return `<span class="vdep ${cls}">${state === 'connected' ? '●' : '○'} ${escHtml(vpn)}</span>`;
}


// tunnelMsgText returns the display text for the error sub-row.
// Propagates VPN root cause: "waiting for VPN: X" → VPN X's own last_error.
function tunnelMsgText(t) {
  if (!t) return '';
  let m = t.last_error || '';
  if (!m) return '';
  if (m.startsWith('waiting for VPN: ')) {
    const vpnName = m.slice('waiting for VPN: '.length);
    const vpn = Alpine.store('hop').vpns[vpnName];
    if (vpn?.last_error) m = vpn.last_error;
  }
  return m;
}

function chartId(name) {
  return 'chart-' + name.replace(/[^a-z0-9]/gi, '_');
}

function setCell(row, col, val, asHtml) {
  const td = row.querySelector(`[data-col="${col}"]`);
  if (!td) return;
  if (asHtml) td.innerHTML = val; else td.textContent = val;
}

function findTunnelRow(name) {
  const tb = document.getElementById('tunnel-tbody');
  return tb ? tb.querySelector(`tr.data-row[data-name="${CSS.escape(name)}"]`) : null;
}

function findVPNRow(name) {
  const tb = document.getElementById('vpn-tbody');
  return tb ? tb.querySelector(`tr.data-row[data-name="${CSS.escape(name)}"]`) : null;
}

function renderVPNTable() {
  const store = Alpine.store('hop');
  const section = document.getElementById('vpn-section');
  const tbody = document.getElementById('vpn-tbody');
  if (!section || !tbody) return;
  const names = Object.keys(store.vpns).sort();
  if (!names.length) { section.style.display = 'none'; tbody.innerHTML = ''; return; }
  section.style.display = '';
  tbody.innerHTML = '';
  for (const name of names) {
    const v = store.vpns[name];
    const tr = document.createElement('tr');
    tr.className = 'data-row'; tr.dataset.name = name;
    tr.innerHTML =
      `<td data-col="name">${escHtml(name)}</td>` +
      `<td data-col="host">${escHtml(v.host || '—')}</td>` +
      `<td data-col="iface">${escHtml(v.tun_iface || '—')}</td>` +
      `<td data-col="port"></td>` +
      `<td data-col="status">${vpnStatusHtml(v.state, v.reconnect_in)}</td>` +
      `<td data-col="uptime">${fmtUptime(v.uptime_seconds)}</td>` +
      `<td data-col="rc">${v.reconnects || 0}</td>` +
      `<td></td><td></td><td></td>`;
    tbody.appendChild(tr);
    // message sub-row — only when not connected and last_error is set
    const vpnMsg = (v.state !== 'connected' && v.last_error) ? v.last_error : '';
    const vpnIsProgress = vpnMsg ? isVPNProgressMsg(vpnMsg) : false;
    const vmtr = document.createElement('tr');
    vmtr.className = 'msg-row'; vmtr.dataset.name = name;
    vmtr.style.display = vpnMsg ? '' : 'none';
    const vpnPrefix = vpnIsProgress ? '◌ ' : '└ ✗ ';
    vmtr.innerHTML = `<td colspan="10" class="msg-row-cell${vpnIsProgress ? ' msg-row-progress' : ''}">${vpnMsg ? escHtml(vpnPrefix + vpnMsg) : ''}</td>`;
    tbody.appendChild(vmtr);
  }
}

let _tunnelKey = null;

function syncTunnelTable() {
  const store = Alpine.store('hop');
  const names = [...Object.keys(store.tunnels).sort(), 'direct'];
  const key = names.join('\x00');
  if (_tunnelKey !== key) { buildTunnelRows(names); _tunnelKey = key; }
  else updateTunnelRows();
}

function buildTunnelRows(names) {
  const tbody = document.getElementById('tunnel-tbody');
  if (!tbody) return;
  for (const name of Object.keys(charts)) { try { charts[name].destroy(); } catch(_){} delete charts[name]; }
  tbody.innerHTML = '';
  const store = Alpine.store('hop');
  for (const name of names) {
    const isDirect = name === 'direct';
    const tr = document.createElement('tr');
    tr.className = isDirect ? 'data-row direct-row' : 'data-row';
    tr.dataset.name = name;
    tr.setAttribute('onclick', 'toggleRowGraph(this)');
    if (isDirect) {
      const d = store.direct;
      tr.innerHTML =
        `<td data-col="name"><span class="row-expand">▶</span>direct</td>` +
        `<td data-col="host"><span class="st-muted">—</span></td>` +
        `<td data-col="vpn"><span class="st-muted">—</span></td>` +
        `<td data-col="port"><span class="st-muted">—</span></td>` +
        `<td data-col="status"><span class="st-muted">—</span></td>` +
        `<td data-col="uptime"><span class="st-muted">—</span></td>` +
        `<td data-col="rc"><span class="st-muted">—</span></td>` +
        `<td class="bps-in"  data-col="bps-in">${fmtBytes(d.bps_in  || 0)}</td>` +
        `<td class="bps-out" data-col="bps-out">${fmtBytes(d.bps_out || 0)}</td>` +
        `<td data-col="active">${d.active || 0}</td>`;
    } else {
      const t = store.tunnels[name];
      if (!t) continue;
      const vpnState = t.requires_vpn && store.vpns[t.requires_vpn] ? store.vpns[t.requires_vpn].state : '';
      tr.innerHTML =
        `<td data-col="name"><span class="row-expand">▶</span>${escHtml(name)}</td>` +
        `<td data-col="host">${escHtml(t.host || '—')}</td>` +
        `<td data-col="vpn">${vpnDepHtml(t.requires_vpn, vpnState)}</td>` +
        `<td data-col="port">${t.local_port || '—'}</td>` +
        `<td data-col="status">${tunnelStatusHtml(t)}</td>` +
        `<td data-col="uptime">${fmtUptime(t.uptime_seconds)}</td>` +
        `<td data-col="rc">${t.reconnect_count || 0}</td>` +
        `<td class="bps-in"  data-col="bps-in">${fmtBytes(t.bps_in  || 0)}</td>` +
        `<td class="bps-out" data-col="bps-out">${fmtBytes(t.bps_out || 0)}</td>` +
        `<td data-col="active">${t.active || 0}</td>`;
    }
    tbody.appendChild(tr);
    // message sub-row — shown for errors (red └ ✗) and progress msgs (amber ◌)
    const msg = tunnelMsgText(store.tunnels[name]);
    const isProgress = msg ? isTunnelProgressMsg(msg) : false;
    const mtr = document.createElement('tr');
    mtr.className = 'msg-row'; mtr.dataset.name = name;
    mtr.style.display = msg ? '' : 'none';
    const prefix = isProgress ? '◌ ' : '└ ✗ ';
    mtr.innerHTML = `<td colspan="10" class="msg-row-cell${isProgress ? ' msg-row-progress' : ''}">${msg ? escHtml(prefix + msg) : ''}</td>`;
    tbody.appendChild(mtr);
    const gtr = document.createElement('tr');
    gtr.className = 'graph-row'; gtr.dataset.name = name;
    gtr.innerHTML = `<td colspan="10"><div class="graph-cell"><canvas id="${chartId(name)}"></canvas></div></td>`;
    tbody.appendChild(gtr);
  }
  if (document.body.classList.contains('graphs-on')) requestAnimationFrame(initAllCharts);
}

function updateTunnelRows() {
  const store = Alpine.store('hop');
  const tbody = document.getElementById('tunnel-tbody');
  if (!tbody) return;
  for (const row of tbody.querySelectorAll('tr.data-row')) {
    const name = row.dataset.name;
    if (name === 'direct') {
      const d = store.direct;
      setCell(row, 'bps-in',  fmtBytes(d.bps_in  || 0));
      setCell(row, 'bps-out', fmtBytes(d.bps_out || 0));
      setCell(row, 'active', d.active || 0);
      continue;
    }
    const t = store.tunnels[name];
    if (!t) continue;
    const vpnState = t.requires_vpn && store.vpns[t.requires_vpn] ? store.vpns[t.requires_vpn].state : '';
    setCell(row, 'vpn', vpnDepHtml(t.requires_vpn, vpnState), true);
    setCell(row, 'status', tunnelStatusHtml(t), true);
    setCell(row, 'uptime', fmtUptime(t.uptime_seconds));
    setCell(row, 'rc', t.reconnect_count || 0);
    setCell(row, 'bps-in',  fmtBytes(t.bps_in  || 0));
    setCell(row, 'bps-out', fmtBytes(t.bps_out || 0));
    setCell(row, 'active', t.active || 0);
    // update msg sub-row
    const mRow = row.nextElementSibling?.classList.contains('msg-row') ? row.nextElementSibling : null;
    if (mRow) {
      const msg = tunnelMsgText(t);
      const isProgress = msg ? isTunnelProgressMsg(msg) : false;
      const cell = mRow.querySelector('.msg-row-cell');
      mRow.style.display = msg ? '' : 'none';
      cell.classList.toggle('msg-row-progress', isProgress);
      const prefix = isProgress ? '◌ ' : '└ ✗ ';
      cell.textContent = msg ? prefix + msg : '';
    }
  }
}

function renderStatusTables() {
  renderVPNTable();
  syncTunnelTable();
}

window.toggleRowGraph = function(row) {
  const expanded = row.classList.toggle('expanded');
  if (expanded) {
    const name = row.dataset.name;
    let next = row.nextElementSibling;
    while (next && !next.classList.contains('graph-row')) next = next.nextElementSibling;
    if (next && !charts[name]) {
      const canvas = next.querySelector('canvas');
      if (canvas) window.initChart(name, canvas);
    }
  }
};

// ── Alpine store ─────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('hop', {
    tunnels: {},
    vpns:    {},
    direct:  { bps_in: 0, bps_out: 0, active: 0 },
    routes:  [],
    meta:    { version: '…', pid: 0, uptime: '…', proxy_port: 0, proxy_bind: '', admin_port: 0, admin_bind: '', status: '…', internet: false, public_ip: '' },

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
      proxy_bind:     st.proxy_bind || '',
      admin_port:     st.admin_port,
      admin_bind:     st.admin_bind || '',
      status:         st.status,
      uplink:         st.uplink ?? true,
      uplink_iface:   st.uplink_iface || '',
      internet:       st.internet ?? true,
      public_ip:      st.public_ip || '',
    };

    // Rebuild tunnel map, preserving live bps/active values from SSE.
    const next = {};
    for (const [name, t] of Object.entries(st.tunnels || {})) {
      const prev = store.tunnels[name] || {};
      next[name] = {
        status:             t.status,
        host:               t.host,
        local_port:         t.local_port,
        requires_vpn:       t.requires_vpn || '',
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
        tun_iface:      v.tun_iface || '',
        reconnects:     v.reconnects || 0,
        uptime_seconds: v.uptime_seconds || 0,
        last_error:     v.last_error || '',
        reconnect_in:   prev.reconnect_in ?? null,
      };
    }
    store.vpns = nextVpns;

    store.routes = st.routes || [];

    if (TAB_INIT['patterns'] && !rulesEditMode) {
      const testerVal = document.getElementById('routes-tester-input')?.value || '';
      renderRoutesTable(testerVal ? findMatchIdx(testerVal) : undefined);
    }

    renderStatusTables();
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
      const row = findTunnelRow(name);
      if (row) {
        setCell(row, 'bps-in',  fmtBytes(t.bps_in  || 0));
        setCell(row, 'bps-out', fmtBytes(t.bps_out || 0));
        setCell(row, 'active', t.active || 0);
        if (store.tunnels[name]) setCell(row, 'status', tunnelStatusHtml(store.tunnels[name]), true);
      }
    }

    for (const [name, v] of Object.entries(d.vpns || {})) {
      if (store.vpns[name]) {
        store.vpns[name].reconnect_in = v.reconnect_in ?? null;
        const row = findVPNRow(name);
        if (row) setCell(row, 'status', vpnStatusHtml(store.vpns[name].state, store.vpns[name].reconnect_in), true);
      }
    }

    store.direct = {
      bps_in:  d.direct?.bps_in  ?? 0,
      bps_out: d.direct?.bps_out ?? 0,
      active:  d.direct?.active  ?? 0,
    };
    pushChart('direct', d.direct?.bps_in ?? 0, d.direct?.bps_out ?? 0);
    const directRow = findTunnelRow('direct');
    if (directRow) {
      const dr = store.direct;
      setCell(directRow, 'bps-in',  fmtBytes(dr.bps_in  || 0));
      setCell(directRow, 'bps-out', fmtBytes(dr.bps_out || 0));
      setCell(directRow, 'active', dr.active || 0);
    }
  };

  es.onerror = () => { es.close(); setTimeout(connectSSE, 3000); };
}

// Start polling/SSE only after Alpine has fully initialized the store.
document.addEventListener('alpine:initialized', () => {
  const savedTab = localStorage.getItem('activeTab');
  if (savedTab) switchTab(savedTab);

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
  localStorage.setItem('activeTab', name);

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
let currentLogLevel = localStorage.getItem('logLevel') || 'INFO';
let activeLogEs = null;

const LOG_LEVEL_ORDER = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 };

function getAnsiUp() {
  if (!ansiUp && window.AnsiUp) {
    ansiUp = new AnsiUp();
    ansiUp.use_classes = false;
  }
  return ansiUp;
}

function logLineLevel(raw) {
  const s = raw.replace(/\x1b\[[0-9;]*[a-zA-Z]/g, '');
  if (s.includes(' DEBU ')) return 'DEBUG';
  if (s.includes(' INFO ')) return 'INFO';
  if (s.includes(' WARN ')) return 'WARN';
  if (s.includes(' ERRO ') || s.includes(' ERROR ')) return 'ERROR';
  return 'INFO';
}

function appendLogLine(scroll, raw) {
  const level = logLineLevel(raw);
  if (LOG_LEVEL_ORDER[level] < LOG_LEVEL_ORDER[currentLogLevel]) return;

  const atBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 40;
  const au = getAnsiUp();
  const html = au ? au.ansi_to_html(raw) : raw.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  const div = document.createElement('div');
  div.className = 'log-line';
  div.dataset.level = level;
  div.innerHTML = html;
  scroll.appendChild(div);
  logLineCount++;
  if (logLineCount > MAX_LOG_LINES) {
    scroll.firstElementChild?.remove();
    logLineCount--;
  }
  if (atBottom) scroll.scrollTop = scroll.scrollHeight;
}

window.setLogLevel = function(level) {
  currentLogLevel = level;
  localStorage.setItem('logLevel', level);
  document.querySelectorAll('.log-chip').forEach(c => {
    c.classList.toggle('active', c.dataset.level === level);
  });
  // Reconnect SSE with the new level so the server filters the backlog too.
  initLogStream();
};

function initLogStream() {
  const scroll = document.getElementById('log-scroll');
  if (!scroll) return;

  document.querySelectorAll('.log-chip').forEach(c => {
    c.classList.toggle('active', c.dataset.level === currentLogLevel);
  });

  if (activeLogEs) { activeLogEs.close(); activeLogEs = null; }
  scroll.innerHTML = '';
  logLineCount = 0;

  const cursor = document.createElement('div');
  cursor.innerHTML = '<span class="log-cursor">▌</span>';

  const es = new EventSource('/logs/stream?level=' + currentLogLevel);
  activeLogEs = es;
  es.onmessage = e => {
    cursor.remove();
    appendLogLine(scroll, e.data);
    scroll.appendChild(cursor);
  };
  es.onerror = () => {
    es.close();
    if (activeLogEs === es) { activeLogEs = null; setTimeout(initLogStream, 3000); }
  };

  scroll.appendChild(cursor);
}

// ── Routes tab ────────────────────────────────────────────────────────────────

function ipToInt(ip) {
  const parts = ip.split('.');
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    const v = parseInt(p, 10);
    if (isNaN(v) || v < 0 || v > 255) return null;
    n = (n << 8) | v;
  }
  return n >>> 0;
}

function matchCIDR(cidr, host) {
  const slash = cidr.indexOf('/');
  if (slash === -1) return false;
  const prefix = parseInt(cidr.slice(slash + 1), 10);
  if (isNaN(prefix) || prefix < 0 || prefix > 32) return false;
  const networkInt = ipToInt(cidr.slice(0, slash));
  const hostInt = ipToInt(host);
  if (networkInt === null || hostInt === null) return false;
  const mask = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0;
  return (networkInt & mask) === (hostInt & mask);
}

function matchPattern(pattern, host) {
  if (pattern === '*') return true;
  if (pattern.includes('/')) return matchCIDR(pattern, host);
  if (pattern.endsWith('.*')) {
    return host.startsWith(pattern.slice(0, -1));
  }
  if (pattern.startsWith('*.')) {
    const suffix = pattern.slice(1);
    return host.endsWith(suffix) || host === pattern.slice(2);
  }
  if (pattern.startsWith('*')) {
    return host.endsWith(pattern.slice(1));
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

function resetRoutesHead() {
  const thead = document.getElementById('routes-thead');
  if (!thead) return;
  thead.innerHTML = `<tr>
    <th style="width:1.2rem"></th>
    <th class="routes-num-h">#</th>
    <th>Pattern</th>
    <th>Via</th>
    <th>Status</th>
  </tr>`;
}

function renderRoutesTable(highlightIdx) {
  const tbody = document.getElementById('routes-tbody');
  if (!tbody) return;
  const routes = Alpine.store('hop').routes || [];
  const tunnels = Alpine.store('hop').tunnels || {};

  if (routes.length === 0) {
    tbody.innerHTML = '<tr><td colspan="5" class="routes-empty">No routing rules configured.</td></tr>';
    return;
  }

  tbody.innerHTML = '';
  routes.forEach((r, i) => {
    const via = r.tunnel || r.via || 'direct';
    const t = tunnels[via];
    const vs = tunnelVisualStatus(t);

    const viaHtml = via === 'block'
      ? `<span class="routes-via-block">${via}</span>`
      : via === 'direct'
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
    const arrow = (highlightIdx === i) ? '▶' : '';
    tr.innerHTML = `<td class="routes-arrow">${arrow}</td><td class="routes-num">${i + 1}</td><td class="routes-pattern">${r.pattern}</td><td>${viaHtml}</td><td>${statusHtml}</td>`;
    tbody.appendChild(tr);
  });
}

function initRoutes() {
  resetRoutesHead();
  renderRoutesTable();
  const tester = document.getElementById('routes-tester-input');
  if (tester && !tester._ctrlNAttached) {
    tester._ctrlNAttached = true;
    tester.addEventListener('keydown', e => {
      if (e.ctrlKey && e.key === 'n') {
        e.preventDefault();
        tester.value = '';
        routesTesterUpdate('');
      }
    });
  }
}

// ── Rules editing ─────────────────────────────────────────────────────────────

let rulesEditMode = false;
let rulesEditData = [];
const _validateTimers = {};

function rulesStartEdit() {
  rulesEditMode = true;
  rulesEditData = (Alpine.store('hop').routes || []).map(r => ({
    ...r,
    _new: false, _deleted: false, _modified: false,
    _origPattern: r.pattern,
    _origTunnel:  r.tunnel || '',
    _origVia:     r.via    || 'direct',
  }));
  document.getElementById('routes-edit-btn').style.display   = 'none';
  document.getElementById('routes-save-btn').style.display   = '';
  document.getElementById('routes-cancel-btn').style.display = '';
  document.getElementById('routes-save-status').textContent  = '';
  document.querySelector('.routes-tester').style.display     = 'none';
  renderEditTable();
}

function rulesCancel() {
  rulesEditMode = false;
  document.getElementById('routes-edit-btn').style.display   = '';
  document.getElementById('routes-save-btn').style.display   = 'none';
  document.getElementById('routes-cancel-btn').style.display = 'none';
  document.getElementById('routes-save-status').textContent  = '';
  document.querySelector('.routes-tester').style.display     = '';
  resetRoutesHead();
  renderRoutesTable();
}

function rulesInsertAfter(i) {
  rulesCollectFromDOM();
  rulesEditData.splice(i + 1, 0, {pattern: '', tunnel: '', via: 'direct', _new: true, _deleted: false});
  renderEditTable(i + 1);
  const rows = document.querySelectorAll('#routes-tbody tr[data-idx]');
  if (rows[i + 1]) rows[i + 1].querySelector('.rules-edit-pattern')?.focus();
}

function rulesDeleteRow(i) {
  rulesCollectFromDOM();
  const r = rulesEditData[i];
  if (r._new) {
    // New (unsaved) row — remove it immediately, no red highlight needed
    rulesEditData.splice(i, 1);
  } else if (r._deleted) {
    // Already marked for deletion — toggle back
    rulesEditData[i]._deleted = false;
  } else {
    // Original row — soft-delete (keep in list, mark red)
    rulesEditData[i]._deleted = true;
  }
  renderEditTable();
}

function rulesMoveUp(i) {
  rulesCollectFromDOM();
  if (i <= 0) return;
  [rulesEditData[i-1], rulesEditData[i]] = [rulesEditData[i], rulesEditData[i-1]];
  renderEditTable();
}

function rulesMoveDown(i) {
  rulesCollectFromDOM();
  if (i >= rulesEditData.length - 1) return;
  [rulesEditData[i], rulesEditData[i+1]] = [rulesEditData[i+1], rulesEditData[i]];
  renderEditTable();
}

function rulesEffectiveVia(r) {
  return r.tunnel || r.via || 'direct';
}

function rulesComputeModified(r) {
  if (r._new || r._deleted) return false;
  return r.pattern !== (r._origPattern || '') ||
         rulesEffectiveVia(r) !== (r._origTunnel || r._origVia || 'direct');
}

function rulesCollectFromDOM() {
  document.querySelectorAll('#routes-tbody tr[data-idx]').forEach(tr => {
    const idx = parseInt(tr.dataset.idx);
    if (rulesEditData[idx]?._deleted) return; // deleted rows keep their original data
    const pattern = tr.querySelector('.rules-edit-pattern')?.value || '';
    const via     = tr.querySelector('.rules-via-picker')?.dataset.value || 'direct';
    rulesEditData[idx] = {
      ...rulesEditData[idx],
      pattern,
      tunnel: (via === 'direct' || via === 'block') ? '' : via,
      via:    (via === 'direct' || via === 'block') ? via : '',
    };
    rulesEditData[idx]._modified = rulesComputeModified(rulesEditData[idx]);
  });
}

// ── Custom via picker ──────────────────────────────────────────────────────────

window.rulesPickerToggle = function(el, event) {
  event.stopPropagation();
  const isOpen = el.classList.contains('open');
  document.querySelectorAll('.rules-via-picker.open').forEach(p => p.classList.remove('open'));
  if (!isOpen) el.classList.add('open');
};

window.rulesPickerSelect = function(rowIdx, value, event) {
  event.stopPropagation();
  rulesCollectFromDOM();
  if (rowIdx < rulesEditData.length) {
    rulesEditData[rowIdx].tunnel = (value === 'direct' || value === 'block') ? '' : value;
    rulesEditData[rowIdx].via    = (value === 'direct' || value === 'block') ? value : '';
    rulesEditData[rowIdx]._pickerValue = value;
    rulesEditData[rowIdx]._modified = rulesComputeModified(rulesEditData[rowIdx]);
    const tr = document.querySelector(`#routes-tbody tr[data-idx="${rowIdx}"]`);
    if (tr) tr.classList.toggle('rules-row-modified', !!rulesEditData[rowIdx]._modified);
  }
  document.querySelectorAll('.rules-via-picker.open').forEach(p => p.classList.remove('open'));
  // Update just the picker label without full re-render
  const picker = document.querySelector(`#routes-tbody tr[data-idx="${rowIdx}"] .rules-via-picker`);
  if (picker) {
    picker.dataset.value = value;
    picker.querySelector('.rules-via-picker-label').textContent = value;
    picker.querySelectorAll('.rules-via-opt').forEach(li => {
      li.classList.toggle('selected', li.dataset.value === value);
    });
  }
};

document.addEventListener('click', () => {
  document.querySelectorAll('.rules-via-picker.open').forEach(p => p.classList.remove('open'));
});

function buildViaPicker(rowIdx, currentVia, tunnelNames) {
  const options = ['direct', ...tunnelNames, 'block'];
  const items = options.map(v =>
    `<li class="rules-via-opt${v === currentVia ? ' selected' : ''}" data-value="${escHtml(v)}"
        onclick="rulesPickerSelect(${rowIdx}, '${escHtml(v)}', event)">${escHtml(v)}</li>`
  ).join('');
  return `<div class="rules-via-picker" data-value="${escHtml(currentVia)}" tabindex="0"
               onclick="rulesPickerToggle(this, event)">
    <span class="rules-via-picker-label">${escHtml(currentVia)}</span>
    <span class="rules-via-picker-arrow">▾</span>
    <ul class="rules-via-picker-list">${items}</ul>
  </div>`;
}

// ── Pattern validation ─────────────────────────────────────────────────────────

window.debounceValidate = function(rowIdx, pattern) {
  clearTimeout(_validateTimers[rowIdx]);
  // Live modified tracking — update flag + CSS class without full re-render
  if (rulesEditData[rowIdx] && !rulesEditData[rowIdx]._new) {
    rulesEditData[rowIdx].pattern  = pattern;
    rulesEditData[rowIdx]._modified = rulesComputeModified(rulesEditData[rowIdx]);
    const tr = document.querySelector(`#routes-tbody tr[data-idx="${rowIdx}"]`);
    if (tr) tr.classList.toggle('rules-row-modified', !!rulesEditData[rowIdx]._modified);
  }
  _validateTimers[rowIdx] = setTimeout(() => _doValidate(rowIdx, pattern), 300);
};

async function _doValidate(rowIdx, pattern) {
  if (!pattern) { _clearValidationError(rowIdx); return; }
  try {
    const res  = await fetch('/api/validate-pattern?p=' + encodeURIComponent(pattern));
    const data = await res.json();
    if (data.valid) {
      _clearValidationError(rowIdx);
    } else {
      _showValidationError(rowIdx, data.error);
    }
  } catch { /* ignore */ }
}

function _showValidationError(rowIdx, msg) {
  const tr = document.querySelector(`#routes-tbody tr[data-idx="${rowIdx}"]`);
  if (!tr) return;
  const inp = tr.querySelector('.rules-edit-pattern');
  if (inp) inp.classList.add('invalid');
  let hint = tr.querySelector('.rules-pattern-error');
  if (!hint) {
    hint = document.createElement('div');
    hint.className = 'rules-pattern-error';
    tr.querySelector('.rules-pattern-cell')?.appendChild(hint);
  }
  hint.textContent = msg;
}

function _clearValidationError(rowIdx) {
  const tr = document.querySelector(`#routes-tbody tr[data-idx="${rowIdx}"]`);
  if (!tr) return;
  tr.querySelector('.rules-edit-pattern')?.classList.remove('invalid');
  tr.querySelector('.rules-pattern-error')?.remove();
}

// ── Save ───────────────────────────────────────────────────────────────────────

async function rulesSave() {
  rulesCollectFromDOM();
  const status  = document.getElementById('routes-save-status');
  const saveBtn = document.getElementById('routes-save-btn');
  saveBtn.disabled = true;
  status.textContent = 'Saving…';
  status.className   = 'routes-save-status';
  try {
    // Strip UI-only metadata and filter out soft-deleted rows before sending
    const payload = rulesEditData
      .filter(r => !r._deleted)
      .map(({_new, _deleted, _modified, _origPattern, _origTunnel, _origVia, _pickerValue, ...r}) => r);
    const res = await fetch('/api/rules', {
      method:  'PUT',
      headers: {'Content-Type': 'application/json'},
      body:    JSON.stringify({rules: payload}),
    });
    if (!res.ok) {
      const txt = await res.text();
      status.textContent = 'Error: ' + txt.trim();
      status.className   = 'routes-save-status routes-save-error';
      return;
    }
    status.textContent = 'Saved ✓';
    status.className   = 'routes-save-status routes-save-ok';
    refreshStatus();
    setTimeout(rulesCancel, 800);
  } catch {
    status.textContent = 'Network error';
    status.className   = 'routes-save-status routes-save-error';
  } finally {
    saveBtn.disabled = false;
  }
}

// ── Render ─────────────────────────────────────────────────────────────────────

function escHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// newRowIdx: index of the just-inserted row (gets enter animation); undefined = no animation
function renderEditTable(newRowIdx) {
  const thead = document.getElementById('routes-thead');
  if (thead) {
    thead.innerHTML = `<tr>
      <th style="width:5rem"></th>
      <th class="routes-num-h">#</th>
      <th>Pattern</th>
      <th>Via</th>
      <th style="width:4rem"></th>
    </tr>`;
  }

  const tb = document.getElementById('routes-tbody');
  if (!tb) return;

  const tunnelNames = Object.keys(Alpine.store('hop').tunnels || {}).sort();

  if (rulesEditData.length === 0) {
    tb.innerHTML = '<tr><td colspan="5" class="routes-empty">No rules — click + Add or use the row buttons to insert.</td></tr>';
    return;
  }

  tb.innerHTML = '';

  // Top insert-before-first row
  const topRow = document.createElement('tr');
  topRow.className = 'rules-top-insert';
  topRow.innerHTML = `<td colspan="4"></td><td class="rules-row-actions"><button class="rules-insert-btn" onclick="rulesInsertAfter(-1)" title="Insert rule at top">+</button></td>`;
  tb.appendChild(topRow);

  rulesEditData.forEach((r, i) => {
    const isNew     = !!r._new;
    const isDeleted = !!r._deleted;
    const currentVia = r.tunnel || (r.via || 'direct');

    const isModified = !!r._modified && !isNew && !isDeleted;

    const tr = document.createElement('tr');
    tr.dataset.idx = i;
    if (isNew)      tr.classList.add('rules-row-new');
    if (isDeleted)  tr.classList.add('rules-row-deleted');
    if (isModified) tr.classList.add('rules-row-modified');
    if (i === newRowIdx) tr.classList.add('rules-row-enter');

    const moveDisabled = isDeleted;
    const viaCell = isDeleted
      ? `<div class="rules-via-picker rules-via-picker-disabled" data-value="${escHtml(currentVia)}">
           <span class="rules-via-picker-label">${escHtml(currentVia)}</span>
           <span class="rules-via-picker-arrow">▾</span>
         </div>`
      : buildViaPicker(i, currentVia, tunnelNames);
    const actionBtn = isDeleted
      ? `<button class="rules-undo-btn"   onclick="rulesDeleteRow(${i})" title="Undo delete">↩</button>`
      : `<button class="rules-delete-btn" onclick="rulesDeleteRow(${i})" title="Delete">✕</button>`;

    tr.innerHTML = `
      <td class="rules-reorder">
        <button class="rules-reorder-btn" onclick="rulesMoveUp(${i})"   ${(i === 0                        || moveDisabled) ? 'disabled' : ''}>↑</button>
        <button class="rules-reorder-btn" onclick="rulesMoveDown(${i})" ${(i === rulesEditData.length - 1 || moveDisabled) ? 'disabled' : ''}>↓</button>
      </td>
      <td class="routes-num">${i + 1}</td>
      <td class="rules-pattern-cell">
        <input type="text" class="rules-edit-pattern" value="${escHtml(r.pattern)}"
               placeholder="*.example.com or 10.0.0.0/8"
               ${isDeleted ? 'disabled' : `oninput="debounceValidate(${i}, this.value)"`}>
      </td>
      <td>${viaCell}</td>
      <td class="rules-row-actions">
        ${isDeleted ? '' : `<button class="rules-insert-btn" onclick="rulesInsertAfter(${i})" title="Insert rule below">+</button>`}
        ${actionBtn}
      </td>
    `;
    tb.appendChild(tr);

    // Re-validate existing patterns so errors are visible after re-render
    if (r.pattern && !isDeleted) _doValidate(i, r.pattern);
  });
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
