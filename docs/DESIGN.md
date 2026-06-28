# UI Design Spec

## Alapelv: TUI ↔ Web UI paritás

A TUI és a web UI **megjelenésben és viselkedésben a lehetőségekhez mérten egyforma legyen.** Ez azt jelenti:

- Azonos színek (hex értékek szintjén)
- Azonos szövegek, prefixek, ikonok
- Azonos sorrend és elrendezés
- Azonos állapot-jelzések és logika

Ha valamelyik felületen változtatás történik — akár szín, akár szöveg, akár új elem — **automatikusan el kell végezni a változtatást a másik felületen is.** Nem kell külön szólni érte.

Kivételek (ahol technikai kényszernél elfogadható az eltérés):
- Interakciós elemek (billentyűkezelés TUI-ban, hover/kattintás web UI-ban)
- Animációk (TUI-ban korlátozott)
- Elválasztók stílusa (TUI = szóköz, web UI = `·`)
- Tördelés és igazítás (terminál karakterrács vs. CSS flexbox)

Ez a dokumentum a kanonikus referencia. Ha valami itt nincs leírva de eltérés van a két felület között, azt hibának kell tekinteni.

---

## Header

**Kanonikus sorrend (bal → jobb):**

```
hopscotch vX.Y.Z  [⚡new_version]  [badge]  [● iface localIP / ○ no link]  [⊕ internet publicIP / ○ no internet]  PID XXXXX  up Xh Xm
```

| Elem | Mindig látható | Feltétel |
|------|---------------|----------|
| `hopscotch vX.Y.Z` | igen | — |
| `⚡X.Y.Z` (update badge) | nem | ha elérhető újabb verzió |
| status badge (`healthy` / `degraded` / ...) | igen | — |
| `● iface localIP` / `○ no link` | igen | uplink állapot alapján; localIP = az interface helyi IP-je; piros ha nincs link |
| `⊕ internet publicIP` | nem | ha `admin.show_public_ip: true` és van internet |
| `○ no internet` | nem | ha link van de internet nincs (`admin.show_public_ip: true`) |
| `PID XXXXX` | igen | — |
| `up Xh Xm` | igen | — |

**Elválasztó:** TUI = két szóköz, web UI = `·` karakter.

---

## Status tábla — Name oszlop színei

| Sor típusa | Szín | Hex |
|------------|------|-----|
| VPN | teal | `#2dd4bf` |
| Tunnel | sky blue | `#38bdf8` |
| direct | violet | `#a78bfa` |

---

## Status tábla — Host / Iface oszlop

Mindkét felületen: `var(--muted)` / `colorMuted (#475569)`.

---

## Status tábla — VPN oszlop (tunnel sorokban)

Formátum: `● vpnname` ha connected, `○ vpnname` ha nem.

Szín: a VPN aktuális állapota alapján (`colorConnected` / `colorConnecting` / `colorDisconnected`).

---

## Status tábla — Error/progress sub-row

Minden tunnel és VPN sor alatt jelenik meg ha `last_error` nem üres és az állapot nem `connected`.

| Típus | Prefix | Szín |
|-------|--------|------|
| Progress (waiting for...) | `◌ ` | amber / `var(--connecting)` |
| Error | `└ ✗ ` | red / `var(--disconnected)` |

Root cause propagáció: ha tunnel `last_error = "waiting for VPN: X"` és VPN X-nek van saját `last_error`-ja, azt kell megjeleníteni (nem a "waiting for VPN: X" szöveget).

---

## Status tábla — Reconnect timer szöveg

```
○ next try: Xs
```

Mindkét felületen azonos szöveg. TUI: `renderStatus()`, web UI: `tunnelStatusHtml()` / `vpnStatusHtml()`.

---

## Footer (TUI)

```
[hints]                                           PROXY bind:port  ADMIN bind:port
```

A hints sor felette, a port sor alatta, jobbra igazítva.

---

## Színpaletta

| Változó | Hex | Szerep |
|---------|-----|--------|
| `--connected` / `colorConnected` | `#34d399` | connected állapot |
| `--connecting` / `colorConnecting` | `#fbbf24` | connecting / progress |
| `--disconnected` / `colorDisconnected` | `#f87171` | error / disconnected |
| `--accent` / `colorAccent` | `#38bdf8` | tunnel nevek, aktív tab |
| `colorVPN` | `#2dd4bf` | VPN nevek |
| `colorDirect` | `#a78bfa` | direct sor/via |
| `--muted` / `colorMuted` | `#475569` | másodlagos szöveg |
| `colorBpsIn` | `#38bdf8` | bejövő forgalom |
| `colorBpsOut` | `#818cf8` | kimenő forgalom |
