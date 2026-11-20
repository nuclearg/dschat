package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dschat/app/core/chatsvr"
)

// occupant 描述占用本地端口的程序。拿不到的字段留空。
type occupant struct {
	PID  int
	Name string // 展示用名称，如 dschat、Google Chrome
	Path string // 可执行文件路径（仅部分平台能拿到）
}

func (o occupant) known() bool { return o.PID > 0 || o.Name != "" }

// describeAs 用给定名称替换自动识别的进程名，避免与说明重复。
func (o occupant) describeAs(name string) string {
	o.Name = name
	return o.describe()
}

// describe 拼出「Google Chrome（PID 1234，/Applications/...）」这样的描述。
func (o occupant) describe() string {
	name := o.Name
	if name == "" {
		name = "未知程序"
	}
	parts := []string{}
	if o.PID > 0 {
		parts = append(parts, fmt.Sprintf("PID %d", o.PID))
	}
	if o.Path != "" {
		parts = append(parts, o.Path)
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s（%s）", name, strings.Join(parts, "，"))
}

// ShowStartupError 把启动失败的原因告知用户：端口被占用时附带占用进程信息。
// 只在带界面的模式下调用；无界面（-serve）模式把日志留给 stderr 即可。
func ShowStartupError(err error, port int) {
	var busy *chatsvr.PortInUseError
	if errors.As(err, &busy) {
		msg := portBusyMessage(port, busy)
		// 先落 stderr：屏幕锁定或弹窗不可见时，日志里仍能查到原因。
		fmt.Fprintf(os.Stderr, "dschat 无法启动：%s\n", msg)
		showErrorDialog("dschat 无法启动", msg)
		return
	}
	fmt.Fprintf(os.Stderr, "dschat 无法启动：%v\n", err)
	showErrorDialog("dschat 无法启动", err.Error())
}

// portBusyMessage 组装端口被占用的说明：谁占着、是不是另一个 dschat、怎么办。
func portBusyMessage(port int, busy *chatsvr.PortInUseError) string {
	occ, isDschat := whoIsOnPort(port)
	return buildPortBusyMessage(port, busy, occ, isDschat)
}

// buildPortBusyMessage 与探测解耦，便于针对文案本身做测试。
func buildPortBusyMessage(port int, busy *chatsvr.PortInUseError, occ occupant, isDschat bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "本地端口 %d 已被占用，dschat 无法启动。\n\n", port)
	switch {
	case isDschat:
		fmt.Fprintf(&b, "占用者：%s\n\n", occ.describeAs("另一个 dschat"))
		b.WriteString("不能同时运行两个 dschat。请先退出已运行的那个，再重新打开本程序。")
	case occ.known():
		fmt.Fprintf(&b, "占用者：%s\n\n", occ.describe())
		b.WriteString("请退出该程序后重试，或用 -port 指定其他端口。")
	default:
		b.WriteString("未能识别占用该端口的程序。\n\n")
		fmt.Fprintf(&b, "可在终端执行 lsof -nP -iTCP:%d -sTCP:LISTEN 查看，或用 -port 指定其他端口。", port)
	}
	if busy.Err != nil {
		fmt.Fprintf(&b, "\n\n错误信息：%v", busy.Err)
	}
	return b.String()
}

// whoIsOnPort 找出监听该端口的程序，并判断它是不是另一个 dschat。
// 进程信息拿不到时仍可通过 /api/health 的 self-report 确认是 dschat。
func whoIsOnPort(port int) (occupant, bool) {
	occ := findListener(port)
	if occ.PID > 0 {
		if occ.Path == "" {
			occ.Path = processPath(occ.PID)
		}
		if occ.Name == "" {
			occ.Name = processName(occ.PID, occ.Path)
		}
	}

	health, probed := probeHealth(port)
	if probed {
		if occ.PID == 0 {
			occ.PID = health.PID
			if occ.Path == "" && occ.PID > 0 {
				occ.Path = processPath(occ.PID)
			}
			if occ.Name == "" {
				occ.Name = processName(occ.PID, occ.Path)
			}
		}
		if health.App == "dschat" {
			return occ, true
		}
	}
	return occ, looksLikeDschat(occ)
}

// looksLikeDschat 在拿不到健康信息时按可执行文件名兜底判断。
func looksLikeDschat(occ occupant) bool {
	for _, v := range []string{occ.Name, occ.Path} {
		if strings.Contains(strings.ToLower(v), "dschat") {
			return true
		}
	}
	return false
}

// healthInfo 是 /api/health 的自报身份，用来确认占用者就是 dschat。
type healthInfo struct {
	App string `json:"app"`
	PID int    `json:"pid"`
}

// probeHealth 请求端口上的 /api/health；超时或非 dschat 服务都算探测失败。
func probeHealth(port int) (healthInfo, bool) {
	client := &http.Client{Timeout: 600 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		return healthInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthInfo{}, false
	}
	var info healthInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info); err != nil {
		return healthInfo{}, false
	}
	return info, true
}

// processName 优先取路径的最后一段；拿不到路径时退回平台提供的名字。
func processName(pid int, path string) string {
	if path != "" {
		return filepath.Base(path)
	}
	if pid <= 0 {
		return ""
	}
	return platformProcessName(pid)
}
