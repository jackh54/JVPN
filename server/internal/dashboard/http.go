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
  <style>
    :root { color-scheme: dark; --bg:#0b0f17; --panel:#11192a; --line:#1f2b43; --muted:#99a7c2; --text:#e7edf7; --accent:#3d7eff; }
    * { box-sizing: border-box; }
    body { margin:0; font:14px/1.45 ui-sans-serif,system-ui,-apple-system,Segoe UI,sans-serif; background:radial-gradient(1200px 600px at 10% -10%, #152038 0%, var(--bg) 55%); color:var(--text); min-height:100vh; }
    .wrap { max-width:1280px; margin:24px auto; padding:0 16px 40px; }
    .top { display:flex; align-items:baseline; justify-content:space-between; gap:12px; margin-bottom:16px; flex-wrap:wrap; }
    h1 { margin:0; font-size:22px; letter-spacing:-0.02em; }
    .live { font-size:12px; color:var(--muted); display:flex; align-items:center; gap:8px; }
    .dot { width:8px; height:8px; border-radius:50%; background:#3ecf8e; box-shadow:0 0 0 3px rgba(62,207,142,.18); }
    .dot.off { background:#c45c5c; box-shadow:0 0 0 3px rgba(196,92,92,.18); }
    .grid { display:grid; grid-template-columns: repeat(auto-fit,minmax(170px,1fr)); gap:12px; }
    .card { background:var(--panel); border:1px solid var(--line); border-radius:12px; padding:14px; }
    .label { color:var(--muted); font-size:11px; text-transform:uppercase; letter-spacing:.08em; }
    .value { margin-top:6px; font-size:22px; font-weight:700; letter-spacing:-0.02em; }
    .muted { color:var(--muted); font-size:12px; }
    .row { display:grid; grid-template-columns:1.2fr .8fr; gap:12px; margin-top:16px; }
    .pill { display:inline-block; padding:2px 8px; border-radius:6px; border:1px solid #2e3e62; color:#b8c7e6; font-size:11px; margin:0 6px 6px 0; }
    .kv { display:grid; grid-template-columns:140px 1fr; gap:8px; font-size:12px; margin:8px 0; }
    .kv .k { color:var(--muted); }
    .dns-list { max-height:220px; overflow:auto; border:1px solid var(--line); border-radius:8px; padding:8px; background:#0d1422; }
    .dns-item { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; font-size:12px; padding:3px 0; border-bottom:1px solid #1a2740; }
    .dns-item:last-child { border-bottom:none; }
    button { background:#1a2a49; color:#dbe7ff; border:1px solid #2e4575; border-radius:8px; padding:5px 9px; cursor:pointer; font-size:12px; }
    button.danger { border-color:#6b3040; background:#2a1620; color:#ffd5dc; }
    button:hover { filter:brightness(1.08); }
    table { width:100%; border-collapse:collapse; }
    th,td { text-align:left; border-bottom:1px solid var(--line); padding:8px 6px; font-size:12px; vertical-align:top; }
    th { color:#9fb0d1; font-weight:600; }
    .mono { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; }
    tr.sel { background:rgba(61,126,255,.08); }
    tr { cursor:pointer; }
    .bat { font-variant-numeric: tabular-nums; }
    @media (max-width: 980px) { .row { grid-template-columns:1fr; } }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="top">
      <h1>JVPN Admin</h1>
      <div class="live"><span class="dot" id="liveDot"></span><span id="liveLabel">Connecting…</span></div>
    </div>
    <div class="grid">
      <div class="card"><div class="label">Active</div><div class="value" id="active">-</div></div>
      <div class="card"><div class="label">Total Sessions</div><div class="value" id="total">-</div></div>
      <div class="card"><div class="label">Auth Failures</div><div class="value" id="auth">-</div></div>
      <div class="card"><div class="label">Upload</div><div class="value" id="up">-</div><div class="muted" id="upRate"></div></div>
      <div class="card"><div class="label">Download</div><div class="value" id="down">-</div><div class="muted" id="downRate"></div></div>
      <div class="card"><div class="label">Uptime</div><div class="value" id="runtime">-</div><div class="muted" id="mem"></div></div>
    </div>
    <div class="row">
      <div class="card">
        <div class="label">Connected Devices</div>
        <table id="activeTbl">
          <thead><tr><th>Session</th><th>Phone</th><th>Battery</th><th>Location</th><th>Tunnel</th><th>Remote</th><th>Traffic</th><th>Duration</th><th></th></tr></thead>
          <tbody></tbody>
        </table>
      </div>
      <div class="card">
        <div class="label">Recent Disconnections</div>
        <table id="closedTbl">
          <thead><tr><th>Session</th><th>Phone</th><th>Tunnel</th><th>Traffic</th><th>Duration</th></tr></thead>
          <tbody></tbody>
        </table>
      </div>
    </div>
    <div class="card" style="margin-top:16px;">
      <div class="label">Session Detail</div>
      <div id="detailEmpty" class="muted" style="margin-top:8px;">Select a device row.</div>
      <div id="detailContent" style="display:none;">
        <div class="kv"><div class="k">Session</div><div id="dSession" class="mono">-</div></div>
        <div class="kv"><div class="k">Client ID</div><div id="dClientId" class="mono">-</div></div>
        <div class="kv"><div class="k">Device</div><div id="dPhone">-</div></div>
        <div class="kv"><div class="k">Battery</div><div id="dBattery">-</div></div>
        <div class="kv"><div class="k">Location</div><div id="dLoc">-</div></div>
        <div class="kv"><div class="k">Telemetry</div><div id="dTelAt">-</div></div>
        <div class="kv"><div class="k">Tunnel IP</div><div id="dClient" class="mono">-</div></div>
        <div class="kv"><div class="k">Remote</div><div id="dRemote" class="mono">-</div></div>
        <div class="kv"><div class="k">Connected</div><div id="dConnected">-</div></div>
        <div class="kv"><div class="k">Duration</div><div id="dDuration">-</div></div>
        <div class="kv"><div class="k">Traffic</div><div id="dTraffic">-</div></div>
        <div class="kv"><div class="k">Raw device</div><div id="dDevice">-</div></div>
        <div class="kv"><div class="k">DNS history</div><div><div class="dns-list" id="dDns"></div></div></div>
        <div style="margin-top:12px; display:flex; gap:8px; flex-wrap:wrap;">
          <button onclick="kick(selectedSessionId,0)">Disconnect</button>
          <button class="danger" onclick="kick(selectedSessionId,15)">Disconnect + block 15m</button>
        </div>
      </div>
    </div>
  </div>
<script>
let prev = null;
let sessions = {};
let selectedSessionId = null;
let es = null;

function fmtBytes(n){ n=Number(n)||0; if(n<1024) return n+" B"; const u=["KB","MB","GB","TB"]; let i=-1; do{n/=1024;i++;}while(n>=1024&&i<u.length-1); return n.toFixed(2)+" "+u[i]; }
function fmtSec(s){ s=Math.max(0,Math.floor(s||0)); const h=Math.floor(s/3600), m=Math.floor((s%3600)/60), ss=s%60; return (h?(h+"h "):"") + m+"m "+ss+"s"; }
function esc(s){ return String(s??'').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function phoneText(x){
  const name = x.device_name || (x.device_info&&x.device_info.device_name) || '';
  const model = x.model || (x.device_info&&x.device_info.model) || '';
  const os = x.os || (x.device_info&&x.device_info.os) || '';
  const main = [name||model||'device', os].filter(Boolean).join(' · ');
  return esc(main || '-');
}
function batteryText(x){
  const pct = x.battery_pct ?? (x.device_info&&x.device_info.battery_pct);
  if(pct===undefined||pct===null||pct==='') return '-';
  const n = Number(pct);
  const ch = x.charging ?? (x.device_info&&(x.device_info.charging==='true'||x.device_info.charging==='1'));
  return '<span class="bat">'+esc(Number.isFinite(n)?(Math.round(n)+'%'):pct)+(ch?' ⚡':'')+'</span>';
}
function locText(x){
  const lat = x.lat ?? (x.device_info&&x.device_info.lat);
  const lon = x.lon ?? (x.device_info&&x.device_info.lon);
  if(lat===undefined||lat===null||lat===''||lon===undefined||lon===null||lon==='') return '-';
  return '<span class="mono">'+esc(Number(lat).toFixed(5))+', '+esc(Number(lon).toFixed(5))+'</span>';
}
function rowActive(x){
  const sel = selectedSessionId===x.session_id ? ' class="sel"' : '';
  return '<tr'+sel+' onclick="selectSession('+x.session_id+')"><td class="mono">'+x.session_id+'</td><td>'+phoneText(x)+'</td><td>'+batteryText(x)+'</td><td>'+locText(x)+'</td><td class="mono">'+esc(x.client_ip)+'</td><td class="mono">'+esc(x.remote_addr)+'</td><td>'+fmtBytes(x.upstream_bytes)+' / '+fmtBytes(x.downstream_bytes)+'</td><td>'+fmtSec(x.duration_seconds)+'</td><td><button onclick="event.stopPropagation();kick('+x.session_id+',0)">Disc</button> <button class="danger" onclick="event.stopPropagation();kick('+x.session_id+',15)">Block</button></td></tr>';
}
function rowClosed(x){
  const sel = selectedSessionId===x.session_id ? ' class="sel"' : '';
  return '<tr'+sel+' onclick="selectSession('+x.session_id+')"><td class="mono">'+x.session_id+'</td><td>'+phoneText(x)+'</td><td class="mono">'+esc(x.client_ip)+'</td><td>'+fmtBytes(x.upstream_bytes)+' / '+fmtBytes(x.downstream_bytes)+'</td><td>'+fmtSec(x.duration_seconds)+'</td></tr>';
}
async function kick(id, blockMin){
  if(!id) return;
  await fetch('/api/disconnect?session_id='+id+'&block_minutes='+(blockMin||0),{method:'POST'});
}
function selectSession(id){ selectedSessionId=id; renderDetails(); refreshTables(); }
function renderDetails(){
  const x = sessions[selectedSessionId];
  const empty = document.getElementById('detailEmpty');
  const body = document.getElementById('detailContent');
  if(!x){ empty.style.display='block'; body.style.display='none'; return; }
  empty.style.display='none'; body.style.display='block';
  document.getElementById('dSession').textContent = x.session_id;
  document.getElementById('dClientId').textContent = x.client_id || (x.device_info&&x.device_info.client_id) || '-';
  document.getElementById('dPhone').innerHTML = phoneText(x);
  document.getElementById('dBattery').innerHTML = batteryText(x);
  document.getElementById('dLoc').innerHTML = locText(x);
  document.getElementById('dTelAt').textContent = x.telemetry_at || (x.device_info&&x.device_info.updated_at) || '-';
  document.getElementById('dClient').textContent = x.client_ip || '-';
  document.getElementById('dRemote').textContent = x.remote_addr || '-';
  document.getElementById('dConnected').textContent = x.connected_at || '-';
  document.getElementById('dDuration').textContent = fmtSec(x.duration_seconds);
  document.getElementById('dTraffic').textContent = 'up '+fmtBytes(x.upstream_bytes)+' / down '+fmtBytes(x.downstream_bytes)+' | pkts '+x.upstream_packets+'/'+x.downstream_packets;
  if(x.device_info){
    document.getElementById('dDevice').innerHTML = Object.keys(x.device_info).map(k => '<span class="pill">'+esc(k)+': '+esc(x.device_info[k])+'</span>').join(' ') || '-';
  } else {
    document.getElementById('dDevice').textContent = '-';
  }
  const dns = (x.dns_recent||[]);
  document.getElementById('dDns').innerHTML = dns.length ? dns.map(v => '<div class="dns-item">'+esc(v)+'</div>').join('') : '<div class="muted">No DNS records yet.</div>';
}
function refreshTables(){
  const d = window.__lastSnap;
  if(!d) return;
  document.querySelector('#activeTbl tbody').innerHTML = (d.active||[]).slice(0,200).map(rowActive).join('');
  document.querySelector('#closedTbl tbody').innerHTML = (d.recent_closed||[]).slice(0,200).map(rowClosed).join('');
}
function applySnap(d){
  window.__lastSnap = d;
  document.getElementById('active').textContent = d.active_count;
  document.getElementById('total').textContent = d.total_sessions;
  document.getElementById('auth').textContent = d.auth_failures;
  document.getElementById('up').textContent = fmtBytes(d.total_up_bytes);
  document.getElementById('down').textContent = fmtBytes(d.total_down_bytes);
  document.getElementById('runtime').textContent = fmtSec(d.uptime_seconds);
  document.getElementById('mem').textContent = 'mem '+fmtBytes(d.mem_alloc_bytes)+' | goroutines '+d.go_routines+(d.tun_ready?' | tun ok':' | tun down');
  if(prev){
    const dt = Math.max(1, (new Date(d.now)-new Date(prev.now))/1000);
    document.getElementById('upRate').textContent = fmtBytes((d.total_up_bytes-prev.total_up_bytes)/dt)+'/s';
    document.getElementById('downRate').textContent = fmtBytes((d.total_down_bytes-prev.total_down_bytes)/dt)+'/s';
  }
  prev = d;
  sessions = {};
  (d.active||[]).forEach(s => sessions[s.session_id]=s);
  (d.recent_closed||[]).forEach(s => { if(!sessions[s.session_id]) sessions[s.session_id]=s; });
  if(selectedSessionId===null && d.active && d.active.length>0){ selectedSessionId=d.active[0].session_id; }
  refreshTables();
  renderDetails();
}
function setLive(ok, msg){
  document.getElementById('liveDot').className = 'dot'+(ok?'':' off');
  document.getElementById('liveLabel').textContent = msg;
}
function connectSSE(){
  if(es){ try{ es.close(); }catch(_){} }
  es = new EventSource('/api/stream');
  es.addEventListener('metrics', (ev) => {
    try { applySnap(JSON.parse(ev.data)); setLive(true, 'Live'); } catch(e) { setLive(false, 'Parse error'); }
  });
  es.onerror = () => {
    setLive(false, 'Reconnecting…');
    es.close();
    setTimeout(connectSSE, 2000);
  };
}
connectSSE();
</script>
</body>
</html>
`
