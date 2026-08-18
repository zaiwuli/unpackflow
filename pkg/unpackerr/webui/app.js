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

function ensureLocalSettings() {
  if ($('local-source-action')) return;
  const workers = $('workers').closest('.field');
  if (!workers) return;
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
    '<small style="color:var(--muted);font-size:12px">0s \u5173\u95ed\u8865\u507f\u626b\u63cf\uff0c\u5b9e\u65f6\u76d1\u542c\u4ecd\u4fdd\u7559</small></label>';
  workers.insertAdjacentElement('afterend', block);
  const select = $('local-source-action');
  select.style.cssText = 'width:100%;border:1px solid #d8dce5;border-radius:8px;padding:9px 10px;background:#fff;font:inherit';
  select.addEventListener('change', updateLocalArchiveVisibility);
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
  $('workers').value = data.totals.workers || 1;
  $('local-source-action').value = (data.settings && data.settings.local_source_action) || 'keep';
  $('local-archive-dir').value = (data.settings && data.settings.local_archive_dir) || '/data/\u5f52\u6863\u76ee\u5f55';
  $('local-source-delay').value = (data.settings && data.settings.local_source_delay) || '0s';
  $('folder-interval').value = (data.settings && data.settings.folder_interval) || '60s';
  const localFolder = (data.folders || []).find(folder => folder.path !== ((data.settings && data.settings.cache_dir) || '/cache')) || (data.folders || [])[0];
  $('local-path-summary').textContent = localFolder ? '\u76d1\u63a7\uff1a' + localFolder.path + '  \u00b7  \u8f93\u51fa\uff1a' + (localFolder.extract_path || '\u539f\u76ee\u5f55') : '';
  updateLocalArchiveVisibility();
  $('cd2-enabled').checked = !!data.clouddrive2.enabled;
  $('cd2-url').value = data.clouddrive2.url || '';
  $('watch-path').value = (data.settings && data.settings.watch_path) || '/';
  $('refresh-interval').value = (data.settings && data.settings.refresh_interval) || '10m';
  $('refresh-path').value = (data.settings && data.settings.refresh_path) || '/';
  $('path-overrides').value = ((data.settings && data.settings.path_overrides) || []).join(',');
  $('cache-dir').value = (data.settings && data.settings.cache_dir) || '/cache';
  $('cache-extract-path').value = (data.settings && data.settings.cache_extract_path) || '/output';
  $('keep-cache').checked = !!(data.settings && data.settings.keep_cache);
  $('delete-source').checked = !!(data.settings && data.settings.delete_source);
  $('cache-delete-delay').value = (data.settings && data.settings.cache_delete_delay) || '1m';
  $('copy-timeout').value = (data.settings && data.settings.copy_timeout) || '24h';
}

function renderTask(task) {
  const hasCopyProgress = Number(task.total) > 0;
  const percent = hasCopyProgress ? Math.min(100, Number(task.bytes || 0) * 100 / Number(task.total)) : 0;
  let detail = task.progress || '';
  if (hasCopyProgress) {
    detail = formatBytes(task.bytes) + ' / ' + formatBytes(task.total);
    if (task.speed) detail += ' · ' + formatBytes(task.speed) + '/s';
    if (task.eta_seconds) detail += ' · 预计 ' + formatDuration(task.eta_seconds);
  }
  return '<article class="task"><div style="min-width:0;flex:1"><div class="task-name">' + esc(task.name) + '</div>' +
    '<div class="task-meta">' + esc(task.source) + ' · ' + esc(task.updated) + '</div>' +
    (detail ? '<div class="progress">' + esc(detail) + '</div>' : '') +
    (hasCopyProgress ? '<div class="copy-bar"><i style="width:' + percent + '%"></i></div>' : '') +
    (task.error ? '<div class="progress" style="color:var(--red)">' + esc(task.error) + '</div>' : '') +
    '</div><div class="task-side"><span class="badge">' + esc(task.status) + '</span></div></article>';
}

function renderStatus(data) {
  $('connection-dot').className = 'online';
  $('updated-at').textContent = '\u66f4\u65b0\u4e8e ' + new Date(data.updated_at).toLocaleTimeString();
  $('active-count').textContent = data.totals.active;
  $('finished-count').textContent = data.totals.finished;
  $('retry-count').textContent = data.totals.retries;
  $('worker-count').textContent = data.totals.workers;
  renderList($('tasks'), data.tasks, renderTask, zh.noTasks);
  renderList($('folders'), data.folders, folder => '<div class="compact-item">' + esc(folder.path) + '<small>' + esc(folder.extract_path || '\u539f\u76ee\u5f55\u8f93\u51fa') + ' · ' + folder.tracked + '</small></div>', zh.noFolders);
  renderList($('history'), data.history, item => '<div class="compact-item history-item"><div class="history-content"><strong title="' + esc(item.path) + '">' + esc(item.path) + '</strong><small>' + esc(item.source) + ' · 解压完成 ' + esc(item.completed_at) + (item.cached_at ? ' · 缓存完成 ' + esc(item.cached_at) : '') + '</small></div><div class="history-actions"><button data-history-action="retry" data-history-key="' + esc(item.key) + '" type="button">重试</button><button data-history-action="delete" data-history-key="' + esc(item.key) + '" type="button">删除</button></div></div>', zh.noHistory);
  latestLogs = data.logs || [];
  renderLogs();
  $('transfers').innerHTML = '';
  renderList($('password-list'), data.passwords, (password, index) => '<div class="compact-item">' + esc(password) + '<button data-remove-password="' + index + '" type="button">\u5220\u9664</button></div>', zh.noPasswords);
  $('cd2-status').textContent = data.clouddrive2.enabled ? '\u5df2\u542f\u7528 · ' + (data.clouddrive2.url || '') : '\u672a\u542f\u7528';
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
  $('refresh-message').textContent = '正在刷新 CD2…';
  try {
    const response = await fetch('api/clouddrive2/refresh', {method: 'POST'});
    const data = await response.json().catch(() => ({}));
    $('refresh-message').textContent = response.ok ? '刷新完成，发现 ' + (data.found || 0) + ' 个压缩文件' : (data.error || '刷新失败');
    load(false);
  } catch (_) { $('refresh-message').textContent = '刷新失败'; }
  $('cd2-refresh').disabled = false;
});
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

$('notify-save').addEventListener('click', async () => {
  const response = await fetch('api/notification', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({
    enabled: $('notify-enabled').checked,
    url: $('notify-url').value.trim(),
    events: {
      discovery: $('notify-discovery').checked,
      cache: $('notify-cache').checked,
      extract: $('notify-extract').checked,
      complete: $('notify-complete').checked,
      cleanup: $('notify-cleanup').checked,
    },
  })});
  $('notify-message').textContent = response.ok ? zh.saved : zh.saveFailed;
});
$('notify-test').addEventListener('click', async () => {
  const response = await fetch('api/notification/test', {method: 'POST'});
  $('notify-message').textContent = response.ok ? zh.submitted : zh.testFailed;
});

$('settings-save').addEventListener('click', async () => {
  const body = {
    workers: Number($('workers').value) || 1,
    local_source_action: $('local-source-action').value,
    local_archive_dir: $('local-archive-dir').value.trim(),
    local_source_delay: $('local-source-delay').value.trim(),
    folder_interval: $('folder-interval').value.trim(),
    cd2_enabled: $('cd2-enabled').checked,
    cd2_url: $('cd2-url').value.trim(), cd2_token: $('cd2-token').value.trim(),
    watch_path: $('watch-path').value.trim(), refresh_interval: $('refresh-interval').value.trim(),
    refresh_path: $('refresh-path').value.trim(), path_overrides: $('path-overrides').value.split(',').map(item => item.trim()).filter(Boolean),
    cache_dir: $('cache-dir').value.trim(), cache_extract_path: $('cache-extract-path').value.trim(),
    keep_cache: $('keep-cache').checked, delete_source: $('delete-source').checked, cache_delete_delay: $('cache-delete-delay').value.trim(), copy_timeout: $('copy-timeout').value.trim(),
  };
  const response = await fetch('api/settings', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
  $('settings-message').textContent = response.ok ? zh.restart : zh.saveFailed;
});

ensureBrandIcon();
load(true);
setInterval(() => load(false), 5000);
