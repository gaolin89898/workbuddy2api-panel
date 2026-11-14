// 把网关最新模型目录同步进两处 DSH 供应商配置：
//   1. 插件包 dsh-plugin/cordis.patch.yml（bundle 层，给新装机器用）
//   2. 用户层 ~/.dsh/profiles/desktop/cordis.patch.yml（本机实际生效层，整段替换）
// 只替换 workbuddy-gateway（本机生效层可能叫 ai-toolbox）路由下 `models:` 之后的块，其余行（含 opencode-go
// 等其他供应商）原样保留。用法：node scripts/sync-dsh-provider-models.mjs
import { readFile, writeFile, copyFile } from 'node:fs/promises';
import { fetchCatalog, renderModelsBlock } from './generate-dsh-provider-models.mjs';

const ROUTES = ['workbuddy-gateway', 'ai-toolbox'];

/**
 * 替换文本中某供应商路由下 models 块。返回新的全文与替换条数。
 * 定位：先找到 `workbuddy-gateway:` 键行，再向下找缩进更深的 `models:` 键行，
 * 块结束于缩进不大于 models 键的下一个非空行。
 */
function replaceModels(text, blockLines) {
  const lines = text.split(/\r?\n/);
  let routeAt = -1, route = null;
  for (const candidate of ROUTES) {
    const at = lines.findIndex(line => new RegExp(`^\\s*${candidate}:\\s*$`).test(line));
    if (at >= 0) { routeAt = at; route = candidate; break; }
  }
  if (routeAt < 0) throw new Error(`未找到 ${ROUTES.join(' / ')} 路由`);
  const routeIndent = lines[routeAt].match(/^\s*/)[0].length;
  let modelsAt = -1;
  for (let i = routeAt + 1; i < lines.length; i++) {
    const line = lines[i];
    if (line.trim() === '') continue;
    const indent = line.match(/^\s*/)[0].length;
    if (indent <= routeIndent) break;
    if (new RegExp(`^\\s{${indent}}models:\\s*$`).test(line)) { modelsAt = i; break; }
  }
  if (modelsAt < 0) throw new Error(`${route} 下没有 models: 块`);
  const modelsIndent = lines[modelsAt].match(/^\s*/)[0].length;
  let end = modelsAt + 1;
  for (; end < lines.length; end++) {
    const line = lines[end];
    if (line.trim() === '') continue;
    if (line.match(/^\s*/)[0].length <= modelsIndent) break;
  }
  const next = [...lines.slice(0, modelsAt + 1), ...blockLines.split('\n'), ...lines.slice(end)];
  return next.join('\n');
}

const entries = await fetchCatalog();
const targets = [
  { path: 'dsh-plugin/cordis.patch.yml', indent: 12, backup: false },
  { path: `${process.env.USERPROFILE || process.env.HOME}/.dsh/profiles/desktop/cordis.patch.yml`, indent: 8, backup: true },
];

for (const target of targets) {
  const text = await readFile(target.path, 'utf8');
  if (target.backup) await copyFile(target.path, `${target.path}.bak-before-models-sync`);
  const next = replaceModels(text, renderModelsBlock(entries, target.indent));
  await writeFile(target.path, next);
  console.log(`已更新 ${target.path}（${entries.length} 个模型）`);
}
