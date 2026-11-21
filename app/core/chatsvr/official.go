package chatsvr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// OfficialBaseURL 官方同步服务的地址，**内置在客户端里**。
//
// 官方模式下用户不填地址，客户端也只有一个「手机号 + 密码」的登录入口；
// 注册与找回密码是 cloudsvr 上的网页（/register、/reset），由客户端把链接打开给用户。
//
// 生产默认地址。`make run` 会用 -ldflags 覆盖为 http://localhost:5219 做本地联调。
var OfficialBaseURL = "http://cloudsvr.nuclearg.info:80"

// officialTimeout 给官方接口一个统一超时：登录要校验 Argon2 哈希，比本地 API 慢。
const officialTimeout = 20 * time.Second

// LoginOfficial 官方模式登录：手机号 + 密码。
func LoginOfficial(ctx context.Context, baseURL, phone, password, name, platform, localDeviceID, language string) (*LoginResult, error) {
	data, err := postAction(ctx, baseURL, "auth.login", language, map[string]string{
		"phone": phone, "password": password,
		"name": name, "platform": platform, "deviceId": localDeviceID,
	})
	if err != nil {
		return nil, err
	}
	return parseLoginResult(data)
}

// postAction 调用 v1 的 RPC 风格 action，返回 data 节点。
//
// 失败时把远端 errCode 原样带回（resultError），让上层能区分「手机号或密码错误」
// 「账号被停用」等分支——这些分支决定前端怎么提示，不能都压成一个"登录失败"。
func postAction(ctx context.Context, baseURL, action, language string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/v1/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if language == "" {
		language = "zh-CN"
	}
	req.Header.Set("X-Language", language)
	resp, err := newSyncHTTPClient(officialTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, resultError(resp.StatusCode, data)
	}
	var env resultEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, resultError(resp.StatusCode, data)
	}
	return env.Data, nil
}

// normalizePhone 把用户输入的 11 位手机号补成 E.164（+86）。
//
// UI 上让用户输 11 位是自然的（和中国用户习惯一致），但云端按 E.164 存，
// 这个转换放在客户端边界上做一次，服务端就只需要一套格式判断。
// 已经带 + 的原样返回：将来支持国际号时不用改这里。
func normalizePhone(phone string) string {
	p := strings.TrimSpace(phone)
	if strings.HasPrefix(p, "+") {
		return p
	}
	if len(p) == 11 {
		return "+86" + p
	}
	return p
}
