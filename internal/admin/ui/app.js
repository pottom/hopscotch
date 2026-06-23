// ── Utilities ────────────────────────────────────────────────────────────────

function fmt(sec) {
  sec = Math.floor(sec);
  if (sec < 60)   return sec + 's';
  if (sec < 3600) return Math.floor(sec / 60) + 'm ' + (sec % 60) + 's';
  return Math.floor(sec / 3600) + 'h ' + Math.floor((sec % 3600) / 60) + 'm';
}

function fmtBytes(bps) {
  if (bps < 1024)        return bps + ' B/s';
  if (bps < 1024 * 1024) return (bps / 1024).toFixed(1) + ' KB/s';
  return (bps / (1024 * 1024)).toFixed(2) + ' MB/s';
}

// ── Status polling ───────────────────────────────────────────────────────────

async function refresh() {
  try {
    const st = await fetch('/status').then(r => r.json());

    document.title = `hopscotch v${st.version}`;

    const badge = document.getElementById('overall-badge');
    badge.textContent = st.status;
    badge.className = 'badge ' + st.status;

    document.getElementById('header-meta').textContent =
      `v${st.version} · PID ${st.pid} · up ${st.uptime}`;

    const names = Object.keys(st.tunnels).sort();
    const grid = document.getElementById('grid');

    grid.innerHTML = names.length === 0
      ? '<div class="empty">no tunnels configured</div>'
      : names.map(name => {
          const t = st.tunnels[name];
          const up = t.status === 'connected' ? fmt(t.uptime_seconds) : '—';
          return `<div class="card ${t.status}">
            <div class="card-name" title="${name}">${name}</div>
            <div class="card-host">${t.host}</div>
            <div class="card-status">
              <span class="dot ${t.status}"></span>
              <span class="status-label ${t.status}">${t.status}</span>
            </div>
            <div class="card-stats">
              <div class="stat"><div class="stat-label">Socks5</div><div class="stat-value">:${t.local_port}</div></div>
              <div class="stat"><div class="stat-label">Uptime</div><div class="stat-value">${up}</div></div>
              <div class="stat"><div class="stat-label">Recon</div><div class="stat-value">${t.reconnect_count}</div></div>
            </div>
          </div>`;
        }).join('');

    document.getElementById('footer-ports').innerHTML =
      `<div class="port-item"><span>PROXY</span>:${st.proxy_port}</div>` +
      `<div class="port-item"><span>ADMIN</span>:${st.admin_port}</div>`;

  } catch {
    document.getElementById('overall-badge').className = 'badge offline';
    document.getElementById('overall-badge').textContent = 'offline';
    document.getElementById('grid').innerHTML =
      '<div class="empty">could not reach /status</div>';
  }

  document.getElementById('footer-ts').textContent =
    'updated ' + new Date().toLocaleTimeString();
}

refresh();
setInterval(refresh, 5000);

// ── Traffic chart (SSE) ──────────────────────────────────────────────────────

const WINDOW   = 60;  // seconds of history to display
const PALETTE  = ['#38bdf8', '#818cf8', '#34d399', '#f59e0b', '#f87171'];
const DIRECT_COLOR = '#64748b';

const chartCtx = document.getElementById('traffic-chart').getContext('2d');
const chart = new Chart(chartCtx, {
  type: 'line',
  data: { labels: [], datasets: [] },
  options: {
    animation: false,
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: 'index', intersect: false },
    scales: {
      x: {
        grid:  { color: 'rgba(26,37,53,0.9)' },
        ticks: { color: '#475569', maxTicksLimit: 8, font: { family: "'JetBrains Mono', monospace", size: 10 } },
      },
      y: {
        beginAtZero: true,
        grid:  { color: 'rgba(26,37,53,0.9)' },
        ticks: {
          color: '#475569',
          font: { family: "'JetBrains Mono', monospace", size: 10 },
          callback: v => fmtBytes(v),
        },
      },
    },
    plugins: {
      legend: { display: false },
      tooltip: {
        backgroundColor: '#0f1623',
        borderColor: '#1a2535',
        borderWidth: 1,
        titleColor: '#94a3b8',
        bodyColor: '#cbd5e1',
        callbacks: {
          label: ctx => ` ${ctx.dataset.label}: ${fmtBytes(ctx.parsed.y)}`,
        },
      },
    },
  },
});

// dataset index by source name
const dsIndex = {};

function ensureDataset(name, color) {
  if (dsIndex[name] !== undefined) return;
  const idx = chart.data.datasets.length;
  dsIndex[name] = idx;
  chart.data.datasets.push({
    label: name,
    data: Array(chart.data.labels.length).fill(0),
    borderColor: color,
    backgroundColor: color + '1a',  // 10% opacity fill
    borderWidth: 1.5,
    pointRadius: 0,
    tension: 0.3,
    fill: true,
  });

  // update legend
  const legend = document.getElementById('chart-legend');
  const item = document.createElement('div');
  item.className = 'legend-item';
  item.innerHTML = `<span class="legend-dot" style="background:${color}"></span>${name}`;
  legend.appendChild(item);
}

function pushTick(label, sources) {
  // add time label, drop oldest if window full
  chart.data.labels.push(label);
  if (chart.data.labels.length > WINDOW) chart.data.labels.shift();

  // push new value (or 0 for sources not in this tick) for every dataset
  chart.data.datasets.forEach(ds => {
    const name = ds.label;
    const val = sources[name] !== undefined ? sources[name] : 0;
    ds.data.push(val);
    if (ds.data.length > WINDOW) ds.data.shift();
  });

  chart.update('none');
}

function connectSSE() {
  const es = new EventSource('/traffic/stream');
  let colorIdx = 0;

  es.onmessage = e => {
    const d = JSON.parse(e.data);
    const ts = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    const sources = {};

    // tunnels
    for (const [name, t] of Object.entries(d.tunnels || {})) {
      const color = PALETTE[colorIdx % PALETTE.length];
      ensureDataset(name, color);
      colorIdx = dsIndex[name] < PALETTE.length ? colorIdx : colorIdx + 1;
      sources[name] = t.bps_in + t.bps_out;
    }

    // direct
    ensureDataset('direct', DIRECT_COLOR);
    sources['direct'] = (d.direct?.bps_in || 0) + (d.direct?.bps_out || 0);

    pushTick(ts, sources);
  };

  es.onerror = () => {
    // SSE auto-reconnects; reset colorIdx so colors stay stable on reconnect
    setTimeout(connectSSE, 3000);
    es.close();
  };
}

connectSSE();

// ── Logo animation ───────────────────────────────────────────────────────────

(function () {
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
})();
