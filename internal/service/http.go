package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// ValidateListen refuses a non-loopback HTTP bind unless remote access was
// explicitly allowed. The status UI has no authentication.
func ValidateListen(address string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing unauthenticated non-loopback listen address %q (pass --allow-remote explicitly)", address)
}

// Handler returns the local status UI and its JSON API. The task board replaces
// this small status surface once per-workspace indexes are implemented.
func Handler(manager *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspaces": len(manager.Statuses())})
	})
	mux.HandleFunc("GET /api/workspaces", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, manager.Statuses())
	})
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		_, _ = io.WriteString(writer, statusHTML)
	})
	return mux
}

// Serve starts one HTTP server for every runtime managed by manager.
func Serve(ctx context.Context, address string, manager *Manager, output io.Writer) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if output != nil {
		fmt.Fprintf(output, "Docket serving http://%s\n", listener.Addr())
	}
	server := &http.Server{
		Handler:           Handler(manager),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

const statusHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Docket</title>
<style>
:root{color-scheme:light;--ground:#f2f5f6;--surface:#fff;--ink:#162126;--soft:#53656d;--line:#cfd8dc;--accent:#087d92;--good:#177245;--warn:#9b5b0d;--bad:#aa2936;--code:#e8edef}
@media(prefers-color-scheme:dark){:root{color-scheme:dark;--ground:#101719;--surface:#182125;--ink:#e3eaec;--soft:#93a5ac;--line:#304047;--accent:#4cc4d8;--good:#67d49b;--warn:#e6ad5b;--bad:#f17983;--code:#222e33}}
*{box-sizing:border-box}body{margin:0;background:var(--ground);color:var(--ink);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}main{width:min(100% - 32px,960px);margin:0 auto;padding:54px 0 80px}header{display:flex;align-items:end;justify-content:space-between;gap:24px;border-bottom:1px solid var(--line);padding-bottom:20px}h1{font-size:32px;line-height:1;margin:0 0 8px;letter-spacing:-.03em}header p{margin:0;color:var(--soft)}.live{font:12px ui-monospace,monospace;color:var(--accent);letter-spacing:.08em;text-transform:uppercase}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:14px;margin-top:24px}.card{background:var(--surface);border:1px solid var(--line);padding:18px}.card-head{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:14px}.name{font:600 17px ui-monospace,monospace}.state{font:11px ui-monospace,monospace;text-transform:uppercase;letter-spacing:.08em}.watching{color:var(--good)}.retrying{color:var(--warn)}.unavailable,.stopped{color:var(--bad)}.path{color:var(--soft);font:12px/1.45 ui-monospace,monospace;overflow-wrap:anywhere;min-height:35px}.metrics{display:grid;grid-template-columns:1fr 1fr;border-top:1px solid var(--line);margin-top:16px;padding-top:14px}.metric strong{display:block;font:600 20px ui-monospace,monospace}.metric span{color:var(--soft);font-size:12px}.error{color:var(--bad);background:var(--code);padding:10px;margin-top:14px;font:12px/1.45 ui-monospace,monospace;overflow-wrap:anywhere}.empty{border:1px dashed var(--line);padding:30px;margin-top:24px;color:var(--soft)}code{font-family:ui-monospace,monospace;background:var(--code);padding:2px 5px}.footer{color:var(--soft);font-size:12px;margin-top:20px}@media(max-width:600px){main{padding-top:30px}header{align-items:start;flex-direction:column}}
</style>
</head>
<body><main><header><div><h1>Docket</h1><p>The job travels with its context.</p></div><div class="live" id="service-state">service live</div></header><div id="workspaces"></div><p class="footer">This is the service status surface. The indexed board and task timeline arrive in the frontend phase.</p></main>
<script>
const root=document.getElementById('workspaces');
const esc=(v)=>String(v??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
async function refresh(){
 try{
  const response=await fetch('/api/workspaces',{cache:'no-store'}); if(!response.ok)throw new Error(response.status);
  const rows=await response.json(); document.getElementById('service-state').textContent='service live · '+rows.length+' workspace'+(rows.length===1?'':'s');
  if(!rows.length){root.innerHTML='<div class="empty">No workspaces registered. Run <code>docket workspace add .</code> inside a Docket workspace.</div>';return}
  root.innerHTML='<div class="grid">'+rows.map(w=>'<article class="card"><div class="card-head"><span class="name">'+esc(w.name)+'</span><span class="state '+esc(w.state)+'">'+esc(w.state)+'</span></div><div class="path">'+esc(w.path)+'</div><div class="metrics"><div class="metric"><strong>'+esc(w.event_count)+'</strong><span>events</span></div><div class="metric"><strong>'+esc(w.handler_count)+'</strong><span>handlers</span></div></div>'+(w.last_error?'<div class="error">'+esc(w.last_error)+'</div>':'')+'</article>').join('')+'</div>';
 }catch(error){document.getElementById('service-state').textContent='service disconnected'}
}
refresh();setInterval(refresh,2000);
</script></body></html>`
