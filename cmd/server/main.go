package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gorilla/websocket"
)

type Browser struct {
	ctx     context.Context
	cancel  context.CancelFunc
	profile string
	mu      sync.Mutex
}

type App struct {
	mu       sync.Mutex
	browsers map[string]*Browser
	profiles string
}

type Cmd struct {
	Type   string  `json:"type"`
	URL    string  `json:"url,omitempty"`
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Button string  `json:"button,omitempty"`
	Key    string  `json:"key,omitempty"`
	Text   string  `json:"text,omitempty"`
	DeltaX float64 `json:"deltaX,omitempty"`
	DeltaY float64 `json:"deltaY,omitempty"`
	Event  string  `json:"event,omitempty"`
}

var up = websocket.Upgrader{
	ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	p := getenv("PROFILE_DIR", "./profiles")
	_ = os.MkdirAll(p, 0700)
	a := &App{browsers: map[string]*Browser{}, profiles: p}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.home)
	mux.HandleFunc("/browse", a.browse)
	mux.HandleFunc("/ws/", a.ws)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	s := &http.Server{Addr: getenv("LISTEN_ADDR", ":"+getenv("PORT", "8080")), Handler: security(mux),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Printf("browser proxy: %s", s.Addr)
	log.Fatal(s.ListenAndServe())
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="ko"><head><meta name="viewport" content="width=device-width,initial-scale=1">
	<meta name="theme-color" content="#090d18"><title>Relay Browser</title><style>
	:root{font-family:Inter,system-ui,-apple-system,sans-serif;color:#eef2ff;background:#090d18}
	*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:20px;background:radial-gradient(circle at 20% 0,#233b72 0,transparent 38%),#090d18}
	main{width:min(680px,100%);padding:clamp(24px,6vw,52px);border:1px solid #2a3a62;border-radius:28px;background:#10182bdb;box-shadow:0 24px 80px #0008}
	.logo{display:inline-flex;align-items:center;gap:9px;color:#9eb5ff;font-weight:800;letter-spacing:.08em;text-transform:uppercase;font-size:12px}
	.logo i{width:10px;height:10px;border-radius:50%;background:#6d8cff;box-shadow:0 0 18px #6d8cff}
	h1{font-size:clamp(30px,7vw,52px);line-height:1.02;margin:18px 0 12px;letter-spacing:-.05em}p{color:#aab6d3;line-height:1.6;margin:0 0 25px}
	form{display:flex;gap:10px}input,button{font:inherit;border-radius:13px;padding:14px 16px;border:1px solid #3b4f7d}input{min-width:0;flex:1;color:#fff;background:#080d19;outline:none}input:focus{border-color:#8aa3ff;box-shadow:0 0 0 3px #718dff22}button{color:#fff;background:#5b78ff;font-weight:750;cursor:pointer;white-space:nowrap}button:hover{background:#708aff}
	.quick{display:flex;flex-wrap:wrap;gap:8px;margin-top:18px}.quick button{padding:9px 12px;font-size:13px;background:#172441;border-color:#304674;color:#cbd7ff}.quick button:hover{background:#22345d}
	.note{margin-top:28px;padding-top:18px;border-top:1px solid #263555;font-size:12px;color:#8090b4}.note b{color:#b9c7eb}@media(max-width:540px){body{padding:12px}main{border-radius:22px;padding:25px 20px}form{display:grid;grid-template-columns:1fr}form button{width:100%}}
	</style></head><body><main><div class="logo"><i></i> Relay Browser</div><h1>웹을 바로 열어보세요.</h1>
	<p>입력한 링크를 서버의 Chromium에서 실행합니다. 로그인과 동적 페이지를 지원하는 원격 브라우저입니다.</p>
	<form action="/browse"><input id="url" name="url" type="url" inputmode="url" autocomplete="url" placeholder="https://www.youtube.com" required><button type="submit">열기</button></form>
	<div class="quick"><button type="button" onclick="openSite('https://www.youtube.com')">YouTube</button><button type="button" onclick="openSite('https://www.instagram.com')">Instagram</button><button type="button" onclick="openSite('https://discord.com/app')">Discord</button><button type="button" onclick="openSite('https://www.facebook.com')">Facebook</button></div>
	<div class="note"><b>지원 안내</b><br>화면은 JPEG 스트리밍으로 전달됩니다. 첫 실행은 Chromium을 시작하느라 조금 느릴 수 있습니다.</div>
	<script>function openSite(u){document.getElementById('url').value=u;document.querySelector('form').submit()}</script></main></body></html>`)
}

func (a *App) browse(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil || !allowed(u) {
		http.Error(w, "허용되지 않은 주소", 403)
		return
	}
	id := randomID()
	profile := filepath.Join(a.profiles, id)
	_ = os.MkdirAll(profile, 0700)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true), chromedp.Flag("disable-gpu", true), chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.UserDataDir(profile), chromedp.WindowSize(1280, 800))
	if chrome := os.Getenv("CHROME_BIN"); chrome != "" {
		opts = append(opts, chromedp.ExecPath(chrome))
	}
	if os.Getenv("TERMUX") == "1" {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
	}
	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bctx, bcancel := chromedp.NewContext(ctx)
	// Keep allocator cleanup through the browser context lifecycle.
	_ = cancel
	bootCtx, bootCancel := context.WithTimeout(bctx, 15*time.Second)
	if err := chromedp.Run(bootCtx, chromedp.Navigate("about:blank")); err != nil {
		bootCancel()
		bcancel()
		http.Error(w, "Chromium 시작 실패: "+err.Error(), 502)
		return
	}
	bootCancel()

	b := &Browser{ctx: bctx, cancel: bcancel, profile: profile}
	a.mu.Lock()
	a.browsers[id] = b
	a.mu.Unlock()
	go func() {
		navCtx, navCancel := context.WithTimeout(bctx, 45*time.Second)
		defer navCancel()
		_ = chromedp.Run(navCtx, chromedp.Navigate(u.String()))
	}()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, strings.Replace(viewer, "__SESSION__", id, 1))
}

const viewer = `<!doctype html><html lang="ko"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Remote Browser</title><style>
*{box-sizing:border-box}html,body{margin:0;height:100%;background:#101216;color:#fff;font-family:system-ui}
#bar{height:52px;display:flex;gap:5px;padding:6px;background:#191c21}button,#url{border:1px solid #383f49;border-radius:9px;background:#252a31;color:#fff;padding:8px 11px;font:inherit}
#url{flex:1;min-width:0;background:#0e1116}.wrap{height:calc(100% - 52px);position:relative;background:#000;overflow:hidden}
#screen{width:100%;height:100%;object-fit:contain;touch-action:none}.hint{position:absolute;bottom:8px;right:8px;background:#000b;padding:5px 8px;border-radius:7px;font-size:12px}
</style><div id="bar"><button onclick="cmd({type:'back'})">←</button><button onclick="cmd({type:'forward'})">→</button>
<button onclick="cmd({type:'reload'})">↻</button><input id="url"><button onclick="go()">이동</button></div>
<div class="wrap"><img id="screen"><div class="hint" id="status">연결 중…</div></div>
<script>
	const id='__SESSION__', img=document.getElementById('screen'), url=document.getElementById('url'), status=document.getElementById('status');
const ws=new WebSocket((location.protocol==='https:'?'wss://':'ws://')+location.host+'/ws/'+id);
let W=1280,H=800;
ws.onopen=()=>status.textContent='연결됨';ws.onclose=()=>status.textContent='연결 종료';ws.onerror=()=>status.textContent='연결 오류';
ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.type==='frame'){W=m.width;H=m.height;img.src='data:image/jpeg;base64,'+m.data}};
function cmd(x){if(ws.readyState===1)ws.send(JSON.stringify(x))}
function go(){cmd({type:'navigate',url:url.value})}
function point(e){const r=img.getBoundingClientRect();return{x:(e.clientX-r.left)*W/r.width,y:(e.clientY-r.top)*H/r.height}}
img.onpointerdown=e=>{img.setPointerCapture(e.pointerId);let p=point(e);cmd({type:'mouse',event:'down',x:p.x,y:p.y,button:'left'})};
img.onpointerup=e=>{let p=point(e);cmd({type:'mouse',event:'up',x:p.x,y:p.y,button:'left'})};
img.onpointermove=e=>{if(e.buttons){let p=point(e);cmd({type:'mouse',event:'move',x:p.x,y:p.y,button:'left'})}};
img.onwheel=e=>{e.preventDefault();cmd({type:'wheel',deltaX:e.deltaX,deltaY:e.deltaY})};
document.onkeydown=e=>{if(e.target===url){if(e.key==='Enter')go();return}e.preventDefault();cmd({type:'key',key:e.key,text:e.key.length===1?e.key:''})};
</script></html>`

func (a *App) ws(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/ws/")
	a.mu.Lock()
	b := a.browsers[id]
	a.mu.Unlock()
	if b == nil {
		http.Error(w, "session not found", 404)
		return
	}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	go stream(ctx, b, c)
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var x Cmd
		if json.Unmarshal(data, &x) != nil {
			continue
		}
		dispatch(b, x)
	}
}

func stream(ctx context.Context, b *Browser, c *websocket.Conn) {
	ch := make(chan *page.EventScreencastFrame)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
			_ = chromedp.Run(lctx, page.StartScreencast().
				WithFormat("jpeg").WithQuality(65).WithMaxWidth(1280).WithMaxHeight(800).WithEveryNthFrame(1))
		}
	}()
	chromedp.ListenTarget(lctx, func(ev any) {
		if f, ok := ev.(*page.EventScreencastFrame); ok {
			select {
			case ch <- f:
			default:
			}
		}
	})
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-ch:
			msg := Frame{Type: "frame", Data: f.Data, Width: 1280, Height: 800}
			_ = c.WriteJSON(msg)
			_ = chromedp.Run(lctx, page.ScreencastFrameAck(f.SessionID))
		}
	}
}

type Frame struct {
	Type, Data    string
	Width, Height int
}

func dispatch(b *Browser, x Cmd) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch x.Type {
	case "navigate":
		if u, e := url.Parse(x.URL); e == nil && allowed(u) {
			_ = chromedp.Run(b.ctx, chromedp.Navigate(u.String()))
		}
	case "reload":
		_ = chromedp.Run(b.ctx, chromedp.Reload())
	case "back":
		_ = chromedp.Run(b.ctx, chromedp.Evaluate(`history.back()`, nil))
	case "forward":
		_ = chromedp.Run(b.ctx, chromedp.Evaluate(`history.forward()`, nil))
	case "mouse":
		typ := input.None
		if x.Button == "left" {
			typ = input.Left
		}
		et := input.MouseMoved
		if x.Event == "down" {
			et = input.MousePressed
		} else if x.Event == "up" {
			et = input.MouseReleased
		}
		_ = chromedp.Run(b.ctx, input.DispatchMouseEvent(et, x.X, x.Y).WithButton(typ))
	case "key":
		_ = chromedp.Run(b.ctx, input.DispatchKeyEvent(input.KeyDown).WithKey(x.Key).WithText(x.Text))
	}
}

func allowed(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "youtube.com" || strings.HasSuffix(h, ".youtube.com") ||
		h == "youtu.be" || strings.HasSuffix(h, ".googlevideo.com") || strings.HasSuffix(h, ".ytimg.com") ||
		h == "discord.com" || strings.HasSuffix(h, ".discord.com") ||
		h == "instagram.com" || strings.HasSuffix(h, ".instagram.com") ||
		h == "facebook.com" || strings.HasSuffix(h, ".facebook.com") || strings.HasSuffix(h, ".facebook.net")
}
func randomID() string { b := make([]byte, 32); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func security(n http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		n.ServeHTTP(w, r)
	})
}
