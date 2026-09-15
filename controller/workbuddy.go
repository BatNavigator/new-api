package controller

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// WorkBuddyProtocol 是 WorkBuddy 一键导入使用的自定义 URL 协议名
const WorkBuddyProtocol = "workbuddy-import"

// WorkBuddyModel 描述一个可用于导出到 WorkBuddy 客户端配置的模型
type WorkBuddyModel struct {
	Id                string `json:"id"`
	SupportsToolCall  bool   `json:"supports_tool_call"`
	SupportsImages    bool   `json:"supports_images"`
	SupportsReasoning bool   `json:"supports_reasoning"`
	// 模型归属信息：origin 为 "domestic" / "foreign"，
	// vendor 为该模型识别到的供应商名（可能为空），仅供前端展示与筛选
	Origin string `json:"origin"`
	Vendor string `json:"vendor,omitempty"`
}

// WorkBuddyModelOriginDomestic / Foreign 是模型归属的取值
const (
	WorkBuddyModelOriginDomestic = "domestic"
	WorkBuddyModelOriginForeign  = "foreign"
)

// workBuddyImageModelKeywords 视觉（多模态）模型启发式关键词
var workBuddyImageModelKeywords = []string{
	"vision", "vl", "omni", "gemini", "claude", "4v", "llava", "minicpm",
	"internvl", "reka", "qvq", "glm-4v", "qwen-vl", "qwen2.5-vl",
	"moonshot-vl", "hunyuan-vl", "bailian-vl", "struct", "4o",
}

// workBuddyReasoningModelKeywords 推理模型启发式关键词
var workBuddyReasoningModelKeywords = []string{
	"reasoner", "thinking", "think", "qwq", "o1", "o3", "o4", "r1", "k1.5",
	"kimi-k2", "kimi-k3", "glm-4.5", "glm-5", "v4", "deepseek-reason",
	"flash-thinking", "doubao-1.5-pro",
}

// inferWorkBuddySupport 根据模型名与支持的端点类型推断 WorkBuddy 能力开关
func inferWorkBuddySupport(modelName string, endpointTypes []constant.EndpointType) (bool, bool, bool) {
	toolCall := false
	for _, et := range endpointTypes {
		if et == constant.EndpointTypeOpenAI ||
			et == constant.EndpointTypeOpenAIResponse ||
			et == constant.EndpointTypeAnthropic ||
			et == constant.EndpointTypeGemini {
			toolCall = true
			break
		}
	}

	lowerName := strings.ToLower(modelName)
	images := false
	for _, kw := range workBuddyImageModelKeywords {
		if strings.Contains(lowerName, kw) {
			images = true
			break
		}
	}

	reasoning := false
	for _, kw := range workBuddyReasoningModelKeywords {
		if strings.Contains(lowerName, kw) {
			reasoning = true
			break
		}
	}

	return toolCall, images, reasoning
}

// GetWorkBuddyModels 返回当前用户可用的模型（含能力推断），以及本站地址，
// 方便前端一键生成 WorkBuddy 客户端模型配置
func GetWorkBuddyModels(c *gin.Context) {
	userId := c.GetInt("id")
	userGroup, err := model.GetUserGroup(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	group := userGroup
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if tokenGroup != "" {
		group = tokenGroup
	}

	var modelNames []string
	if tokenGroup == "auto" {
		for _, autoGroup := range service.GetUserAutoGroup(userGroup) {
			for _, m := range model.GetGroupEnabledModels(autoGroup) {
				if !common.StringsContains(modelNames, m) {
					modelNames = append(modelNames, m)
				}
			}
		}
	} else {
		modelNames = model.GetGroupEnabledModels(group)
	}

	models := make([]WorkBuddyModel, 0, len(modelNames))
	vendorNames := resolveModelVendorNames(modelNames)
	for _, name := range modelNames {
		models = append(models, buildWorkBuddyModel(name, vendorNames[name]))
	}

	// 双重过滤（后端守门）：属于境外的模型直接不返回给前端，
	// 避免用户通过直接调用接口绕过前端限制把境外模型写进 WorkBuddy。
	// 仅在「限制为境内模型」开关打开时生效；关闭时返回全量模型。
	allowForeign := !system_setting.IsWorkBuddyDomesticOnlyEnabled()
	visibleModels := make([]WorkBuddyModel, 0, len(models))
	foreignCount := 0
	for _, m := range models {
		if m.Origin == WorkBuddyModelOriginForeign {
			foreignCount++
			if !allowForeign {
				continue
			}
		}
		visibleModels = append(visibleModels, m)
	}

	common.ApiSuccess(c, gin.H{
		"server_address":        system_setting.ServerAddress,
		"models":                visibleModels,
		"domestic_only":         !allowForeign,
		"total":                 len(visibleModels),
		"filtered_foreign_count": foreignCount,
	})
}


// resolveModelVendorNames 批量解析「模型名 -> 供应商名称」。
//
// 数据来源优先顺序：
//  1. 模型元数据表（model.Model）中显式配置的 VendorID 对应的供应商名称；
//  2. 模型元数据未配置供应商时，回退到 model/pricing_default.go 的关键词规则推断。
//
// 说明：这里只做一次性批量查询，避免在模型循环里反复访问数据库。
func resolveModelVendorNames(modelNames []string) map[string]string {
	result := make(map[string]string, len(modelNames))
	if len(modelNames) == 0 {
		return result
	}

	// 1) 读取模型元数据，建立 模型名 -> VendorID
	var metas []model.Model
	if err := model.DB.Where("model_name IN ?", modelNames).Find(&metas).Error; err == nil {
		vendorIDByName := make(map[string]int, len(metas))
		vendorIDSet := make(map[int]struct{})
		for i := range metas {
			if metas[i].VendorID != 0 {
				vendorIDByName[metas[i].ModelName] = metas[i].VendorID
				vendorIDSet[metas[i].VendorID] = struct{}{}
			}
		}

		// 2) 读取供应商名称
		vendorNameByID := make(map[int]string, len(vendorIDSet))
		if len(vendorIDSet) > 0 {
			ids := make([]int, 0, len(vendorIDSet))
			for id := range vendorIDSet {
				ids = append(ids, id)
			}
			var vendors []model.Vendor
			if err := model.DB.Where("id IN ?", ids).Find(&vendors).Error; err == nil {
				for i := range vendors {
					vendorNameByID[vendors[i].Id] = vendors[i].Name
				}
			}
		}

		for name, vendorID := range vendorIDByName {
			if vendorName, ok := vendorNameByID[vendorID]; ok {
				result[name] = vendorName
			}
		}
	}

	// 3) 未命中元数据的模型，用关键词规则兜底推断供应商名
	for _, name := range modelNames {
		if result[name] != "" {
			continue
		}
		if vendorName := model.InferVendorNameByKeywords(name); vendorName != "" {
			result[name] = vendorName
		}
	}

	return result
}

// buildWorkBuddyModel 组装单个 WorkBuddy 模型条目（含能力推断与归属判定）
func buildWorkBuddyModel(name string, vendorName string) WorkBuddyModel {
	endpointTypes := model.GetModelSupportEndpointTypes(name)
	toolCall, images, reasoning := inferWorkBuddySupport(name, endpointTypes)

	origin := WorkBuddyModelOriginForeign
	if common.IsDomesticModel(name, vendorName, system_setting.GetWorkBuddyDomesticModelAllowList()) {
		origin = WorkBuddyModelOriginDomestic
	}

	return WorkBuddyModel{
		Id:                name,
		SupportsToolCall:  toolCall,
		SupportsImages:    images,
		SupportsReasoning: reasoning,
		Origin:            origin,
		Vendor:            vendorName,
	}
}


// 该启动器由 workbuddy-import:// 协议直接唤起，职责：
//   1) 解析协议 URL 中的 base64 配置载荷
//   2) 写入 %USERPROFILE%\.workbuddy\models.json（旧文件自动备份）
//
// 选 mshta 而非 wscript 的原因：URL 协议可带参数直接调起 mshta，
// 无需预先在本地落盘任何脚本，从而实现真正的一次点击导入。
func workBuddyInstallerHtaScript() string {
	return strings.Join([]string{
		"<html>",
		"<head>",
		"<meta http-equiv=\"Content-Type\" content=\"text/html; charset=utf-8\" />",
		"<title>WorkBuddy Import</title>",
		"<HTA:APPLICATION ID=\"oHTA\" SHOWINTASKBAR=\"no\" SINGLEINSTANCE=\"yes\" WINDOWSTATE=\"minimize\" />",
		"<script language=\"VBScript\">",
		"' Main: parse protocol URL -> decode base64url -> write models.json",
		"Sub Window_OnLoad",
		"  On Error Resume Next",
		"  Dim cl, payload, ok",
		"  ok = False",
		"  cl = oHTA.commandLine",
		"  payload = normalizePayload(extractPayload(cl))",
		"  If Len(payload) > 0 Then ok = WriteConfig(payload)",
		"  If Err.Number <> 0 Then",
		"    logError cl, payload, Err.Number, Err.Description",
		"  ElseIf ok <> True Then",
		"    logError cl, payload, 0, \"write returned false\"",
		"  End If",
		"  WriteResult ok",
		"  self.close",
		"End Sub",
		"",
		"' Persist latest result so the page (or user) can inspect it: ok / fail",
		"Sub WriteResult(ByVal ok)",
		"  On Error Resume Next",
		"  Dim fso, shell, dir, f, ts, text",
		"  Set fso = CreateObject(\"Scripting.FileSystemObject\")",
		"  Set shell = CreateObject(\"WScript.Shell\")",
		"  dir = shell.ExpandEnvironmentStrings(\"%USERPROFILE%\") & \"\\.workbuddy\"",
		"  If Not fso.FolderExists(dir) Then fso.CreateFolder(dir) End If",
		"  f = dir & \"\\import-result.txt\"",
		"  If ok = True Then",
		"    text = \"ok \" & Now",
		"  Else",
		"    text = \"fail \" & Now",
		"  End If",
		"  Set ts = fso.CreateTextFile(f, True)",
		"  ts.Write text",
		"  ts.Close",
		"End Sub",
		"",
		"' Write config file, returns True on success",
		"Function WriteConfig(ByVal payload)",
		"  Dim fso, shell, dir, file, backup, stream, bytes",
		"  Set fso = CreateObject(\"Scripting.FileSystemObject\")",
		"  Set shell = CreateObject(\"WScript.Shell\")",
		"  dir = shell.ExpandEnvironmentStrings(\"%USERPROFILE%\") & \"\\.workbuddy\"",
		"  If Not fso.FolderExists(dir) Then fso.CreateFolder(dir) End If",
		"  file = dir & \"\\models.json\"",
		"  bytes = base64ToBytes(payload)",
		"  If Not IsArray(bytes) Then",
		"    WriteConfig = False",
		"    Exit Function",
		"  End If",
		"  ' Refuse to overwrite a good config with an empty payload",
		"  If UBound(bytes) < 0 Then",
		"    WriteConfig = False",
		"    Exit Function",
		"  End If",
		"  If fso.FileExists(file) Then",
		"    backup = dir & \"\\models.json.backup\"",
		"    fso.CopyFile file, backup, True",
		"  End If",
		"  Set stream = CreateObject(\"ADODB.Stream\")",
		"  stream.Type = 1",
		"  stream.Open",
		"  stream.Write bytes",
		"  stream.SaveToFile file, 2",
		"  stream.Close",
		"  If Err.Number = 0 Then WriteConfig = True Else WriteConfig = False",
		"End Function",
		"",
		"' Take the part after \"?d=\" (Windows may normalize to .../import/?d=...)",
		"Function extractPayload(ByVal s)",
		"  Dim q",
		"  q = InStr(s, \"?d=\")",
		"  If q > 0 Then",
		"    extractPayload = Mid(s, q + 3)",
		"  Else",
		"    extractPayload = \"\"",
		"  End If",
		"End Function",
		"",
		"' Convert payload to standard base64 (re-add = padding).",
		"' Handles both the current base64url form and the legacy percent-encoded form,",
		"' so older cached pages keep working. Only [A-Za-z0-9+/] survives.",
		"Function normalizePayload(ByVal s)",
		"  Dim i, ch, tmp, outStr",
		"  ' Step 1: undo legacy %XX percent-encoding, if any",
		"  tmp = \"\"",
		"  i = 1",
		"  Do While i <= Len(s)",
		"    ch = Mid(s, i, 1)",
		"    If ch = \"%\" And i + 2 <= Len(s) Then",
		"      tmp = tmp & ChrW(CLng(\"&H\" & Mid(s, i + 1, 2)))",
		"      i = i + 3",
		"    Else",
		"      tmp = tmp & ch",
		"      i = i + 1",
		"    End If",
		"  Loop",
		"  ' Step 2: base64url -> base64, keep only valid chars, re-pad to a 4x length",
		"  outStr = \"\"",
		"  For i = 1 To Len(tmp)",
		"    ch = Mid(tmp, i, 1)",
		"    If ch = \"-\" Then",
		"      outStr = outStr & \"+\"",
		"    ElseIf ch = \"_\" Then",
		"      outStr = outStr & \"/\"",
		"    ElseIf (ch >= \"A\" And ch <= \"Z\") Or (ch >= \"a\" And ch <= \"z\") Or (ch >= \"0\" And ch <= \"9\") Or ch = \"+\" Or ch = \"/\" Then",
		"      outStr = outStr & ch",
		"    End If",
		"  Next",
		"  Do While (Len(outStr) Mod 4) <> 0",
		"    outStr = outStr & \"=\"",
		"  Loop",
		"  normalizePayload = outStr",
		"End Function",
		"",
		"' base64 -> byte array (sanitize input first to avoid MSXML E_INVALIDARG)",
		"Function base64ToBytes(ByVal s)",
		"  Dim xml, node, clean, i, ch",
		"  clean = \"\"",
		"  For i = 1 To Len(s)",
		"    ch = Mid(s, i, 1)",
		"    If (ch >= \"A\" And ch <= \"Z\") Or (ch >= \"a\" And ch <= \"z\") Or (ch >= \"0\" And ch <= \"9\") Or ch = \"+\" Or ch = \"/\" Or ch = \"=\" Then",
		"      clean = clean & ch",
		"    End If",
		"  Next",
		"  clean = Replace(clean, \"=\", \"\")",
		"  Do While (Len(clean) Mod 4) <> 0",
		"    clean = clean & \"=\"",
		"  Loop",
		"  Set xml = CreateObject(\"MSXML2.DOMDocument.6.0\")",
		"  Set node = xml.createElement(\"b\")",
		"  node.dataType = \"bin.base64\"",
		"  node.text = clean",
		"  base64ToBytes = node.nodeTypedValue",
		"End Function",
		"",
		"' Write diagnostics on failure",
		"Sub logError(ByVal cl, ByVal payload, ByVal code, ByVal desc)",
		"  On Error Resume Next",
		"  Dim fso, lf, dir, shell",
		"  Set fso = CreateObject(\"Scripting.FileSystemObject\")",
		"  Set shell = CreateObject(\"WScript.Shell\")",
		"  dir = shell.ExpandEnvironmentStrings(\"%USERPROFILE%\") & \"\\.workbuddy\"",
		"  If Not fso.FolderExists(dir) Then fso.CreateFolder(dir) End If",
		"  Set lf = fso.OpenTextFile(dir & \"\\import-error.log\", 8, True)",
		"  lf.WriteLine \"time=\" & Now",
		"  lf.WriteLine \"code=\" & Hex(code) & \" desc=\" & desc",
		"  lf.WriteLine \"commandLine=\" & cl",
		"  lf.WriteLine \"payloadLen=\" & Len(payload)",
		"  lf.Close",
		"End Sub",
		"</script>",
		"</head>",
		"<body></body>",
		"</html>",
	}, "\r\n")
}

// workBuddyInstallerPsScript 返回安装器 PowerShell 脚本：
// 1) 在 %LOCALAPPDATA%\WorkBuddyImport\ 下写入 import.vbs
// 2) 注册 HKCU\Software\Classes\workbuddy-import 自定义协议，指向 wscript + import.vbs
func workBuddyInstallerPsScript() string {
	steps := []string{
		"$ErrorActionPreference = 'Stop'",
		"$root = Join-Path $env:LOCALAPPDATA 'WorkBuddyImport'",
		"if (-not (Test-Path $root)) { New-Item -ItemType Directory -Path $root | Out-Null }",
		"$htaPath = Join-Path $root 'import.hta'",
		"$htaB64 = @'",
		base64Utf8Go(workBuddyInstallerHtaScript()),
		"'@",
		"$htaBytes = [Convert]::FromBase64String(($htaB64 -replace '\\s','').Trim())",
		"[IO.File]::WriteAllBytes($htaPath, $htaBytes)",
		"$proto = 'HKCU:\\Software\\Classes\\" + WorkBuddyProtocol + "'",
		"New-Item -Path $proto -Force | Out-Null",
		"Set-ItemProperty -Path $proto -Name '(Default)' -Value 'URL:WorkBuddy Import Protocol'",
		"Set-ItemProperty -Path $proto -Name 'URL Protocol' -Value ''",
		"$cmdKey = Join-Path $proto 'shell\\open\\command'",
		"New-Item -Path $cmdKey -Force | Out-Null",
		"$cmd = '\"' + (Join-Path $env:SystemRoot 'System32\\mshta.exe') + '\" \"' + $htaPath + '\" \"%1\"'",
		"Set-ItemProperty -Path $cmdKey -Name '(Default)' -Value $cmd",
		"Write-Host ''",
		"Write-Host '  安装完成！' -ForegroundColor Green",
		"Write-Host '  现在回到浏览器点击【一键导入】，即可自动写入 WorkBuddy 配置。' -ForegroundColor Green",
		"Write-Host ''",
	}
	return strings.Join(steps, "\r\n")
}

// base64Utf8Go 以 UTF-8 编码做 base64
func base64Utf8Go(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// base64Utf16LEGo 以 UTF-16LE 编码做 base64（用于 PowerShell -EncodedCommand）
func base64Utf16LEGo(s string) string {
	runes := []rune(s)
	buf := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		if r > 0xFFFF {
			// 补充平面字符，拆分代理对
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			buf = append(buf, byte(hi&0xFF), byte(hi>>8), byte(lo&0xFF), byte(lo>>8))
			continue
		}
		buf = append(buf, byte(r&0xFF), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// GetWorkBuddyInstaller 返回一键导入所需的"本地接收器"安装器（.bat）。
// 用户双击运行一次即可注册 workbuddy-import:// 协议（指向 mshta + import.hta 启动器）；
// 之后在网页点击「一键导入」就能自动把配置写入本机 WorkBuddy。
// 可通过 ?redirect=<url> 指定安装完成后自动打开的页面地址。
func GetWorkBuddyInstaller(c *gin.Context) {
	encoded := base64Utf16LEGo(workBuddyInstallerPsScript())

	redirect := strings.TrimSpace(c.Query("redirect"))
	if redirect == "" {
		redirect = system_setting.ServerAddress
	}
	// 仅允许 http/https，避免把任意内容拼进批处理造成注入
	if !strings.HasPrefix(redirect, "http://") && !strings.HasPrefix(redirect, "https://") {
		redirect = system_setting.ServerAddress
	}
	// 批处理中对引号与百分号做转义，防止命令注入
	redirect = strings.ReplaceAll(redirect, "\"", "")
	redirect = strings.ReplaceAll(redirect, "%", "%%")

	lines := []string{
		"@echo off",
		"setlocal EnableExtensions",
		"chcp 65001 >nul 2>&1",
		"title WorkBuddy 一键导入 - 组件安装（自动完成）",
		"echo.",
		"echo   正在安装 WorkBuddy 一键导入组件，请勿关闭此窗口（约 3 秒）...",
		"echo.",
		"powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + encoded,
		"if errorlevel 1 goto FAILED",
		"echo.",
		"echo   安装成功！正在自动返回网页，回到页面后点击【一键导入 WorkBuddy】即可。",
		"echo.",
		"start \"\" \"" + redirect + "\"",
		"timeout /t 2 /nobreak >nul 2>&1",
		"exit /b 0",
		"",
		":FAILED",
		"echo.",
		"echo   安装失败，请检查当前用户目录权限后重试，或截图联系管理员。",
		"echo.",
		"pause",
		"exit /b 1",
	}

	body := strings.Join(lines, "\r\n")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename=\"workbuddy-install.bat\"")
	c.String(200, "\ufeff"+body)
}

// GetWorkBuddyProtocolName 供前端查询协议名，避免前后端硬编码不一致
func GetWorkBuddyProtocolName(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"protocol": WorkBuddyProtocol,
		"installer_url": fmt.Sprintf("/api/workbuddy/installer"),
	})
}
