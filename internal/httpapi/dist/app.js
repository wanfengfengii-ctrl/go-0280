// Workbench client: polls the Go backend and renders live state, the task list
// and the three-dimensional sampling grid of a selected task.
async function getJSON(path) {
  const res = await fetch(path);
  if (!res.ok) throw new Error('HTTP ' + res.status);
  return res.json();
}

async function refreshHealth() {
  const status = document.getElementById('status');
  const tasks = document.getElementById('tasks');
  const uptime = document.getElementById('uptime');
  try {
    const data = await getJSON('/api/health');
    status.textContent = data.status === 'ok' ? '在线' : data.status;
    status.style.background = data.status === 'ok' ? '#e0f2fe' : '#fee2e2';
    tasks.textContent = String(data.tasks ?? '-');
    uptime.textContent = String(data.uptime_ms ?? '-');
  } catch (err) {
    status.textContent = '离线';
    status.style.background = '#fee2e2';
    tasks.textContent = '-';
    uptime.textContent = '-';
  }
}

async function refreshTasks() {
  const body = document.getElementById('task-body');
  try {
    const data = await getJSON('/api/tasks');
    const list = data.tasks || [];
    if (list.length === 0) {
      body.innerHTML = '<tr><td colspan="3">暂无任务</td></tr>';
      return;
    }
    body.innerHTML = list.map(t =>
      `<tr class="task" onclick="showSnapshot('${t.id}')">
        <td><code>${t.id}</code></td><td>${t.status}</td><td>${t.generation}</td>
      </tr>`).join('');
  } catch (err) {
    body.innerHTML = '<tr><td colspan="3">加载失败</td></tr>';
  }
}

async function showSnapshot(id) {
  const box = document.getElementById('snapshot');
  try {
    const snap = await getJSON('/api/tasks/' + id);
    const cells = (snap.cells || []).map(c => {
      const co = c.coordinate || {};
      const done = c.plugged ? '已封堵' : (c.sealed ? '已封签' : (c.core_mass > 0 ? '已取芯' : '待取芯'));
      return `<tr><td>${co.zone || ''}</td><td>${co.layer ?? ''}</td><td>${co.depth ?? ''}</td>
        <td><code>${c.blind_code || ''}</code></td><td>${done}</td></tr>`;
    }).join('');
    box.innerHTML = `
      <div class="row"><span>状态</span><span class="badge">${snap.task.status}</span></div>
      <div class="row"><span>代次</span><code>${snap.task.generation}</code></div>
      <table><thead><tr><th>窖区</th><th>压实层</th><th>深度</th><th>盲码</th><th>进度</th></tr></thead>
      <tbody>${cells || '<tr><td colspan="5">无采样格</td></tr>'}</tbody></table>`;
  } catch (err) {
    box.textContent = '加载快照失败';
  }
}

window.showSnapshot = showSnapshot;

refreshHealth();
refreshTasks();
setInterval(refreshHealth, 2000);
setInterval(refreshTasks, 3000);
