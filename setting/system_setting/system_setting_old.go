package system_setting

import "strings"

var ServerAddress = "http://localhost:3000"
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false

// ---- WorkBuddy 一键导入配置 ----

// WorkBuddyDomesticOnly 为 true 时，一键导入只允许使用境内（国内）厂商模型，
// 境外模型会在后端接口层被直接过滤掉（前端无法绕过）。
// 默认开启，符合「只让用户导入国内模型」的合规要求。
var WorkBuddyDomesticOnly = true

// WorkBuddyDomesticModelAllowList 是显式放行名单（英文逗号分隔的模型名）。
// 命中该名单的模型一律视为境内模型，优先级高于供应商/关键词判定，
// 用于修正自动判定误伤的自部署模型或私有别名。
var WorkBuddyDomesticModelAllowList = ""

func EnableWorker() bool {
	return WorkerUrl != ""
}

// IsWorkBuddyDomesticOnlyEnabled 返回是否启用「仅允许境内模型」限制
func IsWorkBuddyDomesticOnlyEnabled() bool {
	return WorkBuddyDomesticOnly
}

// GetWorkBuddyDomesticModelAllowList 解析显式放行名单，返回去重后的模型名切片
func GetWorkBuddyDomesticModelAllowList() []string {
	if strings.TrimSpace(WorkBuddyDomesticModelAllowList) == "" {
		return nil
	}
	parts := strings.Split(WorkBuddyDomesticModelAllowList, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	return result
}
