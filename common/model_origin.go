package common

import "strings"

// 模型「境内 / 境外」归属判定。
//
// 背景：WorkBuddy 一键导入等场景只允许用户导入境内（国内）厂商的模型。
// 项目现有数据里并没有「国内外」这样的显式字段，因此这里采用三级判定：
//
//	第 1 级 显式名单：管理员在系统设置里配置的模型白名单，命中即视为境内（最高优先级）
//	第 2 级 供应商名：模型绑定的 vendor 名称（如「智谱」「阿里巴巴」「OpenAI」）
//	第 3 级 模型名关键词：模型名本身（如 qwen-plus、glm-4、gpt-4o）
//
// 三级都无法判定时按「境外」处理（默认拒绝），保证不会因为信息缺失而放行。

// 境内（国内）供应商名称，用于第 2 级判定。
// 名称来源见 model/pricing_default.go 的 defaultVendorRules / defaultVendorIcons。
var domesticVendorNames = []string{
	"智谱", "阿里巴巴", "阿里", "百度", "腾讯", "字节跳动", "字节", "月之暗面",
	"深度求索", "深度", "零一万物", "讯飞", "快手", "即梦", "商汤", "百川",
	"华为", "盘古", "昆仑", "天工", "硅基流动", "魔搭", "阶跃",
	"deepseek", "moonshot", "kimi", "qwen", "zhipu", "glm", "baichuan",
	"ernie", "wenxin", "hunyuan", "doubao", "volcengine", "spark", "iflytek",
	"stepfun", "sensetime", "openbmb", "minicpm", "minimax",
	"yi-", "lingyiwanwu", "internlm", "modelscope", "siliconflow",
	"bailian", "dashscope", "kling", "vidu", "jimeng", "ai360",
}

// 境外（国外）供应商名称，用于第 2 级判定。
// 命中此表会直接判定为境外，避免仅靠关键词误伤（例如国内代理转售的 OpenAI 模型）。
var foreignVendorNames = []string{
	"openai", "anthropic", "google", "microsoft", "azure", "meta", "mistral",
	"cohere", "xai", "grok", "cloudflare", "nvidia", "amazon", "aws", "bedrock",
	"vertex", "perplexity", "together", "groq", "replicate", "huggingface",
	"stability", "midjourney", "runway", "elevenlabs", "deepmind", "ibm",
	"databricks", "sambanova", "fireworks", "openrouter", "ai21", "voyage",
	"jina",
}

// 境内（国内）模型名关键词，用于第 3 级判定。
var domesticModelKeywords = []string{
	// 阿里巴巴 通义千问系列
	"qwen", "tongyi", "dashscope", "bailian", "qwq",
	// 智谱 GLM / ChatGLM 系列
	"glm", "chatglm", "zhipu", "cogview", "cogvideo", "codegeex",
	// DeepSeek 系列
	"deepseek",
	// 月之暗面 Kimi 系列
	"moonshot", "kimi",
	// 百度文心系列
	"ernie", "wenxin", "yiyan",
	// 腾讯混元系列
	"hunyuan",
	// 字节跳动 豆包 / 火山系列
	"doubao", "volc", "skylark", "seed-", "ui-tars",
	// 讯飞星火
	"spark", "xinghuo",
	// 零一万物
	"yi-", "lingyiwanwu",
	// 百川
	"baichuan",
	// MiniMax
	"abab", "minimax", "hailuo",
	// 华为盘古
	"pangu",
	// 商汤
	"sensenova", "sensechat",
	// 阶跃星辰
	"step-", "stepfun",
	// 昆仑万维天工
	"skywork", "tiangong",
	// 快手可灵 / 即梦
	"kling", "jimeng",
	// 书生 / 面壁
	"internlm", "internvl", "minicpm", "openbmb",
	// 360
	"ai360", "360-",
	// 中国电信 / 其他国内模型
	"telechat", "tele-", "xverse", "tigerbot", "aquila",
}

// 境外（国外）模型名关键词，用于第 3 级判定。
// 注意：
//  1. 判定顺序上境内关键词优先，本表仅用于在供应商未知时识别典型境外模型；
//  2. 本表不得包含任何境内厂商的关键词，否则会把国产模型误判为境外。
var foreignModelKeywords = []string{
	// OpenAI
	"gpt", "dall-e", "whisper", "text-embedding", "chatgpt", "sora",
	// Anthropic（claude 同时含 sonnet/opus/haiku 子型号）
	"claude", "sonnet", "opus", "haiku",
	// Google
	"gemini", "palm", "imagen", "veo", "gemma",
	// Meta
	"llama",
	// Mistral
	"mistral", "mixtral", "codestral", "pixtral",
	// Cohere
	"command-r", "command-", "embed-english", "embed-multilingual",
	// xAI
	"grok",
	// 其他境外厂商
	"phi-", "falcon", "stable-diffusion", "flux", "sdxl",
	"nova-", "titan", "j2-", "jurassic",
	"perplexity", "sonar", "dolphin", "wizardlm", "vicuna", "alpaca",
	"solar", "exaone", "olmo", "pythia", "aya-", "hermes",
}

// 独立成词的型号关键词：用「短名 + 数字」形式匹配（如 o1 / o3 / o4），
// 避免纯子串匹配把 "o1" 误命中 "proto1" 之类的名称。
var foreignModelExactPrefixes = []string{
	"o1", "o3", "o4",
}

// containsAnyKeyword 判断名称（已转小写）是否命中任一关键词
func containsAnyKeyword(lowerName string, keywords []string) bool {
	for _, kw := range keywords {
		if kw != "" && strings.Contains(lowerName, kw) {
			return true
		}
	}
	return false
}

// matchExactPrefix 判断短型号名是否作为「独立型号」出现，
// 即该短名位于名称开头、或以分隔符（- / _ / . / 空格 / :）结尾，
// 避免 "o1" 命中 "proto1"、"ai21" 之类的干扰项。
func matchExactPrefix(lowerName string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if lowerName == p || strings.HasPrefix(lowerName, p+"-") ||
			strings.HasPrefix(lowerName, p+"_") || strings.HasPrefix(lowerName, p+".") ||
			strings.HasPrefix(lowerName, p+" ") || strings.HasPrefix(lowerName, p+":") ||
			strings.HasSuffix(lowerName, "-"+p) || strings.HasSuffix(lowerName, "/"+p) {
			return true
		}
	}
	return false
}

// IsDomesticVendorName 依据供应商名称判断是否为境内厂商。
// 返回 (是否境内, 是否成功识别)。识别成功时第二个返回值为 true。
func IsDomesticVendorName(vendorName string) (bool, bool) {
	name := strings.ToLower(strings.TrimSpace(vendorName))
	if name == "" {
		return false, false
	}
	if containsAnyKeyword(name, domesticVendorNames) {
		return true, true
	}
	if containsAnyKeyword(name, foreignVendorNames) {
		return false, true
	}
	return false, false
}

// IsDomesticModelName 依据模型名判断模型归属。
// 返回 (是否境内, 是否成功识别)。
//
// 判定规则：先看境内关键词，命中即境内；再看境外关键词（含 o1/o3/o4 精确型号），
// 命中即境外；两者都不命中则视为「未识别」。
func IsDomesticModelName(modelName string) (bool, bool) {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return false, false
	}
	if containsAnyKeyword(name, domesticModelKeywords) {
		return true, true
	}
	if containsAnyKeyword(name, foreignModelKeywords) || matchExactPrefix(name, foreignModelExactPrefixes) {
		return false, true
	}
	return false, false
}

// IsDomesticModel 综合判定模型是否属于境内模型。
//
// 参数：
//   - modelName：模型名（必填）
//   - vendorName：模型绑定的供应商名称，可为空
//   - explicitAllowList：管理员配置的显式放行名单（模型名精确匹配，忽略大小写），可为空
//
// 三级判定顺序：显式名单 → 供应商名 → 模型名关键词；全部未识别时默认判定为境外（false）。
func IsDomesticModel(modelName string, vendorName string, explicitAllowList []string) bool {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return false
	}

	// 第 1 级：管理员显式放行名单（优先级最高，可覆盖后两级判定）
	for _, allowed := range explicitAllowList {
		if strings.EqualFold(strings.TrimSpace(allowed), trimmed) {
			return true
		}
	}

	// 第 2 级：供应商名称
	if domestic, identified := IsDomesticVendorName(vendorName); identified {
		return domestic
	}

	// 第 3 级：模型名关键词
	if domestic, identified := IsDomesticModelName(trimmed); identified {
		return domestic
	}

	// 无法识别：按境外处理（默认拒绝）
	return false
}
