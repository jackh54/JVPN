package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackh54/jvpn-server/internal/server"
)

func Start(listenAddr, username, password string, hub *server.Hub) error {
	if username == "" || password == "" {
		return fmt.Errorf("dashboard auth requires non-empty username and password")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ok := hub.TUNReady()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "tun_ready": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "tun_ready": true})
	})
	mux.HandleFunc("/", withBasicAuth(username, password, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	}))
	mux.HandleFunc("/api/metrics", withBasicAuth(username, password, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := hub.DashboardSnapshot()
		_ = json.NewEncoder(w).Encode(snap)
	}))
	mux.HandleFunc("/api/stream", withBasicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		writeSnap := func() bool {
			snap := hub.DashboardSnapshot()
			b, err := json.Marshal(snap)
			if err != nil {
				return false
			}
			if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", b); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}
		if !writeSnap() {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if !writeSnap() {
					return
				}
			}
		}
	}))
	mux.HandleFunc("/api/disconnect", withBasicAuth(username, password, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		idStr := r.URL.Query().Get("session_id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil || id == 0 {
			http.Error(w, "invalid session_id", http.StatusBadRequest)
			return
		}
		blockMin := 0
		if raw := r.URL.Query().Get("block_minutes"); raw != "" {
			if v, e := strconv.Atoi(raw); e == nil && v >= 0 && v <= 1440 {
				blockMin = v
			}
		}
		var ok bool
		if blockMin > 0 {
			ok = hub.DisconnectAndBlockSession(id, time.Duration(blockMin)*time.Minute)
		} else {
			ok = hub.DisconnectSession(id)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "blocked_minutes": blockMin})
	}))

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func withBasicAuth(username, password string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != username || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="jvpn-admin", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>JVPN Admin</title>
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500&family=Outfit:wght@400;500;600;700&display=swap" rel="stylesheet" />
  <link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css" integrity="sha256-p4NxAoJBhIIN+hmNHrzRCf9tD/miZyoHS5obTRR9BMY=" crossorigin="" />
  <style>
    :root {
      --bg: #0c0e10;
      --bg2: #12151a;
      --panel: rgba(22, 26, 32, 0.92);
      --panel-solid: #161a20;
      --line: rgba(255,255,255,0.07);
      --line2: rgba(255,255,255,0.12);
      --text: #eef1f4;
      --muted: #8b95a5;
      --accent: #3dde9a;
      --accent-dim: rgba(61,222,154,0.14);
      --warn: #f0b429;
      --danger: #ff6b7a;
      --danger-bg: rgba(255,107,122,0.1);
      --shadow: 0 18px 50px rgba(0,0,0,0.35);
      --radius: 16px;
      --font: "Outfit", ui-sans-serif, system-ui, sans-serif;
      --mono: "IBM Plex Mono", ui-monospace, Menlo, monospace;
    }
    * { box-sizing: border-box; }
    html, body { margin:0; min-height:100%; }
    body {
      font: 14px/1.45 var(--font);
      color: var(--text);
      background:
        radial-gradient(900px 480px at 85% -10%, rgba(61,222,154,0.09), transparent 55%),
        radial-gradient(700px 420px at -10% 20%, rgba(90,140,255,0.07), transparent 50%),
        linear-gradient(180deg, #101318 0%, var(--bg) 40%, #0a0b0d 100%);
      background-attachment: fixed;
    }
    body::before {
      content:"";
      position:fixed; inset:0; pointer-events:none; opacity:0.35; z-index:0;
      background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.8' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='160' height='160' filter='url(%23n)' opacity='0.05'/%3E%3C/svg%3E");
    }
    .app { position:relative; z-index:1; max-width:1440px; margin:0 auto; padding:20px 20px 48px; }
    .topbar {
      display:flex; align-items:center; justify-content:space-between; gap:16px; flex-wrap:wrap;
      padding:14px 18px; margin-bottom:18px;
      background: rgba(14,16,20,0.72); border:1px solid var(--line); border-radius:18px;
      backdrop-filter: blur(14px); box-shadow: var(--shadow);
      animation: rise 0.5s ease both;
    }
    .brand { display:flex; align-items:center; gap:12px; }
    .mark {
      width:36px; height:36px; border-radius:11px;
      background: linear-gradient(145deg, #4af0ad, #1fa86a);
      display:grid; place-items:center; color:#04140c; font-weight:700; font-size:14px; letter-spacing:-0.04em;
      box-shadow: 0 0 0 4px var(--accent-dim);
    }
    .brand h1 { margin:0; font-size:18px; font-weight:600; letter-spacing:-0.03em; }
    .brand p { margin:2px 0 0; font-size:12px; color:var(--muted); }
    .status-cluster { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }
    .chip {
      display:inline-flex; align-items:center; gap:8px;
      padding:7px 12px; border-radius:999px; border:1px solid var(--line);
      background: rgba(255,255,255,0.03); font-size:12px; color:var(--muted);
    }
    .chip strong { color:var(--text); font-weight:560; }
    .dot {
      width:8px; height:8px; border-radius:50%; background:var(--accent);
      box-shadow:0 0 0 0 rgba(61,222,154,0.55);
      animation: pulse 1.8s ease-out infinite;
    }
    .dot.off { background:var(--danger); box-shadow:none; animation:none; }
    .metrics {
      display:grid; grid-template-columns: repeat(6, minmax(0,1fr)); gap:10px;
      margin-bottom:18px; animation: rise 0.55s ease both; animation-delay:0.05s;
    }
    .metric {
      padding:14px 14px 12px; border-radius:14px; border:1px solid var(--line);
      background: var(--panel); backdrop-filter: blur(10px);
      transition: border-color 0.2s ease, transform 0.2s ease;
    }
    .metric:hover { border-color: var(--line2); transform: translateY(-1px); }
    .metric .k { font-size:11px; color:var(--muted); letter-spacing:0.06em; text-transform:uppercase; font-weight:500; }
    .metric .v { margin-top:8px; font-size:24px; font-weight:650; letter-spacing:-0.03em; font-variant-numeric: tabular-nums; }
    .metric .s { margin-top:4px; font-size:12px; color:var(--muted); min-height:1.2em; }
    .stage {
      display:grid; grid-template-columns: 1.35fr 0.9fr; gap:14px; margin-bottom:14px;
      animation: rise 0.6s ease both; animation-delay:0.1s;
    }
    .panel {
      background: var(--panel); border:1px solid var(--line); border-radius: var(--radius);
      box-shadow: var(--shadow); overflow:hidden;
    }
    .panel-hd {
      display:flex; align-items:flex-end; justify-content:space-between; gap:12px;
      padding:16px 18px 12px; border-bottom:1px solid var(--line);
    }
    .panel-hd h2 { margin:0; font-size:15px; font-weight:600; letter-spacing:-0.02em; }
    .panel-hd .sub { font-size:12px; color:var(--muted); }
    .panel-bd { padding:14px 18px 18px; }
    #map {
      height: min(52vh, 460px); width:100%; border-radius:12px; border:1px solid var(--line);
      background:#0e1217; overflow:hidden;
    }
    .leaflet-container { font: inherit; background:#0e1217; }
    .leaflet-control-attribution { background: rgba(10,12,14,0.75) !important; color: var(--muted) !important; }
    .leaflet-control-attribution a { color: var(--accent) !important; }
    .leaflet-popup-content-wrapper, .leaflet-popup-tip {
      background: var(--panel-solid); color: var(--text); border:1px solid var(--line2);
      box-shadow: var(--shadow); border-radius:12px;
    }
    .leaflet-popup-content { margin:12px 14px; font-size:12px; line-height:1.4; }
    .pin {
      width:16px; height:16px; border-radius:50%;
      background: var(--accent); border:2px solid #04140c;
      box-shadow: 0 0 0 4px var(--accent-dim), 0 8px 18px rgba(0,0,0,0.35);
    }
    .inspector-empty {
      display:grid; place-items:center; min-height:280px; text-align:center; color:var(--muted); padding:24px;
    }
    .inspector-empty strong { display:block; color:var(--text); font-size:15px; margin-bottom:6px; }
    .kv { display:grid; grid-template-columns: 112px 1fr; gap:10px 12px; font-size:13px; margin:0; }
    .kv .k { color:var(--muted); padding-top:1px; }
    .kv .v { word-break: break-word; }
    .actions { display:flex; gap:8px; flex-wrap:wrap; margin-top:16px; }
    button {
      font: 500 12px/1 var(--font); letter-spacing:0.01em;
      border-radius:10px; padding:10px 12px; cursor:pointer;
      border:1px solid var(--line2); background: rgba(255,255,255,0.04); color: var(--text);
      transition: background 0.15s ease, border-color 0.15s ease, transform 0.15s ease;
    }
    button:hover { background: rgba(255,255,255,0.07); border-color: rgba(255,255,255,0.18); }
    button:active { transform: scale(0.98); }
    button.primary { background: var(--accent-dim); border-color: rgba(61,222,154,0.35); color: #b6ffe0; }
    button.danger { background: var(--danger-bg); border-color: rgba(255,107,122,0.35); color: #ffc2c9; }
    .split { display:grid; grid-template-columns: 1.4fr 0.8fr; gap:14px; animation: rise 0.65s ease both; animation-delay:0.15s; }
    .table-wrap { overflow:auto; max-height:420px; }
    table { width:100%; border-collapse:collapse; }
    th, td { text-align:left; padding:11px 12px; font-size:12.5px; border-bottom:1px solid var(--line); vertical-align:middle; }
    th {
      position:sticky; top:0; z-index:1; background: rgba(18,21,26,0.96); backdrop-filter: blur(8px);
      color: var(--muted); font-weight:500; font-size:11px; letter-spacing:0.05em; text-transform:uppercase;
    }
    tbody tr { cursor:pointer; transition: background 0.15s ease; }
    tbody tr:hover { background: rgba(255,255,255,0.03); }
    tbody tr.sel { background: var(--accent-dim); }
    tbody tr.sel td:first-child { box-shadow: inset 3px 0 0 var(--accent); }
    .mono { font-family: var(--mono); font-size:12px; }
    .muted { color: var(--muted); }
    .pill {
      display:inline-flex; align-items:center; gap:4px; margin:0 6px 6px 0;
      padding:4px 8px; border-radius:8px; border:1px solid var(--line);
      background: rgba(255,255,255,0.03); font-size:11px; color:#c7d0dc;
    }
    .bat { font-variant-numeric: tabular-nums; font-family: var(--mono); font-size:12px; }
    .bat-bar {
      display:inline-block; width:34px; height:7px; border-radius:99px; margin-right:6px;
      background: rgba(255,255,255,0.08); vertical-align:middle; overflow:hidden;
    }
    .bat-bar > i { display:block; height:100%; background: var(--accent); border-radius:inherit; }
    .bat-bar.low > i { background: var(--warn); }
    .bat-bar.crit > i { background: var(--danger); }
    .dns-list {
      max-height:180px; overflow:auto; border:1px solid var(--line); border-radius:10px;
      padding:8px 10px; background: rgba(0,0,0,0.25);
    }
    .dns-item {
      font-family: var(--mono); font-size:11.5px; padding:5px 0;
      border-bottom:1px solid var(--line); color:#c5ced9;
    }
    .dns-item:last-child { border-bottom:none; }
    .empty-row td { text-align:center; color:var(--muted); padding:28px 12px; cursor:default; }
    .flash { animation: flash 0.45s ease; }
    @keyframes pulse {
      0% { box-shadow: 0 0 0 0 rgba(61,222,154,0.45); }
      70% { box-shadow: 0 0 0 10px rgba(61,222,154,0); }
      100% { box-shadow: 0 0 0 0 rgba(61,222,154,0); }
    }
    @keyframes rise {
      from { opacity:0; transform: translateY(8px); }
      to { opacity:1; transform: none; }
    }
    @keyframes flash {
      from { background: rgba(61,222,154,0.18); }
      to { background: transparent; }
    }
    @media (max-width: 1100px) {
      .metrics { grid-template-columns: repeat(3, minmax(0,1fr)); }
      .stage, .split { grid-template-columns: 1fr; }
      #map { height: 300px; }
    }
    @media (max-width: 640px) {
      .app { padding:14px 12px 36px; }
      .metrics { grid-template-columns: repeat(2, minmax(0,1fr)); }
      .metric .v { font-size:20px; }
      .kv { grid-template-columns: 1fr; gap:4px; }
    }
  </style>
</head>
<body>
  <div class="app">
    <header class="topbar">
      <div class="brand">
        <div class="mark">J</div>
        <div>
          <h1>JVPN Control</h1>
          <p>Live sessions · traffic · device telemetry</p>
        </div>
      </div>
      <div class="status-cluster">
        <div class="chip"><span class="dot" id="liveDot"></span><span id="liveLabel">Connecting…</span></div>
        <div class="chip" id="tunChip">TUN <strong id="tunLabel">—</strong></div>
        <div class="chip">Uptime <strong id="runtimeChip">—</strong></div>
      </div>
    </header>

    <section class="metrics" aria-label="Overview">
      <div class="metric"><div class="k">Active</div><div class="v" id="active">—</div><div class="s" id="activeSub">connected now</div></div>
      <div class="metric"><div class="k">Sessions</div><div class="v" id="total">—</div><div class="s">lifetime</div></div>
      <div class="metric"><div class="k">Auth fails</div><div class="v" id="auth">—</div><div class="s">rejected handshakes</div></div>
      <div class="metric"><div class="k">Upload</div><div class="v" id="up">—</div><div class="s" id="upRate"></div></div>
      <div class="metric"><div class="k">Download</div><div class="v" id="down">—</div><div class="s" id="downRate"></div></div>
      <div class="metric"><div class="k">Memory</div><div class="v" id="memVal">—</div><div class="s" id="memSub"></div></div>
    </section>

    <section class="stage">
      <div class="panel">
        <div class="panel-hd">
          <div>
            <h2>Live map</h2>
            <div class="sub" id="mapHint">Waiting for GPS telemetry…</div>
          </div>
        </div>
        <div class="panel-bd"><div id="map"></div></div>
      </div>
      <div class="panel" id="inspector">
        <div class="panel-hd">
          <div>
            <h2>Session</h2>
            <div class="sub" id="inspectorSub">Select a device</div>
          </div>
        </div>
        <div class="panel-bd">
          <div id="detailEmpty" class="inspector-empty">
            <div>
              <strong>Nothing selected</strong>
              Click a connected device or map pin to inspect battery, location, DNS, and controls.
            </div>
          </div>
          <div id="detailContent" style="display:none;">
            <div class="kv">
              <div class="k">Session</div><div class="v mono" id="dSession">-</div>
              <div class="k">Client ID</div><div class="v mono" id="dClientId">-</div>
              <div class="k">Device</div><div class="v" id="dPhone">-</div>
              <div class="k">Battery</div><div class="v" id="dBattery">-</div>
              <div class="k">Location</div><div class="v" id="dLoc">-</div>
              <div class="k">Telemetry</div><div class="v muted" id="dTelAt">-</div>
              <div class="k">Tunnel IP</div><div class="v mono" id="dClient">-</div>
              <div class="k">Remote</div><div class="v mono" id="dRemote">-</div>
              <div class="k">Connected</div><div class="v" id="dConnected">-</div>
              <div class="k">Duration</div><div class="v" id="dDuration">-</div>
              <div class="k">Traffic</div><div class="v" id="dTraffic">-</div>
              <div class="k">Metadata</div><div class="v" id="dDevice">-</div>
              <div class="k">DNS</div><div class="v"><div class="dns-list" id="dDns"></div></div>
            </div>
            <div class="actions">
              <button class="primary" onclick="kick(selectedSessionId,0)">Disconnect</button>
              <button class="danger" onclick="kick(selectedSessionId,15)">Block 15 min</button>
            </div>
          </div>
        </div>
      </div>
    </section>

    <section class="split">
      <div class="panel">
        <div class="panel-hd">
          <div>
            <h2>Connected devices</h2>
            <div class="sub">Click a row to inspect</div>
          </div>
        </div>
        <div class="table-wrap">
          <table id="activeTbl">
            <thead><tr><th>ID</th><th>Device</th><th>Battery</th><th>Location</th><th>Tunnel</th><th>Remote</th><th>Traffic</th><th>Time</th><th></th></tr></thead>
            <tbody></tbody>
          </table>
        </div>
      </div>
      <div class="panel">
        <div class="panel-hd">
          <div>
            <h2>Recent disconnects</h2>
            <div class="sub">Latest closed sessions</div>
          </div>
        </div>
        <div class="table-wrap">
          <table id="closedTbl">
            <thead><tr><th>ID</th><th>Device</th><th>Tunnel</th><th>Traffic</th><th>Time</th></tr></thead>
            <tbody></tbody>
          </table>
        </div>
      </div>
    </section>
  </div>

<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js" integrity="sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo=" crossorigin=""></script>
<script>
let prev = null;
let sessions = {};
let selectedSessionId = null;
let es = null;
let map = null;
let mapMarkers = {};
let mapFitted = false;

function fmtBytes(n){
  n = Number(n)||0;
  if(n < 1024) return n + " B";
  const u = ["KB","MB","GB","TB"];
  let i = -1;
  do { n /= 1024; i++; } while(n >= 1024 && i < u.length-1);
  return n.toFixed(n >= 10 || i === 0 ? 1 : 2) + " " + u[i];
}
function fmtSec(s){
  s = Math.max(0, Math.floor(s||0));
  const h = Math.floor(s/3600), m = Math.floor((s%3600)/60), ss = s%60;
  if(h) return h + "h " + m + "m";
  if(m) return m + "m " + ss + "s";
  return ss + "s";
}
function esc(s){
  return String(s??"").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
}
function phoneText(x){
  const name = x.device_name || (x.device_info && x.device_info.device_name) || "";
  const model = x.model || (x.device_info && x.device_info.model) || "";
  const os = x.os || (x.device_info && x.device_info.os) || "";
  const main = [name || model || "Unknown device", os].filter(Boolean).join(" · ");
  return esc(main);
}
function phonePlain(x){
  const name = x.device_name || (x.device_info && x.device_info.device_name) || "";
  const model = x.model || (x.device_info && x.device_info.model) || "";
  return name || model || "device";
}
function batteryText(x){
  const pct = x.battery_pct ?? (x.device_info && x.device_info.battery_pct);
  if(pct === undefined || pct === null || pct === "") return "<span class=\"muted\">—</span>";
  const n = Number(pct);
  const ch = x.charging ?? (x.device_info && (x.device_info.charging === "true" || x.device_info.charging === "1"));
  const cls = !Number.isFinite(n) ? "" : (n <= 15 ? " crit" : (n <= 30 ? " low" : ""));
  const width = Number.isFinite(n) ? Math.max(0, Math.min(100, Math.round(n))) : 0;
  return "<span class=\"bat\"><span class=\"bat-bar" + cls + "\"><i style=\"width:" + width + "%\"></i></span>" +
    esc(Number.isFinite(n) ? (Math.round(n) + "%") : pct) + (ch ? " · charging" : "") + "</span>";
}
function sessionLatLon(x){
  const lat = Number(x.lat ?? (x.device_info && x.device_info.lat));
  const lon = Number(x.lon ?? (x.device_info && x.device_info.lon));
  if(!Number.isFinite(lat) || !Number.isFinite(lon)) return null;
  if(Math.abs(lat) > 90 || Math.abs(lon) > 180) return null;
  return {lat, lon};
}
function locText(x){
  const ll = sessionLatLon(x);
  if(!ll) return "<span class=\"muted\">—</span>";
  return "<span class=\"mono\">" + esc(ll.lat.toFixed(4)) + ", " + esc(ll.lon.toFixed(4)) + "</span>";
}
function rowActive(x){
  const sel = selectedSessionId === x.session_id ? " class=\"sel\"" : "";
  return "<tr" + sel + " onclick=\"selectSession(" + x.session_id + ")\">" +
    "<td class=\"mono\">" + x.session_id + "</td>" +
    "<td>" + phoneText(x) + "</td>" +
    "<td>" + batteryText(x) + "</td>" +
    "<td>" + locText(x) + "</td>" +
    "<td class=\"mono\">" + esc(x.client_ip) + "</td>" +
    "<td class=\"mono\">" + esc(x.remote_addr) + "</td>" +
    "<td>" + fmtBytes(x.upstream_bytes) + " ↑<br>" + fmtBytes(x.downstream_bytes) + " ↓</td>" +
    "<td>" + fmtSec(x.duration_seconds) + "</td>" +
    "<td><button onclick=\"event.stopPropagation();kick(" + x.session_id + ",0)\">Disc</button> " +
    "<button class=\"danger\" onclick=\"event.stopPropagation();kick(" + x.session_id + ",15)\">Block</button></td></tr>";
}
function rowClosed(x){
  const sel = selectedSessionId === x.session_id ? " class=\"sel\"" : "";
  return "<tr" + sel + " onclick=\"selectSession(" + x.session_id + ")\">" +
    "<td class=\"mono\">" + x.session_id + "</td>" +
    "<td>" + phoneText(x) + "</td>" +
    "<td class=\"mono\">" + esc(x.client_ip) + "</td>" +
    "<td>" + fmtBytes(x.upstream_bytes) + " / " + fmtBytes(x.downstream_bytes) + "</td>" +
    "<td>" + fmtSec(x.duration_seconds) + "</td></tr>";
}
async function kick(id, blockMin){
  if(!id) return;
  await fetch("/api/disconnect?session_id=" + id + "&block_minutes=" + (blockMin||0), {method:"POST"});
}
function selectSession(id){
  selectedSessionId = id;
  renderDetails();
  refreshTables();
  const x = sessions[id];
  const ll = x && sessionLatLon(x);
  if(ll && map) map.panTo([ll.lat, ll.lon], {animate:true});
  const panel = document.getElementById("inspector");
  panel.classList.remove("flash");
  void panel.offsetWidth;
  panel.classList.add("flash");
}
function pinIcon(){
  return L.divIcon({ className: "", html: "<div class=\"pin\"></div>", iconSize: [16,16], iconAnchor: [8,8], popupAnchor: [0,-10] });
}
function initMap(){
  if(map) return;
  map = L.map("map", { worldCopyJump:true, zoomControl:true }).setView([20, 0], 2);
  L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap &copy; CARTO"
  }).addTo(map);
  setTimeout(function(){ map.invalidateSize(); }, 80);
}
function updateMap(active){
  initMap();
  const seen = {};
  const pts = [];
  (active||[]).forEach(function(x){
    const ll = sessionLatLon(x);
    if(!ll) return;
    const id = String(x.session_id);
    seen[id] = true;
    pts.push([ll.lat, ll.lon]);
    const html = "<strong>#" + esc(id) + " · " + esc(phonePlain(x)) + "</strong><br>" +
      batteryText(x) + "<br><span class=\"mono\">" + esc(ll.lat.toFixed(5)) + ", " + esc(ll.lon.toFixed(5)) + "</span>";
    if(mapMarkers[id]){
      mapMarkers[id].setLatLng([ll.lat, ll.lon]);
      mapMarkers[id].setPopupContent(html);
    } else {
      mapMarkers[id] = L.marker([ll.lat, ll.lon], {icon: pinIcon()}).addTo(map).bindPopup(html);
      mapMarkers[id].on("click", function(){ selectSession(x.session_id); });
    }
  });
  Object.keys(mapMarkers).forEach(function(id){
    if(!seen[id]){ map.removeLayer(mapMarkers[id]); delete mapMarkers[id]; }
  });
  const n = Object.keys(mapMarkers).length;
  document.getElementById("mapHint").textContent = n ? (n + " device" + (n===1?"":"s") + " with live GPS") : "No GPS fixes yet — connect a client with Always location";
  if(pts.length && !mapFitted){
    map.fitBounds(pts, { padding:[36,36], maxZoom:12 });
    mapFitted = true;
  }
  if(!pts.length) mapFitted = false;
  setTimeout(function(){ map && map.invalidateSize(); }, 40);
}
function renderDetails(){
  const x = sessions[selectedSessionId];
  const empty = document.getElementById("detailEmpty");
  const body = document.getElementById("detailContent");
  if(!x){
    empty.style.display = "grid";
    body.style.display = "none";
    document.getElementById("inspectorSub").textContent = "Select a device";
    return;
  }
  empty.style.display = "none";
  body.style.display = "block";
  document.getElementById("inspectorSub").textContent = phonePlain(x);
  document.getElementById("dSession").textContent = x.session_id;
  document.getElementById("dClientId").textContent = x.client_id || (x.device_info && x.device_info.client_id) || "—";
  document.getElementById("dPhone").innerHTML = phoneText(x);
  document.getElementById("dBattery").innerHTML = batteryText(x);
  document.getElementById("dLoc").innerHTML = locText(x);
  document.getElementById("dTelAt").textContent = x.telemetry_at || (x.device_info && x.device_info.updated_at) || "—";
  document.getElementById("dClient").textContent = x.client_ip || "—";
  document.getElementById("dRemote").textContent = x.remote_addr || "—";
  document.getElementById("dConnected").textContent = x.connected_at || "—";
  document.getElementById("dDuration").textContent = fmtSec(x.duration_seconds);
  document.getElementById("dTraffic").textContent = "↑ " + fmtBytes(x.upstream_bytes) + "  ·  ↓ " + fmtBytes(x.downstream_bytes) +
    "  ·  " + (x.upstream_packets||0) + "/" + (x.downstream_packets||0) + " pkts";
  if(x.device_info){
    const keys = Object.keys(x.device_info);
    document.getElementById("dDevice").innerHTML = keys.length
      ? keys.map(function(k){ return "<span class=\"pill\">" + esc(k) + ": " + esc(x.device_info[k]) + "</span>"; }).join("")
      : "<span class=\"muted\">—</span>";
  } else {
    document.getElementById("dDevice").innerHTML = "<span class=\"muted\">—</span>";
  }
  const dns = (x.dns_recent||[]);
  document.getElementById("dDns").innerHTML = dns.length
    ? dns.map(function(v){ return "<div class=\"dns-item\">" + esc(v) + "</div>"; }).join("")
    : "<div class=\"muted\">No DNS records yet.</div>";
}
function refreshTables(){
  const d = window.__lastSnap;
  if(!d) return;
  const activeBody = document.querySelector("#activeTbl tbody");
  const closedBody = document.querySelector("#closedTbl tbody");
  const active = (d.active||[]).slice(0,200);
  const closed = (d.recent_closed||[]).slice(0,200);
  activeBody.innerHTML = active.length ? active.map(rowActive).join("") :
    "<tr class=\"empty-row\"><td colspan=\"9\">No devices connected</td></tr>";
  closedBody.innerHTML = closed.length ? closed.map(rowClosed).join("") :
    "<tr class=\"empty-row\"><td colspan=\"5\">No recent disconnects</td></tr>";
}
function applySnap(d){
  window.__lastSnap = d;
  document.getElementById("active").textContent = d.active_count;
  document.getElementById("activeSub").textContent = (d.active_count===1 ? "device online" : "devices online");
  document.getElementById("total").textContent = d.total_sessions;
  document.getElementById("auth").textContent = d.auth_failures;
  document.getElementById("up").textContent = fmtBytes(d.total_up_bytes);
  document.getElementById("down").textContent = fmtBytes(d.total_down_bytes);
  document.getElementById("runtimeChip").textContent = fmtSec(d.uptime_seconds);
  document.getElementById("memVal").textContent = fmtBytes(d.mem_alloc_bytes);
  document.getElementById("memSub").textContent = d.go_routines + " goroutines";
  document.getElementById("tunLabel").textContent = d.tun_ready ? "ready" : "down";
  document.getElementById("tunChip").style.borderColor = d.tun_ready ? "rgba(61,222,154,0.35)" : "rgba(255,107,122,0.35)";
  if(prev){
    const dt = Math.max(1, (new Date(d.now) - new Date(prev.now)) / 1000);
    document.getElementById("upRate").textContent = fmtBytes((d.total_up_bytes - prev.total_up_bytes) / dt) + "/s";
    document.getElementById("downRate").textContent = fmtBytes((d.total_down_bytes - prev.total_down_bytes) / dt) + "/s";
  }
  prev = d;
  sessions = {};
  (d.active||[]).forEach(function(s){ sessions[s.session_id] = s; });
  (d.recent_closed||[]).forEach(function(s){ if(!sessions[s.session_id]) sessions[s.session_id] = s; });
  if(selectedSessionId === null && d.active && d.active.length > 0) selectedSessionId = d.active[0].session_id;
  if(selectedSessionId !== null && !sessions[selectedSessionId] && d.active && d.active.length) selectedSessionId = d.active[0].session_id;
  refreshTables();
  renderDetails();
  updateMap(d.active||[]);
}
function setLive(ok, msg){
  document.getElementById("liveDot").className = "dot" + (ok ? "" : " off");
  document.getElementById("liveLabel").textContent = msg;
}
function connectSSE(){
  if(es){ try { es.close(); } catch(_){} }
  es = new EventSource("/api/stream");
  es.addEventListener("metrics", function(ev){
    try { applySnap(JSON.parse(ev.data)); setLive(true, "Live"); }
    catch(e) { setLive(false, "Parse error"); }
  });
  es.onerror = function(){
    setLive(false, "Reconnecting…");
    es.close();
    setTimeout(connectSSE, 2000);
  };
}
initMap();
connectSSE();
</script>
</body>
</html>
`
