import {Events, Window} from "@wailsio/runtime";
import {AppService, ConvertConfig, Settings} from "../bindings/imgConvert";

const $ = (id) => document.getElementById(id);

const el = {
    titlebarDrag: $('titlebar-drag'),
    winBtns: $('win-btns'),
    btnMin: $('btn-min'),
    btnMax: $('btn-max'),
    btnClose: $('btn-close'),

    listPanel: $('list-panel'),
    dzHint: $('dz-hint'),
    btnAddFiles: $('btn-add-files'),
    btnAddFolder: $('btn-add-folder'),

    fileCount: $('file-count'),
    sizeTotal: $('size-total'),
    btnRemove: $('btn-remove'),
    btnClear: $('btn-clear'),
    rows: $('file-rows'),
    emptyState: $('empty-state'),
    checkAll: $('check-all'),

    outMode: $('out-mode'),
    outDirWrap: $('out-dir-wrap'),
    outDir: $('out-dir'),
    btnOutDir: $('btn-out-dir'),
    outOverwrite: $('out-overwrite'),
    formatChips: $('format-chips'),
    quality: $('quality'),
    qualityVal: $('quality-val'),
    sizeMode: $('resize-mode'),
    sizeValue: $('resize-value'),
    sizeNum: $('resize-num'),
    sizeSummary: $('size-summary'),

    progressBar: $('progress-bar'),
    stats: $('stats'),
    btnCancel: $('btn-cancel'),
    btnStart: $('btn-start'),

    toast: $('toast'),
};

const state = {
    info: null,
    settings: null,
    items: [],        // FileItem[]
    rows: new Map(),  // id -> {tr, cells, item}
    selected: new Set(),
    running: false,
    total: 0,
    done: 0,
};

// ── 工具 ─────────────────────────────────────────────────

function fmtBytes(n) {
    if (!n || n < 0) return '—';
    if (n < 1024) return n + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let v = n / 1024, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(v < 10 ? 1 : 0) + ' ' + units[i];
}

function fmtDuration(ms) {
    if (!ms) return '—';
    return (ms / 1000).toFixed(2) + ' s';
}

function fmtRatio(pct) {
    if (pct === null || pct === undefined || !isFinite(pct)) return '—';
    return (pct >= 0 ? '−' : '+') + Math.abs(pct).toFixed(1) + '%';
}

function esc(s) {
    return String(s ?? '').replace(/[&<>"']/g, (c) =>
        ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c]));
}

// 根据窗口最大化状态切换右上角按钮的图标与提示。
function syncMaxState(maximised) {
    const isMax = !!maximised;
    el.btnMax.classList.toggle('is-maximised', isMax);
    el.btnMax.title = isMax ? '还原' : '最大化';
    el.btnMax.setAttribute('aria-label', isMax ? '还原' : '最大化');
}

let toastTimer;
function toast(msg, isError = false) {
    el.toast.textContent = msg;
    el.toast.classList.toggle('is-error', isError);
    el.toast.classList.add('is-visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.toast.classList.remove('is-visible'), 3800);
}

function sourceExt(name) {
    const m = /\.([a-z0-9]+)$/i.exec(name || '');
    return m ? m[1].toUpperCase() : '';
}

// ── 初始化 ───────────────────────────────────────────────

async function init() {
    bindEvents();
    // 同步初始最大化状态（覆盖双击标题栏 / 拖到屏幕顶部等系统操作）
    Window.IsMaximised().then(syncMaxState).catch(() => {});

    state.info = await AppService.GetInfo();
    state.settings = await AppService.GetSettings();

    applyInfo(state.info);
    applySettings(state.settings);
    refresh();
}

function applyInfo(info) {
    if (info.frameless) {
        el.winBtns.hidden = false;
        // 双击标题栏空白区最大化/还原，与系统无边框窗口行为一致。
        el.titlebarDrag.addEventListener('dblclick', (e) => {
            if (e.target.closest('.titlebar-actions')) return;
            AppService.WindowToggleMaximise();
        });
    }

    if (info.engineError) {
        el.dzHint.textContent = '引擎不可用，请检查安装或配置';
    } else {
        el.dzHint.textContent = info.inputExts && info.inputExts.length
            ? `已识别 ${info.inputExts.length} 种输入格式 · 文件夹将递归扫描`
            : '支持绝大多数常见图片格式，文件夹将递归扫描';
    }
}

function applySettings(st) {
    // 输出目录
    const custom = st.outputMode === 'custom';
    el.outMode.querySelectorAll('button').forEach((b) =>
        b.classList.toggle('active', b.dataset.mode === st.outputMode));
    el.outDirWrap.classList.toggle('is-disabled', !custom);
    el.outDir.value = custom ? (st.customDir || '') : '';
    el.outDir.placeholder = custom ? '点击右侧按钮选择目录' : '与源文件同级';
    el.outDir.title = el.outDir.value || el.outDir.placeholder;
    el.outOverwrite.checked = !!st.overwrite;

    // 格式（单选）
    renderChips(st.format || 'avif');

    // 质量
    el.quality.value = st.quality || 80;
    syncQuality();

    // 输出尺寸
    const rm = ['', 'fw', 'fh', 'fl', 'pct'].includes(st.resizeMode) ? st.resizeMode : '';
    el.sizeMode.querySelectorAll('button').forEach((b) =>
        b.classList.toggle('active', (b.dataset.mode || '') === rm));
    el.sizeValue.value = st.resizeValue || '';
    syncSize();
}

function renderChips(active) {
    el.formatChips.innerHTML = '';
    (state.info?.formats || []).forEach((f) => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'chip' + (f.name === active ? ' active' : '');
        b.dataset.fmt = f.name;
        b.textContent = f.name;
        b.title = f.tip || '';
        b.addEventListener('click', () => selectFormat(f.name));
        el.formatChips.appendChild(b);
    });
}

function syncQuality() {
    const v = Number(el.quality.value);
    el.quality.style.setProperty('--fill', ((v - 1) / 99 * 100).toFixed(1) + '%');
    el.qualityVal.textContent = String(v);
}

// 尺寸模式文案：按钮 data-mode 为 "" / "fw" / "fh" / "fl" / "pct"
const SIZE_MODE_TEXT = {fw: '固定宽 =', fh: '固定高 =', fl: '固定长边 =', pct: '等比缩放'};

function currentResizeMode() {
    return el.sizeMode.querySelector('button.active')?.dataset.mode ?? '';
}

function syncSize() {
    const mode = currentResizeMode();
    const pct = mode === 'pct';
    const v = parseInt(el.sizeValue.value, 10);
    const valid = mode && isFinite(v) && v > 0;
    const unit = pct ? '%' : 'px';
    el.sizeNum.classList.toggle('is-disabled', !mode);
    el.sizeValue.disabled = !mode;
    el.sizeNum.dataset.unit = unit;
    el.sizeValue.placeholder = pct ? '百分比' : '像素';
    el.sizeValue.max = pct ? '1000' : '100000';
    el.sizeValue.title = pct
        ? '按百分比整体等比缩放：50 = 缩小一半，200 = 放大一倍，100 = 原尺寸'
        : '目标像素值，另一条边按源图比例自动计算；可放大也可缩小';
    el.sizeSummary.textContent = mode
        ? `${SIZE_MODE_TEXT[mode]} ${valid ? v : '?'}${unit}`
        : '原尺寸';
}

function selectedFormat() {
    return el.formatChips.querySelector('.chip.active')?.dataset.fmt || '';
}

function selectFormat(name) {
    if (name === selectedFormat()) return;
    state.settings.format = name;
    renderChips(name);
    persist();
    refreshStartButton();
}

// ── 列表渲染 ─────────────────────────────────────────────

function addItems(items) {
    if (!items || !items.length) return;
    const frag = document.createDocumentFragment();
    for (const item of items) {
        if (state.rows.has(item.id)) continue;
        state.items.push(item);
        frag.appendChild(buildRow(item));
    }
    el.rows.appendChild(frag);
    refresh();
}

function buildRow(item) {
    const tr = document.createElement('tr');
    tr.dataset.id = item.id;
    tr.innerHTML = `
        <td class="col-check"><input type="checkbox" class="row-check" aria-label="选择"/></td>
        <td><div class="cell-name"><span class="fmt-tag">${esc(sourceExt(item.name))}</span><span></span></div></td>
        <td class="num c-size"></td>
        <td class="num"><div class="out-cell"><span class="c-out">—</span></div></td>
        <td class="num ratio c-ratio">—</td>
        <td class="num c-dur">—</td>
        <td><span class="status pending">待处理</span></td>`;

    const nameSpan = tr.querySelector('.cell-name span:last-child');
    nameSpan.textContent = item.path;
    nameSpan.title = item.path;

    tr.querySelector('.c-size').textContent = fmtBytes(item.size);

    const check = tr.querySelector('.row-check');
    check.addEventListener('change', () => {
        if (check.checked) state.selected.add(item.id);
        else state.selected.delete(item.id);
        tr.classList.toggle('selected', check.checked);
        refresh();
    });

    tr.addEventListener('dblclick', () => {
        const r = item.results?.[0];
        AppService.ShowInFolder(r?.outPath || item.path);
    });

    const cells = {
        out: tr.querySelector('.c-out'),
        ratio: tr.querySelector('.c-ratio'),
        dur: tr.querySelector('.c-dur'),
        status: tr.querySelector('.status'),
    };
    state.rows.set(item.id, {tr, cells, item});
    return tr;
}

const STATUS_TEXT = {
    pending: '待处理',
    running: '转换中',
    ok: '完成',
    skip: '已跳过',
    error: '失败',
    canceled: '已取消',
};

function updateRow(id) {
    const entry = state.rows.get(id);
    if (!entry) return;
    const {cells, item} = entry;

    const results = item.results || [];
    if (results.length === 0) {
        cells.out.textContent = '—';
        cells.ratio.textContent = '—';
        cells.ratio.className = 'num ratio';
        cells.dur.textContent = '—';
    } else {
        const outs = results.filter((r) => r.status === 'ok');
        if (outs.length) {
            cells.out.textContent = fmtBytes(item.outSize);
        } else {
            cells.out.textContent = '—';
        }
        const pct = item.ratio;
        cells.ratio.textContent = fmtRatio(pct);
        cells.ratio.className = 'num ratio ' + (pct >= 0 ? 'good' : 'bad');
        cells.dur.textContent = fmtDuration(item.duration);
    }

    cells.status.className = 'status ' + item.status;
    cells.status.textContent = STATUS_TEXT[item.status] || item.status;
    entry.tr.title = item.error || '';
}

function removeRows(ids) {
    const set = new Set(ids);
    state.items = state.items.filter((it) => !set.has(it.id));
    for (const id of ids) {
        const entry = state.rows.get(id);
        if (entry) entry.tr.remove();
        state.rows.delete(id);
        state.selected.delete(id);
    }
    el.checkAll.checked = false;
    refresh();
}

function clearRows() {
    state.items = [];
    state.rows.clear();
    state.selected.clear();
    el.rows.innerHTML = '';
    el.checkAll.checked = false;
    AppService.ClearFiles();
    refresh();
}

function refresh() {
    const n = state.items.length;
    el.fileCount.textContent = String(n);
    el.emptyState.hidden = n > 0;
    const bytes = state.items.reduce((a, b) => a + (b.size || 0), 0);
    el.sizeTotal.textContent = n ? `共 ${fmtBytes(bytes)}` : '';
    el.btnClear.disabled = n === 0 || state.running;
    el.btnRemove.disabled = state.selected.size === 0 || state.running;
    refreshStartButton();
}

function refreshStartButton() {
    el.btnStart.disabled = state.running || state.items.length === 0 || !selectedFormat()
        || !state.info || !!state.info.engineError;
}

// ── 进度 ─────────────────────────────────────────────────

function setRunning(running) {
    state.running = running;
    el.btnStart.disabled = running || state.items.length === 0;
    el.btnCancel.disabled = !running;
    el.btnClear.disabled = running || state.items.length === 0;
    el.btnRemove.disabled = running || state.selected.size === 0;
    el.btnAddFiles.disabled = running;
    el.btnAddFolder.disabled = running;
    el.btnStart.innerHTML = running
        ? '<span class="spin"></span>转换中…'
        : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><polygon points="6 4 20 12 6 20 6 4"/></svg>开始转换';
}

function setStats(text) {
    el.stats.innerHTML = text;
}

function updateProgress(p) {
    const pct = p.total ? Math.min(100, (p.done / p.total) * 100) : 0;
    el.progressBar.style.width = pct.toFixed(1) + '%';
    if (!p.running) return;
    const parts = [
        `已完成 <b>${p.done}</b> / ${p.total}`,
        `成功 <b>${p.ok}</b>`,
        `跳过 <b>${p.skip}</b>`,
        `失败 <b>${p.fail}</b>`,
        `耗时 <b>${fmtDuration(p.elapsedMs)}</b>`,
    ];
    setStats(parts.join('<span class="sep">|</span>'));
}

// ── 事件绑定 ─────────────────────────────────────────────

function bindEvents() {
    // 窗口控制
    el.btnMin.addEventListener('click', AppService.WindowMinimise);
    el.btnMax.addEventListener('click', AppService.WindowToggleMaximise);
    el.btnClose.addEventListener('click', AppService.WindowClose);

    // 添加文件
    el.btnAddFiles.addEventListener('click', async () => {
        const files = await AppService.SelectFiles();
        if (files && files.length) addItems(await AppService.AddPaths(files));
    });
    el.btnAddFolder.addEventListener('click', async () => {
        const dir = await AppService.SelectFolder();
        if (dir) addItems(await AppService.AddPaths([dir]));
    });

    // 列表操作
    el.btnRemove.addEventListener('click', async () => {
        const ids = [...state.selected];
        if (!ids.length) return;
        await AppService.RemoveFiles(ids);
        removeRows(ids);
    });
    el.btnClear.addEventListener('click', clearRows);
    el.checkAll.addEventListener('change', () => {
        const on = el.checkAll.checked;
        state.selected.clear();
        state.rows.forEach(({tr}, id) => {
            tr.querySelector('.row-check').checked = on;
            tr.classList.toggle('selected', on);
            if (on) state.selected.add(id);
        });
        refresh();
    });

    // 输出目录
    el.outMode.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-mode]');
        if (!btn) return;
        state.settings.outputMode = btn.dataset.mode;
        applySettings(state.settings);
        persist();
    });
    el.btnOutDir.addEventListener('click', async () => {
        const dir = await AppService.SelectOutputDir();
        if (!dir) return;
        state.settings.customDir = dir;
        state.settings.outputMode = 'custom';
        applySettings(state.settings);
        persist();
    });
    el.outDirWrap.addEventListener('dblclick', () => {
        if (state.settings.outputMode === 'custom' && state.settings.customDir) {
            AppService.OpenPath(state.settings.customDir);
        }
    });
    el.outOverwrite.addEventListener('change', () => {
        state.settings.overwrite = el.outOverwrite.checked;
        persist();
    });

    // 质量
    el.quality.addEventListener('input', () => {
        syncQuality();
        state.settings.quality = Number(el.quality.value);
        persist();
    });

    // 输出尺寸
    el.sizeMode.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-mode]');
        if (!btn) return;
        state.settings.resizeMode = btn.dataset.mode;
        el.sizeMode.querySelectorAll('button').forEach((b) =>
            b.classList.toggle('active', b === btn));
        syncSize();
        persist();
    });
    el.sizeValue.addEventListener('input', () => {
        const v = parseInt(el.sizeValue.value, 10);
        state.settings.resizeValue = isFinite(v) && v > 0 ? v : 0;
        syncSize();
        persist();
    });

    // 转换
    el.btnStart.addEventListener('click', start);
    el.btnCancel.addEventListener('click', async () => {
        await AppService.Cancel();
        setStats('正在停止…');
    });

    // 拖放（Wails 原生拖放 + HTML5 兜底）
    ['dragenter', 'dragover'].forEach((t) =>
        el.listPanel.addEventListener(t, (e) => {
            e.preventDefault();
            el.listPanel.classList.add('is-over');
        }));
    ['dragleave'].forEach((t) =>
        el.listPanel.addEventListener(t, (e) => {
            if (!el.listPanel.contains(e.relatedTarget)) el.listPanel.classList.remove('is-over');
        }));
    el.listPanel.addEventListener('dragend', () => el.listPanel.classList.remove('is-over'));
    el.listPanel.addEventListener('drop', async (e) => {
        e.preventDefault();
        el.listPanel.classList.remove('is-over');
        const paths = [...(e.dataTransfer?.files || [])].map((f) => f.path || f.name).filter(Boolean);
        if (paths.length) addItems(await AppService.AddPaths(paths));
    });

    // 后端事件
    Events.On('files:added', (ev) => addItems(ev.data));
    Events.On('window:maximise', (ev) => syncMaxState(ev.data));
    Events.On('convert:result', (ev) => {
        const r = ev.data;
        const entry = state.rows.get(r.jobId);
        if (!entry) return;
        if (!entry.item.results) entry.item.results = [];
        entry.item.results.push({
            format: r.format, status: r.status, outPath: r.outPath,
            outSize: r.outBytes, ratio: r.inBytes ? (1 - r.outBytes / r.inBytes) * 100 : 0,
            duration: r.durationMs, error: r.error,
        });
        entry.item.outSize = (entry.item.outSize || 0) + r.outBytes;
        entry.item.duration = (entry.item.duration || 0) + r.durationMs;
        entry.item.ratio = entry.item.size > 0
            ? (1 - entry.item.outSize / (entry.item.size * entry.item.results.length)) * 100
            : 0;
        if (r.status === 'error') {
            entry.item.status = 'error';
            entry.item.error = r.error;
        } else if (entry.item.status !== 'error') {
            entry.item.status = r.status;
            if (entry.item.status === 'pending') entry.item.status = 'running';
        }
        updateRow(r.jobId);
    });
    Events.On('convert:progress', (ev) => updateProgress(ev.data));
    Events.On('convert:error', (ev) => toast(ev.data.message, true));
    Events.On('convert:done', (ev) => {
        const s = ev.data;
        setRunning(false);
        el.progressBar.style.width = '100%';
        const saved = s.inBytes - s.outBytes;
        const savedText = fmtBytes(saved);
        const savedPct = s.inBytes > 0 ? (saved * 100 / s.inBytes).toFixed(1) + '%' : '0%';
        setStats(s.canceled
            ? `已取消 · 成功 <b>${s.ok}</b> · 失败 <b>${s.fail}</b> · 耗时 <b>${fmtDuration(s.elapsedMs)}</b>`
            : `完成 · 成功 <b>${s.ok}</b> · 跳过 <b>${s.skip}</b> · 失败 <b>${s.fail}</b> · ${fmtBytes(s.inBytes)} → <b>${fmtBytes(s.outBytes)}</b>（省 ${savedText}，压缩率 ${savedPct}）· 耗时 <b>${fmtDuration(s.elapsedMs)}</b>`);
        state.rows.forEach(({item}, id) => {
            // 取消时来不及上报的行标为“已取消”，正常收尾才兜底标“完成”
            if (item.status === 'running') item.status = s.canceled ? 'canceled' : 'ok';
            updateRow(id);
        });
        if (s.fail > 0) toast(`完成，${s.fail} 个文件转换失败`, true);
        else if (!s.canceled) toast(`转换完成，共 ${s.ok} 个文件`);
    });
    Events.On('settings:changed', (ev) => {
        state.settings = ev.data;
        applySettings(state.settings);
        refresh();
    });
}

// ── 转换 ─────────────────────────────────────────────────

async function start() {
    const st = state.settings;
    const cfg = new ConvertConfig({
        format: selectedFormat(),
        quality: Number(el.quality.value),
        resizeMode: currentResizeMode(),
        resizeValue: parseInt(el.sizeValue.value, 10) || 0,
        overwrite: !!st.overwrite,
        workers: st.workers || 0,
        outDir: st.outputMode === 'custom' ? (st.customDir || '') : '',
        writeLog: !!st.writeLog,
        logPath: st.logPath || '',
    });

    try {
        await AppService.Start(cfg);
    } catch (err) {
        toast(String(err?.message || err), true);
        return;
    }
    state.rows.forEach(({item}, id) => {
        item.status = 'running';
        item.results = [];
        item.outSize = 0;
        item.duration = 0;
        item.error = '';
        updateRow(id);
    });
    el.progressBar.style.width = '0%';
    setRunning(true);
    setStats('正在转换…');
}

let persistTimer;
function persist() {
    clearTimeout(persistTimer);
    persistTimer = setTimeout(async () => {
        const st = new Settings(state.settings);
        try {
            await AppService.SaveSettings(st);
        } catch (err) {
            console.error(err);
        }
    }, 250);
}

init().catch((err) => {
    console.error(err);
    toast('初始化失败: ' + (err?.message || err), true);
});
