package provider

// vendorSpec is a well-known OpenAI-compatible upstream, activated only when
// its API keys are supplied via KeysEnv (comma-separated => multiple accounts).
type vendorSpec struct {
	ID            string
	BaseURL       string
	KeysEnv       string
	ModelsEnv     string
	BaseURLEnv    string // optional override (e.g. AWS region)
	OwnedBy       string
	DefaultModels []string
}

// vendors mirrors the TS VENDORS table (Chinese + global OpenAI-compatible APIs).
var vendors = []vendorSpec{
	{ID: "deepseek", BaseURL: "https://api.deepseek.com", KeysEnv: "DEEPSEEK_API_KEYS", ModelsEnv: "DEEPSEEK_MODELS", OwnedBy: "deepseek", DefaultModels: []string{"deepseek-chat", "deepseek-reasoner"}},
	{ID: "moonshot", BaseURL: "https://api.moonshot.cn/v1", KeysEnv: "MOONSHOT_API_KEYS", ModelsEnv: "MOONSHOT_MODELS", OwnedBy: "moonshot", DefaultModels: []string{"kimi-k2-0905-preview", "moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"}},
	{ID: "zhipu", BaseURL: "https://open.bigmodel.cn/api/paas/v4", KeysEnv: "ZHIPU_API_KEYS", ModelsEnv: "ZHIPU_MODELS", OwnedBy: "zhipu", DefaultModels: []string{"glm-4.6", "glm-4.5", "glm-4-plus", "glm-4-flash"}},
	{ID: "qwen", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", KeysEnv: "QWEN_API_KEYS", ModelsEnv: "QWEN_MODELS", OwnedBy: "alibaba", DefaultModels: []string{"qwen-max", "qwen-plus", "qwen-turbo", "qwen3-coder-plus"}},
	{ID: "baidu", BaseURL: "https://qianfan.baidubce.com/v2", KeysEnv: "BAIDU_API_KEYS", ModelsEnv: "BAIDU_MODELS", OwnedBy: "baidu", DefaultModels: []string{"ernie-4.5-turbo-128k", "ernie-4.0-turbo-8k", "ernie-x1-turbo-32k", "ernie-3.5-8k"}},
	{ID: "sensetime", BaseURL: "https://api.sensenova.cn/compatible-mode/v1", KeysEnv: "SENSETIME_API_KEYS", ModelsEnv: "SENSETIME_MODELS", OwnedBy: "sensetime", DefaultModels: []string{"SenseChat-5", "SenseChat-5-1202", "SenseChat-Turbo"}},
	{ID: "skywork", BaseURL: "https://api.singularity-ai.com/v1", KeysEnv: "SKYWORK_API_KEYS", ModelsEnv: "SKYWORK_MODELS", OwnedBy: "kunlun", DefaultModels: []string{"SkyChat-MegaVerse"}},
	{ID: "360", BaseURL: "https://api.360.cn/v1", KeysEnv: "AI360_API_KEYS", ModelsEnv: "AI360_MODELS", OwnedBy: "360", DefaultModels: []string{"360gpt-pro", "360gpt2-pro"}},
	{ID: "minimax", BaseURL: "https://api.minimaxi.com/v1", KeysEnv: "MINIMAX_API_KEYS", ModelsEnv: "MINIMAX_MODELS", OwnedBy: "minimax", DefaultModels: []string{"MiniMax-Text-01", "abab6.5s-chat"}},
	{ID: "doubao", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", KeysEnv: "DOUBAO_API_KEYS", ModelsEnv: "DOUBAO_MODELS", OwnedBy: "bytedance", DefaultModels: []string{"doubao-pro-32k", "doubao-pro-256k", "doubao-lite-32k"}},
	{ID: "hunyuan", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1", KeysEnv: "HUNYUAN_API_KEYS", ModelsEnv: "HUNYUAN_MODELS", OwnedBy: "tencent", DefaultModels: []string{"hunyuan-turbo", "hunyuan-pro", "hunyuan-standard"}},
	{ID: "spark", BaseURL: "https://spark-api-open.xf-yun.com/v1", KeysEnv: "SPARK_API_KEYS", ModelsEnv: "SPARK_MODELS", OwnedBy: "iflytek", DefaultModels: []string{"4.0Ultra", "generalv3.5", "max-32k"}},
	{ID: "stepfun", BaseURL: "https://api.stepfun.com/v1", KeysEnv: "STEPFUN_API_KEYS", ModelsEnv: "STEPFUN_MODELS", OwnedBy: "stepfun", DefaultModels: []string{"step-2-16k", "step-1-8k", "step-1-32k"}},
	{ID: "yi", BaseURL: "https://api.lingyiwanwu.com/v1", KeysEnv: "YI_API_KEYS", ModelsEnv: "YI_MODELS", OwnedBy: "01ai", DefaultModels: []string{"yi-lightning", "yi-large"}},
	{ID: "baichuan", BaseURL: "https://api.baichuan-ai.com/v1", KeysEnv: "BAICHUAN_API_KEYS", ModelsEnv: "BAICHUAN_MODELS", OwnedBy: "baichuan", DefaultModels: []string{"Baichuan4-Turbo", "Baichuan4"}},
	{ID: "siliconflow", BaseURL: "https://api.siliconflow.cn/v1", KeysEnv: "SILICONFLOW_API_KEYS", ModelsEnv: "SILICONFLOW_MODELS", OwnedBy: "siliconflow", DefaultModels: []string{"deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct"}},
	{ID: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeysEnv: "GEMINI_API_KEYS", ModelsEnv: "GEMINI_MODELS", OwnedBy: "google", DefaultModels: []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-3-pro-preview"}},
	{ID: "aws-bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", BaseURLEnv: "AWS_BEDROCK_BASE_URL", KeysEnv: "AWS_BEDROCK_API_KEYS", ModelsEnv: "AWS_BEDROCK_MODELS", OwnedBy: "amazon", DefaultModels: []string{"openai.gpt-oss-120b-1:0", "anthropic.claude-sonnet-4-5-20250929-v1:0", "amazon.nova-pro-v1:0", "meta.llama3-3-70b-instruct-v1:0"}},
	{ID: "agnes", BaseURL: "https://apihub.agnes-ai.com/v1", KeysEnv: "AGNES_API_KEYS", ModelsEnv: "AGNES_MODELS", OwnedBy: "agnes-ai", DefaultModels: []string{"agnes-2.0-flash", "agnes-image-2.0-flash"}},
	{ID: "openrouter", BaseURL: "https://openrouter.ai/api/v1", KeysEnv: "OPENROUTER_API_KEYS", ModelsEnv: "OPENROUTER_MODELS", OwnedBy: "openrouter", DefaultModels: []string{"openrouter/auto"}},
	{ID: "grok", BaseURL: "https://api.x.ai/v1", KeysEnv: "XAI_API_KEYS", ModelsEnv: "GROK_MODELS", BaseURLEnv: "XAI_BASE_URL", OwnedBy: "xai", DefaultModels: []string{"grok-4", "grok-4-fast", "grok-3", "grok-3-mini"}},
}
