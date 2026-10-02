package main

import (
	"context"

	corem "github.com/cosmokobo/ComfyUI/launcher/internal/core"
)

// App — WebView에 노출되는 바인딩. 웹 대시보드의 /api/* 핸들러와 동일한 역할.
type App struct {
	ctx context.Context
	l   *corem.Launcher
}

func NewApp(l *corem.Launcher) *App { return &App{l: l} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }
func (a *App) shutdown(ctx context.Context) {}

// Snapshot — 상태·메모리·큐·최근 완료 (프론트엔드가 2초 폴링).
func (a *App) Snapshot() corem.StatusInfo { return a.l.Snapshot() }

// Start / Stop / Restart / Free — 서버 라이프사이클 제어.
func (a *App) Start() error { return a.l.Start() }
func (a *App) Stop() error  { return a.l.Stop() }
func (a *App) Restart() error {
	if err := a.l.Stop(); err != nil {
		return err
	}
	return a.l.Start()
}
func (a *App) Free() error { return a.l.Free() }

// OpenComfyUI — 기본 브라우저로 ComfyUI 웹 UI 열기.
func (a *App) OpenComfyUI() { corem.OpenBrowser(a.l.BaseURL()) }
