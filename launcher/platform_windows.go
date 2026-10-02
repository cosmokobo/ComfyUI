//go:build windows

// Windows 구현 — PID 탐색(netstat -ano), 메모리(tasklist), 트리 종료(taskkill /T),
// 분리 기동(CREATE_NEW_PROCESS_GROUP), 브라우저(cmd /c start).
package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func findListeningPID(port int) int {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return 0
	}
	suffix := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// TCP  127.0.0.1:8188  0.0.0.0:0  LISTENING  12345
		if len(fields) >= 5 && fields[len(fields)-1] != "" {
			state := strings.ToUpper(fields[len(fields)-2])
			local := fields[1]
			if strings.Contains(local, suffix) && (state == "LISTENING") {
				if pid, err := strconv.Atoi(fields[len(fields)-1]); err == nil && pid > 0 {
					return pid
				}
			}
		}
	}
	return 0
}

// physFootprintMB — tasklist CSV의 "Memory Usage" (KB) 기준.
func physFootprintMB(pid int) float64 {
	if pid <= 0 {
		return 0
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(line, "INFO:") {
		return 0
	}
	parts := strings.Split(line, ",")
	last := strings.Trim(parts[len(parts)-1], "\" \r")
	last = strings.TrimSuffix(strings.TrimSpace(last), " K")
	last = strings.ReplaceAll(last, ",", "") // 천 단위 콤마 (로케일별)
	last = strings.ReplaceAll(last, ".", "")
	kb, err := strconv.ParseFloat(strings.TrimSpace(last), 64)
	if err != nil {
		return 0
	}
	return kb / 1024
}

// terminateProcessTree — Windows 콘솔 프로세스는 WM_CLOSE 무시가 기본이라
// 데몬 성격상 곧바로 트리 종료로 통일한다 (taskkill /T /F).
func terminateProcessTree(pid int) { killProcessTree(pid) }

func killProcessTree(pid int) {
	exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

// detach — 신규 프로세스 그룹 + 창 숨김으로 분리 기동.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup}
}

// venvPython — 가상환경 파이썬 경로 (Windows 레이아웃).
func venvPython(comfyDir string) string {
	candidate := comfyDir + "\\.venv\\Scripts\\python.exe"
	if _, err := statFile(candidate); err == nil {
		return candidate
	}
	return "python"
}

// platformPythonEnv — Windows에서 MPS 폴백은 무의미 (CUDA/CPU 빌드).
func platformPythonEnv() []string { return nil }

// openBrowser — 기본 브라우저로 URL 열기.
func openBrowser(url string) {
	exec.Command("cmd", "/c", "start", "", url).Start()
}
