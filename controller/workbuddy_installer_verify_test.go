package controller

import (
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
)

// TestWorkBuddyHtaGeneration 验证 mshta 启动器（HTA）与安装器脚本能被正确生成
func TestWorkBuddyHtaGeneration(t *testing.T) {
	hta := workBuddyInstallerHtaScript()
	if !strings.Contains(hta, "models.json") {
		t.Fatalf("hta 缺少 models.json 写入逻辑")
	}
	if !strings.Contains(hta, ".workbuddy") {
		t.Fatalf("hta 缺少 .workbuddy 目录")
	}
	if !strings.Contains(hta, "oHTA.commandLine") {
		t.Fatalf("hta 未使用 commandLine 接收协议 URL")
	}
	if !strings.Contains(hta, "?d=") {
		t.Fatalf("hta 未从 ?d= 截取载荷")
	}
	if !strings.Contains(hta, "normalizePayload") {
		t.Fatalf("hta 缺少 base64url 归一化逻辑")
	}
	if !strings.Contains(hta, "bin.base64") {
		t.Fatalf("hta 未使用 MSXML base64 解码")
	}
	if !strings.Contains(hta, "import-result.txt") {
		t.Fatalf("hta 未写结果标记，无法判断导入成功与否")
	}
	if !strings.Contains(hta, "<HTA:APPLICATION") {
		t.Fatalf("hta 缺少 HTA:APPLICATION 声明")
	}

	ps := workBuddyInstallerPsScript()
	if !strings.Contains(ps, "workbuddy-import") {
		t.Fatalf("ps 缺少协议名")
	}
	if !strings.Contains(ps, "HKCU:") {
		t.Fatalf("ps 未写入用户注册表")
	}
	if !strings.Contains(ps, "mshta.exe") {
		t.Fatalf("ps 未把协议指向 mshta.exe")
	}
	if !strings.Contains(ps, "import.hta") {
		t.Fatalf("ps 未写入 import.hta 启动器")
	}
	if !strings.Contains(ps, `"%1"`) {
		t.Fatalf("ps 未以位置参数传递 %%1")
	}

	// UTF-16LE base64 解码验证（模拟 PowerShell -EncodedCommand）
	psUtf16 := base64Utf16LEGo(ps)
	decoded, err := base64.StdEncoding.DecodeString(psUtf16)
	if err != nil {
		t.Fatalf("UTF-16 base64 解码失败: %v", err)
	}
	u16 := make([]uint16, 0, len(decoded)/2)
	for i := 0; i+1 < len(decoded); i += 2 {
		u16 = append(u16, uint16(decoded[i])|uint16(decoded[i+1])<<8)
	}
	restored := string(utf16.Decode(u16))
	if restored != ps {
		t.Fatalf("UTF-16LE 往返还原不一致\n期望长度=%d 实际长度=%d", len(ps), len(restored))
	}

	// UTF-8 base64 解码验证（模拟安装器内嵌 HTA 载荷）
	htaB64 := base64Utf8Go(hta)
	htaDecoded, err := base64.StdEncoding.DecodeString(htaB64)
	if err != nil {
		t.Fatalf("UTF-8 base64 解码失败: %v", err)
	}
	if string(htaDecoded) != hta {
		t.Fatalf("UTF-8 往返还原不一致")
	}

	// 落盘样本到系统临时目录，便于人工检查（不污染仓库）
	tmpDir := os.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "workbuddy_sample.ps1"), []byte(ps), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "workbuddy_sample.hta"), []byte(hta), 0644)
	t.Logf("样本已输出到 %s", tmpDir)

	t.Logf("协议=%s", WorkBuddyProtocol)
	t.Logf("PS 脚本长度=%d, HTA 脚本长度=%d", len(ps), len(hta))
}


// toBase64UrlGo 把标准 base64 转为 base64url（去填充，+/ 换 -_），
// 与前端 toBase64Url 保持一致。协议 URL 中不使用任何百分号编码。
func toBase64UrlGo(b64 string) string {
	s := strings.NewReplacer("+", "-", "/", "_").Replace(b64)
	return strings.TrimRight(s, "=")
}

// writeSandboxHta 把 HTA 启动器写入沙箱目录，并把所有目标目录引用替换为沙箱目录，
// 以便端到端测试不触碰真实用户目录。
func writeSandboxHta(t *testing.T, sandbox string) string {
	t.Helper()
	hta := workBuddyInstallerHtaScript()
	oldDir := `shell.ExpandEnvironmentStrings("%USERPROFILE%") & "\.workbuddy"`
	if !strings.Contains(hta, oldDir) {
		t.Fatalf("未找到目标目录表达式，HTA 生成逻辑已变更")
	}
	// 替换全部出现处（WriteConfig / WriteResult / logError 各有一处），
	// 否则结果标记与错误日志会落到真实用户目录，导致测试误判。
	hta = strings.ReplaceAll(hta, oldDir, `"`+sandbox+`"`)
	htaPath := filepath.Join(sandbox, "import.hta")
	if err := os.WriteFile(htaPath, []byte(hta), 0644); err != nil {
		t.Fatalf("写入 HTA 失败: %v", err)
	}
	return htaPath
}

// fileSize 返回文件大小，失败时返回 -1，用于诊断输出。
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}

// runHta 通过 mshta 执行沙箱内的 HTA，模拟协议处理器调用方式。
//
// 两点注意：
//  1. mshta.exe 是 GUI 宿主，在非交互环境下用 `cmd /c start` 无法拉起，
//     必须走 ShellExecute（此处用 PowerShell 的 Start-Process）才与浏览器真实路径一致；
//  2. 启动后不能 Wait()（窗口消息循环会阻塞），改为轮询等待结果文件。
func runHta(t *testing.T, htaPath, url string) {
	t.Helper()
	sandbox := filepath.Dir(htaPath)
	resultFile := filepath.Join(sandbox, "import-result.txt")
	_ = os.Remove(resultFile)

	script := fmt.Sprintf(
		"Start-Process -FilePath 'mshta.exe' -ArgumentList '%s','%s'",
		strings.ReplaceAll(htaPath, "'", "''"),
		strings.ReplaceAll(url, "'", "''"),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("启动 mshta 失败: %v, 输出: %s", err, string(out))
	}
	// 等待 HTA 完成写入（最多 10 秒）
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(resultFile); err == nil {
			return
		}
	}
}

// TestWorkBuddyHtaEndToEnd 用真实 mshta.exe 执行生成的 HTA，
// 验证「协议 URL -> base64url 解码 -> 写入 models.json」全链路（含中文与备份）
func TestWorkBuddyHtaEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("mshta.exe"); err != nil {
		t.Skip("非 Windows 环境，跳过端到端验证")
	}

	sandbox := filepath.Join(os.TempDir(), "workbuddy_hta_e2e_test")
	_ = os.RemoveAll(sandbox)
	if err := os.MkdirAll(sandbox, 0755); err != nil {
		t.Fatalf("创建沙箱失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(sandbox) }()

	htaPath := writeSandboxHta(t, sandbox)

	// 构造配置（含中文、引号，验证 UTF-8 编码链路）
	config := `[{"id":"gpt-4o","name":"测试模型 \"A\"","url":"https://example.com/v1"}]`
	payload := toBase64UrlGo(base64.StdEncoding.EncodeToString([]byte(config)))
	runHta(t, htaPath, WorkBuddyProtocol+"://import?d="+payload)

	written, err := os.ReadFile(filepath.Join(sandbox, "models.json"))
	if err != nil {
		entries, _ := os.ReadDir(sandbox)
		var names []string
		for _, e := range entries {
			names = append(names, fmt.Sprintf("%s(%d)", e.Name(), fileSize(filepath.Join(sandbox, e.Name()))))
		}
		errLog, _ := os.ReadFile(filepath.Join(sandbox, "import-error.log"))
		t.Fatalf("配置未写入: %v\n沙箱内容: %v\n错误日志: %s", err, names, string(errLog))
	}
	if string(written) != config {
		t.Fatalf("写入内容不一致\n期望: %s\n实际: %s", config, string(written))
	}

	// 再次执行，验证旧配置被备份
	config2 := `[{"id":"claude-3","name":"第二版"}]`
	payload2 := toBase64UrlGo(base64.StdEncoding.EncodeToString([]byte(config2)))
	runHta(t, htaPath, WorkBuddyProtocol+"://import?d="+payload2)

	backup, err := os.ReadFile(filepath.Join(sandbox, "models.json.backup"))
	if err != nil {
		t.Fatalf("备份文件未生成: %v", err)
	}
	if string(backup) != config {
		t.Fatalf("备份内容不一致\n期望: %s\n实际: %s", config, string(backup))
	}

	final, _ := os.ReadFile(filepath.Join(sandbox, "models.json"))
	if string(final) != config2 {
		t.Fatalf("二次写入内容不一致\n期望: %s\n实际: %s", config2, string(final))
	}

	// 结果标记应为 ok
	result, err := os.ReadFile(filepath.Join(sandbox, "import-result.txt"))
	if err != nil {
		t.Fatalf("结果标记未生成: %v", err)
	}
	if !strings.HasPrefix(string(result), "ok") {
		t.Fatalf("结果标记应为 ok，实际: %s", string(result))
	}

	t.Logf("端到端验证通过：base64url 解码、写入、备份、结果标记 均正常")
}



// TestWorkBuddyHtaRobustPayload 回归测试：payload 含空白/多余填充等异常字符时仍应成功写入
// （对应真实故障：MSXML bin.base64 对非规范输入抛 0x80070057 E_INVALIDARG）
func TestWorkBuddyHtaRobustPayload(t *testing.T) {
	if _, err := exec.LookPath("mshta.exe"); err != nil {
		t.Skip("非 Windows 环境，跳过端到端验证")
	}

	sandbox := filepath.Join(os.TempDir(), "workbuddy_hta_robust_test")
	_ = os.RemoveAll(sandbox)
	if err := os.MkdirAll(sandbox, 0755); err != nil {
		t.Fatalf("创建沙箱失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(sandbox) }()

	htaPath := writeSandboxHta(t, sandbox)

	config := `[{"id":"gpt-4o","name":"健壮性 测试"}]`
	payload := toBase64UrlGo(base64.StdEncoding.EncodeToString([]byte(config)))

	// 各种脏输入：
	//   1) 标准 base64url 载荷（当前前端格式）
	//   2) Windows 会把 "import?d=" 规范化为 "import/?d="
	//   3) 旧版 %3D 编码载荷（浏览器缓存旧页面时可能出现，需向后兼容）
	//   4) Windows 用引号包裹整个协议 URL（真实 %1 传递形式）
	legacy := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").
		Replace(base64.StdEncoding.EncodeToString([]byte(config)))

	dirty := []string{
		WorkBuddyProtocol + "://import?d=" + payload,
		WorkBuddyProtocol + "://import/?d=" + payload,
		WorkBuddyProtocol + "://import?d=" + legacy,
		"\"" + WorkBuddyProtocol + "://import?d=" + payload + "\"",
	}

	for i, url := range dirty {
		_ = os.Remove(filepath.Join(sandbox, "models.json"))
		runHta(t, htaPath, url)

		w := filepath.Join(sandbox, "models.json")
		written, err := os.ReadFile(w)
		if err != nil {
			errLog, _ := os.ReadFile(filepath.Join(sandbox, "import-error.log"))
			t.Fatalf("用例 %d 配置未写入: %v, 日志: %s", i, err, string(errLog))
		}
		if len(written) == 0 {
			t.Fatalf("用例 %d 写入了空文件（正是历史故障现象）", i)
		}
		if string(written) != config {
			t.Fatalf("用例 %d 写入内容不一致\n期望: %s\n实际: %s", i, config, string(written))
		}
	}

	t.Logf("健壮性验证通过：%d 种异常 payload 均能正确写入", len(dirty))
}

// TestWorkBuddyHtaAsciiOnly 确保生成的 HTA 不含非 ASCII 字符。
// HTA 源码与命令行参数在 VBScript 引擎中按系统 ANSI 解读，非 ASCII 可能被误解码。
func TestWorkBuddyHtaAsciiOnly(t *testing.T) {
	hta := workBuddyInstallerHtaScript()
	for i := 0; i < len(hta); i++ {
		if hta[i] > 127 {
			t.Fatalf("HTA 第 %d 字节为非 ASCII (0x%02X)，可能导致解码异常", i, hta[i])
		}
	}
	t.Logf("HTA 纯 ASCII 校验通过，长度=%d", len(hta))
}

// TestWorkBuddyInstallerHTTP 验证安装器接口返回的 .bat 内容、编码头与 BOM，
// 并确认协议已指向 mshta + import.hta（真正一键导入的关键）
func TestWorkBuddyInstallerHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/workbuddy/installer", nil)

	GetWorkBuddyInstaller(c)

	if w.Code != 200 {
		t.Fatalf("状态码应为 200，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type 不正确: %s", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "workbuddy-install.bat") {
		t.Fatalf("Content-Disposition 不正确: %s", cd)
	}

	body := w.Body.Bytes()
	// UTF-8 BOM（EF BB BF）确保 cmd 正确识别中文
	if !(body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF) {
		t.Fatalf("缺少 UTF-8 BOM，实际前三字节: %v", body[:3])
	}

	text := string(body[3:])
	if !strings.Contains(text, "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ") {
		t.Fatalf("缺少 PowerShell 调用行")
	}
	if !strings.Contains(text, "@echo off") {
		t.Fatalf("缺少 bat 头")
	}
	// 成功路径不应再有阻塞式 pause，否则无法"一键"完成
	successPart := text
	if idx := strings.Index(text, ":FAILED"); idx > 0 {
		successPart = text[:idx]
	}
	if strings.Contains(successPart, "pause") {
		t.Fatalf("成功路径不应包含 pause（会阻塞自动化）")
	}

	// 提取 -EncodedCommand 后的 base64 并还原，确认脚本完整可执行
	lines := strings.Split(text, "\r\n")
	var encoded string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ") {
			encoded = strings.TrimSpace(strings.TrimPrefix(ln, "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand "))
		}
	}
	if encoded == "" {
		t.Fatalf("未提取到 EncodedCommand 载荷")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("EncodedCommand base64 解码失败: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE 字节数应为偶数，实际 %d", len(raw))
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	ps := string(utf16.Decode(u16))
	if !strings.Contains(ps, "HKCU:\\Software\\Classes\\workbuddy-import") {
		t.Fatalf("还原后的 PS 脚本缺少协议注册逻辑")
	}
	if !strings.Contains(ps, "import.hta") {
		t.Fatalf("还原后的 PS 脚本缺少 import.hta 写入逻辑")
	}
	if !strings.Contains(ps, "mshta.exe") {
		t.Fatalf("还原后的 PS 脚本未指向 mshta.exe")
	}

	t.Logf("安装器 bat 大小=%d 字节, 内嵌 PS 脚本=%d 字符", len(body), len(ps))
}
