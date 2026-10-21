package writingaction

// OutputParams 是一次动作执行的产出条件，由客户端在执行前提交。
//
// 取值来自闭合清单，不接受自由描述。每个维度要么被该动作用上并给齐，要么
// 完全不给：不允许把零值当成默认值，也不允许把该动作不用的参数带进来。
type OutputParams struct {
	// Length 是产出长度：short / medium / long。
	Length Length
	// Tone 是产出语气：neutral / formal / academic / plain。
	Tone Tone
	// Ratio 是压缩或扩展的目标百分比，取 10..90 的整数。
	Ratio int
	// Transform 是转写动作的子参数。
	Transform *TransformParams
}

// TransformParams 是转写动作的子参数。
//
// Mode 决定哪个子参数必填：translate 必须带目标语言，explain 必须带读者层次；
// 两者互斥。这里不使用自由文本字段——那等于把写作要求换个名字请回来。
type TransformParams struct {
	// Mode 是转写子模式：translate / explain。
	Mode TransformMode
	// TargetLanguage 是 translate 模式的目标语言。
	TargetLanguage *TargetLanguage
	// Audience 是 explain 模式的读者层次。
	Audience *Audience
}

// TransformMode 是转写动作的子模式。
type TransformMode string

// 两种转写子模式。
const (
	// TransformTranslate 把内容译成目标语言。
	TransformTranslate TransformMode = "translate"
	// TransformExplain 面向目标读者层次解释内容。
	TransformExplain TransformMode = "explain"
)

// Length 是产出长度。
type Length string

// 三档产出长度。
const (
	// LengthShort 短。
	LengthShort Length = "short"
	// LengthMedium 中。
	LengthMedium Length = "medium"
	// LengthLong 长。
	LengthLong Length = "long"
)

// Tone 是产出语气。
type Tone string

// 四种产出语气。
const (
	// ToneNeutral 中性。
	ToneNeutral Tone = "neutral"
	// ToneFormal 正式。
	ToneFormal Tone = "formal"
	// ToneAcademic 学术。
	ToneAcademic Tone = "academic"
	// TonePlain 平实。
	TonePlain Tone = "plain"
)

// TargetLanguage 是转写后的目标语言。
type TargetLanguage string

// 五种目标语言。
const (
	// LangZH 中文。
	LangZH TargetLanguage = "zh"
	// LangEN 英文。
	LangEN TargetLanguage = "en"
	// LangJA 日文。
	LangJA TargetLanguage = "ja"
	// LangDE 德文。
	LangDE TargetLanguage = "de"
	// LangFR 法文。
	LangFR TargetLanguage = "fr"
)

// Audience 是转写后面对的读者层次。
type Audience string

// 三种读者层次。
const (
	// AudiencePeer 同行。
	AudiencePeer Audience = "peer"
	// AudienceReviewer 评审人。
	AudienceReviewer Audience = "reviewer"
	// AudienceNovice 入门读者。
	AudienceNovice Audience = "novice"
)

// 产出参数每个维度的闭合取值集。取值不在这张表里的请求一律被拒。
var (
	lengthValues         = []Length{LengthShort, LengthMedium, LengthLong}
	toneValues           = []Tone{ToneNeutral, ToneFormal, ToneAcademic, TonePlain}
	transformModeValues  = []TransformMode{TransformTranslate, TransformExplain}
	targetLanguageValues = []TargetLanguage{LangZH, LangEN, LangJA, LangDE, LangFR}
	audienceValues       = []Audience{AudiencePeer, AudienceReviewer, AudienceNovice}
)

// 压缩与扩展比例的闭区间边界。
const (
	minRatio = 10
	maxRatio = 90
)
