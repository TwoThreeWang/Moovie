/**
 * Moovie 前端脚本
 * - 观影历史 localStorage 管理
 * - 登录用户定期同步到服务器
 */

// ==================== 观影历史管理 ====================

var HISTORY_KEY = 'moovie_play_state'; // 统一使用该键；var 允许 HTMX 历史恢复安全重载脚本
var SYNC_KEY = 'moovie_lastSyncAt';
var SYNC_CURSOR_KEY = 'moovie_history_cursor_v2';
var SYNC_OUTBOX_KEY = 'moovie_history_outbox_v2';
var SYNC_MIGRATED_KEY = 'moovie_history_migrated_v2';
var DEVICE_ID_KEY = 'moovie_history_device_id';
var DEVICE_SEQ_KEY = 'moovie_history_device_seq';
var MAX_HISTORY = 100;
var SYNC_INTERVAL = 1 * 60 * 1000; // 1 分钟

/**
 * 获取观影历史
 */
function getWatchHistory() {
    try {
        const data = JSON.parse(localStorage.getItem(HISTORY_KEY) || '{}');
        // 存储层使用对象便于按资源键覆盖，页面层统一转换为按时间排序的数组。
        return Object.values(data).sort((a, b) => (b.watchedAt || b.updatedAt || 0) - (a.watchedAt || a.updatedAt || 0));
    } catch {
        return [];
    }
}

/**
 * 保存观影历史
 */
// 内部保存辅助
function _saveWatchHistoryInternal(data) {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(data));
}

/**
 * 保存观影历史（从数组转换回存储对象）
 */
function saveWatchHistory(historyArray) {
    // 按时间降序排序，保留最近 MAX_HISTORY 条，防止 localStorage 无限增长
    historyArray.sort((a, b) => (b.updatedAt || b.watchedAt || 0) - (a.updatedAt || a.watchedAt || 0));
    const trimmed = historyArray.slice(0, MAX_HISTORY);

    const data = {};
    trimmed.forEach(h => {
        const source = h.source || h.source_key || '';
        const vodId = h.vod_id || h.vodId || '';
        const key = source + vodId;
        if (key) {
            data[key] = {
                ...h,
                source_key: source,
                vod_id: vodId,
				douban_id: h.douban_id || h.doubanId || '', // 确保 douban_id 存在
				entry_page: h.entry_page === 'watch' ? 'watch' : 'play',
				img: h.poster || h.img || ''
            };
        }
    });
    _saveWatchHistoryInternal(data);
}



// ==================== 同步逻辑 ====================

var syncTimer = typeof syncTimer === 'undefined' ? null : syncTimer;
var isSyncing = typeof isSyncing === 'undefined' ? false : isSyncing;

/**
 * 检查是否登录
 */
function isLoggedIn() {
    // 检查是否有登录态（通过检测页面元素或 cookie）
    return document.querySelector('[href="/dashboard"]') !== null;
}

function historyUserScope() {
    const userId = document.body && document.body.dataset ? document.body.dataset.userId : '';
    return userId ? `user:${userId}` : 'anonymous';
}

function scopedHistoryKey(baseKey) {
    return `${baseKey}:${historyUserScope()}`;
}

function readJSONStorage(key, fallback) {
    try {
        return JSON.parse(localStorage.getItem(key) || JSON.stringify(fallback));
    } catch {
        return fallback;
    }
}

function createOperationId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
        return window.crypto.randomUUID();
    }
    return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

function getDeviceId() {
    let deviceId = localStorage.getItem(DEVICE_ID_KEY) || '';
    if (!deviceId) {
        deviceId = `browser-${createOperationId()}`;
        localStorage.setItem(DEVICE_ID_KEY, deviceId);
    }
    return deviceId;
}

function nextDeviceSequence() {
    const next = parseInt(localStorage.getItem(DEVICE_SEQ_KEY) || '0', 10) + 1;
    localStorage.setItem(DEVICE_SEQ_KEY, next.toString());
    return next;
}

function historyIdentityKey(item) {
    const mediaUnitId = item.media_unit_id || item.mediaUnitId || 0;
    if (mediaUnitId) return `unit:${mediaUnitId}`;
    const mediaId = item.media_id || item.mediaId || 0;
    const season = item.season_number || item.seasonNumber || 1;
    const episodeKey = item.episode_key || item.episodeKey || item.episode || '';
    if (mediaId && episodeKey) return `media:${mediaId}:${season}:${episodeKey}`;
    const source = item.source_key || item.source || '';
    const vodId = item.vod_id || item.vodId || '';
    if (source && vodId) return `resource:${source}:${vodId}`;
    const historyId = Number(item.id || 0);
    return historyId > 0 ? `history:${historyId}` : '';
}

function queueHistoryOperation(type, item, shouldSchedule = true) {
    if (!item || !isLoggedIn()) return;
    const source = item.source_key || item.source || '';
    if (source === 'iptv' || source === 'manual') return;
    const identity = historyIdentityKey(item);
    if (!identity) return;
    const outboxKey = scopedHistoryKey(SYNC_OUTBOX_KEY);
    const outbox = readJSONStorage(outboxKey, {});
    const historyId = Number(item.id || 0);
    const occurredAt = Number(item.updatedAt || item.watchedAt || Date.now());
    outbox[identity] = {
        operation_id: createOperationId(),
        device_seq: nextDeviceSequence(),
        type: type,
        history_id: historyId > 0 ? historyId : 0,
        media_id: item.media_id || item.mediaId || 0,
        media_unit_id: item.media_unit_id || item.mediaUnitId || 0,
        douban_id: item.douban_id || item.doubanId || '',
        source_key: source,
        vod_id: item.vod_id || item.vodId || '',
        title: item.title || '',
        poster: item.poster || item.img || '',
        episode: item.episode || '',
        season_number: item.season_number || item.seasonNumber || 1,
        episode_key: item.episode_key || item.episodeKey || '',
        position_seconds: item.lastTime || item.last_time || 0,
        duration_seconds: item.duration || 0,
		progress_percent: item.progress || 0,
		entry_page: item.entry_page === 'watch' ? 'watch' : 'play',
		occurred_at: new Date(Number.isFinite(occurredAt) ? occurredAt : Date.now()).toISOString()
    };
    localStorage.setItem(outboxKey, JSON.stringify(outbox));
    if (shouldSchedule) scheduleSync();
}

function ensureInitialHistoryOutbox() {
    const migratedKey = scopedHistoryKey(SYNC_MIGRATED_KEY);
    if (localStorage.getItem(migratedKey) === 'true') return;
    getWatchHistory().forEach(item => queueHistoryOperation('upsert', item, false));
    localStorage.setItem(migratedKey, 'true');
}

window.queueHistoryUpsert = item => queueHistoryOperation('upsert', item);
window.queueHistoryDelete = item => queueHistoryOperation('delete', item);

/**
 * 调度同步任务
 */
function scheduleSync() {
    if (!isLoggedIn() || isSyncing || syncTimer) return;

    const lastSync = parseInt(localStorage.getItem(SYNC_KEY) || '0');
    const elapsed = Date.now() - lastSync;

    if (elapsed >= SYNC_INTERVAL) {
        doSync();
    } else {
        syncTimer = setTimeout(() => {
            syncTimer = null;
            doSync();
        }, SYNC_INTERVAL - elapsed);
    }
}

/**
 * 执行同步
 */
async function doSync() {
    if (!isLoggedIn() || isSyncing) return;
    isSyncing = true;

	try {
		return await syncHistoryV2();
    } catch (error) {
        console.error('同步观影历史失败:', error);
    } finally {
        isSyncing = false;
    }
    return false;
}

async function syncHistoryV2() {
    ensureInitialHistoryOutbox();
    const cursorKey = scopedHistoryKey(SYNC_CURSOR_KEY);
    const outboxKey = scopedHistoryKey(SYNC_OUTBOX_KEY);
    const cursor = parseInt(localStorage.getItem(cursorKey) || '0', 10);
    const outbox = readJSONStorage(outboxKey, {});
    const sentOperations = Object.values(outbox).slice(0, 100);
    const response = await fetch('/api/v2/history/sync', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ device_id: getDeviceId(), cursor: cursor, operations: sentOperations })
    });
	if (!response.ok) throw new Error(`v2 history sync returned ${response.status}`);

    const result = await response.json();
    applyHistoryChanges(result.changes || []);
    (result.conflicts || []).forEach(conflict => {
        if (conflict.current) mergeServerRecords([conflict.current], false);
    });

    const latestOutbox = readJSONStorage(outboxKey, {});
    sentOperations.forEach(operation => {
        Object.keys(latestOutbox).forEach(key => {
            if (latestOutbox[key] && latestOutbox[key].operation_id === operation.operation_id) {
                delete latestOutbox[key];
            }
        });
    });
    localStorage.setItem(outboxKey, JSON.stringify(latestOutbox));
    localStorage.setItem(cursorKey, String(result.cursor || cursor));
    localStorage.setItem(SYNC_KEY, Date.now().toString());
    document.dispatchEvent(new CustomEvent('moovie:history-updated'));
    if (Object.keys(latestOutbox).length > 0) setTimeout(scheduleSync, 0);
    return true;
}

function recordsMatch(left, right) {
    const leftUnit = left.media_unit_id || left.mediaUnitId || 0;
    const rightUnit = right.media_unit_id || right.mediaUnitId || 0;
    if (leftUnit && rightUnit && leftUnit === rightUnit) return true;
    const leftMedia = left.media_id || left.mediaId || 0;
    const rightMedia = right.media_id || right.mediaId || 0;
    const leftEpisode = left.episode_key || left.episodeKey || left.episode || '';
    const rightEpisode = right.episode_key || right.episodeKey || right.episode || '';
    if (leftMedia && rightMedia && leftMedia === rightMedia && leftEpisode === rightEpisode) return true;
    const leftSource = left.source_key || left.source || '';
    const rightSource = right.source_key || right.source || '';
    const leftVod = left.vod_id || left.vodId || '';
    const rightVod = right.vod_id || right.vodId || '';
    if (leftSource && leftVod && leftSource === rightSource && leftVod === rightVod) return true;
    return !!(left.douban_id && left.douban_id === right.douban_id && (left.episode || '') === (right.episode || ''));
}

function mergeServerRecord(localHistory, serverRecord) {
    const recSource = serverRecord.source_key || serverRecord.source || '';
    const recVodId = serverRecord.vod_id || serverRecord.vodId || '';
    const localIdx = localHistory.findIndex(item => recordsMatch(item, serverRecord));
    const parsedTime = new Date(serverRecord.updated_at || serverRecord.watched_at).getTime();
    const serverTime = Number.isFinite(parsedTime) ? parsedTime : 0;
    const normalized = {
        id: serverRecord.id,
        media_id: serverRecord.media_id || 0,
        media_unit_id: serverRecord.media_unit_id || 0,
        season_number: serverRecord.season_number || 1,
		episode_key: serverRecord.episode_key || '',
		entry_page: serverRecord.entry_page === 'watch' ? 'watch' : 'play',
		douban_id: serverRecord.douban_id || '',
        title: serverRecord.title || '',
        poster: serverRecord.poster || '',
        img: serverRecord.poster || '',
        episode: serverRecord.episode || '',
        progress: serverRecord.progress || 0,
        source_key: recSource,
        vod_id: recVodId,
        watchedAt: serverTime,
        updatedAt: serverTime,
        lastTime: Number.isFinite(serverRecord.last_time) ? serverRecord.last_time : 0,
        duration: Number.isFinite(serverRecord.duration) ? serverRecord.duration : 0
    };
    if (localIdx < 0) {
        localHistory.push(normalized);
        return;
    }
    const localTime = localHistory[localIdx].updatedAt || localHistory[localIdx].watchedAt || 0;
    if (serverTime >= localTime) localHistory[localIdx] = { ...localHistory[localIdx], ...normalized };
}

function applyHistoryChanges(changes) {
    let localHistory = getWatchHistory();
    changes.forEach(change => {
        if (!change || !change.record) return;
        if (change.type === 'delete') {
            localHistory = localHistory.filter(item => !recordsMatch(item, change.record));
        } else {
            mergeServerRecord(localHistory, change.record);
        }
    });
    saveWatchHistory(localHistory);
}

/**
 * 合并服务器记录到本地
 */
function mergeServerRecords(serverRecords, notify = true) {
    const localHistory = getWatchHistory();
    serverRecords.forEach(serverRecord => mergeServerRecord(localHistory, serverRecord));
    saveWatchHistory(localHistory);
    if (notify) document.dispatchEvent(new CustomEvent('moovie:history-updated'));
}

// ==================== 搜索建议 ====================

var searchTimeout = typeof searchTimeout === 'undefined' ? null : searchTimeout;
var selectedSuggestionIndex = typeof selectedSuggestionIndex === 'undefined' ? -1 : selectedSuggestionIndex;

/**
 * 处理搜索输入
 */
function handleSearchInput(value) {
    clearTimeout(searchTimeout);

    if (!value || value.trim().length < 1) {
        hideSuggestions();
        return;
    }

    // 防抖，300ms后执行搜索
    searchTimeout = setTimeout(() => {
        fetchSuggestions(value.trim());
    }, 300);
}

/**
 * 获取搜索建议
 */
async function fetchSuggestions(keyword) {
    try {
        const response = await fetch(`/api/v2/media/suggest?q=${encodeURIComponent(keyword)}`);

        if (!response.ok) {
            throw new Error('搜索服务暂时不可用');
        }

        const result = await response.json();

        if (result.data && result.data.length > 0) {
            renderSuggestions(result.data);
        } else {
            hideSuggestions();
        }
    } catch (error) {
        console.error('[搜索建议] 获取失败:', error);
        hideSuggestions();
    }
}

/**
 * 渲染搜索建议
 */
function renderSuggestions(suggestions) {
    const container = document.getElementById('search-suggestions');
    if (!container) {
        console.error('[搜索建议] 容器未找到');
        return;
    }

    selectedSuggestionIndex = -1;

    const rows = suggestions.map((item, index) => {
        // 转换 type 显示
        let typeText = '其他';
        if (item.type === 'movie') typeText = '电影';
        else if (item.type === 'tv') typeText = '剧集';

        // 构建搜索链接
        const searchUrl = `/search?kw=${encodeURIComponent(item.title || '')}&doubanId=${encodeURIComponent(item.id)}`;

        let imgSrc = '/static/img/placeholder.svg';
        if (item.img) {
            if (item.img.startsWith('/api/proxy/image')) {
                imgSrc = item.img;
            } else {
                try {
                    imgSrc = '/api/proxy/image/r76RqSIVvUryzx' + btoa(item.img);
                } catch (_) {
                    imgSrc = '/static/img/placeholder.svg';
                }
            }
        }

        const row = document.createElement('a');
        row.href = searchUrl;
        row.className = 'search-suggestion-item';
        row.dataset.index = String(index);

        const image = document.createElement('img');
        image.src = imgSrc;
        image.alt = item.title || '';
        image.className = 'suggestion-poster';
        image.loading = 'lazy';
        image.onerror = function() {
            this.onerror = null;
            this.src = '/static/img/placeholder.svg';
        };

        const info = document.createElement('div');
        info.className = 'suggestion-info';
        const title = document.createElement('div');
        title.className = 'suggestion-title';
        title.textContent = item.title || '';
        const meta = document.createElement('div');
        meta.className = 'suggestion-meta';
        const type = document.createElement('span');
        type.className = 'suggestion-type';
        type.textContent = typeText;
        meta.appendChild(type);
        if (item.year) {
            const year = document.createElement('span');
            year.className = 'suggestion-year';
            year.textContent = String(item.year);
            meta.appendChild(year);
        }
        info.append(title, meta);
        if (item.sub_title) {
            const subtitle = document.createElement('div');
            subtitle.className = 'suggestion-subtitle';
            subtitle.textContent = item.sub_title;
            info.appendChild(subtitle);
        }
        row.append(image, info);
        return row;
    });
    container.replaceChildren(...rows);

    container.style.display = 'block';
}

/**
 * 隐藏搜索建议
 */
function hideSuggestions() {
    const container = document.getElementById('search-suggestions');
    if (container) {
        container.style.display = 'none';
        container.innerHTML = '';
    }
    selectedSuggestionIndex = -1;
}

/**
 * 键盘导航
 */
function handleSuggestionNavigation(event) {
    const container = document.getElementById('search-suggestions');
    if (!container || container.style.display === 'none') return;

    const items = container.querySelectorAll('.search-suggestion-item');
    if (items.length === 0) return;

    switch (event.key) {
        case 'ArrowDown':
            event.preventDefault();
            selectedSuggestionIndex = Math.min(selectedSuggestionIndex + 1, items.length - 1);
            updateSelectedSuggestion(items);
            break;
        case 'ArrowUp':
            event.preventDefault();
            selectedSuggestionIndex = Math.max(selectedSuggestionIndex - 1, -1);
            updateSelectedSuggestion(items);
            break;
        case 'Enter':
            event.preventDefault();
            if (selectedSuggestionIndex >= 0) {
                items[selectedSuggestionIndex].click();
            }
            break;
        case 'Escape':
            hideSuggestions();
            break;
    }
}

/**
 * 更新选中的建议项
 */
function updateSelectedSuggestion(items) {
    items.forEach((item, index) => {
        if (index === selectedSuggestionIndex) {
            item.classList.add('selected');
            item.scrollIntoView({ block: 'nearest' });
        } else {
            item.classList.remove('selected');
        }
    });
}

// ==================== 最近搜索管理 ====================

var RECENT_SEARCHES_KEY = 'moovie_recentSearches';
var MAX_RECENT_SEARCHES = 10;

/**
 * 获取最近搜索
 */
function getRecentSearches() {
    try {
        return JSON.parse(localStorage.getItem(RECENT_SEARCHES_KEY) || '[]');
    } catch {
        return [];
    }
}

/**
 * 保存最近搜索
 */
function saveRecentSearches(searches) {
    localStorage.setItem(RECENT_SEARCHES_KEY, JSON.stringify(searches.slice(0, MAX_RECENT_SEARCHES)));
}

/**
 * 添加搜索记录
 */
function addRecentSearch(keyword) {
    if (!keyword || !keyword.trim()) return;
    keyword = keyword.trim();

    const searches = getRecentSearches();
    // 移除已存在的相同关键词
    const idx = searches.indexOf(keyword);
    if (idx >= 0) {
        searches.splice(idx, 1);
    }
    // 添加到最前面
    searches.unshift(keyword);
    saveRecentSearches(searches);

    // 如果在首页，立即更新 UI
    if (typeof renderRecentSearches === 'function') {
        renderRecentSearches();
    }
}

/**
 * 删除单个搜索记录
 */
function removeRecentSearch(keyword) {
    const searches = getRecentSearches();
    const idx = searches.indexOf(keyword);
    if (idx >= 0) {
        searches.splice(idx, 1);
        saveRecentSearches(searches);
        renderRecentSearches();
    }
}

/**
 * 清空所有搜索记录
 */
function clearRecentSearches() {
    localStorage.removeItem(RECENT_SEARCHES_KEY);
    renderRecentSearches();
}

/**
 * 渲染最近搜索
 */
function renderRecentSearches() {
    const container = document.getElementById('recent-searches');
    const section = document.getElementById('recent-searches-section');
    if (!container) return;

    const searches = getRecentSearches();

    if (searches.length === 0) {
        if (section) section.style.display = 'none';
        return;
    }

    if (section) section.style.display = 'block';

    const tags = searches.map(keyword => {
        const tag = document.createElement('span');
        tag.className = 'tag tag-deletable';
        const link = document.createElement('a');
        link.href = `/search?kw=${encodeURIComponent(keyword)}`;
        link.textContent = keyword;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'tag-delete';
        remove.textContent = '×';
        remove.addEventListener('click', event => {
            event.preventDefault();
            removeRecentSearch(keyword);
        });
        tag.append(link, remove);
        return tag;
    });
    container.replaceChildren(...tags);
}

// ==================== 初始化 ====================

function initializeMoovieApp() {
    // 渲染首页继续观看
    if (typeof renderContinueWatching === 'function') {
        renderContinueWatching();
    }

    // 渲染最近搜索
    renderRecentSearches();

    // 登录后首次进入尝试同步
    if (isLoggedIn()) {
        scheduleSync();
    }

    // 监听搜索表单提交，记录搜索历史
    const searchForm = document.querySelector('.search-form');
    if (searchForm && searchForm.dataset.moovieAppBound !== 'true') {
        searchForm.dataset.moovieAppBound = 'true';
        searchForm.addEventListener('submit', function(e) {
            const input = this.querySelector('input[name="kw"]');
            if (input && input.value) {
                addRecentSearch(input.value);
            }
        });
    }

    // 监听搜索输入框键盘事件
    const searchInput = document.getElementById('search-input');
    if (searchInput && searchInput.dataset.moovieAppBound !== 'true') {
        searchInput.dataset.moovieAppBound = 'true';
        searchInput.addEventListener('keydown', handleSuggestionNavigation);
    }

    // 防止鼠标进入搜索建议区域时关闭下拉框
    const suggestionsContainer = document.getElementById('search-suggestions');
    if (suggestionsContainer && suggestionsContainer.dataset.moovieAppBound !== 'true') {
        suggestionsContainer.dataset.moovieAppBound = 'true';
        suggestionsContainer.addEventListener('mouseenter', function() {
            // 鼠标进入建议区域时，不清除建议
            clearTimeout(searchTimeout);
        });

        suggestionsContainer.addEventListener('mouseleave', function() {
            // 鼠标离开建议区域时，延迟隐藏建议
            setTimeout(() => {
                if (!suggestionsContainer.matches(':hover') && !document.getElementById('search-input').matches(':focus')) {
                    hideSuggestions();
                }
            }, 200);
        });
    }
}

function closeSuggestionsOutsideSearch(event) {
    const searchContainer = document.querySelector('.search-form');
    if (searchContainer && !searchContainer.contains(event.target)) {
        hideSuggestions();
    }
}

if (!window.__moovieAppGlobalListenersBound) {
    window.__moovieAppGlobalListenersBound = true;
    document.addEventListener('click', closeSuggestionsOutsideSearch);
    document.addEventListener('htmx:historyRestore', initializeMoovieApp);
    window.addEventListener('beforeunload', flushHistoryOutboxBeforeUnload);
}

if (document.readyState === 'loading') {
    if (!window.__moovieAppDOMContentLoadedPending) {
        window.__moovieAppDOMContentLoadedPending = true;
        document.addEventListener('DOMContentLoaded', function() {
            window.__moovieAppDOMContentLoadedPending = false;
            initializeMoovieApp();
        }, { once: true });
    }
} else {
    initializeMoovieApp();
}

// 页面关闭前发送尚未确认的 outbox。响应不会在卸载阶段处理；下次页面
// 会按相同 operation_id 重试，服务端幂等账本不会重复写入。
function flushHistoryOutboxBeforeUnload() {
    if (!isLoggedIn()) return;
    ensureInitialHistoryOutbox();
    const outbox = readJSONStorage(scopedHistoryKey(SYNC_OUTBOX_KEY), {});
    const operations = Object.values(outbox).slice(0, 100);
    if (operations.length === 0) return;
    const cursor = parseInt(localStorage.getItem(scopedHistoryKey(SYNC_CURSOR_KEY)) || '0', 10);
    fetch('/api/v2/history/sync', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        keepalive: true,
        body: JSON.stringify({ device_id: getDeviceId(), cursor: cursor, operations: operations })
    }).catch(() => {});
}

// 从老版本的历史记录转为新版本
function transform(list) {
  const result = {};

  list.forEach(item => {
    const key = item.source + item.vid;

    let name = item.title;
    let episode = '';

    if (item.title.includes('#')) {
      const parts = item.title.split('#');
      name = parts[0].trim();
      episode = parts[1].trim();
    }

    result[key] = {
      id: key,
      douban_id: "",              // 原数据里没有，先留空
      title: `${name} - ${episode}`,
      source_key: item.source,
      vod_id: item.vid,
      play: item.play,
      lastTime: item.lastTime,
      duration: item.duration,
      img: item.img,
      episode: episode,
      updatedAt: item.updatedAt
    };
  });

  return result;
}

(function migrateHistory() {
  const oldKey = 'moovie_history';
  const newKey = 'moovie_play_state';
  // 如果新数据已经存在，就不再迁移
  if (localStorage.getItem(newKey)) {
    return;
  }
  const oldData = localStorage.getItem(oldKey);
  if (!oldData) {
    return;
  }
  try {
    const parsed = JSON.parse(oldData);
    const newData = transform(parsed);
    localStorage.setItem(newKey, JSON.stringify(newData));
    localStorage.removeItem(oldKey);
    console.log('moovie_history 已成功迁移为 moovie_play_state');
  } catch (e) {
    console.error('迁移播放记录失败：', e);
  }
})();
