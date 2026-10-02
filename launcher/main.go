// comfy-launcher — ComfyUI 런처 (GUI 대시보드 + 헤드리스 CLI, Go 표준 라이브러리만 사용)
//
// 헤드리스 모드:
//
//	comfy-launcher start|stop|restart|status|free [--json]
//
// GUI 모드 (내장 웹 대시보드):
//
//	comfy-launcher serve [--port 8280] [--open]
//
// 서버 상태/메모리(footprint·VRAM)/큐·진행상황을 실시간 표시한다.
// 진행률 이벤트는 브라우저가 ComfyUI의 /ws websocket을 직접 구독한다.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed dashboard/index.html
var dashboardFS embed.FS

const defaultComfyPort = 8188
const defaultDashPort = 8280

// ── 공통 유틸 ──────────────────────────────────────────────────────────────

func parseSizeMB(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	unit := s[len(s)-1]
	numStr := strings.TrimRight(s, "KMGkmgiBb ")
	val, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil {
		return 0
	}
	switch unit {
	case 'K', 'k':
		return val / 1024
	case 'G', 'g':
		return val * 1024
	default:
		return val
	}
}

func statFile(path string) (any, error) {
	return os.Stat(path)
}

func getJSON(url string, out any, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func postJSON(url string, body any) error {
	b, _ := json.Marshal(body)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ── ComfyUI 서버 제어 ───────────────────────────────────────────────────────

type launcher struct {
	comfyDir  string
	comfyPort int
}

func (l *launcher) baseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", l.comfyPort) }

func (l *launcher) healthy() bool {
	var v any
	return getJSON(l.baseURL()+"/system_stats", &v, 3*time.Second) == nil
}

type systemStats struct {
	Devices []struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		VRAMTotal int64  `json:"vram_total"`
		VRAMFree  int64  `json:"vram_free"`
	} `json:"devices"`
	System struct {
		ComfyUIVersion string `json:"comfyui_version"`
		PythonVersion  string `json:"python_version"`
		OS             string `json:"os"`
	} `json:"system"`
}

// ComfyUI /queue 항목은 배열: [number, prompt_id, prompt, extra, outputs_to_delete]
type queueResp struct {
	QueueRunning [][]any `json:"queue_running"`
	QueuePending [][]any `json:"queue_pending"`
}

// queueTitle — prompt_id(짧게)와 extra.meta.title(있으면)을 합친 표시용 제목.
func queueTitle(entry []any) string {
	if len(entry) < 2 {
		return "workflow"
	}
	id, _ := entry[1].(string)
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	if len(entry) >= 4 {
		if extra, ok := entry[3].(map[string]any); ok {
			if meta, ok := extra["meta"].(map[string]any); ok {
				if t, ok := meta["title"].(string); ok && t != "" {
					return t + " (" + short + ")"
				}
			}
		}
	}
	if short == "" {
		return "workflow"
	}
	return "workflow " + short
}

type historyEntry struct {
	PromptID string `json:"prompt_id"`
	Status   *struct {
		StatusStr string `json:"status_str"`
		Completed bool   `json:"completed"`
		Messages  []any  `json:"messages"`
	} `json:"status"`
	Outputs map[string]map[string]any `json:"outputs"`
}

// statusInfo — 대시보드/CLI가 공유하는 상태 스냅샷
type statusInfo struct {
	Running       bool    `json:"running"`
	PID           int     `json:"pid"`
	URL           string  `json:"url"`
	FootprintMB   float64 `json:"footprint_mb"`
	ComfyVersion  string  `json:"comfyui_version"`
	PythonVersion string  `json:"python_version"`
	Device        string  `json:"device"`
	VRAMTotalMB   float64 `json:"vram_total_mb"`
	VRAMFreeMB    float64 `json:"vram_free_mb"`
	QueueRunning  int     `json:"queue_running"`
	QueuePending  int     `json:"queue_pending"`
	RunningTitles []string `json:"running_titles"`
	PendingTitles []string `json:"pending_titles"`
	RecentDone    []string `json:"recent_done"`
}

func (l *launcher) snapshot() statusInfo {
	info := statusInfo{URL: l.baseURL(), RunningTitles: []string{}, PendingTitles: []string{}, RecentDone: []string{}}
	info.PID = findListeningPID(l.comfyPort)
	info.Running = info.PID != 0 && l.healthy()
	if !info.Running {
		return info
	}
	info.FootprintMB = physFootprintMB(info.PID)

	var ss systemStats
	if err := getJSON(l.baseURL()+"/system_stats", &ss, 3*time.Second); err == nil {
		info.ComfyVersion = ss.System.ComfyUIVersion
		info.PythonVersion = ss.System.PythonVersion
		if len(ss.Devices) > 0 {
			info.Device = ss.Devices[0].Name
			info.VRAMTotalMB = float64(ss.Devices[0].VRAMTotal) / 1e6
			info.VRAMFreeMB = float64(ss.Devices[0].VRAMFree) / 1e6
		}
	}
	var q queueResp
	if err := getJSON(l.baseURL()+"/queue", &q, 3*time.Second); err == nil {
		info.QueueRunning = len(q.QueueRunning)
		info.QueuePending = len(q.QueuePending)
		for _, e := range q.QueueRunning {
			info.RunningTitles = append(info.RunningTitles, queueTitle(e))
		}
		for _, e := range q.QueuePending {
			info.PendingTitles = append(info.PendingTitles, queueTitle(e))
		}
	}
	var hist map[string]historyEntry
	if err := getJSON(l.baseURL()+"/history?max_items=10", &hist, 3*time.Second); err == nil {
		// 최근 완료(status.completed) 항목의 prompt_id를 최대 5개까지
		for id, e := range hist {
			if e.Status != nil && e.Status.Completed {
				info.RecentDone = append(info.RecentDone, id)
			}
			if len(info.RecentDone) >= 5 {
				break
			}
		}
	}
	return info
}

func (l *launcher) start() error {
	if findListeningPID(l.comfyPort) != 0 && l.healthy() {
		fmt.Println("ComfyUI is already RUNNING on", l.baseURL())
		return nil
	}
	mainPy := filepath.Join(l.comfyDir, "main.py")
	if _, err := os.Stat(mainPy); err != nil {
		return fmt.Errorf("main.py not found in %s", l.comfyDir)
	}
	pythonBin := venvPython(l.comfyDir)
	logPath := filepath.Join(l.comfyDir, "comfyui_service.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Println("Launching ComfyUI in background (logging to", logPath + ")...")
	cmd := exec.Command(pythonBin, "main.py", "--listen", "127.0.0.1", "--port", strconv.Itoa(l.comfyPort))
	cmd.Dir = l.comfyDir
	cmd.Stdout = f
	cmd.Stderr = f
	detach(cmd)
	cmd.Env = append(os.Environ(), platformPythonEnv()...)
	if err := cmd.Start(); err != nil {
		return err
	}

	t0 := time.Now()
	for time.Since(t0) < 60*time.Second {
		time.Sleep(2 * time.Second)
		if l.healthy() {
			pid := findListeningPID(l.comfyPort)
			fmt.Printf("[SUCCESS] ComfyUI started in %.1fs. PID: %d, Footprint: %.0fMB, URL: %s\n",
				time.Since(t0).Seconds(), pid, physFootprintMB(pid), l.baseURL())
			return nil
		}
	}
	return fmt.Errorf("ComfyUI did not respond on %s within 60s", l.baseURL())
}

func (l *launcher) stop() error {
	pid := findListeningPID(l.comfyPort)
	if pid == 0 {
		fmt.Println("ComfyUI is already STOPPED.")
		return nil
	}
	before := physFootprintMB(pid)
	fmt.Printf("Stopping ComfyUI (PID %d, Memory %.0fMB)...\n", pid, before)
	terminateProcessTree(pid)
	t0 := time.Now()
	for time.Since(t0) < 30*time.Second {
		time.Sleep(time.Second)
		if findListeningPID(l.comfyPort) == 0 {
			fmt.Printf("[SUCCESS] ComfyUI stopped. Port %d released. Memory reclaimed: %.0fMB -> 0MB\n",
				l.comfyPort, before)
			return nil
		}
	}
	killProcessTree(pid)
	time.Sleep(2 * time.Second)
	if findListeningPID(l.comfyPort) == 0 {
		fmt.Println("[SUCCESS] ComfyUI stopped (force).")
		return nil
	}
	return fmt.Errorf("failed to stop PID %d", pid)
}

func (l *launcher) free() error {
	if !l.healthy() {
		return fmt.Errorf("ComfyUI is not running")
	}
	if err := postJSON(l.baseURL()+"/free", map[string]any{"unload_models": true, "free_memory": true}); err != nil {
		return err
	}
	fmt.Println("[SUCCESS] Models unloaded / memory freed on ComfyUI.")
	return nil
}

// ── GUI 대시보드 서버 ───────────────────────────────────────────────────────

func (l *launcher) serveDashboard(port int, open bool) error {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := dashboardFS.ReadFile("dashboard/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}))
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(l.snapshot())
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if err := l.start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := l.stop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/free", func(w http.ResponseWriter, r *http.Request) {
		if err := l.free(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	// ComfyUI 서버로의 CORS 프록시 (대시보드 JS가 /ws·/queue 등을 같은 오리진에서 쓰기 위함)
	mux.HandleFunc("/api/proxy/ws-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ws_url": "ws://127.0.0.1:" + strconv.Itoa(l.comfyPort) + "/ws"})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dashboard port %d busy: %w", port, err)
	}
	url := fmt.Sprintf("http://%s", addr)
	fmt.Printf("comfy-launcher dashboard: %s  (ComfyUI: %s, dir: %s)\n", url, l.baseURL(), l.comfyDir)
	if open {
		openBrowser(url)
	}
	return http.Serve(ln, mux)
}

// ── 진입점 ─────────────────────────────────────────────────────────────────

func detectComfyDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	// 실행 파일 위치에서 위로 올라가 main.py가 있는 디렉터리 탐색
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "main.py")); err == nil {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "main.py")); err == nil {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}
	fmt.Fprintln(os.Stderr, "[WARN] ComfyUI 디렉터리를 자동 탐지하지 못했습니다 (--comfyui-dir 지정 권장). CWD를 사용합니다.")
	cwd, _ := os.Getwd()
	return cwd
}

// reorderArgs — "cmd --flag" 순서도 "--flag cmd"처럼 파싱되도록 인자를 재배치한다.
// (Go flag 패키지는 첫 비플래그 인자에서 파싱을 멈추는 규칙 때문)
func reorderArgs(argv []string) []string {
	boolFlags := map[string]bool{"-json": true, "--json": true, "-open": true, "--open": true,
		"-h": true, "--h": true, "-help": true, "--help": true}
	var flags, subs []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			// 값을 갖는 플래그는 다음 토큰을 함께 소비 (불리언 플래그 제외)
			if !strings.Contains(a, "=") && !boolFlags[a] && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				flags = append(flags, argv[i+1])
				i++
			}
		} else {
			subs = append(subs, a)
		}
	}
	return append(flags, subs...)
}

func main() {
	os.Args = append([]string{os.Args[0]}, reorderArgs(os.Args[1:])...)

	comfyDirFlag := flag.String("comfyui-dir", "", "ComfyUI 루트 디렉터리 (기본: 자동 탐지)")
	comfyPortFlag := flag.Int("comfy-port", defaultComfyPort, "ComfyUI 서버 포트")
	jsonFlag := flag.Bool("json", false, "status를 JSON으로 출력 (헤드리스)")
	dashPort := flag.Int("port", defaultDashPort, "대시보드 포트 (serve 모드)")
	openFlag := flag.Bool("open", false, "serve 시 브라우저 자동 열기")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "comfy-launcher — ComfyUI launcher with GUI dashboard & headless CLI\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  comfy-launcher <command> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  serve     GUI 대시보드 실행 (기본 :%d)\n", defaultDashPort)
		fmt.Fprintf(os.Stderr, "  start     ComfyUI 백그라운드 기동\n")
		fmt.Fprintf(os.Stderr, "  stop      ComfyUI 종료 (메모리 반납)\n")
		fmt.Fprintf(os.Stderr, "  restart   재시작\n")
		fmt.Fprintf(os.Stderr, "  free      모델 언로드/메모리 해제 (서버 유지)\n")
		fmt.Fprintf(os.Stderr, "  status    상태 조회 (--json 지원)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	l := &launcher{comfyDir: detectComfyDir(*comfyDirFlag), comfyPort: *comfyPortFlag}
	cmd := flag.Arg(0)
	if cmd == "" {
		flag.Usage()
		os.Exit(2)
	}

	var err error
	switch cmd {
	case "serve":
		err = l.serveDashboard(*dashPort, *openFlag)
	case "start":
		err = l.start()
	case "stop":
		err = l.stop()
	case "restart":
		if err = l.stop(); err == nil {
			err = l.start()
		}
	case "free":
		err = l.free()
	case "status":
		info := l.snapshot()
		if *jsonFlag {
			json.NewEncoder(os.Stdout).Encode(info)
			return
		}
		state := "OFF"
		if info.Running {
			state = fmt.Sprintf("ON (PID %d)", info.PID)
		}
		fmt.Printf("ComfyUI: %s  %s\n", state, info.URL)
		if info.Running {
			fmt.Printf("  Footprint: %.0fMB | Device: %s | VRAM: %.0f/%.0fMB free\n",
				info.FootprintMB, info.Device, info.VRAMFreeMB, info.VRAMTotalMB)
			fmt.Printf("  Version: ComfyUI %s (Python %s)\n", info.ComfyVersion, info.PythonVersion)
			fmt.Printf("  Queue: running %d / pending %d\n", info.QueueRunning, info.QueuePending)
			for _, t := range info.RunningTitles {
				fmt.Println("    ▶ running:", t)
			}
			for _, t := range info.PendingTitles {
				fmt.Println("    ⏳ pending:", t)
			}
		}
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", cmd)
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR]", err)
		os.Exit(1)
	}
}
