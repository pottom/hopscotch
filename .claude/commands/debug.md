# hopscotch debug session

Build, restart, and live-monitor hopscotch together with the user.

## Steps

1. **Build & install**
   ```
   ./build.sh install
   ```
   Ha hibával áll meg, mutasd meg a build outputot és állj meg.

2. **Restart daemon**
   ```
   hopscotch start --restart --verbose
   ```
   Capture the output. Ha a daemon nem indul el (error), mutasd meg és elemezd miért.

3. **Log stream — 50 másodperc**
   ```
   hopscotch logs 2>&1
   ```
   Futtasd background-ban 50 másodpercig, majd öld meg. Ez lefedi a teljes VPN connect ciklust (max 30s timeout + 15s reconnect delay + buffer).

4. **Status snapshot**
   ```
   hopscotch status
   ```

5. **Elemzés** — a log és a status alapján szedd össze:

   **VPN startup:**
   - Mennyi idő alatt csatlakozott? (ping_host consecutive=2 timestamp - subprocess started timestamp)
   - RC értéke? (0 = első kísérlet sikeres, >0 = volt timeout/restart)
   - Volt-e `connect timeout` üzenet? Ha igen, melyik ág: subprocess exited / interface appeared but unreachable / subprocess alive but no interface?

   **Tunnelenkénti státusz:**
   - connected / reconnecting / auth error?
   - RC értéke és uptime (az első csatlakozás óta eltelt idő)
   - go-a-scb külön figyelés: auth error esetén az agent watcher triggerelt-e? ("SSH agent keys changed, retrying immediately")

   **Zaj / meglepő dolgok:**
   - "tunnel waiting for vpn" megjelenik-e reconnect után (VPN-gate skip fix működik-e)?
   - DNS restore dupla log ("already DHCP" + "reset to DHCP") — ez ismert, nem críkus
   - Bármilyen WARN/ERROR ami nem volt ott az előző futásban

6. **Javaslatok** — ha találsz konkrét problémát amire van egyértelmű fix, ajánld fel. Ha több lehetséges ok van, mutasd meg és kérdezd meg a felhasználót.

## Amit NEM kell csinálni
- Ne fixelj semmit automatikusan elemzés nélkül
- Ne commitolj a debug session során
- Ne ölj meg folyamatokat a hopscotch daemon-on kívül

## Kontextus

A hopscotch egy SSH tunnel + SOCKS5 proxy daemon. A fő komponensek:
- **VPN manager**: openconnect subprocess-t indít, ping_host-tal figyeli a tunnel életét
- **Tunnel manager**: VPN gate után SSH-val csatlakozik, keepalive-ot futtat
- **Admin server**: HTTP API + web UI (127.0.0.1:8889)
- **SOCKS5 proxy**: 0.0.0.0:8888

Ismert problémák és megoldásaik:
- Orphan openconnect a previous sessionből → `killOrphanedProcs` startup-kor (SIGTERM, `-P 1` nélkül)
- VPN connect timeout → 3 ágú diagnózis (subprocess exited / iface appeared / no iface)
- SSH auth hiba + YubiKey → `watchAgentKeys` 2s polling, azonnali retry új kulcsra
- "tunnel waiting for vpn" reconnect után → `vpnIsConnected()` check a loop tetején
