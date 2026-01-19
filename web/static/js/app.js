/**
 * Moovie 前端脚本
 * - 观影历史 localStorage 管理
 * - 登录用户定期同步到服务器
 */

// ==================== 观影历史管理 ====================

const HISTORY_KEY = 'moovie_play_state'; // 统一使用该键
const SYNC_KEY = 'moovie_lastSyncAt';
const MAX_HISTORY = 100;
const SYNC_INTERVAL = 1 * 60 * 1000; // 1 分钟

/**
 * 获取观影历史
 */
function getWatchHistory() {
    try {
        const data = JSON.parse(localStorage.getItem(HISTORY_KEY) || '{}');
        // 将对象转换为数组并排序，以便兼容旧的调用处
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
    const data = {};
    historyArray.forEach(h => {
        const source = h.source || h.source_key || '';
        const vodId = h.vod_id || h.vodId || '';
        const key = source + vodId;
        if (key) {
            data[key] = {
                ...h,
                source_key: source,
                vod_id: vodId,
                douban_id: h.douban_id || h.doubanId || '', // 确保 douban_id 存在
                img: h.poster || h.img || ''
            };
        }
    });
    _saveWatchHistoryInternal(data);
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

    const lastSyncAt = parseInt(localStorage.getItem(SYNC_KEY) || '0');
    const data = JSON.parse(localStorage.getItem(HISTORY_KEY) || '{}');

    // 找出需要同步的新记录 (watchedAt/updatedAt > lastSyncAt)
    const newRecords = Object.values(data).filter(h =>
        (h.watchedAt || h.updatedAt || 0) > lastSyncAt
    ).map(h => ({
        douban_id: h.douban_id || h.doubanId || '',
        vod_id: h.vod_id || h.vodId || '',
        title: h.title,
        poster: h.poster || h.img,
        episode: h.episode || '',
        progress: h.progress || (h.duration > 0 ? Math.floor((h.lastTime / h.duration) * 100) : 0),
        last_time: h.lastTime || 0,
        duration: h.duration || 0,
        source: h.source || h.source_key || '',
        watchedAt: h.watchedAt || h.updatedAt || Date.now()
    }));

    // 即使本地没有新记录 (newRecords.length === 0)，也允许发起请求以拉取服务器端可能的更新
    // 如果本地有记录但不需要上传，newRecords 将是空数组，服务器会识别并只返回增量数据数据数据数据


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
            const result = await response.json();
            const data = result.data || {};

            // 合并服务器返回的记录
            if (data.serverRecords && data.serverRecords.length > 0) {
                mergeServerRecords(data.serverRecords);
            }

            // 更新同步时间
            const syncedAt = data.syncedAt || Date.now();
            localStorage.setItem(SYNC_KEY, syncedAt.toString());
            return true;
        }
    } catch (error) {
        console.error('同步观影历史失败:', error);
    }
    return false;
}


/**
 * 合并服务器记录到本地
 */
function mergeServerRecords(serverRecords) {
    const localHistory = getWatchHistory();

    serverRecords.forEach(serverRecord => {
        // 匹配逻辑：优先使用 source_key + vod_id
        const localIdx = localHistory.findIndex(h =>
            (h.source_key === serverRecord.source && h.vod_id === serverRecord.vod_id) ||
            (h.douban_id === serverRecord.douban_id && h.episode === serverRecord.episode && serverRecord.douban_id)
        );

        const serverTime = new Date(serverRecord.watched_at).getTime();

        if (localIdx >= 0) {
            const localTime = localHistory[localIdx].watchedAt || localHistory[localIdx].updatedAt || 0;
            if (serverTime > localTime) {
                localHistory[localIdx] = {
                    ...localHistory[localIdx],
                    id: serverRecord.id, // 服务器记录ID
                    douban_id: serverRecord.douban_id,
                    title: serverRecord.title,
                    poster: serverRecord.poster,
                    img: serverRecord.poster,
                    episode: serverRecord.episode,
                    progress: serverRecord.progress,
                    source_key: serverRecord.source,
                    vod_id: serverRecord.vod_id,
                    watchedAt: serverTime,
                    updatedAt: serverTime
                };
            }
        } else {
            localHistory.push({
                id: serverRecord.id,
                douban_id: serverRecord.douban_id,
                title: serverRecord.title,
                poster: serverRecord.poster,
                img: serverRecord.poster,
                episode: serverRecord.episode,
                progress: serverRecord.progress,
                source_key: serverRecord.source,
                vod_id: serverRecord.vod_id,
                watchedAt: serverTime,
                updatedAt: serverTime,
                lastTime: (serverRecord.progress / 100) * (serverRecord.duration || 0),
                duration: serverRecord.duration || 0
            });
        }
    });

    // 按时间排序
    localHistory.sort((a, b) => (b.watchedAt || 0) - (a.watchedAt || 0));
    saveWatchHistory(localHistory);

    // 同步完成后，触发一个自定义事件，方便页面感知更新（如首页刷新列表）
    document.dispatchEvent(new CustomEvent('moovie:history-updated'));
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
        const response = await fetch(`/api/movies/suggest?kw=${encodeURIComponent(keyword)}`);
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

        // 构建搜索链接
        const searchUrl = `/search?kw=${encodeURIComponent(item.title || '')}&doubanId=${encodeURIComponent(item.id)}`;

        return `
            <a href="${searchUrl}" class="search-suggestion-item" data-index="${index}">
                <img src="${item.img ? 'https://image.baidu.com/search/down?url=' + item.img : '/static/img/placeholder.svg'}"
                     alt="${item.title || ''}"
                     class="suggestion-poster"
                     onerror="this.onerror=null;this.src='/static/img/placeholder.svg'" referrerpolicy="no-referrer">
                <div class="suggestion-info">
                    <div class="suggestion-title">${item.title || ''}</div>
                    <div class="suggestion-meta">
                        <span class="suggestion-type">${typeText}</span>
                        ${item.year ? `<span class="suggestion-year">${item.year}</span>` : ''}
                    </div>
                    ${item.sub_title ? `<div class="suggestion-subtitle">${item.sub_title}</div>` : ''}
                </div>
            </a>
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

    container.innerHTML = searches.map(keyword => `
        <span class="tag tag-deletable">
            <a href="/search?kw=${encodeURIComponent(keyword)}">${keyword}</a>
            <button class="tag-delete" onclick="event.preventDefault(); removeRecentSearch('${keyword.replace(/'/g, "\\'")}')">×</button>
        </span>
    `).join('');
}

// ==================== 初始化 ====================

document.addEventListener('DOMContentLoaded', function() {
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
    if (searchForm) {
        searchForm.addEventListener('submit', function(e) {
            const input = this.querySelector('input[name="kw"]');
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
        const history = getWatchHistory();
        const lastSyncAt = parseInt(localStorage.getItem(SYNC_KEY) || '0');
        const newRecords = history.filter(h => (h.watchedAt || h.updatedAt || 0) > lastSyncAt);

        if (newRecords.length > 0) {
            const payload = JSON.stringify({
                records: newRecords.map(h => ({
                    douban_id: h.douban_id || h.doubanId || '',
                    vod_id: h.vod_id || h.vodId || '',
                    title: h.title,
                    poster: h.poster || h.img,
                    episode: h.episode || '',
                    progress: h.progress || (h.duration > 0 ? Math.floor((h.lastTime / h.duration) * 100) : 0),
                    last_time: h.lastTime || 0,
                    duration: h.duration || 0,
                    source: h.source_key || h.source || '',
                    watchedAt: h.watchedAt || h.updatedAt || Date.now()
                })),
                lastSyncAt: lastSyncAt
            });
            navigator.sendBeacon('/api/history/sync', payload);
        }
    }
});

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
