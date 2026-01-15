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
                <img src="${item.poster || '/static/img/placeholder.jpg'}" alt="${item.title}" loading="lazy" onerror="this.onerror=null;this.src='/static/img/placeholder.jpg'">
            </div>
            <div class="movie-info">
                <h3 class="movie-title">${item.title}</h3>
                <p class="movie-year">${item.episode || '继续观看'}</p>
            </div>
        </a>
    `).join('');
}

// ==================== 搜索建议 ====================

let searchTimeout = null;
let selectedSuggestionIndex = -1;

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
    console.log('[搜索建议] 开始获取:', keyword);
    try {
        const response = await fetch(`/api/movies/suggest?q=${encodeURIComponent(keyword)}`);
        console.log('[搜索建议] API响应状态:', response.status);

        if (!response.ok) {
            throw new Error('搜索服务暂时不可用');
        }

        const result = await response.json();
        console.log('[搜索建议] API返回数据:', result);

        if (result.data && result.data.length > 0) {
            renderSuggestions(result.data);
        } else {
            console.log('[搜索建议] 无数据，隐藏下拉框');
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
    console.log('[搜索建议] 开始渲染，数量:', suggestions.length);
    const container = document.getElementById('search-suggestions');
    if (!container) {
        console.error('[搜索建议] 容器未找到');
        return;
    }

    selectedSuggestionIndex = -1;

    // 根据 API 返回的字段: id, title, sub_title, type, year, img
    container.innerHTML = suggestions.map((item, index) => {
        // 转换 type 显示
        let typeText = '其他';
        if (item.type === 'movie') typeText = '电影';
        else if (item.type === 'tv') typeText = '剧集';

        // 安全处理 title 中的引号
        const safeTitle = (item.title || '').replace(/'/g, "\\'").replace(/"/g, '&quot;');

        return `
            <div class="search-suggestion-item"
                 data-index="${index}"
                 data-id="${item.id}"
                 onclick="selectSuggestion('${item.id}', '${safeTitle}')">
                <img src="${item.img || '/static/img/placeholder.jpg'}"
                     alt="${safeTitle}"
                     class="suggestion-poster"
                     onerror="this.onerror=null;this.src='/static/img/placeholder.jpg'">
                <div class="suggestion-info">
                    <div class="suggestion-title">${item.title || ''}</div>
                    <div class="suggestion-meta">
                        <span class="suggestion-type">${typeText}</span>
                        ${item.year ? `<span class="suggestion-year">${item.year}</span>` : ''}
                    </div>
                    ${item.sub_title ? `<div class="suggestion-subtitle">${item.sub_title}</div>` : ''}
                </div>
            </div>
        `;
    }).join('');

    container.style.display = 'block';
    console.log('[搜索建议] 渲染完成，已显示');
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
 * 选择搜索建议
 * 调用 API 检查电影是否存在，决定跳转到详情页还是搜索页
 */
async function selectSuggestion(doubanId, title) {
    const searchInput = document.getElementById('search-input');
    if (searchInput) {
        searchInput.value = title;
    }
    hideSuggestions();

    try {
        // 调用检查 API
        const response = await fetch(`/api/movies/check/${doubanId}?title=${encodeURIComponent(title)}`);
        if (!response.ok) {
            throw new Error('API 请求失败');
        }

        const result = await response.json();
        console.log('[搜索建议] 检查结果:', result);

        if (result.data && result.data.redirect_url) {
            // 跳转到 API 返回的地址
            window.location.href = result.data.redirect_url;
        } else {
            // 备用：跳转到搜索页
            window.location.href = `/search?q=${encodeURIComponent(title)}`;
        }
    } catch (error) {
        console.error('[搜索建议] 检查失败:', error);
        // 出错时直接跳转到搜索页
        window.location.href = `/search?q=${encodeURIComponent(title)}`;
    }
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

    // 监听搜索输入框键盘事件
    const searchInput = document.getElementById('search-input');
    if (searchInput) {
        searchInput.addEventListener('keydown', handleSuggestionNavigation);
    }

    // 点击外部区域关闭搜索建议
    document.addEventListener('click', function(e) {
        const searchContainer = document.querySelector('.search-form');
        const suggestions = document.getElementById('search-suggestions');
        if (searchContainer && !searchContainer.contains(e.target)) {
            hideSuggestions();
        }
    });

    // 防止鼠标进入搜索建议区域时关闭下拉框
    const suggestionsContainer = document.getElementById('search-suggestions');
    if (suggestionsContainer) {
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
