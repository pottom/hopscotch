function fmt(sec) {
  sec = Math.floor(sec);
  if (sec < 60)   return sec + 's';
  if (sec < 3600) return Math.floor(sec / 60) + 'm ' + (sec % 60) + 's';
  return Math.floor(sec / 3600) + 'h ' + Math.floor((sec % 3600) / 60) + 'm';
}

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

// Logo: animate hop-dot along quadratic bezier M11 26 Q18 7 25 26
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
