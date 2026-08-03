# Free API options for a smaller-context tool-calling experiment

Research date: 2026-08-03

## Conclusion

Keep DeepSeek V4 Flash as the primary model for the delayed-reuse experiment.
Changing provider and model at the same time would confound the proxy/direct
comparison with differences in model capability, tool-call formatting, and tool
training. Measure distance in absolute tokens and include realistic context loads;
do not require the experiment to occupy a fixed percentage of the advertised
window.

If a smaller-window replication is still useful, the best verified free option is
Cloudflare Workers AI's `@cf/meta/llama-3.1-8b-instruct-fp8`: 32,000 tokens,
API-accessible, and its model API schema includes `tool_calls`. Treat it as a
cross-model replication, not as another arm in the same causal comparison.

## Current DeepSeek baseline

- DeepSeek currently documents `deepseek-v4-flash` as
  `DeepSeek-V4-Flash-0731`, with a 1M-token context window and tool-call support.
  It exposes an OpenAI-format API at `https://api.deepseek.com`.
  [DeepSeek Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing/)
- This current model label matters for continuity: record the response
  `system_fingerprint` and do not pool new trials with prior trials unless the
  fingerprint/model deployment is compatible. The official page identifies the
  current model version, but it does not promise that a floating model ID is
  immutable.

## Smaller-window free options

### 1. Cloudflare Workers AI — best fit for a secondary replication

- `@cf/meta/llama-3.1-8b-instruct-fp8` has a documented 32,000-token context
  window. The model page exposes REST and OpenAI-compatible access and includes a
  `tool_calls` array in its synchronous output schema.
  [Cloudflare model page](https://developers.cloudflare.com/workers-ai/models/llama-3.1-8b-instruct-fp8/)
- Workers AI includes 10,000 Neurons per day at no charge on the Free plan; the
  allocation resets at 00:00 UTC. This model is listed at 13,778 neurons per
  million input tokens and 26,128 neurons per million output tokens.
  [Cloudflare Workers AI pricing](https://developers.cloudflare.com/workers-ai/platform/pricing/)
- Cloudflare documents traditional function calling as passing tool definitions
  and receiving structured calls for application-side execution.
  [Cloudflare function calling](https://developers.cloudflare.com/workers-ai/features/function-calling/)

Caveats:

- Llama 3.1 8B is a much smaller and different model from DeepSeek V4 Flash.
  Lower reliability may reflect base model capability rather than context pressure
  or proxy mechanics.
- The 10,000-Neuron daily allocation is finite. At the published neuron price,
  inputs alone amount to roughly 725,000 tokens per day before outputs. A broad
  repeated experiment near 32k would need to be small or spread over days.
- Run a one-case round-trip smoke before adapting the harness. A generic
  `tool_calls` response field does not establish parity with DeepSeek's exact
  multi-turn protocol behavior.

### 2. GroqCloud — free and convenient, but free TPM limits defeat the goal

- Groq supports application-managed local tool calling: the API receives tool
  schemas and returns structured tool names and arguments.
  [Groq local tool calling](https://console.groq.com/docs/tool-use/local-tool-calling)
- Current production language models including `llama-3.1-8b-instant`,
  `llama-3.3-70b-versatile`, `openai/gpt-oss-20b`, and
  `openai/gpt-oss-120b` have 131,072-token context windows.
  [Groq supported models](https://console.groq.com/docs/models)
- Groq has a Free plan, but its published base limits are only 6K TPM for
  `llama-3.1-8b-instant`, 12K TPM for Llama 3.3 70B, and 8K TPM for each GPT-OSS
  model. Exact organization limits may differ.
  [Groq rate limits](https://console.groq.com/docs/rate-limits)

Caveat: these free token-per-minute ceilings make high-occupancy requests poor
fits even though the models advertise 131k windows. Groq is useful for cheap
low-context smoke tests, not this context-pressure experiment.

### 3. Mistral API — real free mode, but not sufficiently predictable here

- Mistral API keys work in Free mode by default without a credit card, with
  limited usage and rate limits intended for evaluation and prototyping.
  [Mistral API key setup](https://docs.mistral.ai/getting-started/quickstarts/studio/activate-and-generate-api-key)
- Mistral documents function calling and successive tool calls. Its current
  function-capable families have context windows of 128k or 256k in the published
  limitations table.
  [Mistral function calling](https://docs.mistral.ai/studio-api/conversations/function-calling),
  [Mistral known limitations](https://docs.mistral.ai/resources/known-limitations)
- Mistral does not publish one stable numeric Free-mode allowance on those docs;
  it directs users to the account's Limits page because limits vary by model and
  organization.
  [Mistral rate-limit guidance](https://help.mistral.ai/en/articles/698531-why-am-i-hitting-api-rate-limits-and-how-do-i-increase-them)

Caveat: the windows are smaller than DeepSeek's 1M but not especially small, and
the unenumerated free allowance makes an experiment budget hard to plan.

### 4. Hugging Face Inference Providers — credits exist, but are too small

- Free Hugging Face users receive $0.10 in monthly routed-inference credits.
  [Hugging Face pricing](https://huggingface.co/docs/inference-providers/en/pricing)
- The routed chat-completion API accepts `tools` and `tool_choice`, but actual
  support and behavior depend on the selected model and underlying provider.
  [Hugging Face function calling](https://huggingface.co/docs/inference-providers/guides/function-calling)

Caveat: $0.10 is a recurring credit, not a substantial trial grant. Dynamic
model/provider routing also adds another variable unless a provider is pinned.

## Options excluded

- GitHub Models is not an option: GitHub states that its playground, catalog,
  inference API, and BYOK support were fully retired on 2026-07-30.
  [GitHub Models retirement notice](https://docs.github.com/en/github-models)
- Gemini's API has a free tier and function calling, but its common current models
  use very large context windows, so it does not solve the smaller-window request.
  [Gemini long-context documentation](https://ai.google.dev/gemini-api/docs/long-context)

## Recommendation for the experiment

1. Run the primary delayed-reuse experiment on DeepSeek V4 Flash, preserving the
   current harness metrics and model fingerprint.
2. Use absolute transcript distances chosen to resemble actual sessions rather
   than filling arbitrary fractions of a 1M window. Context-window percentage is
   not known to be a provider-independent measure of retrieval difficulty.
3. If the DeepSeek result is decision-relevant, repeat only the smallest decisive
   condition on Cloudflare's 32k Llama 3.1 8B model. Label that result a robustness
   check across models, not pooled evidence.
