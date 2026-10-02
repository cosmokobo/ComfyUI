// comfy-launcher GUI — Wails v2 네이티브 창 앱.
// 웹 대시보드와 동일한 internal/core 로직을 Go 바인딩으로 노출하고,
// 프론트엔드(frontend/index.html)는 웹 대시보드를 재사용한다.
// 진행률 websocket은 브라우저(WebView)가 ComfyUI /ws를 직접 구독한다.
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	corem "github.com/cosmokobo/ComfyUI/launcher/internal/core"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp(corem.New(corem.DetectComfyDir(""), corem.DefaultComfyPort))

	err := wails.Run(&options.App{
		Title:            "ComfyUI Launcher",
		Width:            920,
		Height:           780,
		MinWidth:         720,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 1}, // #0f1115
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
