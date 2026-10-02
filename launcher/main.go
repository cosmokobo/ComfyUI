// comfy-launcher — ComfyUI 런처 (GUI + 헤드리스 CLI, Go 표준 라이브러리만 사용)
//
// 헤드리스 모드:
//
//	comfy-launcher start|stop|restart|status|free [--json]
//
// 웹 대시보드 모드:
//
//	comfy-launcher serve [--port 8280] [--open]
//
// 네이티브 창 앱:
//
//	gui/ 디렉터리의 Wails 앱 참고 (comfy-launcher.app) — 동일 내부 로직(internal/core) 공유
//
// 서버 상태/메모리(footprint·VRAM)/큐·진행상황을 실시간 표시한다.
// 진행률 이벤트는 브라우저가 ComfyUI의 /ws websocket을 직접 구독한다.
package main

import (
	corem "github.com/cosmokobo/ComfyUI/launcher/internal/core"

	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed dashboard/index.html
var dashboardFS embed.FS

const defaultDashPort = 8280

// ── 웹 대시보드 서버 ─────────────────────────────────────────────────────────

func serveDashboard(l *corem.Launcher, port int, open bool) error {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := dashboardFS.ReadFile("dashboard/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}))
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(l.Snapshot())
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if err := l.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := l.Stop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/free", func(w http.ResponseWriter, r *http.Request) {
		if err := l.Free(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dashboard port %d busy: %w", port, err)
	}
	url := fmt.Sprintf("http://%s", addr)
	fmt.Printf("comfy-launcher dashboard: %s  (ComfyUI: %s)\n", url, l.BaseURL())
	if open {
		corem.OpenBrowser(url)
	}
	return http.Serve(ln, mux)
}

// ── 진입점 ─────────────────────────────────────────────────────────────────

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
	comfyPortFlag := flag.Int("comfy-port", corem.DefaultComfyPort, "ComfyUI 서버 포트")
	jsonFlag := flag.Bool("json", false, "status를 JSON으로 출력 (헤드리스)")
	dashPort := flag.Int("port", defaultDashPort, "대시보드 포트 (serve 모드)")
	openFlag := flag.Bool("open", false, "serve 시 브라우저 자동 열기")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "comfy-launcher — ComfyUI launcher with GUI & headless CLI\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  comfy-launcher <command> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  serve     웹 대시보드 실행 (기본 :%d) / 네이티브 앱은 gui/ 빌드\n", defaultDashPort)
		fmt.Fprintf(os.Stderr, "  start     ComfyUI 백그라운드 기동\n")
		fmt.Fprintf(os.Stderr, "  stop      ComfyUI 종료 (메모리 반납)\n")
		fmt.Fprintf(os.Stderr, "  restart   재시작\n")
		fmt.Fprintf(os.Stderr, "  free      모델 언로드/메모리 해제 (서버 유지)\n")
		fmt.Fprintf(os.Stderr, "  status    상태 조회 (--json 지원)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	l := corem.New(corem.DetectComfyDir(*comfyDirFlag), *comfyPortFlag)
	cmd := flag.Arg(0)
	if cmd == "" {
		flag.Usage()
		os.Exit(2)
	}

	var err error
	switch cmd {
	case "serve":
		err = serveDashboard(l, *dashPort, *openFlag)
	case "start":
		err = l.Start()
	case "stop":
		err = l.Stop()
	case "restart":
		err = l.Restart()
	case "free":
		err = l.Free()
	case "status":
		info := l.Snapshot()
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
