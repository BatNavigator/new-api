package common

import (
	"testing"
)

// TestIsDomesticModel_ModelNameOnly 验证仅凭模型名（无供应商信息）的判定结果。
// 覆盖国内外典型模型，确保不会把国产模型误判为境外，反之亦然。
func TestIsDomesticModel_ModelNameOnly(t *testing.T) {
	domesticCases := []string{
		"qwen-plus",
		"qwen2.5-72b-instruct",
		"qwq-32b",
		"glm-4-plus",
		"glm-4.5",
		"chatglm3-6b",
		"deepseek-chat",
		"deepseek-reasoner",
		"deepseek-r1",
		"moonshot-v1-8k",
		"kimi-k2-0711-preview",
		"ernie-4.0-8k",
		"hunyuan-lite",
		"doubao-pro-32k",
		"spark-max",
		"yi-large",
		"baichuan2-13b",
		"abab6.5s-chat",
		"minimax-abab6",
		"step-2-16k",
		"internlm2-chat-20b",
		"minicpm-v",
		"ai360-fusion",
		"kling-v1",
		"jimeng-v2",
		"pangu-weather",
		"sensenova-chat",
		"telechat-12b",
		"skywork-13b",
	}
	for _, name := range domesticCases {
		if !IsDomesticModel(name, "", nil) {
			t.Errorf("模型 %q 应判定为境内，实际判定为境外", name)
		}
	}

	foreignCases := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-3.5-turbo",
		"o1-preview",
		"o3-mini",
		"o4-mini",
		"dall-e-3",
		"whisper-1",
		"text-embedding-3-large",
		"claude-3-5-sonnet-20241022",
		"claude-opus-4",
		"claude-3-haiku",
		"gemini-2.5-pro",
		"gemini-2.0-flash",
		"gemma-2-9b",
		"llama-3.1-70b",
		"mistral-large-latest",
		"mixtral-8x7b",
		"command-r-plus",
		"grok-2",
		"phi-3-mini",
		"stable-diffusion-xl",
		"perplexity-sonar",
		"falcon-180b",
	}
	for _, name := range foreignCases {
		if IsDomesticModel(name, "", nil) {
			t.Errorf("模型 %q 应判定为境外，实际判定为境内", name)
		}
	}
}

// TestIsDomesticModel_UnknownDefaultsToForeign 验证三级判定全部未识别时
// 默认按境外处理（默认拒绝），这是合规要求下的安全兜底行为。
func TestIsDomesticModel_UnknownDefaultsToForeign(t *testing.T) {
	unknownCases := []string{
		"my-private-model",
		"internal-llm-v3",
		"whatever-unknown-name",
		"foo",
	}
	for _, name := range unknownCases {
		if IsDomesticModel(name, "", nil) {
			t.Errorf("未识别的模型 %q 应默认判定为境外，实际判定为境内", name)
		}
	}
}

// TestIsDomesticModel_VendorTakesPrecedenceOverKeywords 验证第 2 级（供应商名）
// 优先于第 3 级（模型名关键词）。
//
// 场景：国内厂商的供应商名 + 一个看起来像境外模型的模型名。
// 例如站长把 OpenAI 兼容网关挂在「智谱」供应商下，供应商信息应作为权威判据。
func TestIsDomesticModel_VendorTakesPrecedenceOverKeywords(t *testing.T) {
	// 供应商为国内厂商，即便模型名含 "gpt" 也应按境内处理
	if !IsDomesticModel("custom-gpt-proxy", "智谱", nil) {
		t.Error("供应商为国内厂商时，应判定为境内模型")
	}

	// 供应商为境外厂商，即便模型名含 "qwen" 也应判定为境外
	if IsDomesticModel("qwen-proxy-by-openai", "OpenAI", nil) {
		t.Error("供应商为境外厂商时，应判定为境外模型")
	}
}

// TestIsDomesticModel_AllowListOverridesEverything 验证第 1 级（显式放行名单）
// 优先级最高，可覆盖供应商与关键词判定。
func TestIsDomesticModel_AllowListOverridesEverything(t *testing.T) {
	allowList := []string{"gpt-4o-tuned", "my-own-model", " Custom-Model "}

	// 显式放行的境外名模型 -> 放行
	if !IsDomesticModel("gpt-4o-tuned", "OpenAI", allowList) {
		t.Error("显式放行名单中的模型应判定为境内")
	}
	// 大小写不敏感
	if !IsDomesticModel("MY-OWN-MODEL", "OpenAI", allowList) {
		t.Error("显式放行名单匹配应忽略大小写")
	}
	// 名单中的前后空格应被裁掉
	if !IsDomesticModel("custom-model", "", allowList) {
		t.Error("显式放行名单匹配应忽略首尾空格")
	}
	// 不在名单中的境外模型仍被拒绝
	if IsDomesticModel("gpt-4o", "OpenAI", allowList) {
		t.Error("不在放行名单中的境外模型应判定为境外")
	}
}

// TestIsDomesticModel_EmptyName 验证空模型名不会误判为境内
func TestIsDomesticModel_EmptyName(t *testing.T) {
	if IsDomesticModel("", "智谱", []string{"whatever"}) {
		t.Error("空模型名应判定为境外（false）")
	}
	if IsDomesticModel("   ", "", nil) {
		t.Error("纯空白模型名应判定为境外（false）")
	}
}

// TestIsDomesticModelName_ShortModelPrecision 验证 o1/o3/o4 这类短型号
// 仅在作为独立型号出现时才判定为境外，避免子串误伤。
func TestIsDomesticModelName_ShortModelPrecision(t *testing.T) {
	// 独立出现 -> 境外
	for _, name := range []string{"o1", "o1-mini", "o3-mini", "o4", "o1_preview", "gpt-o1"} {
		if IsDomesticModel(name, "", nil) {
			t.Errorf("短型号 %q 应判定为境外", name)
		}
	}

	// 作为其他单词的一部分 -> 不应被 o1 规则命中（并因无法识别而默认境外，
	// 但这里重点验证的是不会因 o1 子串而产生错误判定路径）
	if domestic, identified := IsDomesticModelName("proto1"); domestic || identified {
		t.Error("proto1 不应被 o1 规则命中为「已识别的境外模型」")
	}
	if domestic, identified := IsDomesticModelName("ai21-jamba"); domestic || identified {
		t.Error("ai21-jamba 不应被 o1 规则命中为「已识别的境外模型」")
	}
}

// TestIsDomesticVendorName 验证供应商名判定
func TestIsDomesticVendorName(t *testing.T) {
	domesticVendors := []string{"智谱", "阿里巴巴", "DeepSeek", "Moonshot", "腾讯", "字节跳动", "百度", "零一万物"}
	for _, v := range domesticVendors {
		domestic, identified := IsDomesticVendorName(v)
		if !identified || !domestic {
			t.Errorf("供应商 %q 应被识别为境内厂商", v)
		}
	}

	foreignVendors := []string{"OpenAI", "Anthropic", "Google", "Meta", "Mistral", "xAI", "Cohere"}
	for _, v := range foreignVendors {
		domestic, identified := IsDomesticVendorName(v)
		if !identified || domestic {
			t.Errorf("供应商 %q 应被识别为境外厂商", v)
		}
	}

	// 未识别的供应商名
	if _, identified := IsDomesticVendorName("某不存在厂商"); identified {
		t.Error("未知供应商名不应被识别")
	}
	if _, identified := IsDomesticVendorName(""); identified {
		t.Error("空供应商名不应被识别")
	}
}
