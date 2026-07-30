// Code generated from https://models.dev/api.json — DO NOT EDIT.
// Regenerate: go run ./cmd/catalog-gen
// Snapshot taken 2026-07-27 — 120 providers.
//
// Curated Registry entries always win: ids, base URLs and names already
// present above are skipped here, so hand-tuned OAuth flows and wire
// protocols are never clobbered by generated data.

package catalog

func init() { Registry = appendUnique(Registry, Generated) }

// Generated is the models.dev slice appended to Registry at init.
var Generated = []Entry{
	{ID: "302ai", Name: "302.AI", Auth: AuthNone, Flavor: FlavorOpenAI,
		BaseURL: "https://api.302.ai/v1",
		KeyHint: "doc.302.ai"},

	{ID: "abacus", Name: "Abacus", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://routellm.abacus.ai/v1",
		KeyEnv:  []string{"ABACUS_API_KEY"},
		KeyHint: "abacus.ai/help/api"},

	{ID: "abliteration-ai", Name: "abliteration.ai", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.abliteration.ai/v1",
		KeyEnv:  []string{"ABLIT_KEY"},
		KeyHint: "docs.abliteration.ai/models"},

	{ID: "ai-router", Name: "AI-ROUTER", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.ai-router.dev/v1",
		KeyEnv:  []string{"AI_ROUTER_API_KEY"},
		KeyHint: "ai-router.dev/openai-compatible-api-gateway"},

	{ID: "aiand", Name: "ai&", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.aiand.com/v1",
		KeyEnv:  []string{"AIAND_API_KEY"},
		KeyHint: "docs.aiand.com"},

	{ID: "aki-io", Name: "AKI.IO", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://aki.io/v1",
		KeyEnv:  []string{"AKI_IO_API_KEY"},
		KeyHint: "aki.io/docs"},

	{ID: "alibaba-cn", Name: "Alibaba (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		KeyEnv:  []string{"DASHSCOPE_API_KEY"},
		KeyHint: "www.alibabacloud.com/help/en/model-studio/models"},

	{ID: "alibaba-coding-plan", Name: "Alibaba Coding Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://coding-intl.dashscope.aliyuncs.com/v1",
		KeyEnv:  []string{"ALIBABA_CODING_PLAN_API_KEY"},
		KeyHint: "www.alibabacloud.com/help/en/model-studio/coding-plan"},

	{ID: "alibaba-coding-plan-cn", Name: "Alibaba Coding Plan (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://coding.dashscope.aliyuncs.com/v1",
		KeyEnv:  []string{"ALIBABA_CODING_PLAN_API_KEY"},
		KeyHint: "help.aliyun.com/zh/model-studio/coding-plan"},

	{ID: "alibaba-token-plan", Name: "Alibaba Token Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		KeyEnv:  []string{"ALIBABA_TOKEN_PLAN_API_KEY"},
		KeyHint: "www.alibabacloud.com/help/en/model-studio/token-plan-overview"},

	{ID: "alibaba-token-plan-cn", Name: "Alibaba Token Plan (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		KeyEnv:  []string{"ALIBABA_TOKEN_PLAN_API_KEY"},
		KeyHint: "www.alibabacloud.com/help/zh/model-studio/token-plan-overview"},

	{ID: "ambient", Name: "Ambient", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.ambient.xyz/v1",
		KeyEnv:  []string{"AMBIENT_API_KEY"},
		KeyHint: "ambient.xyz"},

	{ID: "anyapi", Name: "AnyAPI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.anyapi.ai/v1",
		KeyEnv:  []string{"ANYAPI_API_KEY"},
		KeyHint: "docs.anyapi.ai"},

	{ID: "atomic-chat", Name: "Atomic Chat", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "http://127.0.0.1:1337/v1",
		KeyEnv:  []string{"ATOMIC_CHAT_API_KEY"},
		KeyHint: "atomic.chat"},

	{ID: "auriko", Name: "Auriko", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.auriko.ai/v1",
		KeyEnv:  []string{"AURIKO_API_KEY"},
		KeyHint: "docs.auriko.ai"},

	{ID: "bailing", Name: "Bailing", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.tbox.cn/api/llm/v1/chat/completions",
		KeyEnv:  []string{"BAILING_API_TOKEN"},
		KeyHint: "alipaytbox.yuque.com/sxs0ba/ling/intro"},

	{ID: "baseten", Name: "Baseten", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://inference.baseten.co/v1",
		KeyEnv:  []string{"BASETEN_API_KEY"},
		KeyHint: "docs.baseten.co/inference/model-apis/overview"},

	{ID: "berget", Name: "Berget.AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.berget.ai/v1",
		KeyEnv:  []string{"BERGET_API_KEY"},
		KeyHint: "api.berget.ai"},

	{ID: "blueclaw", Name: "Blue Claw", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://openai.blueclaw.network/v1",
		KeyEnv:  []string{"BLUECLAW_API_KEY"},
		KeyHint: "blueclaw.network"},

	{ID: "chutes", Name: "Chutes", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://llm.chutes.ai/v1",
		KeyEnv:  []string{"CHUTES_API_KEY"},
		KeyHint: "llm.chutes.ai/v1/models"},

	{ID: "clarifai", Name: "Clarifai", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.clarifai.com/v2/ext/openai/v1",
		KeyEnv:  []string{"CLARIFAI_PAT"},
		KeyHint: "docs.clarifai.com/compute/inference"},

	{ID: "claudinio", Name: "Claudinio", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.claudin.io/v1",
		KeyEnv:  []string{"CLAUDINIO_API_KEY"},
		KeyHint: "claudin.io"},

	{ID: "cline-pass", Name: "ClinePass", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.cline.bot/api/v1",
		KeyEnv:  []string{"CLINE_API_KEY"},
		KeyHint: "docs.cline.bot/getting-started/clinepass"},

	{ID: "cloudferro-sherlock", Name: "CloudFerro Sherlock", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api-sherlock.cloudferro.com/openai/v1",
		KeyEnv:  []string{"CLOUDFERRO_SHERLOCK_API_KEY"},
		KeyHint: "docs.sherlock.cloudferro.com"},

	{ID: "cortecs", Name: "Cortecs", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.cortecs.ai/v1",
		KeyEnv:  []string{"CORTECS_API_KEY"},
		KeyHint: "api.cortecs.ai/v1/models"},

	{ID: "crof", Name: "CrofAI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://crof.ai/v1",
		KeyEnv:  []string{"CROF_API_KEY"},
		KeyHint: "crof.ai/docs"},

	{ID: "crossmodel", Name: "CrossModel", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.crossmodel.ai/v1",
		KeyEnv:  []string{"CROSSMODEL_API_KEY"},
		KeyHint: "www.crossmodel.ai/docs"},

	{ID: "daoxe", Name: "DaoXE", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://daoxe.com/v1",
		KeyEnv:  []string{"DAOXE_API_KEY"},
		KeyHint: "daoxe.com/pricing"},

	{ID: "digitalocean", Name: "DigitalOcean", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://inference.do-ai.run/v1",
		KeyEnv:  []string{"DIGITALOCEAN_ACCESS_TOKEN"},
		KeyHint: "docs.digitalocean.com/products/gradient-ai-platform/details/models"},

	{ID: "dinference", Name: "DInference", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.dinference.com/v1",
		KeyEnv:  []string{"DINFERENCE_API_KEY"},
		KeyHint: "dinference.com"},

	{ID: "drun", Name: "D.Run (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://chat.d.run/v1",
		KeyEnv:  []string{"DRUN_API_KEY"},
		KeyHint: "www.d.run"},

	{ID: "ebcloud", Name: "EBCloud", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://maas-api.ebcloud.com/v1",
		KeyEnv:  []string{"EBCLOUD_API_KEY"},
		KeyHint: "docs.ebtech.com/ai/model-api.html"},

	{ID: "empiriolabs", Name: "EmpirioLabs AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.empiriolabs.ai/v1",
		KeyEnv:  []string{"EMPIRIOLABS_API_KEY"},
		KeyHint: "docs.empiriolabs.ai"},

	{ID: "evroc", Name: "evroc", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://models.think.evroc.com/v1",
		KeyEnv:  []string{"EVROC_API_KEY"},
		KeyHint: "docs.evroc.com/products/think/overview.html"},

	{ID: "fastrouter", Name: "FastRouter", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://go.fastrouter.ai/api/v1",
		KeyEnv:  []string{"FASTROUTER_API_KEY"},
		KeyHint: "fastrouter.ai/models"},

	{ID: "freemodel", Name: "FreeModel", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://cc.freemodel.dev/v1",
		KeyEnv:  []string{"FREEMODEL_API_KEY"},
		KeyHint: "freemodel.dev"},

	{ID: "friendli", Name: "Friendli", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.friendli.ai/serverless/v1",
		KeyEnv:  []string{"FRIENDLI_TOKEN"},
		KeyHint: "friendli.ai/docs/guides/serverless_endpoints/introduction"},

	{ID: "frogbot", Name: "FrogBot", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://app.frogbot.ai/api/v1",
		KeyEnv:  []string{"FROGBOT_API_KEY"},
		KeyHint: "docs.frogbot.ai"},

	{ID: "github-models", Name: "GitHub Models", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://models.github.ai/inference",
		KeyEnv:  []string{"GITHUB_TOKEN"},
		KeyHint: "docs.github.com/en/github-models"},

	{ID: "helicone", Name: "Helicone", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://ai-gateway.helicone.ai/v1",
		KeyEnv:  []string{"HELICONE_API_KEY"},
		KeyHint: "helicone.ai/models"},

	{ID: "hetzner", Name: "Hetzner", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://inference.hetzner.com/api/v1",
		KeyEnv:  []string{"HETZNER_API_KEY"},
		KeyHint: "experiments.hetzner.com/docs/inference"},

	{ID: "hpc-ai", Name: "HPC-AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.hpc-ai.com/inference/v1",
		KeyEnv:  []string{"HPC_AI_API_KEY"},
		KeyHint: "www.hpc-ai.com/doc/docs/quickstart"},

	{ID: "iflowcn", Name: "iFlow", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://apis.iflow.cn/v1",
		KeyEnv:  []string{"IFLOW_API_KEY"},
		KeyHint: "platform.iflow.cn/en/docs"},

	{ID: "inception", Name: "Inception", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.inceptionlabs.ai/v1",
		KeyEnv:  []string{"INCEPTION_API_KEY"},
		KeyHint: "platform.inceptionlabs.ai/docs"},

	{ID: "inceptron", Name: "Inceptron", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.inceptron.io/v1",
		KeyEnv:  []string{"INCEPTRON_API_KEY"},
		KeyHint: "docs.inceptron.io"},

	{ID: "inferx", Name: "InferX", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://model.inferx.net/endpoints/v1",
		KeyEnv:  []string{"INFERX_API_KEY"},
		KeyHint: "model.inferx.net/endpoints"},

	{ID: "io-net", Name: "IO.NET", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.intelligence.io.solutions/api/v1",
		KeyEnv:  []string{"IOINTELLIGENCE_API_KEY"},
		KeyHint: "io.net/docs/guides/intelligence/io-intelligence"},

	{ID: "jiekou", Name: "Jiekou.AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.jiekou.ai/openai",
		KeyEnv:  []string{"JIEKOU_API_KEY"},
		KeyHint: "docs.jiekou.ai/docs/support/quickstart?utm_source=github_models.dev"},

	{ID: "kenari", Name: "Kenari", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://kenari.id/v1",
		KeyEnv:  []string{"KENARI_API_KEY"},
		KeyHint: "kenari.id/docs"},

	{ID: "kilo", Name: "Kilo Gateway", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.kilo.ai/api/gateway",
		KeyEnv:  []string{"KILO_API_KEY"},
		KeyHint: "kilo.ai"},

	{ID: "kimi-for-coding", Name: "Kimi For Coding", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://api.kimi.com/coding/v1",
		KeyEnv:  []string{"KIMI_API_KEY"},
		KeyHint: "www.kimi.com/code/docs/en/third-party-tools/other-coding-agents.html"},

	{ID: "kuae-cloud-coding-plan", Name: "KUAE Cloud Coding Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://coding-plan-endpoint.kuaecloud.net/v1",
		KeyEnv:  []string{"KUAE_API_KEY"},
		KeyHint: "docs.mthreads.com/kuaecloud/kuaecloud-doc-online/coding_plan"},

	{ID: "lilac", Name: "Lilac", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.getlilac.com/v1",
		KeyEnv:  []string{"LILAC_API_KEY"},
		KeyHint: "docs.getlilac.com/inference/models"},

	{ID: "llama", Name: "Llama", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.llama.com/compat/v1",
		KeyEnv:  []string{"LLAMA_API_KEY"},
		KeyHint: "llama.developer.meta.com/docs/models"},

	{ID: "llmgateway", Name: "LLM Gateway", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.llmgateway.io/v1",
		KeyEnv:  []string{"LLMGATEWAY_API_KEY"},
		KeyHint: "llmgateway.io/docs"},

	{ID: "llmtr", Name: "LLMTR", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://llmtr.com/v1",
		KeyEnv:  []string{"LLMTR_API_KEY"},
		KeyHint: "llmtr.com/docs"},

	{ID: "lucidquery", Name: "LucidQuery", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.lucidquery.com/v1",
		KeyEnv:  []string{"LUCIDQUERY_API_KEY"},
		KeyHint: "lucidquery.com/docs"},

	{ID: "lynkr", Name: "Lynkr", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "http://127.0.0.1:8081/v1",
		KeyEnv:  []string{"LYNKR_API_KEY"},
		KeyHint: "github.com/Fast-Editor/Lynkr"},

	{ID: "meganova", Name: "Meganova", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.meganova.ai/v1",
		KeyEnv:  []string{"MEGANOVA_API_KEY"},
		KeyHint: "docs.meganova.ai"},

	{ID: "meta", Name: "Meta", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.meta.ai/v1",
		KeyEnv:  []string{"META_MODEL_API_KEY"},
		KeyHint: "dev.meta.ai/docs"},

	{ID: "minimax-cn", Name: "MiniMax (minimaxi.com)", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://api.minimaxi.com/anthropic/v1",
		KeyEnv:  []string{"MINIMAX_API_KEY"},
		KeyHint: "platform.minimaxi.com/docs/guides/quickstart"},

	{ID: "minimax-coding-plan", Name: "MiniMax Token Plan (minimax.io)", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://api.minimax.io/anthropic/v1",
		KeyEnv:  []string{"MINIMAX_API_KEY"},
		KeyHint: "platform.minimax.io/docs/token-plan/intro"},

	{ID: "mixlayer", Name: "Mixlayer", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://models.mixlayer.ai/v1",
		KeyEnv:  []string{"MIXLAYER_API_KEY"},
		KeyHint: "docs.mixlayer.com"},

	{ID: "moark", Name: "Moark", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://moark.com/v1",
		KeyEnv:  []string{"MOARK_API_KEY"},
		KeyHint: "moark.com/docs/openapi/v1#tag/%E6%96%87%E6%9C%AC%E7%94%9F%E6%88%90"},

	{ID: "model-oracle-ai", Name: "Model Oracle AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.modeloracle.com/api/v1",
		KeyEnv:  []string{"MODEL_ORACLE_API_KEY"},
		KeyHint: "modeloracle.com/setup"},

	{ID: "modelscope", Name: "ModelScope", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api-inference.modelscope.cn/v1",
		KeyEnv:  []string{"MODELSCOPE_API_KEY"},
		KeyHint: "modelscope.cn/docs/model-service/API-Inference/intro"},

	{ID: "morph", Name: "Morph", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.morphllm.com/v1",
		KeyEnv:  []string{"MORPH_API_KEY"},
		KeyHint: "docs.morphllm.com/api-reference/introduction"},

	{ID: "nano-gpt", Name: "NanoGPT", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://nano-gpt.com/api/v1",
		KeyEnv:  []string{"NANO_GPT_API_KEY"},
		KeyHint: "docs.nano-gpt.com"},

	{ID: "nearai", Name: "NEAR AI Cloud", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://cloud-api.near.ai/v1",
		KeyEnv:  []string{"NEARAI_API_KEY"},
		KeyHint: "docs.near.ai"},

	{ID: "nebius", Name: "Nebius Token Factory", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.tokenfactory.nebius.com/v1",
		KeyEnv:  []string{"NEBIUS_API_KEY"},
		KeyHint: "docs.tokenfactory.nebius.com"},

	{ID: "neuralwatt", Name: "Neuralwatt", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.neuralwatt.com/v1",
		KeyEnv:  []string{"NEURALWATT_API_KEY"},
		KeyHint: "portal.neuralwatt.com/docs"},

	{ID: "nova", Name: "Nova", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.nova.amazon.com/v1",
		KeyEnv:  []string{"NOVA_API_KEY"},
		KeyHint: "nova.amazon.com/dev/documentation"},

	{ID: "ofox", Name: "Ofox", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.ofox.ai/v1",
		KeyEnv:  []string{"OFOX_API_KEY"},
		KeyHint: "ofox.ai/docs"},

	{ID: "orcarouter", Name: "OrcaRouter", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.orcarouter.ai/v1",
		KeyEnv:  []string{"ORCAROUTER_API_KEY"},
		KeyHint: "docs.orcarouter.ai"},

	{ID: "ovhcloud", Name: "OVHcloud AI Endpoints", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1",
		KeyEnv:  []string{"OVHCLOUD_API_KEY"},
		KeyHint: "www.ovhcloud.com/en/public-cloud/ai-endpoints/catalog"},

	{ID: "perplexity-agent", Name: "Perplexity Agent", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.perplexity.ai/v1",
		KeyEnv:  []string{"PERPLEXITY_API_KEY"},
		KeyHint: "docs.perplexity.ai/docs/agent-api/models"},

	{ID: "pioneer", Name: "Pioneer", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.pioneer.ai/v1",
		KeyEnv:  []string{"PIONEER_API_KEY"},
		KeyHint: "agent.pioneer.ai/llms.txt"},

	{ID: "poe", Name: "Poe", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.poe.com/v1",
		KeyEnv:  []string{"POE_API_KEY"},
		KeyHint: "creator.poe.com/docs/external-applications/openai-compatible-api"},

	{ID: "poolside", Name: "Poolside", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://inference.poolside.ai/v1",
		KeyEnv:  []string{"POOLSIDE_API_KEY"},
		KeyHint: "platform.poolside.ai"},

	{ID: "privatemode-ai", Name: "Privatemode AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "http://localhost:8080/v1",
		KeyEnv:  []string{"PRIVATEMODE_API_KEY", "PRIVATEMODE_ENDPOINT"},
		KeyHint: "docs.privatemode.ai/api/overview"},

	{ID: "qihang-ai", Name: "QiHang", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.qhaigc.net/v1",
		KeyEnv:  []string{"QIHANG_API_KEY"},
		KeyHint: "www.qhaigc.net/docs"},

	{ID: "qiniu-ai", Name: "Qiniu", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.qnaigc.com/v1",
		KeyEnv:  []string{"QINIU_API_KEY"},
		KeyHint: "developer.qiniu.com/aitokenapi"},

	{ID: "regolo-ai", Name: "Regolo AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.regolo.ai/v1",
		KeyEnv:  []string{"REGOLO_API_KEY"},
		KeyHint: "docs.regolo.ai"},

	{ID: "requesty", Name: "Requesty", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://router.requesty.ai/v1",
		KeyEnv:  []string{"REQUESTY_API_KEY"},
		KeyHint: "requesty.ai/solution/llm-routing/models"},

	{ID: "routing-run", Name: "routing.run", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.routing.run/v1",
		KeyEnv:  []string{"ROUTING_RUN_API_KEY"},
		KeyHint: "docs.routing.run/api-reference/models"},

	{ID: "sakana", Name: "Sakana AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.sakana.ai/v1",
		KeyEnv:  []string{"SAKANA_API_KEY"},
		KeyHint: "console.sakana.ai/models"},

	{ID: "sarvam", Name: "Sarvam AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.sarvam.ai/v1",
		KeyEnv:  []string{"SARVAM_API_KEY"},
		KeyHint: "docs.sarvam.ai/api-reference-docs/getting-started/models"},

	{ID: "scaleway", Name: "Scaleway", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.scaleway.ai/v1",
		KeyEnv:  []string{"SCALEWAY_API_KEY"},
		KeyHint: "www.scaleway.com/en/docs/generative-apis"},

	{ID: "siliconflow", Name: "SiliconFlow", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.siliconflow.com/v1",
		KeyEnv:  []string{"SILICONFLOW_API_KEY"},
		KeyHint: "cloud.siliconflow.com/models"},

	{ID: "siliconflow-cn", Name: "SiliconFlow (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.siliconflow.cn/v1",
		KeyEnv:  []string{"SILICONFLOW_CN_API_KEY"},
		KeyHint: "cloud.siliconflow.com/models"},

	{ID: "stackit", Name: "STACKIT", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.openai-compat.model-serving.eu01.onstackit.cloud/v1",
		KeyEnv:  []string{"STACKIT_API_KEY"},
		KeyHint: "docs.stackit.cloud/products/data-and-ai/ai-model-serving/basics/available-shared-models"},

	{ID: "stepfun-ai", Name: "StepFun (Global)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.stepfun.ai/v1",
		KeyEnv:  []string{"STEPFUN_API_KEY"},
		KeyHint: "platform.stepfun.ai/docs/en/overview/concept"},

	{ID: "stepfun-step-plan", Name: "StepFun Step Plan (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.stepfun.com/step_plan/v1",
		KeyEnv:  []string{"STEPFUN_API_KEY"},
		KeyHint: "platform.stepfun.com/docs/zh/step-plan/integrations/reasoning-api"},

	{ID: "subconscious", Name: "Subconscious", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://api.subconscious.dev/v1",
		KeyEnv:  []string{"SUBCONSCIOUS_API_KEY"},
		KeyHint: "docs.subconscious.dev"},

	{ID: "submodel", Name: "submodel", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://llm.submodel.ai/v1",
		KeyEnv:  []string{"SUBMODEL_INSTAGEN_ACCESS_KEY"},
		KeyHint: "submodel.gitbook.io"},

	{ID: "synthetic", Name: "Synthetic", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.synthetic.new/openai/v1",
		KeyEnv:  []string{"SYNTHETIC_API_KEY"},
		KeyHint: "synthetic.new/pricing"},

	{ID: "tencent-coding-plan", Name: "Tencent Coding Plan (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.lkeap.cloud.tencent.com/coding/v3",
		KeyEnv:  []string{"TENCENT_CODING_PLAN_API_KEY"},
		KeyHint: "cloud.tencent.com/document/product/1772/128947"},

	{ID: "tencent-token-plan", Name: "Tencent Token Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.lkeap.cloud.tencent.com/plan/v3",
		KeyEnv:  []string{"TENCENT_TOKEN_PLAN_API_KEY"},
		KeyHint: "cloud.tencent.com/document/product/1823/130060"},

	{ID: "tencent-tokenhub", Name: "Tencent TokenHub", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://tokenhub.tencentmaas.com/v1",
		KeyEnv:  []string{"TENCENT_TOKENHUB_API_KEY"},
		KeyHint: "cloud.tencent.com/document/product/1823/130050"},

	{ID: "the-grid-ai", Name: "The Grid AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.thegrid.ai/v1",
		KeyEnv:  []string{"THEGRIDAI_API_KEY"},
		KeyHint: "thegrid.ai/docs"},

	{ID: "thinkingmachines", Name: "Thinking Machines", Auth: AuthAPIKey, Flavor: FlavorAnthropic,
		BaseURL: "https://tinker.thinkingmachines.dev/services/tinker-prod/anthropic/api/v1",
		KeyEnv:  []string{"TINKER_API_KEY"},
		KeyHint: "tinker-docs.thinkingmachines.ai/tinker/compatible-apis/anthropic"},

	{ID: "tinfoil", Name: "Tinfoil", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://inference.tinfoil.sh/v1",
		KeyEnv:  []string{"TINFOIL_API_KEY"},
		KeyHint: "docs.tinfoil.sh"},

	{ID: "trustedrouter", Name: "TrustedRouter", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.trustedrouter.com/v1",
		KeyEnv:  []string{"TRUSTEDROUTER_API_KEY"},
		KeyHint: "trustedrouter.com/docs"},

	{ID: "umans-ai", Name: "Umans AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.code.umans.ai/v1",
		KeyEnv:  []string{"UMANS_AI_API_KEY"},
		KeyHint: "app.umans.ai/offers/code/docs/orgs"},

	{ID: "unorouter", Name: "UnoRouter", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.unorouter.com/v1",
		KeyEnv:  []string{"UNOROUTER_API_KEY"},
		KeyHint: "unorouter.com/models"},

	{ID: "upstage", Name: "Upstage", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.upstage.ai/v1/solar",
		KeyEnv:  []string{"UPSTAGE_API_KEY"},
		KeyHint: "developers.upstage.ai/docs/apis/chat"},

	{ID: "vivgrid", Name: "Vivgrid", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.vivgrid.com/v1",
		KeyEnv:  []string{"VIVGRID_API_KEY"},
		KeyHint: "docs.vivgrid.com/models"},

	{ID: "vultr", Name: "Vultr", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.vultrinference.com/v1",
		KeyEnv:  []string{"VULTR_API_KEY"},
		KeyHint: "api.vultrinference.com"},

	{ID: "wafer.ai", Name: "Wafer", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://pass.wafer.ai/v1",
		KeyEnv:  []string{"WAFER_API_KEY"},
		KeyHint: "docs.wafer.ai/wafer-pass"},

	{ID: "wandb", Name: "Weights & Biases", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.inference.wandb.ai/v1",
		KeyEnv:  []string{"WANDB_API_KEY"},
		KeyHint: "docs.wandb.ai/guides/integrations/inference"},

	{ID: "xiaomi-token-plan-ams", Name: "Xiaomi Token Plan (Europe)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://token-plan-ams.xiaomimimo.com/v1",
		KeyEnv:  []string{"XIAOMI_API_KEY"},
		KeyHint: "platform.xiaomimimo.com/#/docs"},

	{ID: "xiaomi-token-plan-cn", Name: "Xiaomi Token Plan (China)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		KeyEnv:  []string{"XIAOMI_API_KEY"},
		KeyHint: "platform.xiaomimimo.com/#/docs"},

	{ID: "xiaomi-token-plan-sgp", Name: "Xiaomi Token Plan (Singapore)", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1",
		KeyEnv:  []string{"XIAOMI_API_KEY"},
		KeyHint: "platform.xiaomimimo.com/#/docs"},

	{ID: "xpersona", Name: "Xpersona", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://www.xpersona.co/v1",
		KeyEnv:  []string{"XPERSONA_API_KEY"},
		KeyHint: "www.xpersona.co/docs"},

	{ID: "zai-coding-plan", Name: "Z.AI Coding Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.z.ai/api/coding/paas/v4",
		KeyEnv:  []string{"ZHIPU_API_KEY"},
		KeyHint: "docs.z.ai/devpack/overview"},

	{ID: "zeldoc", Name: "Zeldoc", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://api.zeldoc.ai/v1",
		KeyEnv:  []string{"ZELDOC_API_KEY"},
		KeyHint: "docs.zeldoc.ai"},

	{ID: "zenifra", Name: "Zenifra", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://ai.zenifra.com/v1",
		KeyEnv:  []string{"ZENIFRA_AI_KEY"},
		KeyHint: "docs.zenifra.com"},

	{ID: "zenmux", Name: "ZenMux", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://zenmux.ai/api/v1",
		KeyEnv:  []string{"ZENMUX_API_KEY"},
		KeyHint: "docs.zenmux.ai"},

	{ID: "zhipuai", Name: "Zhipu AI", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		KeyEnv:  []string{"ZHIPU_API_KEY"},
		KeyHint: "docs.z.ai/guides/overview/pricing"},

	{ID: "zhipuai-coding-plan", Name: "Zhipu AI Coding Plan", Auth: AuthAPIKey, Flavor: FlavorOpenAI,
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		KeyEnv:  []string{"ZHIPU_API_KEY"},
		KeyHint: "docs.bigmodel.cn/cn/coding-plan/overview"},
}
