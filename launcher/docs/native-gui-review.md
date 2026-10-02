# comfy-launcher 네이티브 실행 프로그램 전환 — 기술 검토

현재 comfy-launcher의 "GUI"는 **로컬 웹 대시보드**(`serve` → 127.0.0.1:8280, 브라우저로 이용)다.
"웹 기반이 아닌 실행 프로그램(독립 윈도우 앱)"으로 전환할 때의 선택지를 검토한다.

## 요구 조건 정리

- macOS(Apple Silicon) + Windows 네이티브 창에서 동작하는 단일 실행 파일
- 현재 대시보드 기능 유지: ON/OFF/재시작/메모리 해제, footprint·VRAM, 대기열, 실시간 진행률(websocket)
- 빌드/배포 파이프라인 단순 (가능하면 `go build` 한 줄)
- Go 유지 (러처 본체·헤드리스 CLI와 코드 공유)

## 후보 비교

| 항목 | **Wails v2/v3** | **Fyne** | **Gio** | 현재(웹 대시보드) 유지 |
| --- | --- | --- | --- | --- |
| 방식 | Go 백엔드 + 시스템 WebView로 HTML UI를 네이티브 창에 렌더링 | 순수 Go 위젯 툴킷(OpenGL) | 즉시모드(immediate mode) 순수 Go | 브라우저 |
| 겉모습 | **기존 dashboard/index.html을 거의 그대로 재사용** — 전환 비용 최소 | Go 코드로 UI 재작성 필요 (위젯 풍부하나 대시보드 스타일 재현 공수 中) | Go 코드 재작성 + 위젯 직접 그림 (공수 大) | — |
| cgo 필요 | O (WebView2/WKWebView 바인딩) | O (OpenGL) | **X (완전 순수 Go)** | X |
| 교차 컴파일 | mac→win 불가 (플랫폼별 빌드 필요: Windows에선 WebView2 SDK) | `CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc` 로 가능 (mingw 필요) | **`GOOS=windows go build` 한 줄** (현재와 동일) | 현재와 동일 |
| Windows 사전요건 | WebView2 런타임 (Win10/11 기본 내장, 거의 문제 없음) | 없음 | 없음 | 브라우저 |
| 실행 파일 크기 | ~10-15MB | ~25-40MB | ~8-12MB | 10MB |
| websocket 실시간 진행률 | JS 그대로 → **무수정 재사용** | Go로 WS 재구현 (gorilla/websocket 등 외부 의존) | 동일 재구현 | — |
| 성숙도/커뮤니티 | 높음 (v2 안정) | 높음 | 中 (학습곡선 가파름) | — |

## 평가

### 권장: **Wails v2** — "실행 프로그램" 요구를 가장 싸게 충족

- **전환 비용이 압도적으로 낮다**: 이미 검증된 HTML 대시보드(진행률 websocket 포함)를 네이티브 창 안에
  그대로 올린다. Go 측 바인딩(Wails Bind)만 붙이면 `comfy-launcher gui` 서브커맨드로 창 앱이 뜬다.
  `.app`(macOS) / `.exe`(Windows) 산출, 작업표시줄 아이콘, 시스템 트레이 상주도 지원한다.
- 단점: cgo 때문에 **플랫폼별 빌드 환경**이 필요하다 (mac: Xcode CLT, win: WebView2 SDK/mingw 혹은
  Windows 빌드 머신). 이 리포지터리처럼 GitHub Actions 매트릭스 빌드가 있으면 오히려 깔끔하게 해결된다.
- 실시간 진행률(websocket), 다크 테이블 대시보드 같은 풍부한 UI는 HTML이 압도적으로 생산적이다.

### 차선: **Fyne** — 순수 코드 통일이 최우선일 때

- 단일 Go 코드베이스로 완결. 다만 현재 대시보드를 위젯으로 재현하는 공수가 있고,
  websocket 실시간 진행률을 Go에서 다시 구현해야 한다(외부 의존 추가).
  교차 컴파일은 mingw만 있으면 mac→win 가능.

### 특수 선택: **Gio** — `go build` 한 줄 교차컴파일이 최우선일 때

- 의존성 제로 철학을 가장 잘 지킨다(현재 런처와 동일한 빌드 경험).
  대신 위젯을 직접 그리는 즉시모드 UI라 개발 공수가 크고, 데스크톱 대시보드 용도로는 과하다.

### 유지(현재) + 래핑

- 지금도 `comfy-launcher serve --open`이면 사실상 "앱처럼" 쓸 수 있다(브라우저 창 1개).
  Electron 같은 래퍼는 무겁고 관리 부담만 커서 비추천.

## 권장 아키텍처 (Wails 전환 시)

```
launcher/
├── main.go, platform_*.go   # 헤드리스 CLI (그대로)
├── dashboard/index.html     # 현재 대시보드 (Wails 프론트로 재사용)
└── gui/                     # Wails 앱 (신규)
    └── main.go              # 창 띄우고 dashboard를 asset으로 포함, Go 메서드 바인딩
```

- `comfy-launcher gui` → 네이티브 창 앱, `serve`/CLI는 병행 유지 (자동화·CI 호환)
- 빌드: macOS `wails build`, Windows는 GitHub Actions `windows-latest` 매트릭스에서 `.exe` 산출

## 결론

| 우선순위 | 선택 |
| --- | --- |
| 기존 UI 재사용 + 빠른 전환 + 완성도 | **Wails v2 (권장)** |
| 순수 Go 단일 코드 | Fyne |
| 의존성 제로 + 교차컴파일 한 줄 | Gio |
| 지금 당장 변경 없음 | 현재 웹 대시보드 유지 |

Wails 전환을 진행할 경우 예상 공수는 런처 기준 반나절~1일 수준이며, 기능 손실 없이
"브라우저 탭"이 "독립 실행 프로그램 창"으로 바뀐다.
