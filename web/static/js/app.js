/**
 * Moovie 前端脚本
 * - 观影历史 localStorage 管理
 * - 登录用户定期同步到服务器
 */

// ==================== 观影历史管理 ====================

const HISTORY_KEY = 'moovie_watchHistory';
const SYNC_KEY = 'moovie_lastSyncAt';
const MAX_HISTORY = 100;
const SYNC_INTERVAL = 5 * 60 * 1000; // 5 分钟

/**
 * 获取观影历史
 */
function getWatchHistory() {
    try {
        return JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]');
    } catch {
        return [];
    }
}

/**
 * 保存观影历史
 */
function saveWatchHistory(history) {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(history.slice(0, MAX_HISTORY)));
}

/**
 * 记录观影
 * @param {Object} item - { doubanId, title, poster, episode, progress, source }
 */
function recordWatch(item) {
    if (!item.doubanId) return;

    const history = getWatchHistory();
    const now = Date.now();

    // 查找是否已存在同一剧集
    const idx = history.findIndex(h =>
        h.doubanId === item.doubanId && h.episode === item.episode
    );

    const record = {
        ...item,
        watchedAt: now
    };

    if (idx >= 0) {
        // 更新已有记录
        history[idx] = { ...history[idx], ...record };
        // 移到最前面
        const updated = history.splice(idx, 1)[0];
        history.unshift(updated);
    } else {
        // 添加新记录
        history.unshift(record);
    }

    saveWatchHistory(history);
    scheduleSync();
}

/**
 * 更新播放进度
 */
function updateProgress(doubanId, episode, progress) {
    const history = getWatchHistory();
    const record = history.find(h => h.doubanId === doubanId && h.episode === episode);
    if (record) {
        record.progress = progress;
        record.watchedAt = Date.now();
        saveWatchHistory(history);
    }
}

// ==================== 同步逻辑 ====================

let syncTimer = null;

/**
 * 检查是否登录
 */
function isLoggedIn() {
    // 检查是否有登录态（通过检测页面元素或 cookie）
    return document.querySelector('[href="/dashboard"]') !== null;
}

/**
 * 调度同步任务
 */
function scheduleSync() {
    if (!isLoggedIn() || syncTimer) return;

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
    if (!isLoggedIn()) return;

    const history = getWatchHistory();
    const lastSyncAt = parseInt(localStorage.getItem(SYNC_KEY) || '0');

    // 只同步上次同步后的新记录
    const newRecords = history.filter(h => h.watchedAt > lastSyncAt);
    if (newRecords.length === 0) return;

    try {
        const response = await fetch('/api/history/sync', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                records: newRecords,
                lastSyncAt: lastSyncAt
            })
        });

        if (response.ok) {
            const data = await response.json();

            // 合并服务器返回的记录
            if (data.serverRecords && data.serverRecords.length > 0) {
                mergeServerRecords(data.serverRecords);
            }

            // 更新同步时间
            localStorage.setItem(SYNC_KEY, Date.now().toString());
        }
    } catch (error) {
        console.error('同步观影历史失败:', error);
    }
}

/**
 * 合并服务器记录到本地
 */
function mergeServerRecords(serverRecords) {
    const localHistory = getWatchHistory();

    serverRecords.forEach(serverRecord => {
        const localIdx = localHistory.findIndex(h =>
            h.doubanId === serverRecord.douban_id && h.episode === serverRecord.episode
        );

        if (localIdx >= 0) {
            // 保留较新的记录
            const localTime = localHistory[localIdx].watchedAt;
            const serverTime = new Date(serverRecord.watched_at).getTime();
            if (serverTime > localTime) {
                localHistory[localIdx] = {
                    doubanId: serverRecord.douban_id,
                    title: serverRecord.title,
                    poster: serverRecord.poster,
                    episode: serverRecord.episode,
                    progress: serverRecord.progress,
                    source: serverRecord.source,
                    watchedAt: serverTime
                };
            }
        } else {
            // 添加服务器记录
            localHistory.push({
                doubanId: serverRecord.douban_id,
                title: serverRecord.title,
                poster: serverRecord.poster,
                episode: serverRecord.episode,
                progress: serverRecord.progress,
                source: serverRecord.source,
                watchedAt: new Date(serverRecord.watched_at).getTime()
            });
        }
    });

    // 按时间排序
    localHistory.sort((a, b) => b.watchedAt - a.watchedAt);
    saveWatchHistory(localHistory);
}

// ==================== 首页继续观看 ====================

/**
 * 渲染继续观看列表
 */
function renderContinueWatching() {
    const container = document.getElementById('continue-watching');
    if (!container) return;

    const history = getWatchHistory().slice(0, 6);

    if (history.length === 0) {
        container.innerHTML = '<p class="empty-state">暂无观看记录</p>';
        return;
    }

    container.innerHTML = history.map(item => `
        <a href="/play/${item.doubanId}?source=${item.source || ''}&ep=${item.episode || ''}" class="movie-card">
            <div class="movie-poster">
                <img src="${item.poster || '/static/img/placeholder.jpg'}" alt="${item.title}" loading="lazy">
            </div>
            <div class="movie-info">
                <h3 class="movie-title">${item.title}</h3>
                <p class="movie-year">${item.episode || '继续观看'}</p>
            </div>
        </a>
    `).join('');
}

// ==================== 最近搜索管理 ====================

const RECENT_SEARCHES_KEY = 'moovie_recentSearches';
const MAX_RECENT_SEARCHES = 10;

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

    container.innerHTML = searches.map(keyword => `
        <span class="tag tag-deletable">
            <a href="/search?q=${encodeURIComponent(keyword)}">${keyword}</a>
            <button class="tag-delete" onclick="event.preventDefault(); removeRecentSearch('${keyword.replace(/'/g, "\\'")}')">×</button>
        </span>
    `).join('');
}

// ==================== 初始化 ====================

document.addEventListener('DOMContentLoaded', function() {
    // 渲染首页继续观看
    renderContinueWatching();

    // 渲染最近搜索
    renderRecentSearches();

    // 如果当前是搜索结果页，记录搜索词
    const urlParams = new URLSearchParams(window.location.search);
    const searchQuery = urlParams.get('q');
    if (searchQuery && window.location.pathname === '/search') {
        addRecentSearch(searchQuery);
    }

    // 登录后首次进入尝试同步
    if (isLoggedIn()) {
        scheduleSync();
    }

    // 监听搜索表单提交，记录搜索历史
    const searchForm = document.querySelector('.search-form');
    if (searchForm) {
        searchForm.addEventListener('submit', function(e) {
            const input = this.querySelector('input[name="q"]');
            if (input && input.value) {
                addRecentSearch(input.value);
            }
        });
    }
});

// 页面关闭前尝试同步（仅提示，不阻塞）
window.addEventListener('beforeunload', function() {
    if (isLoggedIn() && getWatchHistory().length > 0) {
        // 使用 sendBeacon 发送最后的同步请求
        const history = getWatchHistory();
        const lastSyncAt = parseInt(localStorage.getItem(SYNC_KEY) || '0');
        const newRecords = history.filter(h => h.watchedAt > lastSyncAt);

        if (newRecords.length > 0) {
            navigator.sendBeacon('/api/history/sync', JSON.stringify({
                records: newRecords,
                lastSyncAt: lastSyncAt
            }));
        }
    }
});
