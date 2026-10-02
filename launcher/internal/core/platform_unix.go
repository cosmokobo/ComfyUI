//go:build !windows

// 유닉스(macOS/Linux) 플랫폼 구현 — PID 탐색(lsof), 메모리(footprint/ps),
// 프로세스 그룹 종료(signal), 분리 기동(setsid), 브라우저(open/xdg-open).
package core

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func findListeningPID(port int) int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("TCP:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(line); err == nil {
			return pid
		}
	}
	return 0
}

func rssMB(pid int) float64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return float64(kb) / 1024
}

// physFootprintMB — macOS `footprint`(통합 메모리 물리 풋프린트) 우선, Linux는 ps RSS.
func physFootprintMB(pid int) float64 {
	if pid <= 0 {
		return 0
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("footprint", strconv.Itoa(pid)).Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "footprint:") {
					return parseSizeMB(strings.TrimSpace(strings.TrimPrefix(line, "footprint:")))
				}
			}
		}
	}
	return rssMB(pid)
}

// terminateProcessTree — 프로세스 그룹에 SIGTERM (graceful).
func terminateProcessTree(pid int) {
	syscall.Kill(-pid, syscall.SIGTERM)
	syscall.Kill(pid, syscall.SIGTERM)
}

// killProcessTree — 강제 종료.
func killProcessTree(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
	syscall.Kill(pid, syscall.SIGKILL)
}

// detach — 백그라운드 데몬처럼 분리 기동 (자식 프로세스 그룹).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// venvPython — 가상환경 파이썬 경로 (유닉스 레이아웃).
func venvPython(comfyDir string) string {
	candidate := comfyDir + "/.venv/bin/python3"
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "python3"
}

// platformPythonEnv — macOS MPS 폴백 등 플랫폼별 추가 환경변수.
func platformPythonEnv() []string {
	if runtime.GOOS == "darwin" {
		return []string{"PYTORCH_ENABLE_MPS_FALLBACK=1"}
	}
	return nil
}

// openBrowser — 기본 브라우저로 URL 열기.
func OpenBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}
