# comfy-launcher

ComfyUI 런처 — **GUI 대시보드 + 헤드리스 CLI** 가 내장된 단일 Go 바이너리.
의존성 없음(Go 표준 라이브러리만 사용), macOS(ARM64) 기준.

## 기능

- **서버 ON/OFF/재시작/메모리 해제** — 기존 `manage-comfyui.sh`와 동일한 시맨틱
  (`.venv/bin/python3 main.py --listen 127.0.0.1 --port 8188`, `PYTORCH_ENABLE_MPS_FALLBACK=1`,
  `comfyui_service.log` 기록, 프로세스 그룹 종료)
- **메모리 점유율** — macOS `footprint`(통합 메모리 물리 풋프린트) + ComfyUI `/system_stats` VRAM
- **진행 상황 트래킹** — 대기열(실행/대기 워크플로우 목록) 폴링 + ComfyUI `/ws` websocket 실시간
  샘플링 진행률/노드 실행 이벤트 (대시보드에서 프로그레스 바)

## 빌드 (크로스플랫폼 — 표준 라이브러리만 사용해 교차컴파일 가능)

```bash
cd launcher
go build -o comfy-launcher .                      # 현재 플랫폼 (macOS/Linux)
GOOS=windows GOARCH=amd64 go build -o comfy-launcher.exe .   # Windows exe
GOOS=windows GOARCH=arm64 go build -o comfy-launcher-arm64.exe .
```

## 플랫폼 지원 매트릭스

| 기능 | macOS | Windows | Linux |
| --- | --- | --- | --- |
| start/stop/restart/free | ✅ (setsid, 프로세스 그룹 SIGTERM→KILL) | ✅ (CREATE_NEW_PROCESS_GROUP, `taskkill /T /F`) | ✅ |
| PID 탐색 | `lsof` | `netstat -ano` | `lsof` |
| 프로세스 메모리 | `footprint` (통합 메모리) | `tasklist` (작업 세트) | `ps` RSS |
| venv 파이썬 | `.venv/bin/python3` | `.venv/Scripts/python.exe` | `.venv/bin/python3` |
| MPS 폴백 env | `PYTORCH_ENABLE_MPS_FALLBACK=1` | — (미설정) | — |

Windows 실기 검증 전 상태이며(교차컴파일·정적 검증 완료), 첫 Windows 사용 시
`comfy-launcher status` 동작부터 확인을 권장한다. 네이티브 창 앱 전환 검토는
`docs/native-gui-review.md` 참고.

## 네이티브 창 앱 (Wails GUI)

```bash
cd gui
wails build            # → gui/build/bin/comfy-launcher.app  (macOS)
# Windows: Windows 머신/CI에서 동일 명령 → comfy-launcher.exe
```

웹 대시보드와 동일한 UI를 독립 창 앱으로 띄운다 (본체 로직 internal/core 공유,
통신은 Go 바인딩, 진행률 websocket은 WebView가 서버 직접 구독).
macOS 배포 시 서명·공증(notarization) 권장, Windows는 WebView2 런타임 필요(Win10/11 기본 내장).

## 사용

### GUI (대시보드)

```bash
./comfy-launcher serve            # http://127.0.0.1:8280
./comfy-launcher serve --open     # 브라우저 자동 열기
```

대시보드에서 상태 확인, ON/OFF/재시작/메모리 해제 버튼, 메모리/VRAM 카드,
실시간 진행률 바, 대기열 목록을 제공한다. ComfyUI 웹 UI로 바로 이동하는 링크 포함.

### 헤드리스 (자동화/TeamCity)

```bash
./comfy-launcher start            # 백그라운드 기동 (60초 헬스체크)
./comfy-launcher stop             # 종료 + 메모리 반납 확인
./comfy-launcher restart
./comfy-launcher free             # 모델 언로드/메모리 해제 (서버 유지)
./comfy-launcher status           # 사람이 읽는 상태 출력
./comfy-launcher status --json    # 머신 판독용 JSON (파이프라인 연동)
```

플래그: `--comfyui-dir`(기본 자동 탐지: 실행 위치에서 위로 main.py 탐색), `--comfy-port`(기본 8188),
`--port`(대시보드, 기본 8280).

## 상태 JSON 예시

```json
{
  "running": true, "pid": 51275, "url": "http://127.0.0.1:8188",
  "footprint_mb": 8123.4,
  "comfyui_version": "0.3.x", "python_version": "3.12.7",
  "device": "mps", "vram_total_mb": 19327, "vram_free_mb": 2100,
  "queue_running": 1, "queue_pending": 0,
  "running_titles": ["workflow"], "pending_titles": [], "recent_done": ["059b89a6-..."]
}
```
