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
  const text = document.createElement('div');
  while (title.firstChild) text.appendChild(title.firstChild);
  const icon = document.createElement('img');
  icon.className = 'brand-icon';
  icon.src = 'icon.svg';
  icon.alt = 'UnpackFlow';
  icon.width = 46;
  icon.height = 46;
  icon.style.cssText = 'display:block;flex:0 0 auto;border-radius:11px;box-shadow:0 8px 20px #10182824';
  title.style.cssText = 'display:flex;align-items:center;gap:12px';
  title.append(icon, text);
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
	$('cd2-refresh').textContent = '立即刷新';
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
    '<h3>115 \u751f\u6d3b\u4e8b\u4ef6</h3>' +
    '<label class="check-row"><input id="115-enabled" type="checkbox"> \u542f\u7528 115 \u4e8b\u4ef6\u76d1\u63a7</label>' +
    '<label class="field"><span>115 Cookie</span><input id="115-cookie" type="text" autocomplete="off"></label>' +
    '<label class="field"><span>Cookie \u6765\u6e90\u5907\u6ce8</span><input id="115-cookie-remark" type="text" placeholder="\u4f8b\u5982\uff1a115 \u7f51\u9875\u5f00\u53d1\u8005\u5de5\u5177"></label>' +
    '<label class="check-row"><input id="115-event-enabled" type="checkbox"> \u542f\u7528\u6700\u8fd1\u64cd\u4f5c\u4e8b\u4ef6</label>' +
    '<label class="field"><span>\u4e8b\u4ef6\u540c\u6b65\u95f4\u9694</span><input id="115-event-interval" type="text" placeholder="5m"></label>' +
    '<div class="form-actions"><button id="115-sync" type="button">\u624b\u52a8\u540c\u6b65 115</button></div><p id="115-sync-message" class="form-message"></p>' +
		'<h3>云解压来源</h3><div class="field"><div id="115-sources" class="mapping-list"></div><div class="form-actions"><button id="115-source-add" type="button">添加来源文件夹</button></div></div>' +
    '<label class="field"><span>云解压成功后的原包处理</span><select id="115-success-action"><option value="keep">保留原包</option><option value="delete">删除原包</option><option value="archive">移入成功归档目录</option></select></label>' +
    '<div class="field" id="115-archive-cid-row"><span>成功归档目录</span><div class="settings-pair"><input id="115-archive-cid" type="text" placeholder="115 成功归档文件夹 CID"><input id="115-archive-remark" type="text" placeholder="备注，例如：云解压成功归档"></div></div>' +
		'<h3>云解压失败</h3><div class="field"><span>失败归档目录</span><div class="settings-pair"><input id="115-failure-cid" type="text" placeholder="115 解压失败文件夹 CID"><input id="115-failure-remark" type="text" placeholder="备注，例如：云解压失败"></div></div>' +
		'<label class="field"><span>失败目录的 CD2 路径</span><input id="115-failure-path" type="text" placeholder="/115open/解压失败"></label>' +
    '<label class="check-row"><input id="115-auto-fallback" type="checkbox"> 最终失败后自动下载到本地解压</label><small style="color:var(--muted);font-size:12px">关闭后只移动到失败目录，并在任务页等待批准。</small>' +
		'<div class="field"><span>失败重试</span><div class="settings-pair"><input id="115-retry-count" type="number" min="1" max="10" placeholder="3"><input id="115-retry-delay" type="text" placeholder="2m"></div><small style="color:var(--muted);font-size:12px">尝试次数与两次尝试之间的等待时间。</small></div>' +
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
  nav.innerHTML = '<button class="settings-switch-button active" data-settings-view="settings-basic" type="button">基础</button><button class="settings-switch-button" data-settings-view="settings-local" type="button">本地</button><button class="settings-switch-button" data-settings-view="settings-cloud" type="button">云端</button><button class="settings-switch-button" data-settings-view="settings-maintenance" type="button">数据维护</button>';
  const basic = document.createElement('section'); basic.id = 'settings-basic'; basic.className = 'settings-section active-settings-section';
  const local = document.createElement('section'); local.id = 'settings-local'; local.className = 'settings-section';
  const cloud = document.createElement('section'); cloud.id = 'settings-cloud'; cloud.className = 'settings-section';
  const maintenance = document.createElement('section'); maintenance.id = 'settings-maintenance'; maintenance.className = 'settings-section';
  maintenance.innerHTML = '<div class="panel-heading"><div><h3>数据维护</h3><p>危险操作需要二次确认。清理缓存前必须先在任务页停止任务系统。</p></div></div><div class="form-actions"><button id="clear-all-cache" type="button">清除所有缓存</button><button id="clear-all-history" type="button">清除所有历史记录</button></div><p id="maintenance-message" class="form-message"></p>';
  heading.insertAdjacentElement('afterend', nav); nav.insertAdjacentElement('afterend', basic); basic.insertAdjacentElement('afterend', local); local.insertAdjacentElement('afterend', cloud); cloud.insertAdjacentElement('afterend', maintenance);
  basic.appendChild(workers);
  const all = Array.from(view.children);
  const save = $('settings-save').closest('.form-actions');
  const message = $('settings-message');
  for (const node of all) {
    if (node === heading || node === nav || node === basic || node === local || node === cloud || node === maintenance || node === save || node === message || node === localBlock) continue;
    cloud.appendChild(node);
  }
  const localChildren = Array.from(localBlock.children);
  const splitAt = localChildren.findIndex(node => node.tagName === 'H3' && node.textContent.indexOf('CD2') >= 0);
  local.appendChild(localBlock);
  if (splitAt >= 0) {
    const cloudPart = document.createElement('div');
    cloudPart.className = 'cloud-extra-settings';
    localChildren.slice(splitAt).forEach(node => cloudPart.appendChild(node));
    cloud.appendChild(cloudPart);
  }
  // Keep saving outside the three sections. Otherwise changes made on the
  // 基础 / 本地 tabs have no visible save action after the settings are split.
  cloud.insertAdjacentElement('afterend', save);
  save.insertAdjacentElement('afterend', message);
  nav.querySelectorAll('.settings-switch-button').forEach(button => button.addEventListener('click', () => {
    nav.querySelectorAll('.settings-switch-button').forEach(item => item.classList.remove('active'));
    view.querySelectorAll('.settings-section').forEach(item => item.classList.remove('active-settings-section'));
    button.classList.add('active'); $(button.dataset.settingsView).classList.add('active-settings-section');
  }));
  $('clear-all-cache').addEventListener('click', () => runMaintenance('clear_cache'));
  $('clear-all-history').addEventListener('click', () => runMaintenance('clear_history'));
}

async function runMaintenance(action) {
  const cache = action === 'clear_cache';
  const message = cache ? '将删除全部本地缓存，但不会删除解压输出或云端原包。任务系统必须已暂停。确定继续吗？' : '将清除全部历史记录和防重复记录；实际文件不会删除，旧压缩包之后可能再次被识别。确定继续吗？';
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
  $('115-sync-message').textContent = '\u6b63\u5728\u63d0\u4ea4\u540c\u6b65\u2026';
  try {
    const response = await fetch('api/115/sync', {method: 'POST'});
    const text = await response.text();
    $('115-sync-message').textContent = response.ok ? '\u5df2\u5f00\u59cb\u540c\u6b65 115 \u6587\u4ef6\u5939' : (text || '\u540c\u6b65\u5931\u8d25');
  } catch (_) { $('115-sync-message').textContent = '\u540c\u6b65\u5931\u8d25'; }
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
  $('115-event-interval').value = (data.settings && data.settings['115_event_interval']) || '5m';
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
	$('115-retry-count').value = (data.settings && data.settings['115_retry_count']) || 3;
	$('115-retry-delay').value = (data.settings && data.settings['115_retry_delay']) || '2m';
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

function renderTask(task) {
  const hasCopyProgress = Number(task.total) > 0;
  const percent = hasCopyProgress ? Math.min(100, Number(task.bytes || 0) * 100 / Number(task.total)) : 0;
  let detail = task.progress || '';
  if (hasCopyProgress) {
    detail = '下载进度 ' + percent.toFixed(1) + '% · ' + formatBytes(task.bytes) + ' / ' + formatBytes(task.total);
    if (task.speed) detail += ' · ' + formatBytes(task.speed) + '/s';
    if (task.eta_seconds) detail += ' · 预计 ' + formatDuration(task.eta_seconds);
  }
  const canCancel = ['已取消', '已完成', '已解压', '已导入', '解压失败', '清理失败'].indexOf(task.status) < 0;
  const canIgnore = task.status.includes('失败') || task.error;
  return '<article class="task"><div class="task-content"><div class="task-name" title="' + esc(task.name) + '">' + esc(task.name) + '</div>' +
    '<div class="task-meta">' + esc(task.source) + ' · ' + esc(task.updated) + '</div>' +
    (detail ? '<div class="progress">' + esc(detail) + '</div>' : '') +
    (hasCopyProgress ? '<div class="copy-bar"><i style="width:' + percent + '%"></i></div>' : '') +
    (task.error ? '<div class="progress" style="color:var(--red)">' + esc(task.error) + '</div>' : '') +
    '</div><div class="task-side"><span class="badge">' + esc(task.status) + '</span>' + (task.can_fallback ? '<button data-fallback-task="' + esc(task.fallback_key || task.key) + '" type="button" style="margin-left:8px">批准下载</button>' : '') + (canIgnore ? '<button data-ignore-task="' + esc(task.cancel_key || task.key) + '" type="button" style="margin-left:8px">忽略</button>' : '') + (canCancel ? '<button data-cancel-task="' + esc(task.cancel_key || task.key) + '" type="button" style="margin-left:8px">取消</button>' : '') + '</div></article>';
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
  if ($('cd2-refresh')) $('cd2-refresh').disabled = taskSystemPaused;
  currentTasks = data.tasks || [];
  renderCurrentTasks();
  renderList($('folders'), data.folders, folder => '<div class="compact-item">' + esc(folder.path) + '<small>' + esc(folder.extract_path || '\u539f\u76ee\u5f55\u8f93\u51fa') + ' · ' + folder.tracked + '</small></div>', zh.noFolders);
  renderList($('history'), data.history, item => '<div class="compact-item history-item"><div class="history-content"><strong title="' + esc(item.path) + '">' + esc(item.path) + '</strong><small>' + esc(item.source) + ' · 解压完成 ' + esc(item.completed_at) + (item.cached_at ? ' · 缓存完成 ' + esc(item.cached_at) : '') + '</small></div><div class="history-actions"><button data-history-action="retry" data-history-key="' + esc(item.key) + '" type="button">重试</button><button data-history-action="delete" data-history-key="' + esc(item.key) + '" type="button">删除</button></div></div>', zh.noHistory);
  latestLogs = data.logs || [];
  renderLogs();
  $('transfers').innerHTML = '';
  renderList($('password-list'), data.passwords, (password, index) => '<div class="compact-item">' + esc(password) + '<button data-remove-password="' + index + '" type="button">\u5220\u9664</button></div>', zh.noPasswords);
  $('cd2-status').textContent = data.clouddrive2.enabled ? '\u5df2\u542f\u7528 · ' + (data.clouddrive2.url || '') : '\u672a\u542f\u7528';
}

function taskGroup(task) {
  const status = task.status || '';
  if (status.includes('失败') || task.error) return 'failed';
  if (status.includes('等待') || status.includes('排队') || status.includes('暂停') || status.includes('批准')) return 'waiting';
  return 'active';
}

function renderCurrentTasks() {
  const values = taskFilter === 'all' ? currentTasks : currentTasks.filter(task => taskGroup(task) === taskFilter);
  renderList($('tasks'), values, renderTask, zh.noTasks);
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

$('refresh').addEventListener('click', () => load(false));
$('cd2-refresh').addEventListener('click', async () => {
  $('cd2-refresh').disabled = true;
  $('refresh-message').textContent = '正在刷新…';
  try {
    const response = await fetch('api/clouddrive2/refresh', {method: 'POST'});
    const data = await response.json().catch(() => ({}));
    $('refresh-message').textContent = response.ok ? (data.message || ('同步完成，发现 ' + (data.found || 0) + ' 个压缩文件')) : (data.error || '同步失败');
    load(false);
  } catch (_) { $('refresh-message').textContent = '刷新失败'; }
  $('cd2-refresh').disabled = false;
});
function ensureTaskControls() {
  if ($('task-system-control')) return;
  const actions = $('cd2-refresh').parentElement;
  const control = document.createElement('button');
  control.id = 'task-system-control'; control.type = 'button';
  actions.appendChild(control);
  const state = document.createElement('p');
  state.id = 'task-system-state'; state.className = 'form-message';
  actions.parentElement.insertAdjacentElement('afterend', state);
  const filters = document.createElement('div');
  filters.id = 'task-filters'; filters.className = 'task-switch';
  filters.innerHTML = '<button class="task-filter active" data-task-filter="all" type="button">全部</button><button class="task-filter" data-task-filter="active" type="button">进行中</button><button class="task-filter" data-task-filter="waiting" type="button">等待中</button><button class="task-filter" data-task-filter="failed" type="button">失败</button>';
  $('current-task-panel').insertAdjacentElement('afterbegin', filters);
  filters.addEventListener('click', event => {
    if (!event.target.dataset.taskFilter) return;
    taskFilter = event.target.dataset.taskFilter;
    filters.querySelectorAll('button').forEach(button => button.classList.toggle('active', button === event.target));
    renderCurrentTasks();
  });
  control.addEventListener('click', async () => {
    if (!taskSystemPaused && !window.confirm('将暂停本地监听、CD2 推送与扫描、115 生活事件，并清除等待、复制、重试和待批准任务及未完成缓存。正在解压和已经开始的 115 云解压不会停止，云端原包不会删除。确定继续吗？')) return;
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
  const response = await fetch('api/history/delete', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key, action})});
  if (response.ok) load(false);
});

$('tasks').addEventListener('click', async event => {
  const ignoreKey = event.target.dataset.ignoreTask;
  if (ignoreKey) {
    event.target.disabled = true;
    await fetch('api/tasks/cancel', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key: ignoreKey, action: 'ignore'})});
    load(false); return;
  }
	const fallbackKey = event.target.dataset.fallbackTask;
	if (fallbackKey) {
		event.target.disabled = true;
		const response = await fetch('api/115/fallback', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key: fallbackKey})});
		if (response.ok) load(false); else { $('refresh-message').textContent = await response.text() || '批准本地下载失败'; event.target.disabled = false; }
		return;
	}
	const key = event.target.dataset.cancelTask;
  if (!key) return;
  event.target.disabled = true;
  const response = await fetch('api/tasks/cancel', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({key})});
  if (response.ok) load(false);
  else event.target.disabled = false;
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
		'115_retry_count': Number($('115-retry-count').value) || 3,
		'115_retry_delay': $('115-retry-delay').value.trim(),
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
    $('settings-message').textContent = response.ok ? zh.restart : (text || zh.saveFailed);
  } catch (error) {
    $('settings-message').textContent = error && error.name === 'AbortError' ? '保存超时：请查看容器日志' : '保存失败：请刷新页面后重试';
  } finally {
    button.disabled = false;
  }
});

ensureBrandIcon();
load(true);
setInterval(() => load(false), 5000);
