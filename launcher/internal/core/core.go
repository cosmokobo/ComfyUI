// core — comfy-launcher 본체 로직 (CLI·웹 대시보드·네이티브 GUI가 공유).
// ComfyUI 서버 라이프사이클 제어와 상태 스냅샷(메모리·큐·히스토리)을 제공한다.
package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DefaultComfyPort = 8188

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

// Launcher — ComfyUI 서버 하나를 관리한다.
type Launcher struct {
	comfyDir  string
	comfyPort int
}

// New — ComfyUI 디렉터리와 포트로 런처를 만든다.
func New(comfyDir string, comfyPort int) *Launcher {
	return &Launcher{comfyDir: comfyDir, comfyPort: comfyPort}
}

// DetectComfyDir — 명시 경로 우선, 없으면 실행 위치/CWD에서 위로 main.py를 찾는다.
func DetectComfyDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, start := range []func() (string, error){os.Getwd} {
		if dir, err := start(); err == nil {
			for i := 0; i < 5; i++ {
				if _, err := os.Stat(filepath.Join(dir, "main.py")); err == nil {
					return dir
				}
				dir = filepath.Dir(dir)
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "main.py")); err == nil {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}
	fmt.Fprintln(os.Stderr, "[WARN] ComfyUI 디렉터리 자동 탐색 실패 (--comfyui-dir 지정 권장). CWD 사용.")
	cwd, _ := os.Getwd()
	return cwd
}

func (l *Launcher) BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", l.comfyPort) }

func (l *Launcher) Healthy() bool {
	var v any
	return getJSON(l.BaseURL()+"/system_stats", &v, 3*time.Second) == nil
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

// StatusInfo — 대시보드/CLI/GUI가 공유하는 상태 스냅샷.
type StatusInfo struct {
	Running       bool     `json:"running"`
	PID           int      `json:"pid"`
	URL           string   `json:"url"`
	FootprintMB   float64  `json:"footprint_mb"`
	ComfyVersion  string   `json:"comfyui_version"`
	PythonVersion string   `json:"python_version"`
	Device        string   `json:"device"`
	VRAMTotalMB   float64  `json:"vram_total_mb"`
	VRAMFreeMB    float64  `json:"vram_free_mb"`
	QueueRunning  int      `json:"queue_running"`
	QueuePending  int      `json:"queue_pending"`
	RunningTitles []string `json:"running_titles"`
	PendingTitles []string `json:"pending_titles"`
	RecentDone    []string `json:"recent_done"`
}

// Snapshot — 서버 상태·메모리·큐·최근 완료를 한 번에 수집한다.
func (l *Launcher) Snapshot() StatusInfo {
	info := StatusInfo{URL: l.BaseURL(), RunningTitles: []string{}, PendingTitles: []string{}, RecentDone: []string{}}
	info.PID = findListeningPID(l.comfyPort)
	info.Running = info.PID != 0 && l.Healthy()
	if !info.Running {
		return info
	}
	info.FootprintMB = physFootprintMB(info.PID)

	var ss systemStats
	if err := getJSON(l.BaseURL()+"/system_stats", &ss, 3*time.Second); err == nil {
		info.ComfyVersion = ss.System.ComfyUIVersion
		info.PythonVersion = ss.System.PythonVersion
		if len(ss.Devices) > 0 {
			info.Device = ss.Devices[0].Name
			info.VRAMTotalMB = float64(ss.Devices[0].VRAMTotal) / 1e6
			info.VRAMFreeMB = float64(ss.Devices[0].VRAMFree) / 1e6
		}
	}
	var q queueResp
	if err := getJSON(l.BaseURL()+"/queue", &q, 3*time.Second); err == nil {
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
	if err := getJSON(l.BaseURL()+"/history?max_items=10", &hist, 3*time.Second); err == nil {
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

// Start — 백그라운드 기동 + 헬스체크(60s).
func (l *Launcher) Start() error {
	if findListeningPID(l.comfyPort) != 0 && l.Healthy() {
		fmt.Println("ComfyUI is already RUNNING on", l.BaseURL())
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

	fmt.Println("Launching ComfyUI in background (logging to", logPath+")...")
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
		if l.Healthy() {
			pid := findListeningPID(l.comfyPort)
			fmt.Printf("[SUCCESS] ComfyUI started in %.1fs. PID: %d, Footprint: %.0fMB, URL: %s\n",
				time.Since(t0).Seconds(), pid, physFootprintMB(pid), l.BaseURL())
			return nil
		}
	}
	return fmt.Errorf("ComfyUI did not respond on %s within 60s", l.BaseURL())
}

// Stop — 종료 + 포트 반납/메모리 회수 확인.
func (l *Launcher) Stop() error {
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

// Restart — 재시작.
func (l *Launcher) Restart() error {
	if err := l.Stop(); err != nil {
		return err
	}
	return l.Start()
}

// Free — 모델 언로드/메모리 해제 (서버 유지).
func (l *Launcher) Free() error {
	if !l.Healthy() {
		return fmt.Errorf("ComfyUI is not running")
	}
	if err := postJSON(l.BaseURL()+"/free", map[string]any{"unload_models": true, "free_memory": true}); err != nil {
		return err
	}
	fmt.Println("[SUCCESS] Models unloaded / memory freed on ComfyUI.")
	return nil
}
