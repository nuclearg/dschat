(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const el = (tag, cls, html) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (html !== undefined) n.innerHTML = html;
    return n;
  };

  const S = {
    boot: null,
    sessions: [],
    currentId: null,
    current: null,
    streaming: null,
    files: [],
    editing: null,
    connected: false,
  };

  // ---------------- api ----------------
  async function api(path, opts = {}) {
    const init = { method: opts.method || 'GET', headers: {}, credentials: 'same-origin' };
    if (opts.form) {
      init.body = opts.form;
    } else if (opts.body !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(opts.body);
    }
    const res = await fetch(path, init);
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
    if (!res.ok) {
      const msg = (data && data.error && data.error.message) || (data && data.raw) || `HTTP ${res.status}`;
      const err = new Error(msg);
      err.status = res.status;
      err.code = data && data.error && data.error.code;
      err.extra = data;
      throw err;
    }
    return data;
  }

  // ---------------- text ----------------
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  function inline(s) {
    s = esc(s);
    s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
    s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
    return s.replace(/\n/g, '<br>');
  }

  function md(src) {
    if (!src) return '';
    const codes = [];
    src = String(src).replace(/```([\w+-]*)\r?\n([\s\S]*?)```/g, (_m, _lang, body) => {
      codes.push(`<pre class="code"><code>${esc(body)}</code></pre>`);
      return `\n\u0000${codes.length - 1}\u0000\n`;
    });
    const lines = src.split(/\r?\n/);
    const out = [];
    let para = [];
    let list = null;
    const flushP = () => { if (para.length) { out.push(`<p>${inline(para.join('\n'))}</p>`); para = []; } };
    const closeList = () => { if (list) { out.push(`</${list}>`); list = null; } };
    for (const line of lines) {
      const marker = line.trim().match(/^\u0000(\d+)\u0000$/);
      if (marker) { flushP(); closeList(); out.push(codes[Number(marker[1])] || ''); continue; }
      const h = line.match(/^(#{1,6})\s+(.*)$/);
      if (h) { flushP(); closeList(); out.push(`<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`); continue; }
      if (/^\s*[-*]\s+/.test(line)) { flushP(); if (list !== 'ul') { closeList(); out.push('<ul>'); list = 'ul'; } out.push(`<li>${inline(line.replace(/^\s*[-*]\s+/, ''))}</li>`); continue; }
      if (/^\s*\d+\.\s+/.test(line)) { flushP(); if (list !== 'ol') { closeList(); out.push('<ol>'); list = 'ol'; } out.push(`<li>${inline(line.replace(/^\s*\d+\.\s+/, ''))}</li>`); continue; }
      if (/^>\s?/.test(line)) { flushP(); closeList(); out.push(`<blockquote>${inline(line.replace(/^>\s?/, ''))}</blockquote>`); continue; }
      if (/^\s*(---|\*\*\*)\s*$/.test(line)) { flushP(); closeList(); out.push('<hr>'); continue; }
      if (!line.trim()) { flushP(); closeList(); continue; }
      para.push(line);
    }
    flushP(); closeList();
    return out.join('\n');
  }

  function relTime(ms) {
    if (!ms) return '';
    const d = Date.now() - ms;
    if (d < 60e3) return '刚刚';
    if (d < 3600e3) return `${Math.floor(d / 60e3)} 分钟前`;
    if (d < 864e5) return `${Math.floor(d / 3600e3)} 小时前`;
    const t = new Date(ms);
    return `${t.getMonth() + 1}/${t.getDate()}`;
  }

  // ---------------- boot ----------------
  async function boot() {
    const m = location.hash.match(/t=([^&]+)/);
    if (m) {
      try {
        await api('/api/auth', { method: 'POST', body: { token: decodeURIComponent(m[1]) } });
      } catch (e) {
        fatal('鉴权失败，请从托盘菜单重新打开。');
        return;
      }
      history.replaceState(null, '', location.pathname);
    }
    try {
      await refreshBootstrap();
    } catch (e) {
      if (e.status === 401) fatal('未登录。请从托盘菜单「打开」重新进入。');
      else fatal(e.message);
      return;
    }
    bind();
    connectEvents();
    renderAll();
    if (S.sessions.length) openSession(S.sessions[0].id);
  }

  function fatal(msg) {
    $('#messages').innerHTML = `<div class="empty-chat"><h1>需要授权</h1><p>${esc(msg)}</p></div>`;
  }

  async function refreshBootstrap() {
    const b = await api('/api/bootstrap');
    S.boot = b;
    S.sessions = b.sessions || [];
    S.settings = b.settings || S.settings;
    if (S.currentId && !S.sessions.some((s) => s.id === S.currentId)) S.currentId = null;
    renderSessions();
    renderServer();
    renderTitle();
    renderComposer();
  }

  async function openSession(id) {
    if (!id) return;
    try {
      S.current = await api(`/api/sessions/${id}`);
      S.currentId = id;
      S.editing = null;
      renderAll();
      scrollBottom(true);
    } catch (e) {
      if (e.status === 404) {
        S.current = null;
        S.currentId = null;
        renderAll();
      } else toast(e.message, true);
    }
  }

  // ---------------- render ----------------
  function renderAll() {
    renderSessions();
    renderServer();
    renderTitle();
    renderChat();
    renderComposer();
    renderFiles();
  }

  function renderSessions() {
    const box = $('#session-list');
    if (!S.sessions.length) {
      box.innerHTML = '<div class="session-empty">还没有会话</div>';
      return;
    }
    box.innerHTML = S.sessions.map((s) => {
      const title = esc(s.title || '新对话');
      const dot = s.hasOpenTurn ? '<span class="s-dot dot-open" title="有未完成的回答"></span>'
        : s.readOnly ? '<span class="s-dot dot-readonly" title="只读"></span>' : '';
      return `<div class="session-item ${s.id === S.currentId ? 'active' : ''}" data-session="${esc(s.id)}">
        ${dot}<span class="s-title">${title}</span><span class="s-time">${relTime(s.updatedAt)}</span>
      </div>`;
    }).join('');
  }

  function renderServer() {
    const badge = $('#server-badge');
    const server = S.boot && S.boot.server;
    if (server && server.paired) {
      badge.textContent = `同步 · ${server.url.replace(/^https?:\/\//, '')}`;
      badge.classList.add('online');
      badge.title = server.url;
    } else {
      badge.textContent = '本地';
      badge.classList.remove('online');
      badge.title = '未连接 sessionsvr';
    }
  }

  function renderTitle() {
    const t = S.current && S.current.session;
    $('#chat-title').textContent = t ? (t.title || '新对话') : 'dschat';
    let sub = '';
    if (t) {
      if (S.current.readOnly) sub = '只读 · 另一台设备持有此会话';
      else if (t.syncState === 'synced') sub = '已同步';
      else sub = '仅本机';
    }
    $('#chat-subtitle').textContent = sub;

    const actions = $('#topbar-actions');
    actions.innerHTML = '';
    if (!S.current) return;
    if (S.current.readOnly) {
      const btn = el('button', 'ghost-btn', '接管');
      btn.onclick = claim;
      actions.appendChild(btn);
    } else {
      if (S.current.hasOpenTurn) {
        const btn = el('button', 'ghost-btn', '完成上一轮');
        btn.onclick = () => resync(S.currentId);
        actions.appendChild(btn);
      }
      const rename = el('button', 'icon-btn', '✎');
      rename.title = '重命名';
      rename.onclick = renameSession;
      actions.appendChild(rename);
      const del = el('button', 'icon-btn', '🗑');
      del.title = '删除会话';
      del.onclick = deleteSession;
      actions.appendChild(del);
    }
  }

  function turnMap() {
    const map = new Map();
    if (S.current) for (const t of S.current.turns) map.set(t.id, t);
    return map;
  }

  function siblings(turn) {
    if (!S.current) return [turn];
    return S.current.turns
      .filter((t) => t.parentTurnId === turn.parentTurnId)
      .sort((a, b) => (a.createdAt - b.createdAt) || (a.id < b.id ? -1 : 1));
  }

  function renderChat() {
    const box = $('#messages');
    if (!S.current) {
      box.innerHTML = `<div class="empty-chat">
        <h1>开始一个新对话</h1>
        <p>${S.settings && !S.settings.hasKey ? '先在「设置」里填入 DeepSeek API Key。' : '消息保存在本机的 chatsvr 中。'}</p>
      </div>`;
      return;
    }
    const map = turnMap();
    const ids = S.current.path.slice();
    if (S.streaming && !ids.includes(S.streaming.turnId)) ids.push(S.streaming.turnId);
    const parts = [];
    for (const id of ids) {
      const t = map.get(id);
      if (!t) continue;
      parts.push(renderTurn(t));
    }
    box.innerHTML = `<div class="chat-col">${parts.join('')}</div>`;
    if (S.streaming) scrollBottom(false);
  }

  function renderTurn(turn) {
    const sibs = siblings(turn);
    const sibIdx = sibs.findIndex((x) => x.id === turn.id);
    const version = sibs.length > 1
      ? `<span class="version-bar"><button data-act="prev" data-turn="${esc(turn.id)}">‹</button>${sibIdx + 1}/${sibs.length}<button data-act="next" data-turn="${esc(turn.id)}">›</button></span>`
      : '';
    return renderUser(turn, version) + renderAssistant(turn);
  }

  function renderUser(turn, version) {
    const q = turn.q || {};
    const atts = (q.attachments || []).map((a) =>
      `<span class="attach-chip">${esc(a.name)}</span>`).join('');
    const editing = S.editing === turn.id;
    const canEdit = S.current && !S.current.readOnly;
    if (editing) {
      return `<div class="msg user"><div style="width:min(560px,100%)">
        <div class="msg-meta" style="justify-content:flex-end">${version}<span>编辑问题</span></div>
        <textarea id="edit-text" style="width:100%;min-height:90px;background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:10px;outline:none">${esc(q.text || '')}</textarea>
        <div class="modal-actions" style="margin-top:8px">
          <button class="ghost-btn" data-act="cancel-edit" data-turn="${esc(turn.id)}">取消</button>
          <button class="primary-btn" data-act="save-edit" data-turn="${esc(turn.id)}">发送</button>
        </div>
      </div></div>`;
    }
    const actions = canEdit
      ? `<button class="mini-btn" data-act="edit" data-turn="${esc(turn.id)}">编辑</button>`
      : '';
    return `<div class="msg user"><div style="max-width:78%">
      <div class="msg-meta" style="justify-content:flex-end">${version}
        <span class="msg-actions">${actions}</span>
      </div>
      <div class="bubble">${esc(q.text || '')}${atts ? `<div style="margin-top:6px;display:flex;gap:5px;flex-wrap:wrap">${atts}</div>` : ''}</div>
    </div></div>`;
  }

  function renderAssistant(turn) {
    const live = S.streaming && S.streaming.turnId === turn.id;
    const blocks = live ? S.streaming.blocks : (turn.answer ? turn.answer.blocks || [] : []);
    const streamMark = live ? '<span class="cursor"></span>' : '';
    const model = turn.answer && turn.answer.model ? esc(turn.answer.model) : '';
    const actions = [];
    if (!S.current.readOnly) {
      if (live) actions.push(`<button class="mini-btn" data-act="stop" data-turn="${esc(turn.id)}">停止</button>`);
      else if (turn.answer) actions.push(`<button class="mini-btn" data-act="retry" data-turn="${esc(turn.id)}">重试</button>`);
    }
    actions.push(`<button class="mini-btn" data-act="copy" data-turn="${esc(turn.id)}">复制</button>`);

    let body = renderBlocks(blocks, live);
    if (!body) {
      if (live) body = `<span class="waiting">思考中…</span>`;
      else if (turn.answer && turn.answer.status === 'aborted') body = '';
      else if (turn.answer) body = `<span class="waiting">（无内容）</span>`;
      else body = `<span class="waiting">等待回答…</span>`;
    }
    let extra = '';
    if (turn.answer && turn.answer.status === 'aborted') extra += '<div class="interrupted">本轮已中断</div>';
    if (S.current.localOutput && S.current.localOutput[turn.id] && S.current.localOutput[turn.id].length) {
      extra += `<div class="residue"><div class="residue-head">本机未同步的输出（不会上传）</div>${renderBlocks(S.current.localOutput[turn.id], false)}</div>`;
    }
    return `<div class="msg assistant">
      <div class="avatar">DS</div>
      <div class="bubble">
        <div class="msg-meta">${model || 'DeepSeek'}<span class="msg-actions">${actions.join('')}</span></div>
        ${body}${streamMark}${extra}
      </div>
    </div>`;
  }

  function renderBlocks(blocks, live) {
    if (!blocks || !blocks.length) return '';
    const out = [];
    for (const b of blocks) {
      if (!b) continue;
      if (b.type === 'thinking') {
        const text = b.thinking || '';
        const open = live ? ' open' : '';
        out.push(`<details class="thinking"${open}><summary>思考过程</summary><div class="thinking-body">${esc(text)}</div></details>`);
      } else if (b.type === 'text') {
        out.push(`<div class="text">${md(b.text || '')}</div>`);
        out.push(renderCitations(b.citations));
      } else if (b.type === 'server_tool_use') {
        out.push(`<div class="tool-chip">${toolLabel(b)}</div>`);
      } else if (b.type === 'web_search_tool_result') {
        out.push(renderSearchResult(b));
      } else if (b.type === 'web_fetch_tool_result') {
        out.push(`<div class="tool-chip">📄 已读取网页</div>`);
      }
    }
    return out.join('');
  }

  function toolLabel(b) {
    if (b.type !== 'server_tool_use') return '';
    if (b.kind === 'search') {
      const qs = (b.input && b.input.queries) || [];
      return `🔍 搜索：${esc(qs.join(' / '))}`;
    }
    if (b.kind === 'read_link') return `🔗 阅读：${esc((b.input && b.input.url) || '')}`;
    if (b.kind === 'find_in_page') return `🔎 在页面中查找`;
    return '🔧 工具调用';
  }

  function renderSearchResult(b) {
    const items = Array.isArray(b.content) ? b.content.filter((x) => x && x.type === 'web_search_result') : [];
    if (!items.length) return '<div class="tool-chip">📄 搜索结果</div>';
    const links = items.slice(0, 5).map((x) =>
      `<a href="${esc(x.url)}" target="_blank" rel="noreferrer">${esc(x.title || x.url)}</a>`).join('');
    return `<div class="cites"><span>来源：</span>${links}</div>`;
  }

  function renderCitations(cits) {
    if (!Array.isArray(cits) || !cits.length) return '';
    const seen = new Set();
    const links = [];
    for (const c of cits) {
      if (!c || !c.url || seen.has(c.url)) continue;
      seen.add(c.url);
      links.push(`<a href="${esc(c.url)}" target="_blank" rel="noreferrer">${esc(c.title || c.url)}</a>`);
    }
    return links.length ? `<div class="cites"><span>来源：</span>${links.join('')}</div>` : '';
  }

  function renderComposer() {
    const input = $('#input');
    const send = $('#send');
    const readOnly = S.current && S.current.readOnly;
    const streaming = !!S.streaming;
    input.disabled = !!readOnly;
    if (streaming) {
      send.textContent = '停止';
      send.classList.add('danger');
      send.disabled = false;
    } else {
      send.textContent = '发送';
      send.classList.remove('danger');
      send.disabled = !!readOnly;
    }
    if (readOnly) $('#hint').textContent = '只读：此会话由另一台设备持有';
    else if (S.settings && !S.settings.hasKey) $('#hint').textContent = '未配置 API Key';
    else if (S.current && S.current.hasOpenTurn) $('#hint').textContent = '上一轮尚未完成';
    else $('#hint').textContent = '';
    if (S.settings) {
      $('#toggle-thinking').checked = !!S.settings.thinking;
      $('#toggle-search').checked = !!S.settings.search;
    }
  }

  function renderFiles() {
    const box = $('#attach-list');
    box.innerHTML = S.files.map((f, i) =>
      `<span class="attach-chip">${esc(f.name)}<button data-file="${i}" title="移除">×</button></span>`).join('');
  }

  function scrollBottom(force) {
    const box = $('#messages');
    if (!box) return;
    const near = box.scrollHeight - box.scrollTop - box.clientHeight < 140;
    if (force || near) box.scrollTop = box.scrollHeight;
  }

  // ---------------- events ----------------
  let bootTimer = 0, sessTimer = 0, raf = 0, lastRender = 0;

  function refreshBootstrapSoon() {
    clearTimeout(bootTimer);
    bootTimer = setTimeout(() => { refreshBootstrap().catch(() => {}); }, 250);
  }

  function refreshSessionSoon() {
    clearTimeout(sessTimer);
    sessTimer = setTimeout(async () => {
      if (!S.currentId) return;
      try {
        S.current = await api(`/api/sessions/${S.currentId}`);
        renderTitle(); renderChat(); renderComposer();
      } catch (e) {
        if (e.status === 404) { S.current = null; S.currentId = null; renderAll(); }
      }
    }, 250);
  }

  function scheduleRender() {
    const now = Date.now();
    if (now - lastRender > 70) { lastRender = now; renderChat(); return; }
    if (raf) return;
    raf = requestAnimationFrame(() => { raf = 0; lastRender = Date.now(); renderChat(); });
  }

  function onEvent(ev) {
    switch (ev.type) {
      case 'hello':
        if (S.connected) { refreshBootstrapSoon(); refreshSessionSoon(); }
        S.connected = true;
        return;
      case 'turn_started':
        if (ev.sessionId === S.currentId) {
          S.streaming = { turnId: ev.turnId, blocks: [] };
          renderChat(); renderComposer();
        }
        return;
      case 'block_start':
        if (S.streaming && ev.sessionId === S.currentId) {
          S.streaming.blocks[ev.index] = ev.block || {};
          scheduleRender();
        }
        return;
      case 'delta':
        if (S.streaming && ev.sessionId === S.currentId) {
          const b = S.streaming.blocks[ev.index] || (S.streaming.blocks[ev.index] = { type: ev.kind === 'thinking' ? 'thinking' : 'text' });
          const key = ev.kind === 'thinking' ? 'thinking' : 'text';
          b[key] = (b[key] || '') + (ev.text || '');
          scheduleRender();
        }
        return;
      case 'signature':
        if (S.streaming && ev.sessionId === S.currentId) {
          const b = S.streaming.blocks[ev.index] || (S.streaming.blocks[ev.index] = { type: 'thinking' });
          b.signature = ev.signature;
        }
        return;
      case 'citation':
        if (S.streaming && ev.sessionId === S.currentId) {
          const b = S.streaming.blocks[ev.index] || (S.streaming.blocks[ev.index] = { type: 'text' });
          b.citations = b.citations || [];
          b.citations.push(ev.citation);
        }
        return;
      case 'turn_finished':
        if (S.streaming && S.streaming.turnId === ev.turnId) S.streaming = null;
        refreshSessionSoon();
        refreshBootstrapSoon();
        renderComposer();
        return;
      case 'turn_preempted':
        S.streaming = null;
        toast('已被其他设备接管，本轮输出已作废', true);
        refreshSessionSoon();
        refreshBootstrapSoon();
        return;
      case 'error':
        toast(ev.message || '出错了', true);
        return;
      case 'session_updated':
      case 'remote_changed':
      case 'sessions_changed':
        refreshBootstrapSoon();
        if (!ev.sessionId || ev.sessionId === S.currentId) refreshSessionSoon();
        return;
      case 'session_deleted':
        if (ev.sessionId === S.currentId) { S.current = null; S.currentId = null; renderAll(); }
        refreshBootstrapSoon();
        return;
      case 'session_created':
        refreshBootstrapSoon();
        return;
    }
  }

  function connectEvents() {
    if (location.search.indexOf('nostream') >= 0) return;
    const es = new EventSource('/api/events');
    es.onmessage = (e) => {
      try { onEvent(JSON.parse(e.data)); } catch { /* ignore */ }
    };
  }

  // ---------------- actions ----------------
  async function newChat() {
    try {
      const s = await api('/api/sessions', { method: 'POST', body: {} });
      await refreshBootstrap();
      await openSession(s.id);
      $('#input').focus();
    } catch (e) { toast(e.message, true); }
  }

  async function send() {
    if (S.streaming) return stop();
    if (S.current && S.current.readOnly) return;
    if (S.settings && !S.settings.hasKey) { toast('先在设置里填入 DeepSeek API Key', true); openSettings(); return; }
    const input = $('#input');
    const text = input.value.trim();
    if (!text && !S.files.length) return;
    let id = S.currentId;
    try {
      if (!id) {
        const s = await api('/api/sessions', { method: 'POST', body: {} });
        id = s.id;
        await refreshBootstrap();
        await openSession(id);
      }
      await api(`/api/sessions/${id}/turns`, {
        method: 'POST',
        body: { text, attachmentIds: S.files.map((f) => f.id) },
      });
      input.value = '';
      S.files = [];
      renderFiles();
      input.style.height = 'auto';
      await openSession(id);
    } catch (e) {
      toast(e.message, true);
    }
  }

  async function stop() {
    if (!S.streaming) return;
    try {
      await api(`/api/sessions/${S.currentId}/turns/${S.streaming.turnId}/stop`, { method: 'POST' });
    } catch (e) { toast(e.message, true); }
  }

  async function retry(turnId) {
    try {
      await api(`/api/sessions/${S.currentId}/turns`, { method: 'POST', body: { retryOf: turnId } });
      await openSession(S.currentId);
    } catch (e) { toast(e.message, true); }
  }

  async function resync(sessionId) {
    try {
      await api(`/api/sessions/${sessionId}/sync`, { method: 'POST' });
      await openSession(sessionId);
    } catch (e) { toast(e.message, true); }
  }

  async function claim() {
    if (S.current && S.current.hasOpenTurn) {
      if (!confirm('该轮回答尚未完成，接管会中断且不可恢复，是否继续？')) return;
    }
    try {
      await api(`/api/sessions/${S.currentId}/claim`, { method: 'POST', body: { force: true } });
      await refreshBootstrap();
      await openSession(S.currentId);
      toast('已接管会话');
    } catch (e) { toast(e.message, true); }
  }

  async function switchVersion(turnId, delta) {
    const map = turnMap();
    const turn = map.get(turnId);
    if (!turn) return;
    const sibs = siblings(turn);
    if (sibs.length < 2) return;
    const i = sibs.findIndex((x) => x.id === turnId);
    const next = sibs[(i + delta + sibs.length) % sibs.length];
    try {
      await api(`/api/sessions/${S.currentId}/select`, { method: 'POST', body: { turnId: next.id } });
      await openSession(S.currentId);
    } catch (e) { toast(e.message, true); }
  }

  async function saveEdit(turnId) {
    const ta = $('#edit-text');
    if (!ta) return;
    const text = ta.value.trim();
    try {
      await api(`/api/sessions/${S.currentId}/turns`, { method: 'POST', body: { editOf: turnId, text } });
      S.editing = null;
      await openSession(S.currentId);
    } catch (e) { toast(e.message, true); }
  }

  async function renameSession() {
    if (!S.current) return;
    const name = prompt('会话名称', S.current.session.title || '新对话');
    if (name == null) return;
    try {
      await api(`/api/sessions/${S.currentId}`, { method: 'PATCH', body: { title: name.trim() } });
      await refreshBootstrap();
      await openSession(S.currentId);
    } catch (e) { toast(e.message, true); }
  }

  async function deleteSession() {
    if (!S.current) return;
    if (!confirm('删除这个会话？此操作会同步到其他设备。')) return;
    try {
      await api(`/api/sessions/${S.currentId}`, { method: 'DELETE' });
      S.current = null;
      S.currentId = null;
      await refreshBootstrap();
      renderAll();
    } catch (e) { toast(e.message, true); }
  }

  async function uploadFiles(files) {
    for (const f of files) {
      if (S.files.length >= 5) { toast('最多 5 个附件', true); break; }
      const fd = new FormData();
      fd.append('file', f);
      if (S.currentId) fd.append('sessionId', S.currentId);
      try {
        const att = await api('/api/attachments', { method: 'POST', form: fd });
        S.files.push(att);
        renderFiles();
      } catch (e) { toast(`${f.name}: ${e.message}`, true); }
    }
  }

  // ---------------- settings ----------------
  function closeModal() {
    const modal = $('#modal');
    modal.hidden = true;
    $('#modal-box').innerHTML = '';
  }

  function openSettings() {
    const s = S.settings || {};
    const server = (S.boot && S.boot.server) || {};
    const box = $('#modal-box');
    box.innerHTML = `
      <h2>设置</h2>
      <div class="desc">API Key 只保存在本机 chatsvr，不会发往 sessionsvr。</div>
      <div class="field"><label>DeepSeek API Key</label>
        <input id="set-key" type="password" autocomplete="off" placeholder="${s.hasKey ? '已配置（留空不修改）' : 'sk-...'}" />
        ${s.hasKey ? '<div class="note"><a href="#" id="clear-key">移除已保存的 key</a></div>' : ''}
      </div>
      <div class="field"><label>API 地址</label><input id="set-base" value="${esc(s.baseUrl || 'https://api.deepseek.com')}" /></div>
      <div class="field"><label>模型</label>
        <select id="set-model">
          <option value="deepseek-flash" ${s.model === 'deepseek-flash' ? 'selected' : ''}>deepseek-flash</option>
          <option value="deepseek-v4-pro" ${s.model === 'deepseek-v4-pro' ? 'selected' : ''}>deepseek-v4-pro</option>
        </select>
      </div>
      <div class="field"><label>设备名称</label><input id="set-device" value="${esc(s.deviceName || '')}" /></div>
      <div class="modal-actions">
        <button class="ghost-btn" id="set-cancel">取消</button>
        <button class="primary-btn" id="set-save">保存</button>
      </div>
      <div class="divider"></div>
      <h2>sessionsvr</h2>
      <div class="status-line">状态：<span class="pill ${server.paired ? 'on' : ''}">${server.paired ? '已连接' : '未连接'}</span>
        <span>${server.paired ? esc(server.url) : '连接后可在设备间同步'}</span></div>
      <div class="field"><label>地址</label><input id="srv-url" placeholder="https://sync.example.com" value="${server.paired ? esc(server.url) : ''}" /></div>
      <div class="field"><label>配对码</label><input id="srv-code" placeholder="sessionsvr 启动时打印的 6 位数字" /></div>
      <div class="modal-actions">
        ${server.paired ? '<button class="ghost-btn" id="srv-disconnect">断开</button>' : ''}
        <button class="primary-btn" id="srv-pair">连接</button>
      </div>`;
    $('#modal').hidden = false;

    $('#set-cancel').onclick = closeModal;
    $('#set-save').onclick = saveSettings;
    $('#srv-pair').onclick = pair;
    if ($('#srv-disconnect')) $('#srv-disconnect').onclick = disconnect;
    if ($('#clear-key')) $('#clear-key').onclick = async (e) => {
      e.preventDefault();
      await api('/api/settings', { method: 'PUT', body: { clearKey: true } });
      S.settings = await api('/api/settings');
      closeModal();
      toast('已移除 API Key');
    };
    setTimeout(() => $('#set-key').focus(), 30);
  }

  async function saveSettings() {
    const body = {
      baseUrl: $('#set-base').value.trim(),
      model: $('#set-model').value,
      deviceName: $('#set-device').value.trim(),
    };
    const key = $('#set-key').value.trim();
    if (key) body.apiKey = key;
    try {
      await api('/api/settings', { method: 'PUT', body });
      S.settings = await api('/api/settings');
      closeModal();
      await openSettings();
      toast('已保存');
    } catch (e) { toast(e.message, true); }
  }

  async function pair() {
    const url = $('#srv-url').value.trim();
    const code = $('#srv-code').value.trim();
    if (!url || !code) { toast('填写地址和配对码', true); return; }
    try {
      await api('/api/server/pair', { method: 'POST', body: { url, code } });
      closeModal();
      await refreshBootstrap();
      toast('已连接 sessionsvr');
    } catch (e) { toast(e.message, true); }
  }

  async function disconnect() {
    if (!confirm('断开 sessionsvr？已同步的会话会保留在本机。')) return;
    try {
      await api('/api/server/disconnect', { method: 'POST' });
      closeModal();
      await refreshBootstrap();
      toast('已断开');
    } catch (e) { toast(e.message, true); }
  }

  // ---------------- dom ----------------
  function bind() {
    $('#new-chat').onclick = newChat;
    $('#open-settings').onclick = openSettings;
    $('#menu').onclick = () => $('#sidebar').classList.toggle('open');
    $('#send').onclick = () => {
      if (S.streaming) stop();
      else send();
    };
    $('#attach').onclick = () => $('#file-input').click();
    $('#file-input').onchange = (e) => {
      const files = Array.from(e.target.files || []);
      e.target.value = '';
      uploadFiles(files);
    };
    $('#attach-list').onclick = (e) => {
      const b = e.target.closest('[data-file]');
      if (!b) return;
      S.files.splice(Number(b.dataset.file), 1);
      renderFiles();
    };
    $('#session-list').onclick = (e) => {
      const item = e.target.closest('[data-session]');
      if (!item) return;
      openSession(item.dataset.session);
      $('#sidebar').classList.remove('open');
    };
    $('#messages').onclick = (e) => {
      const b = e.target.closest('[data-act]');
      if (!b) return;
      const act = b.dataset.act;
      const turnId = b.dataset.turn;
      if (act === 'prev') switchVersion(turnId, -1);
      else if (act === 'next') switchVersion(turnId, 1);
      else if (act === 'retry') retry(turnId);
      else if (act === 'stop') stop();
      else if (act === 'edit') { S.editing = turnId; renderChat(); setTimeout(() => { const t = $('#edit-text'); if (t) t.focus(); }, 20); }
      else if (act === 'cancel-edit') { S.editing = null; renderChat(); }
      else if (act === 'save-edit') saveEdit(turnId);
      else if (act === 'copy') {
        const map = turnMap();
        const t = map.get(turnId);
        const text = t && t.answer ? (t.answer.blocks || []).filter((x) => x.type === 'text').map((x) => x.text || '').join('\n') : '';
        navigator.clipboard.writeText(text).then(() => toast('已复制')).catch(() => {});
      }
    };
    $('#modal').onclick = (e) => { if (e.target.id === 'modal') closeModal(); };
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closeModal();
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && document.activeElement === $('#input')) {
        e.preventDefault();
        send();
      }
    });
    const input = $('#input');
    input.addEventListener('input', () => {
      input.style.height = 'auto';
      input.style.height = `${Math.min(220, input.scrollHeight)}px`;
    });
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
        e.preventDefault();
        send();
      }
    });
    $('#toggle-thinking').onchange = (e) => saveToggle('thinking', e.target.checked);
    $('#toggle-search').onchange = (e) => saveToggle('search', e.target.checked);
  }

  async function saveToggle(key, value) {
    if (S.settings) S.settings[key] = value;
    try { await api('/api/settings', { method: 'PUT', body: { [key]: value } }); } catch { /* ignore */ }
  }

  function toast(msg, isError) {
    const box = $('#toasts');
    const n = el('div', `toast${isError ? ' error' : ''}`, esc(msg));
    box.appendChild(n);
    setTimeout(() => { n.style.opacity = '0'; n.style.transition = 'opacity .3s'; }, 3200);
    setTimeout(() => n.remove(), 3600);
  }

  boot();
})();
