const $ = id => document.getElementById(id);
const zh = {
  noTasks: '\u6682\u65e0\u4efb\u52a1', noPasswords: '\u6682\u65e0\u5bc6\u7801',
  noHistory: '\u6682\u65e0\u5386\u53f2\u8bb0\u5f55', noFolders: '\u5c1a\u672a\u914d\u7f6e\u76d1\u63a7\u76ee\u5f55', noLogs: '\u6682\u65e0\u8fd0\u884c\u65e5\u5fd7',
  saved: '\u5df2\u4fdd\u5b58', saveFailed: '\u4fdd\u5b58\u5931\u8d25',
  restart: '\u5df2\u4fdd\u5b58\uff0c\u8bf7\u91cd\u542f\u670d\u52a1\u751f\u6548', submitted: '\u5df2\u63d0\u4ea4',
  testFailed: '\u6d4b\u8bd5\u5931\u8d25', cannotConnect: '\u65e0\u6cd5\u8fde\u63a5',
};
let formLoaded = false;

function esc(value) {
  const element = document.createElement('div');
  element.textContent = value == null ? '' : value;
  return element.innerHTML;
}

function renderList(element, values, render, empty) {
  element.innerHTML = values && values.length ? values.map(render).join('') : `<p class="empty">${empty}</p>`;
}

function fillForms(data) {
  $('notify-enabled').checked = !!data.notification.enabled;
  $('notify-url').value = data.notification.url || '';
  $('workers').value = data.totals.workers || 1;
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
}

function renderStatus(data) {
  $('connection-dot').className = 'online';
  $('updated-at').textContent = '\u66f4\u65b0\u4e8e ' + new Date(data.updated_at).toLocaleTimeString();
  $('active-count').textContent = data.totals.active;
  $('finished-count').textContent = data.totals.finished;
  $('retry-count').textContent = data.totals.retries;
  $('worker-count').textContent = data.totals.workers;
  renderList($('tasks'), data.tasks, task => '<article class="task"><div><div class="task-name">' + esc(task.name) + '</div><div class="task-meta">' + esc(task.source) + ' · ' + esc(task.updated) + '</div></div><div class="task-side"><span class="badge">' + esc(task.status) + '</span></div></article>', zh.noTasks);
  renderList($('folders'), data.folders, folder => '<div class="compact-item">' + esc(folder.path) + '<small>' + esc(folder.extract_path || '\u539f\u76ee\u5f55\u8f93\u51fa') + ' · ' + folder.tracked + '</small></div>', zh.noFolders);
  renderList($('history'), data.history, item => '<div class="compact-item"><div>' + esc(item.path) + '<small>' + esc(item.source) + ' · ' + esc(item.completed_at) + '</small></div><div><button data-history-action="retry" data-history-key="' + esc(item.key) + '" type="button">重试</button><button data-history-action="delete" data-history-key="' + esc(item.key) + '" type="button">删除</button></div></div>', zh.noHistory);
  renderList($('logs'), data.logs, item => '<article class="log-item ' + (item.level === '\u9519\u8bef' ? 'log-error' : '') + '"><time>' + esc(item.time) + '</time><span>' + esc(item.level) + '</span><p>' + esc(item.message) + '</p></article>', zh.noLogs);
  renderList($('password-list'), data.passwords, (password, index) => '<div class="compact-item">' + esc(password) + '<button data-remove-password="' + index + '" type="button">\u5220\u9664</button></div>', zh.noPasswords);
  $('cd2-status').textContent = data.clouddrive2.enabled ? '\u5df2\u542f\u7528 · ' + (data.clouddrive2.url || '') : '\u672a\u542f\u7528';
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

$('refresh').addEventListener('click', () => load(false));
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
  const response = await fetch('api/notification', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({enabled: $('notify-enabled').checked, url: $('notify-url').value.trim()})});
  $('notify-message').textContent = response.ok ? zh.saved : zh.saveFailed;
});
$('notify-test').addEventListener('click', async () => {
  const response = await fetch('api/notification/test', {method: 'POST'});
  $('notify-message').textContent = response.ok ? zh.submitted : zh.testFailed;
});

$('settings-save').addEventListener('click', async () => {
  const body = {
    workers: Number($('workers').value) || 1, cd2_enabled: $('cd2-enabled').checked,
    cd2_url: $('cd2-url').value.trim(), cd2_token: $('cd2-token').value.trim(),
    watch_path: $('watch-path').value.trim(), refresh_interval: $('refresh-interval').value.trim(),
    refresh_path: $('refresh-path').value.trim(), path_overrides: $('path-overrides').value.split(',').map(item => item.trim()).filter(Boolean),
    cache_dir: $('cache-dir').value.trim(), cache_extract_path: $('cache-extract-path').value.trim(),
    keep_cache: $('keep-cache').checked, delete_source: $('delete-source').checked, cache_delete_delay: $('cache-delete-delay').value.trim(),
  };
  const response = await fetch('api/settings', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
  $('settings-message').textContent = response.ok ? zh.restart : zh.saveFailed;
});

load(true);
setInterval(() => load(false), 5000);
