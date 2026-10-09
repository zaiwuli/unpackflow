const $ = id => document.getElementById(id);
const zh = {
  noTasks: '\u6682\u65e0\u4efb\u52a1', noPasswords: '\u6682\u65e0\u5bc6\u7801',
  noHistory: '\u6682\u65e0\u5386\u53f2\u8bb0\u5f55', noFolders: '\u5c1a\u672a\u914d\u7f6e\u76d1\u63a7\u76ee\u5f55', noLogs: '\u6682\u65e0\u8fd0\u884c\u65e5\u5fd7',
  saved: '\u5df2\u4fdd\u5b58', saveFailed: '\u4fdd\u5b58\u5931\u8d25',
  restart: '\u5df2\u4fdd\u5b58\uff0c\u8bf7\u91cd\u542f\u670d\u52a1\u751f\u6548', submitted: '\u5df2\u63d0\u4ea4',
  testFailed: '\u6d4b\u8bd5\u5931\u8d25', cannotConnect: '\u65e0\u6cd5\u8fde\u63a5',
};
let formLoaded = false;
let latestLogs = [];
let logView = 'user';
let notificationTemplates = [];
let activeNotificationTemplateID = '';
let taskSystemPaused = false;
let currentTasks = [];
let taskFilter = 'all';
let historyFilter = 'all';
let currentHistory = [];
let currentOfflineBatches = [];
let offlineRecordFilter = 'all';
const pendingActions = new Set();
const expandedTasks = new Set();

function esc(value) {
  const element = document.createElement('div');
  element.textContent = value == null ? '' : value;
  return element.innerHTML;
}

function renderList(element, values, render, empty) {
  element.innerHTML = values && values.length ? values.map(render).join('') : `<p class="empty">${empty}</p>`;
}

function ensureBrandIcon() {
  if (document.querySelector('.brand-icon')) return;
  const title = document.querySelector('.topbar > div:first-child');
  if (!title) return;
  title.replaceChildren();
  const icon = document.createElement('img');
  icon.className = 'brand-icon';
  icon.src = 'icon.svg';
  icon.alt = 'UnpackFlow';
  icon.width = 46;
  icon.height = 46;
  icon.style.cssText = 'display:block;flex:0 0 auto;border-radius:11px;box-shadow:0 8px 20px #10182824';
  title.className = 'brand-only';
  title.append(icon);
  const favicon = document.createElement('link');
  favicon.rel = 'icon';
  favicon.href = 'icon.svg';
  document.head.appendChild(favicon);
}

function ensureNotificationOptions() {
  if ($('notify-options')) return;
  const options = document.createElement('div');
  options.id = 'notify-options';
  options.innerHTML = '<h3>通知阶段</h3>' +
    '<label class="check-row"><input id="notify-discovery" type="checkbox"> 发现压缩包</label>' +
    '<label class="check-row"><input id="notify-cache" type="checkbox"> 缓存完成</label>' +
    '<label class="check-row"><input id="notify-extract" type="checkbox"> 开始解压</label>' +
    '<label class="check-row"><input id="notify-complete" type="checkbox"> 完成结果（成功或失败）</label>' +
    '<label class="check-row"><input id="notify-cleanup" type="checkbox"> 清理完成</label>';
  $('notify-url').closest('.field').insertAdjacentElement('afterend', options);
  const style = document.createElement('style');
  style.textContent = '.active-provider{background:#e9efff;color:var(--brand);border-color:var(--brand);font-weight:700}';
  document.head.appendChild(style);

  const templates = document.createElement('div');
  templates.id = 'notify-templates';
  templates.style.cssText = 'margin-top:22px;padding-top:18px;border-top:1px solid var(--line,#e5e7eb)';
  templates.innerHTML = '<div class="panel-heading"><div><h3>\u901a\u77e5\u6a21\u677f</h3><p>\u53ef\u4f7f\u7528 {{icon}}\u3001{{title}}\u3001{{source}}\u3001{{task}}\u3001{{time}} \u548c {{separator}}</p></div></div>' +
    '<label class="field"><span>\u5f53\u524d\u6a21\u677f</span><select id="notify-template-select"></select></label>' +
    '<label class="field"><span>\u6a21\u677f\u540d\u79f0</span><input id="notify-template-name" type="text" placeholder="\u4f8b\u5982\uff1a\u7b80\u6d01\u901a\u77e5"></label>' +
    '<label class="field"><span>\u5907\u6ce8</span><input id="notify-template-remark" type="text" placeholder="\u8bf4\u660e\u6a21\u677f\u7528\u9014"></label>' +
    '<label class="field"><span>\u6a21\u677f\u5185\u5bb9</span><textarea id="notify-template-content" rows="8" style="width:100%;resize:vertical;border:1px solid #d8dce5;border-radius:8px;padding:10px;font:inherit;line-height:1.6"></textarea></label>' +
    '<div class="form-actions"><button id="notify-template-new" type="button">\u65b0\u589e\u6a21\u677f</button><button id="notify-template-save" type="button">\u4fdd\u5b58\u6a21\u677f</button><button id="notify-template-select-button" type="button">\u8bbe\u4e3a\u5f53\u524d</button><button id="notify-template-delete" type="button">\u5220\u9664\u6a21\u677f</button></div>';
  options.insertAdjacentElement('afterend', templates);
  $('notify-template-select').addEventListener('change', event => showNotificationTemplate(event.target.value));
  $('notify-template-new').addEventListener('click', newNotificationTemplate);
  $('notify-template-save').addEventListener('click', saveNotificationTemplate);
  $('notify-template-select-button').addEventListener('click', selectNotificationTemplate);
  $('notify-template-delete').addEventListener('click', deleteNotificationTemplate);
}

function renderNotificationTemplates(settings) {
  notificationTemplates = (settings.templates || []).slice();
  activeNotificationTemplateID = settings.active_template_id || (notificationTemplates[0] && notificationTemplates[0].id) || '';
  const select = $('notify-template-select');
  select.innerHTML = notificationTemplates.map(item => '<option value="' + esc(item.id) + '">' + esc(item.name) + (item.id === activeNotificationTemplateID ? ' \u00b7 \u5f53\u524d' : '') + '</option>').join('');
  select.value = activeNotificationTemplateID;
  showNotificationTemplate(select.value || (notificationTemplates[0] && notificationTemplates[0].id));
}

function updateNotificationAddressLabel() {
  const ms = $('notify-ms') && $('notify-ms').classList.contains('active-provider');
  const field = $('notify-url').closest('.field');
  if (!field) return;
  const label = field.querySelector('span');
  if (label) label.textContent = ms ? 'MS 服务地址' : '通知地址';
  $('notify-url').placeholder = ms ? '例如：http://192.168.31.2:8888/' : '';
}

function showNotificationTemplate(id) {
  const item = notificationTemplates.find(template => template.id === id);
  if (!item) return;
  $('notify-template-select').value = item.id;
  $('notify-template-name').value = item.name || '';
  $('notify-template-remark').value = item.remark || '';
  $('notify-template-content').value = item.content || '';
  $('notify-template-delete').disabled = item.id === 'default';
}

function newNotificationTemplate() {
  $('notify-template-select').value = '';
  $('notify-template-name').value = '';
  $('notify-template-remark').value = '';
  $('notify-template-content').value = '{{icon}} UnpackFlow {{title}}\n{{separator}}\n\u23f1\ufe0f \u65f6\u95f4: {{time}}\n\ud83d\udce6 \u6765\u6e90: {{source}}\n\ud83d\udcc4 \u4efb\u52a1: {{task}}';
  $('notify-template-delete').disabled = true;
  $('notify-template-name').focus();
}

async function templateAction(body) {
  const response = await fetch('api/notification/templates', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
  const raw = await response.text();
  let data = {}; try { data = JSON.parse(raw); } catch (_) {}
  if (!response.ok) { $('notify-message').textContent = raw || zh.saveFailed; return null; }
  renderNotificationTemplates(data.notification);
  $('notify-message').textContent = zh.saved;
  return data;
}

async function saveNotificationTemplate() {
  const id = $('notify-template-select').value;
  await templateAction({action: id ? 'update' : 'create', id, name: $('notify-template-name').value.trim(), remark: $('notify-template-remark').value.trim(), content: $('notify-template-content').value});
}
async function selectNotificationTemplate() { const id = $('notify-template-select').value; if (id) await templateAction({action: 'select', id}); }
async function deleteNotificationTemplate() { const id = $('notify-template-select').value; if (id && id !== 'default') await templateAction({action: 'delete', id}); }

// Fixed notification providers: keep the page simple while allowing the
// transport details to evolve independently in the backend.
function ensureNotificationOptions() {
  if ($('notify-provider-options')) return;
  const options = document.createElement('div');
  options.id = 'notify-provider-options';
  options.innerHTML = '<h3>通知阶段</h3>' +
    '<label class="check-row"><input id="notify-discovery" type="checkbox"> 发现压缩包</label>' +
    '<label class="check-row"><input id="notify-cache" type="checkbox"> 缓存完成</label>' +
    '<label class="check-row"><input id="notify-extract" type="checkbox"> 开始解压</label>' +
    '<label class="check-row"><input id="notify-complete" type="checkbox"> 完成结果（成功或失败）</label>' +
    '<label class="check-row"><input id="notify-cleanup" type="checkbox"> 清理完成</label>' +
    '<h3>通知方式</h3>' +
    '<div class="form-actions" style="margin-top:8px"><button id="notify-mp" type="button">MP 模板通知</button><button id="notify-ms" type="button">MS 模板通知</button></div>' +
    '<label class="field" id="notify-api-key-row"><span>MS 密钥</span><input id="notify-api-key" type="text" autocomplete="off" placeholder="输入 MS 的 apiKey"></label>';
  $('notify-url').closest('.field').insertAdjacentElement('afterend', options);
  $('notify-mp').addEventListener('click', () => selectNotificationProvider('mp'));
  $('notify-ms').addEventListener('click', () => selectNotificationProvider('ms'));
}

function renderNotificationTemplates(settings) {
  const provider = settings.provider === 'ms' ? 'ms' : 'mp';
  $('notify-api-key').value = settings.api_key || '';
  $('notify-mp').classList.toggle('active-provider', provider === 'mp');
  $('notify-ms').classList.toggle('active-provider', provider === 'ms');
  $('notify-api-key-row').style.display = provider === 'ms' ? 'flex' : 'none';
  $('notify-mp').dataset.provider = provider;
  $('notify-ms').dataset.provider = provider;
}

async function saveNotificationSettings() {
  const response = await fetch('api/notification', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({
    enabled: $('notify-enabled').checked,
    url: $('notify-url').value.trim(),
    provider: $('notify-ms').classList.contains('active-provider') ? 'ms' : 'mp',
    api_key: $('notify-api-key').value.trim(),
    events: {
      discovery: $('notify-discovery').checked,
      cache: $('notify-cache').checked,
      extract: $('notify-extract').checked,
      complete: $('notify-complete').checked,
      cleanup: $('notify-cleanup').checked,
    },
  })});
  $('notify-message').textContent = response.ok ? zh.saved : zh.saveFailed;
  return response.ok;
}

async function selectNotificationProvider(provider) {
  $('notify-mp').classList.toggle('active-provider', provider === 'mp');
  $('notify-ms').classList.toggle('active-provider', provider === 'ms');
  $('notify-api-key-row').style.display = provider === 'ms' ? 'flex' : 'none';
  updateNotificationAddressLabel();
  await saveNotificationSettings();
}

function ensureLocalSettings() {
  if ($('local-source-action')) return;
	$('cd2-refresh').textContent = '全链路刷新';
	const taskRefresh = $('refresh');
	if (taskRefresh) taskRefresh.remove();
  const workers = $('workers').closest('.field');
  if (!workers) return;
	const oldDeleteSource = $('delete-source');
	if (oldDeleteSource) oldDeleteSource.closest('.check-row').remove();
	[$('watch-path'), $('refresh-path')].forEach(input => { if (input) input.closest('.field').style.display = 'none'; });
	const pathOverrides = $('path-overrides');
	if (pathOverrides) pathOverrides.closest('.field').style.display = 'none';
  const block = document.createElement('div');
  block.id = 'local-settings';
  block.innerHTML = '<div class="panel-heading" style="margin-top:18px"><div><h3>\u672c\u5730\u76ee\u5f55</h3>' +
    '<p>\u5b9e\u65f6\u76d1\u542c\u6587\u4ef6\u53d8\u5316\uff0c\u5e76\u7528\u5b9a\u65f6\u626b\u63cf\u9632\u6b62\u6f0f\u4e8b\u4ef6</p>' +
    '<p id="local-path-summary" style="margin-top:4px"></p></div></div>' +
    '<label class="field"><span>\u89e3\u538b\u6210\u529f\u540e\u7684\u539f\u5305\u5904\u7406</span><select id="local-source-action">' +
    '<option value="keep">\u4fdd\u7559\u539f\u5305</option><option value="delete">\u5220\u9664\u539f\u5305</option><option value="archive">\u5f52\u6863\u539f\u5305</option></select></label>' +
    '<label class="field"><span>\u539f\u5305\u5904\u7406\u5ef6\u8fdf</span><input id="local-source-delay" type="text" placeholder="0s">' +
    '<small style="color:var(--muted);font-size:12px">0s \u8868\u793a\u89e3\u538b\u6210\u529f\u540e\u7acb\u5373\u5904\u7406</small></label>' +
    '<label class="field" id="local-archive-row"><span>\u672c\u5730\u5f52\u6863\u76ee\u5f55</span><input id="local-archive-dir" type="text" placeholder="/data/\u5f52\u6863\u76ee\u5f55"></label>' +
    '<label class="field"><span>\u8865\u507f\u626b\u63cf\u95f4\u9694</span><input id="folder-interval" type="text" placeholder="60s">' +
    '<small style="color:var(--muted);font-size:12px">0s \u5173\u95ed\u8865\u507f\u626b\u63cf\uff0c\u5b9e\u65f6\u76d1\u542c\u4ecd\u4fdd\u7559</small></label>' +
    '<h3>CD2 定时扫描</h3>' +
    '<label class="check-row"><input id="cd2-fallback-enabled" type="checkbox"> 启用 CD2 定时扫描</label>' +
    '<label class="field"><span>CD2 定时扫描间隔</span><input id="cd2-fallback-interval" type="text" placeholder="30m"></label>' +
		'<h3>115 云端工作流</h3>' +
		'<label class="check-row"><input id="115-enabled" type="checkbox"> 启用 115 云解压</label>' +
    '<label class="field"><span>115 Cookie</span><input id="115-cookie" type="text" autocomplete="off"></label>' +
    '<label class="field"><span>Cookie \u6765\u6e90\u5907\u6ce8</span><input id="115-cookie-remark" type="text" placeholder="\u4f8b\u5982\uff1a115 \u7f51\u9875\u5f00\u53d1\u8005\u5de5\u5177"></label>' +
    '<label class="check-row"><input id="115-event-enabled" type="checkbox"> 启用 115 云目录定时兜底扫描</label>' +
    '<small style="color:var(--muted);font-size:12px">直接读取下方配置的云解压来源和日常下载文件夹，不使用 115 生活事件接口；服务启动时也会扫描一次。</small>' +
    '<label class="field"><span>定时兜底扫描间隔（最少 30m）</span><input id="115-event-interval" type="text" placeholder="30m"><small style="color:var(--muted);font-size:12px">该间隔只控制后台兜底扫描，不影响任务页的“全链路刷新”。</small></label>' +
    '<div class="form-actions"><button id="115-sync" type="button">\u626b\u63cf 115 \u4e91\u76ee\u5f55</button></div><p id="115-sync-message" class="form-message"></p>' +
		'<h3>云解压来源</h3><div class="field"><div id="115-sources" class="mapping-list"></div><div class="form-actions"><button id="115-source-add" type="button">添加来源文件夹</button></div></div>' +
    '<label class="check-row"><input id="115-extract-by-date" type="checkbox"> 按日期创建云解压目录</label><small style="color:var(--muted);font-size:12px">开启后先在目标目录创建 YYYY-MM-DD 日期文件夹，再按压缩包名称分别解压。</small>' +
    '<label class="field"><span>云解压成功后的原包处理</span><select id="115-success-action"><option value="keep">保留原包</option><option value="delete">删除原包</option><option value="archive">移入成功归档目录</option></select></label>' +
    '<div class="field" id="115-archive-cid-row"><span>成功归档目录</span><div class="settings-pair"><input id="115-archive-cid" type="text" placeholder="115 成功归档文件夹 CID"><input id="115-archive-remark" type="text" placeholder="备注，例如：云解压成功归档"></div></div>' +
		'<h3>云解压失败</h3><div class="field"><span>失败归档目录</span><div class="settings-pair"><input id="115-failure-cid" type="text" placeholder="115 解压失败文件夹 CID"><input id="115-failure-remark" type="text" placeholder="备注，例如：云解压失败"></div></div>' +
		'<label class="field"><span>失败目录的 CD2 路径</span><input id="115-failure-path" type="text" placeholder="/115open/解压失败"></label>' +
    '<label class="check-row"><input id="115-auto-fallback" type="checkbox"> 最终失败后自动下载到本地解压</label><small style="color:var(--muted);font-size:12px">关闭后只移动到失败目录，并在任务页等待批准。</small>' +
    '<label class="check-row"><input id="115-scan-failure" type="checkbox"> 主动扫描失败目录</label><small style="color:var(--muted);font-size:12px">关闭后只处理刚刚云解压失败的文件。</small>' +
		'<div class="field"><span>失败重试</span><div class="settings-pair"><input id="115-retry-count" type="number" min="1" max="10" placeholder="3"><input id="115-retry-delay" type="text" placeholder="2m"></div><small style="color:var(--muted);font-size:12px">尝试次数与两次尝试之间的等待时间。</small></div>' +
		'<label class="field"><span>两个云解压任务之间的间隔</span><input id="115-task-interval" type="text" placeholder="30s"><small style="color:var(--muted);font-size:12px">默认 30s；填写 0s 表示上一个任务完成后立即放行下一个。</small></label>' +
		'<h3>115 离线下载</h3>' +
		'<label class="field"><span>离线保存目录 CID</span><input id="115-offline-cid" type="text" placeholder="指定父目录，提交时自动创建 YYYY-MM-DD 子文件夹"><small style="color:var(--muted);font-size:12px">每次导入都会保存到当天日期子文件夹，再在离线完成后自动查找压缩包。</small></label>' +
		'<label class="field"><span>离线状态兜底复查间隔</span><input id="115-offline-fallback" type="text" placeholder="60m"><small style="color:var(--muted);font-size:12px">前三次按 1m、3m、5m 查询；之后按此间隔复查未完成任务，0s 关闭定时兜底。</small></label>' +
		'<div class="field"><span>iPhone 快捷指令导入令牌</span><div class="settings-pair"><input id="115-offline-token" type="text" autocomplete="off" placeholder="用于快捷指令调用，不是115 Cookie"><button id="115-offline-token-generate" type="button">生成令牌</button></div><small style="color:var(--muted);font-size:12px">快捷指令只保存此令牌；115 Cookie 始终留在服务器。</small></div>' +
		'<h3>日常本地下载</h3><div class="field"><div id="115-downloads" class="mapping-list"></div><div class="form-actions"><button id="115-download-add" type="button">添加下载文件夹</button></div><small style="color:var(--muted);font-size:12px">手动将压缩包移入这些 115 文件夹后，工具刷新对应 CD2 路径；每行可选择自动下载或等待批准。</small></div>' +
		'<h3>CD2 挂载路径映射</h3><div class="field"><div id="path-mappings" class="mapping-list"></div><div class="form-actions"><button id="path-mapping-add" type="button">添加路径映射</button></div></div>';
  workers.insertAdjacentElement('afterend', block);
  if (!document.getElementById('115-mapping-style')) {
    const style = document.createElement('style');
    style.id = '115-mapping-style';
    style.textContent = '.mapping-list{display:grid;gap:8px;margin-top:8px}.mapping-row{display:grid;grid-template-columns:minmax(120px,1fr) minmax(180px,2fr) minmax(120px,1fr) 34px;gap:8px;align-items:center}.mapping-row.single{grid-template-columns:1fr 34px}.mapping-row.pair{grid-template-columns:1fr 1.4fr 34px}.mapping-row.source-rule{grid-template-columns:minmax(0,1fr) 34px}.source-rule-grid{display:grid;grid-template-columns:minmax(150px,0.8fr) minmax(120px,1fr) minmax(150px,0.8fr) minmax(120px,1fr);gap:8px}.source-rule-grid input[data-field=cid],.source-rule-grid input[data-field=extract_cid]{max-width:25ch}.mapping-input,.mapping-select{min-width:0;width:100%;border:1px solid #d8dce5;border-radius:8px;padding:9px 10px;background:#fff;font:inherit}.mapping-remove{width:34px;height:34px;padding:0;border:1px solid #d8dce5;border-radius:8px;background:#fff;color:#b42318;font-size:22px;line-height:1}.settings-pair{display:grid;grid-template-columns:1fr 1fr;gap:8px}@media(max-width:680px){.mapping-row,.mapping-row.single,.mapping-row.pair,.mapping-row.source-rule,.settings-pair{grid-template-columns:1fr}.source-rule-grid{grid-template-columns:1fr}.mapping-remove{width:100%;font-size:16px}}';
    document.head.appendChild(style);
  }
  const select = $('local-source-action');
  select.style.cssText = 'width:100%;border:1px solid #d8dce5;border-radius:8px;padding:9px 10px;background:#fff;font:inherit';
  select.addEventListener('change', updateLocalArchiveVisibility);
	$('115-source-add').addEventListener('click', () => addSourceRow());
	$('115-download-add').addEventListener('click', () => addDownloadRow());
	$('path-mapping-add').addEventListener('click', () => addPathMappingRow());
	$('115-offline-token-generate').addEventListener('click', () => {
		const bytes = new Uint8Array(24); crypto.getRandomValues(bytes);
		$('115-offline-token').value = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
	});
	$('115-success-action').addEventListener('change', update115ArchiveCIDVisibility);
	$('115-sync').addEventListener('click', sync115Now);
	buildSettingsSections(block, workers);
}

function buildSettingsSections(localBlock, workers) {
  if ($('settings-switch')) return;
  const view = $('settings-view');
  const heading = view.querySelector('.panel-heading');
  const nav = document.createElement('div');
  nav.id = 'settings-switch'; nav.className = 'settings-switch';
  nav.innerHTML = '<button class="settings-switch-button active" data-settings-view="settings-basic" type="button">基础</button><button class="settings-switch-button" data-settings-view="settings-local" type="button">本地</button><button class="settings-switch-button" data-settings-view="settings-cloud" type="button">云端工作流</button><button class="settings-switch-button" data-settings-view="settings-notify" type="button">通知</button><button class="settings-switch-button" data-settings-view="settings-logs" type="button">日志</button><button class="settings-switch-button" data-settings-view="settings-maintenance" type="button">数据维护</button>';
  const basic = document.createElement('section'); basic.id = 'settings-basic'; basic.className = 'settings-section active-settings-section';
  const local = document.createElement('section'); local.id = 'settings-local'; local.className = 'settings-section';
  const cloud = document.createElement('section'); cloud.id = 'settings-cloud'; cloud.className = 'settings-section';
  const maintenance = document.createElement('section'); maintenance.id = 'settings-maintenance'; maintenance.className = 'settings-section';
  const notify = document.createElement('section'); notify.id = 'settings-notify'; notify.className = 'settings-section';
  const logs = document.createElement('section'); logs.id = 'settings-logs'; logs.className = 'settings-section';
  maintenance.innerHTML = '<div class="panel-heading"><div><h3>数据维护</h3><p>“清空历史展示”保留防重复保护；要让相同压缩包重新解压，请先停止任务系统，再使用“重置处理记录”。</p></div></div><div class="form-actions"><button id="clear-all-cache" type="button">清除所有缓存</button><button id="clear-all-history" type="button">清空历史展示</button><button id="reset-all-history" type="button">重置处理记录</button></div><p id="maintenance-message" class="form-message"></p>';
  heading.insertAdjacentElement('afterend', nav); nav.insertAdjacentElement('afterend', basic); basic.insertAdjacentElement('afterend', local); local.insertAdjacentElement('afterend', cloud); cloud.insertAdjacentElement('afterend', notify); notify.insertAdjacentElement('afterend', logs); logs.insertAdjacentElement('afterend', maintenance);
  const notifyView = $('notify-view');
  const logsView = $('logs-view');
  if (notifyView) { while (notifyView.firstChild) notify.appendChild(notifyView.firstChild); notifyView.remove(); }
  if (logsView) { while (logsView.firstChild) logs.appendChild(logsView.firstChild); logsView.remove(); }
  document.querySelectorAll('[data-view="notify-view"],[data-view="logs-view"]').forEach(button => button.remove());
  basic.appendChild(workers);
  const all = Array.from(view.children);
  const save = $('settings-save').closest('.form-actions');
  const message = $('settings-message');
  for (const node of all) {
    if (node === heading || node === nav || node === basic || node === local || node === cloud || node === notify || node === logs || node === maintenance || node === save || node === message || node === localBlock) continue;
    cloud.appendChild(node);
  }
  const localChildren = Array.from(localBlock.children);
  const splitAt = localChildren.findIndex(node => node.tagName === 'H3' && node.textContent.indexOf('CD2') >= 0);
  local.appendChild(localBlock);
  if (splitAt >= 0) {
    const cloudPart = document.createElement('div');
    cloudPart.className = 'cloud-extra-settings';
    cloudPart.insertAdjacentHTML('afterbegin', '<div class="panel-heading"><div><h3>云端处理流程</h3><p>按来源目录 → 云解压目标 → 成功原包处理 → 失败转本地 → 日常下载 → CD2 路径映射依次配置。</p></div></div>');
    localChildren.slice(splitAt).forEach(node => cloudPart.appendChild(node));
    cloud.appendChild(cloudPart);
  }
  // Keep saving outside the three sections. Otherwise changes made on the
  // 基础 / 本地 tabs have no visible save action after the settings are split.
  cloud.insertAdjacentElement('afterend', save);
  save.insertAdjacentElement('afterend', message);
  buildCloudWorkflowTabs(cloud);
  nav.querySelectorAll('.settings-switch-button').forEach(button => button.addEventListener('click', () => {
    nav.querySelectorAll('.settings-switch-button').forEach(item => item.classList.remove('active'));
    view.querySelectorAll('.settings-section').forEach(item => item.classList.remove('active-settings-section'));
    button.classList.add('active'); $(button.dataset.settingsView).classList.add('active-settings-section');
  }));
  $('clear-all-cache').addEventListener('click', () => runMaintenance('clear_cache'));
  $('clear-all-history').addEventListener('click', () => runMaintenance('clear_history'));
  $('reset-all-history').addEventListener('click', () => runMaintenance('reset_history'));
}

function buildCloudWorkflowTabs(cloud) {
  if ($('cloud-workflow-switch')) return;
  const nav = document.createElement('div');
  nav.id = 'cloud-workflow-switch'; nav.className = 'settings-switch cloud-workflow-switch';
  nav.innerHTML = '<button class="settings-switch-button active" data-cloud-view="cloud-cd2" type="button">CD2</button><button class="settings-switch-button" data-cloud-view="cloud-extract" type="button">云解压</button><button class="settings-switch-button" data-cloud-view="cloud-download" type="button">云下载</button>';
  const definitions = [
    ['cloud-cd2', 'CD2 连接与本地挂载', '配置连接、缓存、定时扫描及云路径到本地路径的映射。'],
    ['cloud-extract', '115 云解压', '配置云解压来源、目标、成功处理和失败转本地流程。'],
    ['cloud-download', '115 云下载', '配置离线下载保存目录、状态兜底及日常本地下载目录。'],
  ];
  const groups = {};
  definitions.forEach(([id, title, description], index) => {
    const section = document.createElement('section');
    section.id = id; section.className = 'cloud-workflow-section' + (index === 0 ? ' active-cloud-workflow-section' : '');
    section.innerHTML = '<div class="panel-heading"><div><h3>' + title + '</h3><p>' + description + '</p></div></div>';
    groups[id] = section;
  });
  cloud.prepend(nav, ...definitions.map(item => groups[item[0]]));
  const move = (id, target) => {
    const element = $(id); if (!element) return;
    const root = element.closest('.field,.check-row,.form-actions') || element;
    const help = root.nextElementSibling && root.nextElementSibling.tagName === 'SMALL' ? root.nextElementSibling : null;
    groups[target].appendChild(root); if (help) groups[target].appendChild(help);
  };
  ['cd2-status','cd2-enabled','cd2-url','cd2-token','refresh-interval','cache-dir','cache-extract-path','keep-cache','cache-delete-delay','copy-timeout','cd2-fallback-enabled','cd2-fallback-interval','path-mappings'].forEach(id => move(id, 'cloud-cd2'));
  ['115-enabled','115-cookie','115-cookie-remark','115-event-enabled','115-event-interval','115-sync','115-sources','115-extract-by-date','115-success-action','115-archive-cid','115-failure-cid','115-failure-path','115-auto-fallback','115-scan-failure','115-retry-count','115-task-interval'].forEach(id => move(id, 'cloud-extract'));
  ['115-offline-cid','115-offline-fallback','115-offline-token','115-downloads'].forEach(id => move(id, 'cloud-download'));
  const shortcut = document.createElement('section');
  shortcut.className = 'shortcut-card';
  shortcut.innerHTML = '<div class="panel-heading"><div><h3>iPhone 快捷指令</h3><p>支持分享选中文本、TXT文件或直接读取剪贴板。</p></div></div><code id="offline-shortcut-url"></code><ol><li>快捷指令接收文本、URL和文件。</li><li>文件输入使用“获取文件内容”；没有输入时读取剪贴板。</li><li>使用“获取 URL 内容”发送 POST JSON。</li><li>请求头填写 Authorization: Bearer 你的离线导入令牌。</li></ol><div class="form-actions"><button id="offline-copy-url" type="button">复制接口地址</button><a id="offline-shortcut-download" class="button-link" href="api/115/offline/shortcut" download>下载快捷指令配置</a></div>';
  groups['cloud-download'].appendChild(shortcut);
  const shortcutURL = new URL('api/115/offline/import', window.location.href).href;
  $('offline-shortcut-url').textContent = shortcutURL;
  $('offline-copy-url').addEventListener('click', async () => { await navigator.clipboard.writeText(shortcutURL); $('settings-message').textContent = '快捷指令接口地址已复制'; });
  Array.from(cloud.querySelectorAll(':scope > h3, .cloud-extra-settings > h3')).forEach(node => node.remove());
  Array.from(cloud.querySelectorAll('.cloud-extra-settings > .panel-heading')).forEach(node => {
    if (node.textContent.includes('CloudDrive2') || node.textContent.includes('云端处理流程')) node.remove();
  });
  nav.querySelectorAll('button').forEach(button => button.addEventListener('click', () => {
    nav.querySelectorAll('button').forEach(item => item.classList.toggle('active', item === button));
    Object.values(groups).forEach(section => section.classList.toggle('active-cloud-workflow-section', section.id === button.dataset.cloudView));
  }));
}

async function runMaintenance(action) {
  const cache = action === 'clear_cache';
  const reset = action === 'reset_history';
  const message = cache ? '将删除全部本地缓存，但不会删除解压输出或云端原包。任务系统必须已暂停。确定继续吗？' : reset ? '将永久删除成功、失败、忽略和取消的防重复记录，使相同压缩包可以重新提交。请确认任务系统已停止并清空等待任务。确定继续吗？' : '将清空历史展示，但保留防重复和忽略规则，不删除实际文件。确定继续吗？';
  if (!window.confirm(message)) return;
  $('maintenance-message').textContent = '正在处理…';
  try {
    const response = await fetch('api/maintenance', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action})});
    const data = await response.json().catch(() => ({}));
    $('maintenance-message').textContent = response.ok ? data.message : (data.error || '操作失败');
    if (response.ok) await load(false);
  } catch (_) { $('maintenance-message').textContent = '操作失败'; }
}

function update115ArchiveCIDVisibility() {
  if (!$('115-success-action')) return;
  $('115-archive-cid-row').style.display = $('115-success-action').value === 'archive' ? 'flex' : 'none';
}

async function sync115Now() {
  const button = $('115-sync');
  button.disabled = true;
  $('115-sync-message').textContent = '\u6b63\u5728\u626b\u63cf 115 \u4e91\u76ee\u5f55\u2026';
  try {
    const response = await fetch('api/115/sync', {method: 'POST'});
    const text = await response.text();
    if (!response.ok) {
      $('115-sync-message').textContent = text || '\u626b\u63cf\u5931\u8d25';
    } else {
      const result = JSON.parse(text || '{}');
      $('115-sync-message').textContent = '\u626b\u63cf\u5b8c\u6210\uff1a\u76ee\u5f55 ' + (result.folders || 0) +
        '\uff0c\u8bfb\u53d6 ' + (result.files || 0) + '\uff0c\u63d0\u4ea4 ' + (result.queued || 0) +
        '\uff0c\u5df2\u5904\u7406 ' + (result.skipped_processed || 0) + '\uff0c\u5904\u7406\u4e2d ' + (result.skipped_pending || 0) +
        '\uff0c\u5df2\u5ffd\u7565 ' + (result.skipped_ignored || 0) + '\uff0c\u975e\u538b\u7f29\u5305 ' + (result.skipped_invalid || 0) +
        '\uff0c\u5f85\u7eed\u626b ' + (result.partial || 0) + '\uff0c\u5931\u8d25 ' + (result.errors || 0);
      load(false);
    }
  } catch (_) { $('115-sync-message').textContent = '\u626b\u63cf\u5931\u8d25'; }
  button.disabled = false;
}

function mappingInput(placeholder, value, field) {
  return '<input class="mapping-input" data-field="' + field + '" type="text" placeholder="' + placeholder + '" value="' + esc(value || '') + '">';
}

function removableRow(className, html) {
  const row = document.createElement('div');
  row.className = 'mapping-row ' + className;
  row.innerHTML = html + '<button class="mapping-remove" type="button" title="删除">×</button>';
  row.querySelector('.mapping-remove').addEventListener('click', () => row.remove());
  return row;
}

function addSourceRow(value, remarks) {
  const list = $('115-sources');
  const cid = typeof value === 'string' ? value : ((value && value.cid) || '');
  const remark = typeof value === 'object' && value ? (value.remark || '') : ((remarks || {})[cid] || '');
  const extractRemark = typeof value === 'object' && value ? (value.extract_remark || '') : '';
  if (list) list.appendChild(removableRow('source-rule', '<div class="source-rule-grid">' + mappingInput('来源 CID（最多25位）', cid, 'cid') + mappingInput('来源备注', remark, 'remark') + mappingInput('目标 CID（最多25位，留空使用来源）', typeof value === 'object' && value ? (value.extract_cid || '') : '', 'extract_cid') + mappingInput('目标备注', extractRemark, 'extract_remark') + '</div>'));
}

function fillSourceRows(values, remarks) {
  const list = $('115-sources');
  if (!list) return;
  list.innerHTML = '';
  (values || []).forEach(value => addSourceRow(value, remarks));
  if (!list.children.length) addSourceRow('');
}

function collectSourceCIDs() {
  return Array.from($('115-sources').querySelectorAll('[data-field="cid"]')).map(input => input.value.trim()).filter(Boolean);
}

function collectSourceRules() {
  return Array.from($('115-sources').querySelectorAll('.mapping-row')).map(row => {
    const cid = row.querySelector('[data-field="cid"]').value.trim();
    const remark = row.querySelector('[data-field="remark"]').value.trim();
    const extract = row.querySelector('[data-field="extract_cid"]');
    const extractRemark = row.querySelector('[data-field="extract_remark"]');
    return cid ? {id: 'source:' + cid, cid, extract_cid: extract ? extract.value.trim() : '', remark, extract_remark: extractRemark ? extractRemark.value.trim() : ''} : null;
  }).filter(Boolean);
}

function collectCIDRemarks() {
  const result = {};
  $('115-sources').querySelectorAll('.mapping-row').forEach(row => {
    const cid = row.querySelector('[data-field="cid"]').value.trim();
    const remark = row.querySelector('[data-field="remark"]').value.trim();
    if (cid && remark) result[cid] = remark;
  });
  const add = (cid, remark) => { if (cid && remark) result[cid.trim()] = remark.trim(); };
  add($('115-archive-cid').value, $('115-archive-remark').value);
  add($('115-failure-cid').value, $('115-failure-remark').value);
  $('115-downloads').querySelectorAll('.mapping-row').forEach(row => add(row.querySelector('[data-field="cid"]').value, row.querySelector('[data-field="remark"]').value));
  return result;
}

function parseDownloadMapping(value) {
  const parts = String(value || '').split('=>').map(item => item.trim());
  return {cid: parts[0] || '', path: parts[1] || '', mode: ['approval', 'manual'].includes((parts[2] || '').toLowerCase()) ? 'approval' : 'auto'};
}

function addDownloadRow(value, remarks) {
  const list = $('115-downloads');
  if (!list) return;
  const item = typeof value === 'string' ? parseDownloadMapping(value) : (value || {});
  const mode = item.mode === 'approval' ? 'approval' : 'auto';
  const select = '<select class="mapping-select" data-field="mode"><option value="auto"' + (mode === 'auto' ? ' selected' : '') + '>自动下载</option><option value="approval"' + (mode === 'approval' ? ' selected' : '') + '>等待批准</option></select>';
  list.appendChild(removableRow('', mappingInput('115 文件夹 CID', item.cid, 'cid') + mappingInput('备注，例如：手动下载', item.remark || (remarks || {})[item.cid] || '', 'remark') + mappingInput('对应 CD2 路径', item.cd2_path || item.path, 'path') + select));
}

function fillDownloadRows(values, remarks) {
  const list = $('115-downloads');
  if (!list) return;
  list.innerHTML = '';
  (values || []).forEach(value => addDownloadRow(value, remarks));
  if (!list.children.length) addDownloadRow({});
}

function collectDownloadMappings() {
  return Array.from($('115-downloads').querySelectorAll('.mapping-row')).map(row => {
    const cid = row.querySelector('[data-field="cid"]').value.trim();
    const path = row.querySelector('[data-field="path"]').value.trim();
    const mode = row.querySelector('[data-field="mode"]').value;
    return cid && path ? cid + ' => ' + path + ' => ' + mode : '';
  }).filter(Boolean);
}

function collectDownloadRules() {
  return Array.from($('115-downloads').querySelectorAll('.mapping-row')).map(row => {
    const cid = row.querySelector('[data-field="cid"]').value.trim();
    const remark = row.querySelector('[data-field="remark"]').value.trim();
    const cd2Path = row.querySelector('[data-field="path"]').value.trim();
    const mode = row.querySelector('[data-field="mode"]').value;
    return cid && cd2Path ? {id: 'download:' + cid, cid, remark, cd2_path: cd2Path, mode} : null;
  }).filter(Boolean);
}

function parsePathMapping(value) {
  const parts = String(value || '').split('=>').map(item => item.trim());
  return {cloud: parts[0] || '', local: parts.slice(1).join('=>').trim()};
}

function addPathMappingRow(value) {
  const list = $('path-mappings');
  if (!list) return;
  const item = typeof value === 'string' ? parsePathMapping(value) : (value || {});
  list.appendChild(removableRow('pair', mappingInput('CD2 云端根路径', item.cloud, 'cloud') + mappingInput('容器挂载路径', item.local, 'local')));
}

function fillPathMappingRows(values) {
  const list = $('path-mappings');
  if (!list) return;
  list.innerHTML = '';
  (values || []).forEach(addPathMappingRow);
  if (!list.children.length) addPathMappingRow({});
}

function collectPathMappings() {
  return Array.from($('path-mappings').querySelectorAll('.mapping-row')).map(row => {
    const cloud = row.querySelector('[data-field="cloud"]').value.trim();
    const local = row.querySelector('[data-field="local"]').value.trim();
    return cloud && local ? cloud + '=>' + local : '';
  }).filter(Boolean);
}

function updateLocalArchiveVisibility() {
  if (!$('local-source-action')) return;
  $('local-archive-row').style.display = $('local-source-action').value === 'archive' ? 'flex' : 'none';
}

function fillForms(data) {
  ensureNotificationOptions();
  ensureLocalSettings();
  $('notify-enabled').checked = !!data.notification.enabled;
  $('notify-url').value = data.notification.url || '';
  const notifyEvents = data.notification.events || {discovery: true, cache: true, extract: true, complete: true, cleanup: true};
  $('notify-discovery').checked = !!notifyEvents.discovery;
  $('notify-cache').checked = !!notifyEvents.cache;
  $('notify-extract').checked = !!notifyEvents.extract;
  $('notify-complete').checked = !!notifyEvents.complete;
  $('notify-cleanup').checked = !!notifyEvents.cleanup;
  renderNotificationTemplates(data.notification);
  updateNotificationAddressLabel();
  $('workers').value = data.totals.workers || 1;
  $('local-source-action').value = (data.settings && data.settings.local_source_action) || 'keep';
  $('local-archive-dir').value = (data.settings && data.settings.local_archive_dir) || '/data/\u5f52\u6863\u76ee\u5f55';
  $('local-source-delay').value = (data.settings && data.settings.local_source_delay) || '0s';
  $('folder-interval').value = (data.settings && data.settings.folder_interval) || '60s';
  $('cd2-fallback-enabled').checked = data.settings && data.settings.cd2_fallback_enabled !== false;
  $('cd2-fallback-interval').value = (data.settings && data.settings.cd2_fallback_interval) || '30m';
  $('115-enabled').checked = !!(data.settings && data.settings['115_enabled']);
  $('115-event-enabled').checked = !!(data.settings && data.settings['115_event_enabled']);
  $('115-cookie').value = '';
  $('115-cookie').placeholder = (data.settings && data.settings['115_cookie']) || '已保存，留空表示不修改';
  $('115-cookie-remark').value = (data.settings && data.settings['115_cookie_remark']) || '';
  $('115-event-interval').value = (data.settings && data.settings['115_event_interval']) || '30m';
  $('115-success-action').value = (data.settings && data.settings['115_success_action']) || 'keep';
  const archiveRule = (data.settings && data.settings['115_archive']) || {};
  $('115-archive-cid').value = archiveRule.cid || (data.settings && data.settings['115_archive_cid']) || '';
  const cidRemarks = (data.settings && data.settings['115_cid_remarks']) || {};
  $('115-archive-remark').value = archiveRule.remark || cidRemarks[$('115-archive-cid').value] || '';
  const failureRule = (data.settings && data.settings['115_failure']) || {};
	$('115-failure-cid').value = failureRule.cid || (data.settings && data.settings['115_failure_cid']) || '';
  $('115-failure-remark').value = failureRule.remark || cidRemarks[$('115-failure-cid').value] || '';
	$('115-failure-path').value = failureRule.cd2_path || (data.settings && data.settings['115_failure_cd2_path']) || '';
  $('115-auto-fallback').checked = !!(data.settings && data.settings['115_auto_fallback']);
  $('115-scan-failure').checked = !!(data.settings && data.settings['115_scan_failure']);
  $('115-extract-by-date').checked = !!(data.settings && data.settings['115_extract_by_date']);
	$('115-retry-count').value = (data.settings && data.settings['115_retry_count']) || 3;
	$('115-retry-delay').value = (data.settings && data.settings['115_retry_delay']) || '2m';
	$('115-task-interval').value = (data.settings && data.settings['115_task_interval']) || '30s';
	$('115-offline-cid').value = (data.settings && data.settings['115_offline_cid']) || '';
	$('115-offline-fallback').value = (data.settings && data.settings['115_offline_fallback']) || '60m';
	$('115-offline-token').value = '';
	$('115-offline-token').placeholder = (data.settings && data.settings['115_offline_token']) ? '已保存，留空表示不修改' : '用于快捷指令调用，不是115 Cookie';
	fillSourceRows((data.settings && data.settings['115_sources']) || (data.settings && data.settings['115_source_cids']) || [], cidRemarks);
	fillDownloadRows((data.settings && data.settings['115_download_rules']) || (data.settings && data.settings['115_download_mappings']) || [], cidRemarks);
	fillPathMappingRows((data.settings && data.settings.path_overrides) || []);
  const localFolder = (data.folders || []).find(folder => folder.path !== ((data.settings && data.settings.cache_dir) || '/cache')) || (data.folders || [])[0];
  $('local-path-summary').textContent = localFolder ? '\u76d1\u63a7\uff1a' + localFolder.path + '  \u00b7  \u8f93\u51fa\uff1a' + (localFolder.extract_path || '\u539f\u76ee\u5f55') : '';
  updateLocalArchiveVisibility();
  $('cd2-enabled').checked = !!data.clouddrive2.enabled;
  $('cd2-url').value = data.clouddrive2.url || '';
  $('cd2-token').value = (data.settings && data.settings.cd2_token) || '';
  $('cd2-token').placeholder = data.settings && data.settings.cd2_token ? '已保存，输入新 Token 可替换' : '请输入 CD2 Token';
	$('watch-path').value = '';
  $('refresh-interval').value = (data.settings && data.settings.refresh_interval) || '10m';
  $('refresh-path').value = (data.settings && data.settings.refresh_path) || '/';
	$('path-overrides').value = '';
  $('cache-dir').value = (data.settings && data.settings.cache_dir) || '/cache';
  $('cache-extract-path').value = (data.settings && data.settings.cache_extract_path) || '/output';
  $('keep-cache').checked = !!(data.settings && data.settings.keep_cache);
  update115ArchiveCIDVisibility();
  $('cache-delete-delay').value = (data.settings && data.settings.cache_delete_delay) || '1m';
  $('copy-timeout').value = (data.settings && data.settings.copy_timeout) || '24h';
}

function renderTaskDetails(task) {
  const row = (label, value) => '<dt>' + label + '</dt><dd>' + esc(value || '暂无记录') + '</dd>';
  let rows = row('文件名称', task.name) + row('任务来源', task.source) + row('处理流程', taskTimeline(task)) + row('当前阶段', task.status);
  rows += row(task.source_cid ? '原包名称或挂载路径' : '原包路径', task.path || task.name);
  if (task.source_cid) {
    rows += row('来源目录', task.source_label || '未设置备注') + row('来源目录 CID', task.source_cid) + row('文件 ID', task.file_id);
  }
  if (task.target_cid) rows += row('配置输出目录 CID', task.target_cid);
  if (task.target_cid || task.output_cid) {
    rows += row('实际输出目录', task.output_name || '尚未创建') + row('实际输出目录 CID', task.output_cid || '尚未创建');
  }
  if (task.cached_path || !task.target_cid) rows += row('缓存路径', task.cached_path || '尚未缓存或无需缓存') + row('本地输出路径', task.output_path);
  if (task.files && task.files.length) rows += row('原包文件列表', task.files.join('\n'));
  rows += row('开始时间', task.started_at && !task.started_at.startsWith('0001-') ? new Date(task.started_at).toLocaleString() : '') + row('更新时间', task.updated) + row('重试次数', String(task.retries || 0));
  if (task.next_attempt) rows += row('下次重试时间', task.next_attempt);
  if (task.error) rows += row('错误详情', task.error);
  rows += row('任务标识', task.key);
  return '<details class="task-details" data-detail-key="' + encodeURIComponent(task.key) + '"' + (expandedTasks.has(task.key) ? ' open' : '') + '><summary>详情</summary><dl>' + rows + '</dl></details>';
}

function taskTimeline(task) {
  const stages = ['发现'];
  const source = task.source || '';
  const status = task.status || '';
  if (source.includes('115') || task.source_cid) stages.push('云目录扫描');
  if (status.includes('云解压') || task.target_cid || task.output_cid) stages.push('云解压');
  if (status.includes('批准') || status.includes('下载') || source.includes('转本地')) stages.push('本地兜底');
  if (status.includes('复制') || task.cached_path) stages.push('缓存');
  if (status.includes('解压') && !status.includes('云解压')) stages.push('本地解压');
  if (status.includes('清理') || status.includes('归档') || status.includes('删除')) stages.push('原包处理');
  stages.push(status.includes('失败') ? '失败' : status.includes('取消') ? '取消' : status.includes('完成') || status.includes('已解压') ? '完成' : '当前');
  return stages.filter((stage, index) => index === 0 || stage !== stages[index - 1]).join(' → ');
}

function renderTask(task) {
  const hasCopyProgress = Number(task.total) > 0;
  const percent = hasCopyProgress ? Math.min(100, Number(task.bytes || 0) * 100 / Number(task.total)) : 0;
  let detail = task.progress || '';
  if (hasCopyProgress) {
    detail = (task.status.includes('正在解压') ? '解压进度 ' : '下载进度 ') + percent.toFixed(1) + '% · ' + formatBytes(task.bytes) + ' / ' + formatBytes(task.total);
    if (task.speed) detail += ' · ' + formatBytes(task.speed) + '/s';
    if (task.eta_seconds) detail += ' · 预计 ' + formatDuration(task.eta_seconds);
  }
  const canCancel = !task.can_fallback && !['已取消', '正在取消', '已完成', '已解压', '已导入', '解压失败', '清理失败'].includes(task.status);
  const canIgnore = task.can_fallback || taskGroup(task) === 'waiting';
  const disabled = pendingActions.has(task.cancel_key || task.key) ? ' disabled' : '';
  return '<article class="task"><div class="task-content"><div class="task-name" title="' + esc(task.name) + '">' + esc(task.name) + '</div>' +
    '<div class="task-meta">' + esc(task.source) + ' · ' + esc(task.updated) + '</div>' +
    (detail ? '<div class="progress">' + esc(detail) + '</div>' : '') +
    (hasCopyProgress ? '<div class="copy-bar"><i style="width:' + percent + '%"></i></div>' : '') +
    (task.error ? '<div class="progress" style="color:var(--red)">' + esc(task.error) + '</div>' : '') +
    renderTaskDetails(task) +
    '</div><div class="task-side"><span class="badge">' + esc(task.status) + '</span>' + (task.can_cloud_retry ? '<button data-cloud-retry-task="' + esc(task.fallback_key || task.key) + '" type="button"' + disabled + '>重试云解压</button>' : '') + (task.can_fallback ? '<button data-fallback-task="' + esc(task.fallback_key || task.key) + '" type="button"' + disabled + '>批准下载</button>' : '') + (canIgnore ? '<button data-ignore-task="' + esc(task.cancel_key || task.key) + '" type="button"' + disabled + '>忽略</button>' : '') + (canCancel ? '<button data-cancel-task="' + esc(task.cancel_key || task.key) + '" type="button"' + disabled + '>取消</button>' : '') + '</div></article>';
}

function renderStatus(data) {
  $('connection-dot').className = 'online';
  $('updated-at').textContent = '\u66f4\u65b0\u4e8e ' + new Date(data.updated_at).toLocaleTimeString();
  $('active-count').textContent = data.totals.active;
  $('finished-count').textContent = data.totals.finished;
  $('retry-count').textContent = data.totals.retries;
  $('worker-count').textContent = data.totals.workers;
  taskSystemPaused = !!data.paused;
  const control = $('task-system-control');
  if (control) {
    control.textContent = taskSystemPaused ? '恢复任务系统' : '停止并清空等待任务';
    control.classList.toggle('danger-button', !taskSystemPaused);
  }
  if ($('task-system-state')) {
    $('task-system-state').textContent = taskSystemPaused ? '任务系统已暂停：不会发现或提交新任务，正在解压的任务会继续完成。' : '任务系统运行中';
    $('task-system-state').className = 'form-message ' + (taskSystemPaused ? 'paused-state' : 'running-state');
  }
  if ($('115-scan-state')) {
    const scan = (data.clouddrive2 && data.clouddrive2['115_scan']) || {};
    if (scan.scanned_at) {
      $('115-scan-state').textContent = '115 上次扫描：' + new Date(scan.scanned_at).toLocaleString() +
        ' · 目录 ' + (scan.folders || 0) + ' · 读取 ' + (scan.files || 0) + ' · 提交 ' + (scan.queued || 0) +
        ' · 已处理 ' + (scan.skipped_processed || 0) + ' · 处理中 ' + (scan.skipped_pending || 0) +
        ' · 已忽略 ' + (scan.skipped_ignored || 0) + ' · 非压缩包 ' + (scan.skipped_invalid || 0) +
        ' · 待续扫 ' + (scan.partial || 0) + ' · 失败 ' + (scan.errors || 0) + ' · ' + (scan.duration_ms || 0) + 'ms';
      $('115-scan-state').className = 'form-message ' + (scan.errors ? 'paused-state' : 'running-state');
    } else {
      $('115-scan-state').textContent = '115 云目录尚未完成扫描';
      $('115-scan-state').className = 'form-message';
    }
  }
  if ($('cd2-refresh')) $('cd2-refresh').disabled = taskSystemPaused;
  currentTasks = data.tasks || [];
  renderCurrentTasks();
  renderList($('folders'), data.folders, folder => '<div class="compact-item">' + esc(folder.path) + '<small>' + esc(folder.extract_path || '\u539f\u76ee\u5f55\u8f93\u51fa') + ' · ' + folder.tracked + '</small></div>', zh.noFolders);
  currentHistory = data.history || [];
  renderHistory();
  loadOfflineBatches(true);
  latestLogs = data.logs || [];
  renderLogs();
  $('transfers').innerHTML = '';
  renderList($('password-list'), data.passwords, (password, index) => '<div class="compact-item">' + esc(password) + '<button data-remove-password="' + index + '" type="button">\u5220\u9664</button></div>', zh.noPasswords);
  $('cd2-status').textContent = data.clouddrive2.enabled ? '\u5df2\u542f\u7528 · ' + (data.clouddrive2.url || '') : '\u672a\u542f\u7528';
}

function taskGroup(task) {
  const status = task.status || '';
  if (task.can_fallback || status.includes('等待') || status.includes('排队') || status.includes('暂停') || status.includes('批准') || status.includes('重试中')) return 'waiting';
  if (status.includes('失败')) return 'failed';
  return 'active';
}

function renderHistory() {
  const labels = {success: '已完成', failed: '失败', cancelled: '已取消', ignored: '已忽略'};
  const values = currentHistory.filter(item => historyFilter === 'all' || item.status === historyFilter);
  renderList($('history'), values, item => {
    const disabled = pendingActions.has(item.key) ? ' disabled' : '';
    const button = (action, label) => '<button data-history-action="' + action + '" data-history-key="' + esc(item.key) + '" type="button"' + disabled + '>' + label + '</button>';
    const flow = item.status === 'success' ? '发现 → 处理 → 完成' : item.status === 'cancelled' ? '发现 → 处理 → 取消' : item.status === 'ignored' ? '发现 → 忽略' : '发现 → ' + (item.stage === 'cleanup' ? '原包处理' : item.stage === 'move' ? '云端移动' : '解压') + ' → 失败';
    return '<div class="compact-item history-item"><div class="history-content"><strong title="' + esc(item.path) + '">' + esc(item.path) + '</strong><small>' + esc(item.source) + ' · ' + (labels[item.status] || '已完成') + ' · ' + esc(item.completed_at) + '</small><small>流程：' + esc(flow) + '</small>' + (item.error ? '<small class="history-error">' + esc(item.error) + '</small>' : '') + '</div><div class="history-actions">' + (item.ignored ? button('unignore', '取消忽略') : (item.can_retry ? button('retry', item.stage === 'cleanup' ? '重试清理' : item.stage === 'move' ? '重试移动' : '重试') + button('ignore', '忽略') : '')) + button('delete', '删除记录') + '</div></div>';
  }, zh.noHistory);
}

async function runTaskAction(key, endpoint, action, button) {
  if (pendingActions.has(key)) return;
  pendingActions.add(key);
  if (button) button.disabled = true;
  try {
    const response = await fetch(endpoint, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key, action})});
    if (!response.ok) throw new Error(await response.text() || '操作失败');
    $('refresh-message').textContent = '操作已提交';
    await load(false);
  } catch (error) {
    $('refresh-message').textContent = error.message || '网络异常，请重试';
  } finally {
    pendingActions.delete(key);
    if (button) button.disabled = false;
    renderHistory();
    renderCurrentTasks();
  }
}

function renderCurrentTasks() {
  document.querySelectorAll('#tasks details[data-detail-key]').forEach(detail => {
    const key = decodeURIComponent(detail.dataset.detailKey);
    if (detail.open) expandedTasks.add(key); else expandedTasks.delete(key);
  });
  const values = taskFilter === 'all' ? currentTasks : currentTasks.filter(task => taskGroup(task) === taskFilter);
  renderList($('tasks'), values, renderTask, zh.noTasks);
}

function renderOfflineTaskRecords() {
  const container = $('task-offline-records');
  if (!container) return;
  const labels = {submitting:'提交中', submitted:'等待离线', downloading:'离线下载中', success:'离线成功', failed:'离线失败', submit_failed:'提交失败', unknown:'等待兜底复查'};
  const rows = [];
  currentOfflineBatches.forEach(batch => (batch.tasks || []).forEach(task => rows.push({batch, task})));
  const filtered = rows.filter(row => {
    if (offlineRecordFilter === 'all') return true;
    if (offlineRecordFilter === 'success') return row.task.status === 'success';
    if (offlineRecordFilter === 'failed') return row.task.status === 'failed' || row.task.status === 'submit_failed';
    return ['submitting','submitted','downloading','unknown'].includes(row.task.status);
  });
  renderList(container, filtered, row => {
    const task = row.task, batch = row.batch;
    return '<article class="task offline-record-card"><div class="task-content"><div class="task-name" title="' + esc(task.name) + '">' + esc(task.name) + '</div><div class="task-meta"><span class="offline-source-mark">115离线</span> · ' + esc(task.kind.toUpperCase()) + ' · ' + esc(batch.target_name) + '</div><div class="progress">' + esc(task.error || task.info_hash || '等待115返回任务信息') + '</div><details class="task-details"><summary>查看详情</summary><dl><dt>导入时间</dt><dd>' + esc(new Date(task.created_at).toLocaleString()) + '</dd><dt>目标目录 CID</dt><dd>' + esc(batch.target_cid) + '</dd><dt>查询次数</dt><dd>' + esc(batch.check_count) + '</dd><dt>下次复查</dt><dd>' + esc(batch.next_check ? new Date(batch.next_check).toLocaleString() : '无需自动复查') + '</dd></dl></details></div><div class="task-side"><span class="badge offline-badge">' + esc(labels[task.status] || task.status) + '</span></div></article>';
  }, '暂无离线记录');
}

function renderLogs() {
  const values = logView === 'system' ? latestLogs : latestLogs.filter(item => item.kind === 'user');
  renderList($('logs'), values, item => '<article class="log-item ' + (item.level === '\u9519\u8bef' ? 'log-error' : '') + '"><time>' + esc(item.time) + '</time><span>' + esc(item.level) + '</span><p>' + esc(item.message) + '</p></article>', logView === 'system' ? '暂无系统日志' : '暂无用户日志');
}

function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let n = Number(value), i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(i ? 1 : 0) + ' ' + units[i];
}
function formatDuration(seconds) {
  if (seconds < 60) return Math.max(1, Math.round(seconds)) + ' 秒';
  if (seconds < 3600) return Math.ceil(seconds / 60) + ' 分钟';
  return (seconds / 3600).toFixed(1) + ' 小时';
}

async function load(includeForms) {
  try {
    const response = await fetch('api/status', {cache: 'no-store'});
    if (!response.ok) throw new Error('status failed');
    const data = await response.json();
    renderStatus(data);
    if (includeForms || !formLoaded) {
      fillForms(data);
      formLoaded = true;
    }
  } catch (_) {
    $('updated-at').textContent = zh.cannotConnect;
  }
}

document.querySelectorAll('.tab').forEach(button => button.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach(item => item.classList.remove('active'));
  document.querySelectorAll('.view').forEach(item => item.classList.remove('active-view'));
  button.classList.add('active');
  $(button.dataset.view).classList.add('active-view');
}));
document.querySelectorAll('.task-switch-button').forEach(button => button.addEventListener('click', () => {
  document.querySelectorAll('.task-switch-button').forEach(item => item.classList.remove('active'));
  document.querySelectorAll('.task-subview').forEach(item => item.classList.remove('active-task-subview'));
  button.classList.add('active');
  $(button.dataset.taskView).classList.add('active-task-subview');
}));
document.querySelectorAll('.log-switch-button').forEach(button => button.addEventListener('click', () => {
  document.querySelectorAll('.log-switch-button').forEach(item => item.classList.remove('active'));
  button.classList.add('active');
  logView = button.dataset.logView;
  renderLogs();
}));

$('cd2-refresh').addEventListener('click', async () => {
  $('cd2-refresh').disabled = true;
  $('refresh-message').textContent = '正在扫描 115 来源、刷新 CD2 挂载并补扫本地目录…';
  try {
    const response = await fetch('api/115/sync', {method: 'POST'});
    const text = await response.text();
    let data = {}; try { data = JSON.parse(text); } catch (_) {}
    const scan = data.scan || {};
    $('refresh-message').textContent = response.ok
      ? ('刷新完成：115 来源 ' + (scan.folders || 0) + ' 个，读取 ' + (scan.files || 0) + '，提交 ' + (scan.queued || 0) +
        '；CD2 目录 ' + (data.cd2_paths || 0) + ' 个，发现 ' + (data.cd2_found || 0) + '，失败 ' + (data.cd2_errors || 0) +
        '；本地监控发现 ' + (data.local_found || 0) + ' 个')
      : (text || '全链路刷新失败');
    load(false);
  } catch (_) { $('refresh-message').textContent = '全链路刷新失败'; }
  $('cd2-refresh').disabled = false;
});
function ensureTaskControls() {
  if ($('task-system-control')) return;
  const actions = $('cd2-refresh').parentElement;
  actions.classList.add('task-primary-actions');
  const control = document.createElement('button');
  control.id = 'task-system-control'; control.type = 'button';
  control.textContent = '停止并清空等待任务';
  control.className = 'danger-button';
  actions.appendChild(control);
  const state = document.createElement('p');
  state.id = 'task-system-state'; state.className = 'form-message';
  actions.parentElement.insertAdjacentElement('afterend', state);
  const scanState = document.createElement('p');
  scanState.id = '115-scan-state'; scanState.className = 'form-message';
  state.insertAdjacentElement('afterend', scanState);
  const filters = document.createElement('div');
  filters.id = 'task-filters'; filters.className = 'task-switch';
  filters.innerHTML = '<button class="task-filter active" data-task-filter="all" type="button">全部</button><button class="task-filter" data-task-filter="active" type="button">进行中</button><button class="task-filter" data-task-filter="waiting" type="button">等待中</button>';
  $('current-task-panel').insertAdjacentElement('afterbegin', filters);
  filters.addEventListener('click', event => {
    if (!event.target.dataset.taskFilter) return;
    taskFilter = event.target.dataset.taskFilter;
    filters.querySelectorAll('button').forEach(button => button.classList.toggle('active', button === event.target));
    renderCurrentTasks();
  });
  const historyFilters = document.createElement('div');
  historyFilters.className = 'task-switch';
  historyFilters.setAttribute('aria-label', '历史状态筛选');
  historyFilters.innerHTML = Object.entries({all:'全部', success:'成功', failed:'失败', cancelled:'已取消', ignored:'已忽略'}).map(([value, label]) => '<button type="button" data-history-filter="' + value + '" class="task-filter' + (value === 'all' ? ' active' : '') + '">' + label + '</button>').join('');
  $('task-history-panel').prepend(historyFilters);
  historyFilters.addEventListener('click', event => {
    if (!event.target.dataset.historyFilter) return;
    historyFilter = event.target.dataset.historyFilter;
    historyFilters.querySelectorAll('button').forEach(button => button.classList.toggle('active', button === event.target));
    renderHistory();
  });
  const switcher = document.querySelector('.task-heading .task-switch');
  const offlineTab = document.createElement('button');
  offlineTab.className = 'task-switch-button'; offlineTab.type = 'button'; offlineTab.dataset.taskView = 'task-offline-panel'; offlineTab.textContent = '离线记录';
  switcher.appendChild(offlineTab);
  const offlinePanel = document.createElement('div');
  offlinePanel.id = 'task-offline-panel'; offlinePanel.className = 'task-subview';
  offlinePanel.innerHTML = '<div id="offline-record-filters" class="task-switch"><button class="task-filter active" data-offline-filter="all" type="button">全部</button><button class="task-filter" data-offline-filter="active" type="button">进行中</button><button class="task-filter" data-offline-filter="success" type="button">成功</button><button class="task-filter" data-offline-filter="failed" type="button">失败</button></div><div id="task-offline-records" class="task-list"></div>';
  $('task-history-panel').insertAdjacentElement('afterend', offlinePanel);
  $('offline-record-filters').addEventListener('click', event => {
    if (!event.target.dataset.offlineFilter) return;
    offlineRecordFilter = event.target.dataset.offlineFilter;
    $('offline-record-filters').querySelectorAll('button').forEach(button => button.classList.toggle('active', button === event.target));
    renderOfflineTaskRecords();
  });
  offlineTab.addEventListener('click', () => loadOfflineBatches(true));
  offlineTab.addEventListener('click', () => {
    document.querySelectorAll('.task-switch-button').forEach(item => item.classList.remove('active'));
    document.querySelectorAll('.task-subview').forEach(item => item.classList.remove('active-task-subview'));
    offlineTab.classList.add('active'); offlinePanel.classList.add('active-task-subview');
  });
  control.addEventListener('click', async () => {
    if (!taskSystemPaused && !window.confirm('将暂停本地监听、CD2 推送与扫描、115 云目录扫描，并清除等待、复制、重试和待批准任务及未完成缓存。正在解压和已经开始的 115 云解压不会停止，云端原包不会删除。确定继续吗？')) return;
    control.disabled = true;
    const action = taskSystemPaused ? 'resume' : 'stop_clear';
    $('refresh-message').textContent = taskSystemPaused ? '正在恢复任务系统…' : '正在停止并清理等待任务…';
    try {
      const response = await fetch('api/tasks/system', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action})});
      const data = await response.json().catch(() => ({}));
      $('refresh-message').textContent = response.ok ? data.message : (data.error || '操作失败');
      await load(false);
    } catch (_) { $('refresh-message').textContent = '操作失败'; }
    control.disabled = false;
  });
}
ensureTaskControls();
$('password-form').addEventListener('submit', async event => {
  event.preventDefault();
  const password = $('password-input').value.trim();
  if (!password) return;
  const response = await fetch('api/passwords', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action: 'add', password})});
  if (response.ok) { $('password-input').value = ''; load(false); }
});

$('password-list').addEventListener('click', async event => {
  if (event.target.dataset.removePassword === undefined) return;
  await fetch('api/passwords', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action: 'remove', index: Number(event.target.dataset.removePassword)})});
  load(false);
});

$('history').addEventListener('click', async event => {
  const key = event.target.dataset.historyKey;
  const action = event.target.dataset.historyAction;
  if (!key || !action) return;
  await runTaskAction(key, action === 'ignore' || action === 'unignore' ? 'api/tasks/cancel' : 'api/history/delete', action, event.target);
});

$('tasks').addEventListener('click', async event => {
  const data = event.target.dataset;
  const key = data.ignoreTask || data.cloudRetryTask || data.fallbackTask || data.cancelTask;
  if (!key) return;
  await runTaskAction(key, data.cloudRetryTask || data.fallbackTask ? 'api/115/fallback' : 'api/tasks/cancel', data.ignoreTask ? 'ignore' : data.cloudRetryTask ? 'retry_cloud' : data.fallbackTask ? 'approve' : 'cancel', event.target);
});

$('notify-save').addEventListener('click', async () => {
  await saveNotificationSettings();
});
$('notify-test').addEventListener('click', async () => {
  const response = await fetch('api/notification/test', {method: 'POST'});
  $('notify-message').textContent = response.ok ? zh.submitted : zh.testFailed;
});

$('settings-save').addEventListener('click', async () => {
  const button = $('settings-save');
  button.disabled = true;
  $('settings-message').textContent = '正在保存…';
  try {
    const body = {
      workers: Number($('workers').value) || 1,
      local_source_action: $('local-source-action').value,
      local_archive_dir: $('local-archive-dir').value.trim(),
      local_source_delay: $('local-source-delay').value.trim(),
      folder_interval: $('folder-interval').value.trim(),
      cd2_fallback_enabled: $('cd2-fallback-enabled').checked,
      cd2_fallback_interval: $('cd2-fallback-interval').value.trim(),
      '115_enabled': $('115-enabled').checked,
      '115_event_enabled': $('115-event-enabled').checked,
      '115_cookie': $('115-cookie').value.trim(),
      '115_cookie_remark': $('115-cookie-remark').value.trim(),
      '115_event_interval': $('115-event-interval').value.trim(),
      '115_success_action': $('115-success-action').value,
      '115_archive': {cid: $('115-archive-cid').value.trim(), remark: $('115-archive-remark').value.trim()},
      '115_failure': {id: 'cloud-failure', cid: $('115-failure-cid').value.trim(), remark: $('115-failure-remark').value.trim(), cd2_path: $('115-failure-path').value.trim(), mode: $('115-auto-fallback').checked ? 'auto' : 'approval'},
      '115_auto_fallback': $('115-auto-fallback').checked,
      '115_scan_failure': $('115-scan-failure').checked,
      '115_extract_by_date': $('115-extract-by-date').checked,
		'115_retry_count': Number($('115-retry-count').value) || 3,
		'115_retry_delay': $('115-retry-delay').value.trim(),
		'115_task_interval': $('115-task-interval').value.trim(),
		'115_offline_cid': $('115-offline-cid').value.trim(),
		'115_offline_fallback': $('115-offline-fallback').value.trim(),
		'115_offline_token': $('115-offline-token').value.trim(),
		'115_sources': collectSourceRules(),
		'115_download_rules': collectDownloadRules(),
		'115_mappings': [],
      cd2_enabled: $('cd2-enabled').checked,
      cd2_url: $('cd2-url').value.trim(), cd2_token: $('cd2-token').value.trim(),
		manual_watch_paths: [], watch_path: '', refresh_interval: $('refresh-interval').value.trim(),
		refresh_path: '', path_overrides: collectPathMappings(),
      cache_dir: $('cache-dir').value.trim(), cache_extract_path: $('cache-extract-path').value.trim(),
      keep_cache: $('keep-cache').checked, cache_delete_delay: $('cache-delete-delay').value.trim(), copy_timeout: $('copy-timeout').value.trim(),
    };
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    let response;
    try {
      response = await fetch('api/settings', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), signal: controller.signal});
    } finally { clearTimeout(timer); }
    const text = await response.text();
    let result = {}; try { result = JSON.parse(text); } catch (_) {}
    $('settings-message').textContent = response.ok ? (result.message || zh.restart) : (text || zh.saveFailed);
  } catch (error) {
    $('settings-message').textContent = error && error.name === 'AbortError' ? '保存超时：请查看容器日志' : '保存失败：请刷新页面后重试';
  } finally {
    button.disabled = false;
  }
});

ensureBrandIcon();
ensureLocalSettings();
ensureOfflineUI();
load(true);
setInterval(() => load(false), 5000);

function ensureOfflineUI() {
  if ($('offline-view')) return;
  const passwordTab = document.querySelector('[data-view="password-view"]');
  const button = document.createElement('button');
  button.className = 'tab'; button.dataset.view = 'offline-view'; button.type = 'button'; button.textContent = '115 离线';
  passwordTab.parentElement.insertBefore(button, passwordTab);
  const panel = document.createElement('section');
  panel.id = 'offline-view'; panel.className = 'view panel';
  panel.innerHTML = '<div class="panel-heading offline-page-heading"><div><h2>115 离线</h2><p>批量导入 ED2K、磁力或 TXT，离线完成后可自动接入现有云解压流程。</p></div><div class="form-actions"><button id="offline-refresh" type="button">立即复查</button><button id="offline-clear" type="button">清空记录</button></div></div>' +
    '<label class="field"><span>链接内容</span><textarea id="offline-text" rows="9" placeholder="粘贴多条 ed2k:// 或 magnet:? 链接"></textarea></label>' +
    '<label class="field"><span>TXT 文件</span><input id="offline-file" type="file" accept=".txt,text/plain"></label>' +
    '<label class="check-row"><input id="offline-auto-extract" type="checkbox" checked> 离线完成后自动提交115云解压</label>' +
    '<p id="offline-preview" class="form-message">等待输入</p><div class="form-actions"><button id="offline-submit" type="button">导入离线任务</button></div><p id="offline-message" class="form-message"></p>';
  $('tasks-view').insertAdjacentElement('afterend', panel);
  const style = document.createElement('style');
  style.textContent = '#offline-view textarea{width:100%;box-sizing:border-box;border:1px solid #d8dce5;border-radius:10px;padding:12px;font:inherit;resize:vertical}.offline-page-heading .form-actions{margin-top:0}.offline-batch{border:1px solid #e4e7ec;border-radius:12px;padding:12px;margin-top:10px}.offline-batch summary{cursor:pointer;font-weight:700}.offline-task{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px;padding:8px 0;border-top:1px solid #eee}.offline-task small{display:block;color:var(--muted)}@media(max-width:680px){.offline-page-heading{align-items:flex-start;gap:12px}.offline-page-heading .form-actions{width:100%;display:grid;grid-template-columns:1fr 1fr}.offline-page-heading .form-actions button{width:100%}.offline-task{grid-template-columns:1fr}}';
  document.head.appendChild(style);
  button.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(item => item.classList.remove('active'));
    document.querySelectorAll('.view').forEach(item => item.classList.remove('active-view'));
    button.classList.add('active');
    panel.classList.add('active-view');
    loadOfflineBatches();
  });
  $('offline-text').addEventListener('input', updateOfflinePreview);
  $('offline-file').addEventListener('change', updateOfflinePreview);
  $('offline-submit').addEventListener('click', submitOfflineImport);
  $('offline-refresh').addEventListener('click', async () => { await fetch('api/115/offline/refresh', {method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}); await loadOfflineBatches(); });
  $('offline-clear').addEventListener('click', clearOfflineRecords);
}

function offlineLinkCount(text) {
  return (text.match(/(?:ed2k:\/\/\|file\|.*?\|\/|magnet:\?[^\s]+|(?:https?|ftp):\/\/[^\s]+)/gi) || []).length;
}

async function updateOfflinePreview() {
  let text = $('offline-text').value;
  const file = $('offline-file').files[0];
  if (file) text += '\n' + await file.text().catch(() => '');
  $('offline-preview').textContent = '当前识别约 ' + offlineLinkCount(text) + ' 条链接；提交时后端会按哈希再次去重。';
}

async function submitOfflineImport() {
  const button = $('offline-submit'); button.disabled = true; $('offline-message').textContent = '正在导入并提交到115…';
  const form = new FormData(); form.append('text', $('offline-text').value); form.append('auto_extract', $('offline-auto-extract').checked ? 'true' : 'false');
  const file = $('offline-file').files[0]; if (file) form.append('file', file);
  try {
    const response = await fetch('api/115/offline/import', {method:'POST', body:form});
    const raw = await response.text(); let data = {}; try { data = JSON.parse(raw); } catch (_) {}
    $('offline-message').textContent = response.ok ? ('已创建批次，识别 ' + data.recognized + ' 条，新增 ' + data.submitted + ' 条；首次查询将在60秒后进行。') : raw;
    if (response.ok) { $('offline-text').value = ''; $('offline-file').value = ''; await loadOfflineBatches(); }
  } catch (_) { $('offline-message').textContent = '导入失败，请检查网络和服务日志'; }
  button.disabled = false;
}

async function loadOfflineBatches(silent) {
  try {
    const response = await fetch('api/115/offline', {cache:'no-store'}); const data = await response.json();
    const batches = data.batches || [];
    currentOfflineBatches = batches;
    renderOfflineTaskRecords();
    if ($('offline-batches')) $('offline-batches').innerHTML = batches.length ? batches.map(batch => {
      const counts = {}; (batch.tasks || []).forEach(task => counts[task.status] = (counts[task.status] || 0) + 1);
      const next = batch.next_check ? new Date(batch.next_check).toLocaleString() : '无需复查';
      return '<details class="offline-batch"><summary>' + esc(batch.target_name) + ' · ' + batch.tasks.length + '条 · 成功' + (counts.success || 0) + ' · 进行中' + ((counts.submitted || 0)+(counts.downloading || 0)+(counts.unknown || 0)) + ' · 失败' + ((counts.failed || 0)+(counts.submit_failed || 0)) + '</summary><p class="form-message">已查询 ' + batch.check_count + ' 次，下次：' + esc(next) + '</p>' + batch.tasks.map(task => '<div class="offline-task"><div>' + esc(task.name) + '<small>' + esc(task.kind.toUpperCase()) + ' · ' + esc(task.error || task.info_hash || '') + '</small></div><strong>' + esc(task.status) + '</strong></div>').join('') + '</details>';
    }).join('') : '<p class="empty">暂无离线记录</p>';
  } catch (_) { if (!silent && $('offline-batches')) $('offline-batches').innerHTML = '<p class="empty">读取离线记录失败</p>'; }
}

async function clearOfflineRecords() {
  if (!window.confirm('只清除 UnpackFlow 本地离线记录，不删除115中的任务或文件。清除后相同链接可重新导入。确定继续吗？')) return;
  const response = await fetch('api/115/offline/clear', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({scope:'all'})});
  $('offline-message').textContent = response.ok ? '离线记录已清空' : '清空失败';
  await loadOfflineBatches();
}
