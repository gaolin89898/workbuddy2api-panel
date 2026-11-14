// 从本机 wb2api 网关 /v1/models 生成 DSH pi-ai 供应商路由的模型目录。
//
// 上游 name 形如 "Deepseek-V4.1-Flash · f · x0.11"：中间是上游 vendor 厂商标识，
// 末尾是积分倍率。DSH 的模型选择器保留倍率、去掉厂商段（面板与 /v1/models 仍
// 保留上游原文）。name 与 id 相同或清洗后为空时省略 name，让 DSH 回落显示 id。
// auto 类模型（路由到「自动选模型」的条目）不进入 DSH 目录：这些条目在 DSH 里
// 只能靠模型名本身触发自动路由，选中后无法确定实际模型，列出反而误导。
import { readFile } from 'node:fs/promises';

const VENDOR_CREDIT = /\s*\u00b7\s*[^\u00b7]+\s*\u00b7\s*(x[0-9.]+)\s*$/u;
const VENDOR_ONLY = /\s*\u00b7\s*[^\u00b7]+\s*$/u;

/** 去掉上游 name 的厂商标识段，保留倍率；无有效名字时返回空串。 */
export function cleanName(raw, id) {
  let name = String(raw || '').trim();
  if (!name) return '';
  if (VENDOR_CREDIT.test(name)) name = name.replace(VENDOR_CREDIT, ' \u00b7 $1');
  else name = name.replace(VENDOR_ONLY, '');
  name = name.trim();
  if (!name) return '';
  const bare = bareId(id);
  if (name === id || name === bare) return '';
  return name;
}

/** 去掉 realm / 供应商前缀的裸模型 id。 */
export function bareId(id) {
  if (id.includes(':')) return id.slice(id.indexOf(':') + 1);
  return id.includes('/') ? id.split('/').pop() : id;
}

/** 自动选模型的条目（Auto / auto-chat / default-model 等）。 */
export function isAutoModel(model) {
  const bare = bareId(model.id).toLowerCase();
  if (bare === 'auto' || bare.endsWith('-auto') || bare.startsWith('auto-')) return true;
  const head = String(model.name || '').split('\u00b7')[0].trim().toLowerCase();
  return head === 'auto';
}

/** 把 /v1/models 条目映射为 DSH 供应商目录条目。 */
export function catalogEntry(model) {
  const entry = { id: model.id };
  const name = cleanName(model.name, model.id);
  if (name) entry.name = name;
  if (model.context_length) entry.contextWindow = model.context_length;
  if (model.max_output_tokens) entry.maxTokens = model.max_output_tokens;
  entry.input = model.supports_images ? ['text', 'image'] : ['text'];
  // DSH 的 reasoningEfforts 只接受 off/minimal/low/medium/high/xhigh/max；
  // 网关目录里的 none 映射成 off，其余不认识的档位直接剔除，避免整段配置校验失败。
  const SUPPORTED_EFFORTS = new Set(['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']);
  const efforts = model.supports_reasoning && Array.isArray(model.supported_efforts) && model.supported_efforts.length
    ? [...new Set(model.supported_efforts.map(level => String(level).toLowerCase() === 'none' ? 'off' : String(level).toLowerCase()).filter(level => SUPPORTED_EFFORTS.has(level)))]
    : null;
  entry.reasoningEfforts = efforts ? Object.fromEntries(efforts.map(level => [level, level])) : false;
  return entry;
}

/** 拉取网关上当前模型目录（需要 config.json 的 api_key）。 */
export async function fetchCatalog({ root = '.', port = 7863 } = {}) {
  const config = JSON.parse(await readFile(`${root}/config.json`, 'utf8'));
  if (!config.api_key) throw new Error('config.json 缺少 api_key');
  const res = await fetch(`http://127.0.0.1:${port}/v1/models`, { headers: { authorization: `Bearer ${config.api_key}` } });
  if (!res.ok) throw new Error(`网关返回 ${res.status}`);
  const data = await res.json();
  return (data.data || []).filter(model => !isAutoModel(model)).map(catalogEntry);
}

/**
 * 渲染 `models:` 之后的条目行。baseIndent 是 `models:` 键所在的缩进，
 * 条目比它深 2，字段再深 2。
 * @param entries - catalogEntry() 结果
 * @param baseIndent - models 键缩进空格数
 */
export function renderModelsBlock(entries, baseIndent) {
  const pad = ' '.repeat(baseIndent + 2);
  const field = pad + '  ';
  const lines = [];
  for (const e of entries) {
    lines.push(`${pad}- id: ${JSON.stringify(e.id)}`);
    if (e.name) lines.push(`${field}name: ${JSON.stringify(e.name)}`);
    if (e.contextWindow) lines.push(`${field}contextWindow: ${e.contextWindow}`);
    if (e.maxTokens) lines.push(`${field}maxTokens: ${e.maxTokens}`);
    lines.push(`${field}input:`);
    for (const modality of e.input) lines.push(`${field}  - ${JSON.stringify(modality)}`);
    if (e.reasoningEfforts === false) {
      lines.push(`${field}reasoningEfforts: false`);
    } else {
      lines.push(`${field}reasoningEfforts:`);
      for (const [level, wire] of Object.entries(e.reasoningEfforts)) lines.push(`${field}  ${level}: ${JSON.stringify(wire)}`);
    }
  }
  return lines.join('\n');
}
