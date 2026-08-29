// 辅助函数：Base64 编码
function btoa64(str) {
    try {
        return btoa(encodeURIComponent(str).replace(/%([0-9A-F]{2})/g, function(match, p1) {
            return String.fromCharCode('0x' + p1);
        }));
    } catch (e) {
        return btoa(str);
    }
}

// 辅助函数：格式化时间
function formatTime(seconds) {
    var h = Math.floor(seconds / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    var s = Math.floor(seconds % 60);
    return [h, m, s].map(v => v < 10 ? "0" + v : v).filter((v, i) => v !== "00" || i > 0).join(":");
}

// 辅助函数：显示消息（暂时 fallback 到 console 或如果有 UI 组件则使用）
function showMsg(msg, type) {
    console.log(`[Player Msg] ${type}: ${msg}`);
}

// 辅助函数：检测是否为 iOS 设备
function isIOS() {
    return /iPad|iPhone|iPod/.test(navigator.userAgent)
        || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

// 本地存储管理
var Storage = {
    key: 'moovie_play_state',
    maxItems: 100,
    get: function() {
        try {
            return JSON.parse(localStorage.getItem(this.key) || '{}');
        } catch (e) {
            return {};
        }
    },
    upsert: function(item) {
        if (item && (item.source_key === 'iptv' || item.source === 'iptv' || item.sourceKey === 'iptv')) return;
        var data = this.get();
        data[item.id] = item;
        // 裁剪：保留最近 maxItems 条，防止 localStorage 无限增长
        var keys = Object.keys(data);
        if (keys.length > this.maxItems) {
            keys.sort(function(a, b) {
                return (data[b].updatedAt || 0) - (data[a].updatedAt || 0);
            });
            var trimmed = {};
            for (var i = 0; i < this.maxItems; i++) {
                trimmed[keys[i]] = data[keys[i]];
            }
            data = trimmed;
        }
        localStorage.setItem(this.key, JSON.stringify(data));
        if (typeof window.queueHistoryUpsert === 'function') {
            window.queueHistoryUpsert(item);
        }
    },
    find: function(id) {
        return this.get()[id] || null;
    }
};

// 检测视频类型
function detectVideoType(url) {
    if (!url) return '';
    const lowerUrl = url.toLowerCase();
    if (lowerUrl.includes('.m3u8') || lowerUrl.includes('m3u8')) {
        return 'm3u8';
    }
    if (lowerUrl.includes('.flv')) {
        return 'flv';
    }
    if (lowerUrl.includes('.mp4')) {
        return 'mp4';
    }
    // 默认尝试 m3u8
    return 'm3u8';
}
function createPlaybackAttemptId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') return window.crypto.randomUUID();
    return 'attempt-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2);
}

function reportPlaybackEvent(eventType, elapsedMs, reason, context) {
    if (!context || !context.attempt_id || !context.candidate_id || !context.media_unit_id) return;
    fetch('/api/v2/playback/events', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        credentials: 'same-origin',
        keepalive: true,
        body: JSON.stringify({
            attempt_id: context.attempt_id,
            candidate_session_id: context.candidate_session_id || '',
            event_type: eventType,
            candidate_id: context.candidate_id,
            media_unit_id: context.media_unit_id,
            source_key: context.sourceKey,
            vod_id: context.vodId,
            elapsed_ms: Math.max(0, Math.round(elapsedMs || 0)),
            reason: reason || ''
        })
    }).catch(function() {});
}

var MAX_AUTOMATIC_FAILOVERS = 2;
var MIN_AUTOMATIC_MAPPING_CONFIDENCE = 0.90;

function failoverStateKey(options) {
    return 'moovie_failover:unit:' + options.media_unit_id;
}

function readFailoverState(options) {
    var state = {};
    try {
        state = JSON.parse(sessionStorage.getItem(failoverStateKey(options)) || '{}');
    } catch (e) {
        state = {};
    }
    if (!Array.isArray(state.failed_candidate_ids)) state.failed_candidate_ids = [];
    if (!Array.isArray(state.failed_candidate_keys)) state.failed_candidate_keys = [];
    state.switch_count = Math.max(0, Number(state.switch_count) || 0);
    return state;
}

function writeFailoverState(options, state) {
    try {
        sessionStorage.setItem(failoverStateKey(options), JSON.stringify(state));
    } catch (e) {}
}

function failoverCandidateKey(candidate) {
    var candidateID = Number(candidate.candidate_id) || 0;
    if (candidateID > 0) return 'candidate:' + candidateID;
    return 'resource:' + (candidate.source_key || '') + ':' + (candidate.vod_id || '') + ':' + (candidate.play_url || '');
}

function rememberFailedCandidate(state, candidate) {
    var candidateID = Number(candidate.candidate_id) || 0;
    var key = failoverCandidateKey(candidate);
    if (candidateID > 0 && state.failed_candidate_ids.indexOf(candidateID) === -1) {
        state.failed_candidate_ids.push(candidateID);
    }
    if (state.failed_candidate_keys.indexOf(key) === -1) {
        state.failed_candidate_keys.push(key);
    }
}

// 自动换源只在前后端开关都开启时执行，并严格限制在同一规范剧集内。
function failoverToHealthyEpisode(options) {
    if (!options || !options.auto_failover || !options.media_unit_id ||
        options.sourceKey === 'iptv' || options.sourceKey === 'manual') {
        return Promise.resolve(false);
    }
    if (options._failover_in_progress) return Promise.resolve(true);

    var state = readFailoverState(options);
    rememberFailedCandidate(state, {
        candidate_id: options.candidate_id,
        source_key: options.sourceKey,
        vod_id: options.vodId,
        play_url: options._current_url || ''
    });
    state.candidate_session_id = state.candidate_session_id || options.candidate_session_id || createPlaybackAttemptId();
    options.candidate_session_id = state.candidate_session_id;
    writeFailoverState(options, state);
    if (state.switch_count >= MAX_AUTOMATIC_FAILOVERS) return Promise.resolve(false);

    options._failover_in_progress = true;
    var endpoint = '/api/v2/media-units/' + encodeURIComponent(options.media_unit_id) + '/playback-candidates';
    return fetch(endpoint, { credentials: 'same-origin' })
        .then(function(response) { return response.ok ? response.json() : null; })
        .then(function(payload) {
            if (!payload || payload.auto_failover_enabled !== true ||
                Number(payload.unit_id) !== Number(options.media_unit_id)) return false;

            var candidates = Array.isArray(payload.candidates) ? payload.candidates : [];
            var next = null;
            for (var i = 0; i < candidates.length; i++) {
                var candidate = candidates[i] || {};
                var candidateID = Number(candidate.candidate_id) || 0;
                var candidateKey = failoverCandidateKey(candidate);
                if (!candidate.play_url || Number(candidate.mapping_confidence) < MIN_AUTOMATIC_MAPPING_CONFIDENCE) continue;
                if (candidateID > 0 && state.failed_candidate_ids.indexOf(candidateID) !== -1) continue;
                if (state.failed_candidate_keys.indexOf(candidateKey) !== -1) continue;
                next = candidate;
                break;
            }
            if (!next || !currentArt || typeof currentArt.switchUrl !== 'function') return false;

            var art = currentArt;
            var resumePosition = Math.max(0, Number(art.currentTime) || 0);
            state.switch_count++;
            writeFailoverState(options, state);

            reportPlaybackEvent('source_switched', 0, 'automatic', options);
            options.candidate_id = Number(next.candidate_id) || 0;
            options.sourceKey = next.source_key || '';
            options.vodId = next.vod_id || '';
            options.episode_key = next.episode_key || options.episode_key || '';
            options.attempt_id = createPlaybackAttemptId();
            options._attempt_started_at = Date.now();
            options._load_reported = false;
            options._current_url = next.play_url;
            reportPlaybackEvent('attempt_started', 0, 'automatic_failover', options);

            if (art.notice) art.notice.show = '当前线路异常，正在自动切换备用线路…';
            art.once('video:canplay', function() {
                if (resumePosition > 0) art.currentTime = resumePosition;
                options._recovery_requested = false;
                if (art.notice) art.notice.show = '已自动切换到 ' + (next.line_label || next.source_key || '备用线路');
                if (art.video) art.video.play().catch(function() {});
            });

            try {
                var switched = art.switchUrl(next.play_url);
                options._recovery_requested = false;
                if (switched && typeof switched.then === 'function') {
                    switched.catch(function() {
                        rememberFailedCandidate(state, next);
                        writeFailoverState(options, state);
                    });
                }
                return true;
            } catch (e) {
                options._recovery_requested = false;
                rememberFailedCandidate(state, next);
                writeFailoverState(options, state);
                return false;
            }
        })
        .catch(function() { return false; })
        .finally(function() { options._failover_in_progress = false; });
}

function recoverPlaybackOrShowAlternatives(options) {
    return failoverToHealthyEpisode(options).then(function(switched) {
        return switched ? true : showFailoverAlternatives(options);
    });
}

// 自动换源不可用或已达到上限时，展示可用备选线路供用户手动选择。
function showFailoverAlternatives(options) {
    if (!options || !options.media_unit_id || options.sourceKey === 'iptv' || options.sourceKey === 'manual') {
        return Promise.resolve(false);
    }
    var currentKey = options.sourceKey + ':' + options.vodId;
    var endpoint = '/api/v2/media-units/' + encodeURIComponent(options.media_unit_id) + '/playback-candidates';
    return fetch(endpoint, { credentials: 'same-origin' })
        .then(function(response) { return response.ok ? response.json() : null; })
        .then(function(payload) {
            if (!payload || Number(payload.unit_id) !== Number(options.media_unit_id)) return false;
            var resources = Array.isArray(payload.candidates) ? payload.candidates : [];
            var alternatives = [];
            for (var i = 0; i < resources.length; i++) {
                var resource = resources[i] || {};
                var key = (resource.source_key || '') + ':' + (resource.vod_id || '');
                if (!resource.source_key || !resource.vod_id || key === currentKey) continue;
                alternatives.push(resource);
            }
            if (alternatives.length === 0) return false;
            showAlternativeSourcesUI(alternatives, options);
            return true;
        })
        .catch(function() { return false; });
}

// 在播放器区域展示备选线路供用户手动点击切换
function showAlternativeSourcesUI(alternatives, options) {
    var container = document.getElementById('artplayer-app');
    if (!container) return;
    var existing = container.querySelector('.failover-alternatives');
    if (existing) existing.remove();

    var overlay = document.createElement('div');
    overlay.className = 'failover-alternatives';
    overlay.style.cssText = 'position:absolute;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.85);display:flex;flex-direction:column;align-items:center;justify-content:center;z-index:100;color:#fff;padding:1rem';

    var title = document.createElement('div');
    title.style.cssText = 'font-size:1.1rem;margin-bottom:.5rem';
    title.textContent = '当前线路播放失败';
    overlay.appendChild(title);

    var hint = document.createElement('div');
    hint.style.cssText = 'font-size:.85rem;color:#aaa;margin-bottom:1rem';
    hint.textContent = '请选择其他可用线路：';
    overlay.appendChild(hint);

    var list = document.createElement('div');
    list.style.cssText = 'display:flex;flex-wrap:wrap;gap:8px;justify-content:center;max-width:400px';

    for (var i = 0; i < Math.min(alternatives.length, 6); i++) {
        (function(res) {
            var btn = document.createElement('button');
            btn.style.cssText = 'padding:8px 16px;border:1px solid #555;border-radius:6px;background:#222;color:#fff;cursor:pointer;font-size:.9rem';
            btn.textContent = (res.line_label || res.source_key || '备用源') + (res.source_key ? ' [' + res.source_key + ']' : '');
            btn.addEventListener('mouseover', function() { btn.style.borderColor = '#f60c3e'; });
            btn.addEventListener('mouseout', function() { btn.style.borderColor = '#555'; });
            btn.addEventListener('click', function() {
                reportPlaybackEvent('source_switched', 0, 'manual', options);
                var query = '?ep=' + encodeURIComponent(res.episode_label || options.episode || res.episode_key);
                if (res.line_label) query += '&source=' + encodeURIComponent(res.line_label);
                if (options.douban_id) query += '&douban_id=' + encodeURIComponent(options.douban_id);
                window.location.href = '/play/' + encodeURIComponent(res.source_key) + '/' + encodeURIComponent(res.vod_id) + query;
            });
            list.appendChild(btn);
        })(alternatives[i]);
    }
    overlay.appendChild(list);

    var dismiss = document.createElement('div');
    dismiss.style.cssText = 'margin-top:1rem;font-size:.8rem;color:#666;cursor:pointer';
    dismiss.textContent = '关闭';
    dismiss.addEventListener('click', function() { overlay.remove(); });
    overlay.appendChild(dismiss);

    if (getComputedStyle(container).position === 'static') {
        container.style.position = 'relative';
    }
    container.appendChild(overlay);
}

// XSS 防护：转义 HTML
function escapeHtml(str) {
    var div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

// 自动播放下一集管理器
function createAutoPlayManager(options) {
    var AUTO_PLAY_KEY = 'moovie_auto_play_next';
    var COUNTDOWN_SECONDS = 15;
    var episodes = options.episodes;
    var currentEpisode = options.episode;

    // 找到当前集索引
    var currentIndex = -1;
    for (var i = 0; i < episodes.length; i++) {
        if (episodes[i].title === currentEpisode) {
            currentIndex = i;
            break;
        }
    }

    // 最后一集或未找到，不触发
    if (currentIndex < 0 || currentIndex >= episodes.length - 1) {
        return null;
    }

    var nextEpisode = episodes[currentIndex + 1];
    var countdownTimer = null;
    var overlayEl = null;

    function isEnabled() {
        return localStorage.getItem(AUTO_PLAY_KEY) !== 'false';
    }

    function setEnabled(enabled) {
        localStorage.setItem(AUTO_PLAY_KEY, enabled ? 'true' : 'false');
    }

    function getOverlay() {
        if (overlayEl) return overlayEl;

        var container = document.getElementById('artplayer-app');
        if (!container) return null;

        if (getComputedStyle(container).position === 'static') {
            container.style.position = 'relative';
        }

        var circumference = 2 * Math.PI * 36;

        overlayEl = document.createElement('div');
        overlayEl.className = 'autoplay-overlay';
        overlayEl.innerHTML =
            '<div class="autoplay-card">' +
                '<div class="autoplay-progress-ring">' +
                    '<svg viewBox="0 0 80 80">' +
                        '<circle class="ring-bg" cx="40" cy="40" r="36"/>' +
                        '<circle class="ring-fg" cx="40" cy="40" r="36" ' +
                            'stroke-dasharray="' + circumference + '" ' +
                            'stroke-dashoffset="0"/>' +
                    '</svg>' +
                    '<div class="autoplay-countdown">' + COUNTDOWN_SECONDS + '</div>' +
                '</div>' +
                '<div class="autoplay-title">即将播放</div>' +
                '<div class="autoplay-next-name">' + escapeHtml(nextEpisode.title) + '</div>' +
                '<div class="autoplay-actions">' +
                    '<button class="autoplay-btn autoplay-btn-cancel">取消</button>' +
                    '<button class="autoplay-btn autoplay-btn-play">立即播放</button>' +
                '</div>' +
                '<div class="autoplay-toggle">不再自动播放下一集</div>' +
            '</div>';

        overlayEl.querySelector('.autoplay-btn-cancel').addEventListener('click', function(e) {
            e.stopPropagation();
            hideOverlay();
        });
        overlayEl.querySelector('.autoplay-btn-play').addEventListener('click', function(e) {
            e.stopPropagation();
            navigateToNext();
        });
        overlayEl.querySelector('.autoplay-toggle').addEventListener('click', function(e) {
            e.stopPropagation();
            setEnabled(false);
            hideOverlay();
        });
        overlayEl.addEventListener('click', function() {
            hideOverlay();
        });
        overlayEl.querySelector('.autoplay-card').addEventListener('click', function(e) {
            e.stopPropagation();
        });

        return overlayEl;
    }

    function showOverlay() {
        if (!isEnabled()) return;

        var el = getOverlay();
        if (!el) return;

        var container = document.getElementById('artplayer-app');
        container.appendChild(el);

        var remaining = COUNTDOWN_SECONDS;
        var circumference = 2 * Math.PI * 36;
        var countdownEl = el.querySelector('.autoplay-countdown');
        var ringFg = el.querySelector('.ring-fg');

        countdownEl.textContent = remaining;
        ringFg.style.strokeDashoffset = '0';

        countdownTimer = setInterval(function() {
            remaining--;
            if (remaining <= 0) {
                clearInterval(countdownTimer);
                countdownTimer = null;
                navigateToNext();
                return;
            }
            countdownEl.textContent = remaining;
            var progress = (COUNTDOWN_SECONDS - remaining) / COUNTDOWN_SECONDS;
            ringFg.style.strokeDashoffset = (circumference * progress).toString();
        }, 1000);
    }

    function hideOverlay() {
        if (countdownTimer) {
            clearInterval(countdownTimer);
            countdownTimer = null;
        }
        if (overlayEl && overlayEl.parentNode) {
            overlayEl.parentNode.removeChild(overlayEl);
        }
    }

    function navigateToNext() {
        hideOverlay();
        window.location.href = nextEpisode.url;
    }

    return {
        isEnabled: isEnabled,
        setEnabled: setEnabled,
        trigger: showOverlay,
        cancel: hideOverlay,
        destroy: hideOverlay
    };
}

// 构建弹幕插件配置
// 依赖 artplayer-plugin-danmuku，未加载时静默跳过（弹幕永远不能影响正片播放）
var DANMAKU_VISIBLE_KEY = 'moovie_danmaku_visible';

function buildDanmakuPlugin(options) {
    if (typeof artplayerPluginDanmuku === 'undefined') return null;
    // 手动播放和 IPTV 直播没有片名，无从匹配弹幕
    if (!options.vodName || options.sourceKey === 'iptv' || options.sourceKey === 'manual') return null;

    return artplayerPluginDanmuku({
        // 传函数而不是 URL 字符串：字符串形式在请求失败时插件会 throw，
        // 函数形式可以 catch 后返回空数组，静默降级
        danmuku: function() {
            var q = '/api/danmaku?title=' + encodeURIComponent(options.vodName);
            if (options.episode) {
                q += '&episode=' + encodeURIComponent(options.episode);
            }
            return fetch(q)
                .then(function(r) { return r.ok ? r.json() : []; })
                .then(function(list) { return Array.isArray(list) ? list : []; })
                .catch(function(e) {
                    console.warn('[Player] 弹幕加载失败，已跳过', e);
                    return [];
                });
        },
        speed: 10,
        opacity: 1,
        fontSize: 22,
        margin: [10, '30%'],
        mode: 0,
        modes: [0, 1, 2],
        antiOverlap: true,
        synchronousPlayback: true,   // 倍速播放时弹幕同步变速
        heatmap: false,              // 进度条上的弹幕密度热力图
        // 插件的配置校验要求 emitter 必须是 boolean，不能传对象
        emitter: !!options.canSendDanmaku,
        maxLength: 50,
        visible: localStorage.getItem(DANMAKU_VISIBLE_KEY) !== 'false',
        filter: function(danmu) {
            return danmu.text && danmu.text.length <= 60;
        },
        // 发送前先落库，后端拒绝就不上屏，避免用户以为发出去了其实没有
        beforeEmit: function(danmu) {
            return fetch('/api/danmaku', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'same-origin',
                body: JSON.stringify({
                    title: options.vodName,
                    episode: options.episode || '',
                    text: danmu.text,
                    time: danmu.time,
                    mode: danmu.mode,
                    color: danmu.color
                })
            }).then(function(r) {
                if (r.ok) return true;
                return r.json().catch(function() { return {}; }).then(function(body) {
                    showDanmakuError(body.error || '弹幕发送失败');
                    return false;
                });
            }).catch(function() {
                showDanmakuError('网络异常，弹幕发送失败');
                return false;
            });
        }
    });
}

// 弹幕发送失败提示。挂在播放器的 notice 上，没有播放器实例时退回 console
var currentArt = null;
function showDanmakuError(msg) {
    if (currentArt && currentArt.notice) {
        currentArt.notice.show = msg;
    } else {
        console.warn('[Player] ' + msg);
    }
}

// ---- Ad-skip: M3U8 广告指纹众包跳过 ----

function adSkipResolvePath(uri, base) {
    try { return new URL(uri, base || 'http://x').pathname; }
    catch(e) { return uri; }
}

function adSkipPathFamily(uri) {
    try { var p = new URL(uri, 'http://x').pathname; var i = p.lastIndexOf('/'); return i > 0 ? p.substring(0, i) : '/'; }
    catch(e) { return '/'; }
}

function adSkipParse(text, baseURL) {
    var lines = text.split('\n'), segs = [], keyURI = '', keyMethod = 'NONE', byterange = '', extinf = null, discont = false;
    for (var i = 0; i < lines.length; i++) {
        var L = lines[i].trim();
        if (L.startsWith('#EXT-X-DISCONTINUITY')) { discont = true; continue; }
        if (L.startsWith('#EXT-X-KEY:')) {
            var mm = L.match(/METHOD=([^,]+)/), mu = L.match(/URI="([^"]+)"/);
            var nm = mm ? mm[1] : 'NONE', nu = mu ? mu[1] : '';
            if (nm !== keyMethod || nu !== keyURI) { discont = true; keyMethod = nm; keyURI = nu; }
            continue;
        }
        if (L.startsWith('#EXT-X-BYTERANGE:')) { byterange = L.substring(17); continue; }
        if (L.startsWith('#EXTINF:')) { extinf = parseFloat(L.substring(8).split(',')[0]) || 0; continue; }
        if (L.startsWith('#') || !L) continue;
        if (extinf !== null) {
            var np = adSkipResolvePath(L, baseURL);
            segs.push({ dur: extinf, uri: L, path: np, family: adSkipPathFamily(np), br: byterange,
                        kURI: keyURI, kMethod: keyMethod, line: i, discont: discont });
            extinf = null; byterange = ''; discont = false;
        }
    }
    return segs;
}

function adSkipBlocks(segs) {
    if (!segs.length) return [];
    var blocks = [], cur = { segs: [segs[0]], family: segs[0].family, kURI: segs[0].kURI, kMethod: segs[0].kMethod };
    for (var i = 1; i < segs.length; i++) {
        var s = segs[i];
        if (s.discont || s.kURI !== cur.kURI || s.kMethod !== cur.kMethod || s.family !== cur.family) {
            blocks.push(cur);
            cur = { segs: [s], family: s.family, kURI: s.kURI, kMethod: s.kMethod };
        } else { cur.segs.push(s); }
    }
    blocks.push(cur);
    var t = 0;
    for (var b = 0; b < blocks.length; b++) {
        var bl = blocks[b]; bl.start = t; bl.dur = 0; bl.idx = b;
        for (var j = 0; j < bl.segs.length; j++) bl.dur += bl.segs[j].dur;
        bl.end = t + bl.dur; t = bl.end;
        bl.first = b === 0; bl.last = b === blocks.length - 1;
    }
    return blocks;
}

function adSkipMainBody(blocks) {
    if (!blocks.length) return null;
    var best = blocks[0], total = 0;
    for (var i = 0; i < blocks.length; i++) { total += blocks[i].dur; if (blocks[i].dur > best.dur) best = blocks[i]; }
    return best.dur >= total * 0.3 ? best : null;
}

function adSkipScore(bl, main, blocks) {
    if (bl === main || bl.dur < 5 || bl.dur > 120 || bl.segs.length < 2) return 0;
    var total = 0;
    for (var i = 0; i < blocks.length; i++) total += blocks[i].dur;
    if (bl.dur > total * 0.5) return 0;
    var sc = 0;
    if (bl.first || bl.last) sc++;
    if (bl.segs[0].discont) sc++;
    else if (bl.idx < blocks.length - 1 && blocks[bl.idx + 1].segs.length && blocks[bl.idx + 1].segs[0].discont) sc++;
    if (bl.family !== main.family) sc++;
    if (bl.kURI !== main.kURI || bl.kMethod !== main.kMethod) sc++;
    var bAvg = bl.dur / bl.segs.length, mAvg = main.dur / main.segs.length;
    if (Math.abs(bAvg - mAvg) > mAvg * 0.3) sc++;
    for (var i = 0; i < blocks.length; i++) {
        if (blocks[i] === bl || blocks[i] === main) continue;
        if (blocks[i].family === bl.family && blocks[i].segs.length === bl.segs.length &&
            Math.abs(blocks[i].dur - bl.dur) < 1) { sc++; break; }
    }
    return sc;
}

function adSkipAnalyze(text, url) {
    if (!text.includes('#EXTINF') || !text.includes('#EXT-X-ENDLIST')) return null;
    var segs = adSkipParse(text, url);
    if (segs.length < 3) return null;
    var blocks = adSkipBlocks(segs);
    if (blocks.length < 2) return null;
    var main = adSkipMainBody(blocks);
    if (!main) return null;
    var cands = [];
    for (var i = 0; i < blocks.length; i++) {
        var sc = adSkipScore(blocks[i], main, blocks);
        if (sc >= 3) cands.push({ block: blocks[i], score: sc, hex: null });
    }
    return cands.length ? { candidates: cands } : null;
}

function adSkipCanonical(cand, url) {
    var lines = [];
    for (var i = 0; i < cand.block.segs.length; i++) {
        var s = cand.block.segs[i], line = Math.round(s.dur * 1000) + '|' + adSkipResolvePath(s.uri, url);
        if (s.br) line += '|' + s.br;
        lines.push(line);
    }
    return lines.join('\n');
}

function adSkipFingerprints(cands, url) {
    if (!window.crypto || !window.crypto.subtle) return Promise.resolve([]);
    return Promise.all(cands.map(function(c) {
        var data = new TextEncoder().encode(adSkipCanonical(c, url));
        return crypto.subtle.digest('SHA-256', data).then(function(buf) {
            var a = new Uint8Array(buf), h = '';
            for (var i = 0; i < a.length; i++) h += ('0' + a[i].toString(16)).slice(-2);
            c.hex = h;
            return c;
        });
    }));
}

function adSkipMatchAPI(hexes) {
    if (!hexes.length) return Promise.resolve({});
    return new Promise(function(resolve) {
        var timer = setTimeout(function() { resolve({}); }, 250);
        fetch('/api/ad-fingerprints/match', {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            credentials: 'same-origin',
            body: JSON.stringify({ fingerprints: hexes.slice(0, 20) })
        }).then(function(r) { return r.ok ? r.json() : {}; })
        .then(function(d) { clearTimeout(timer); resolve(d.matches || {}); })
        .catch(function() { clearTimeout(timer); resolve({}); });
    });
}

function adSkipRewrite(text, gapLines) {
    if (!gapLines.length) return text;
    var lines = text.split('\n'), out = [], set = {};
    for (var i = 0; i < gapLines.length; i++) set[gapLines[i]] = true;
    for (var i = 0; i < lines.length; i++) {
        if (set[i]) out.push('#EXT-X-GAP');
        out.push(lines[i]);
    }
    return out.join('\n');
}

function adSkipProcess(text, url, state) {
    var analysis = adSkipAnalyze(text, url);
    if (!analysis) return Promise.resolve(null);
    return adSkipFingerprints(analysis.candidates, url).then(function(cands) {
        var hexes = [];
        for (var i = 0; i < cands.length; i++) if (cands[i].hex) hexes.push(cands[i].hex);
        return adSkipMatchAPI(hexes).then(function(matches) {
            var gapLines = [];
            state.candidates = [];
            for (var i = 0; i < cands.length; i++) {
                var c = cands[i], m = c.hex ? matches[c.hex] : null;
                var status = m ? m.status : 'unknown';
                var info = { start: c.block.start, end: c.block.end, dur: c.block.dur, hex: c.hex, status: status };
                state.candidates.push(info);
                if (status === 'confirmed' && c.block.dur <= 120 && !state.undone[c.hex]) {
                    for (var j = 0; j < c.block.segs.length; j++) gapLines.push(c.block.segs[j].line);
                }
            }
            return gapLines.length ? adSkipRewrite(text, gapLines) : null;
        });
    });
}

function adSkipCreateLoader(state) {
    var Default = Hls.DefaultConfig.loader;
    function Loader(cfg) { this._l = new Default(cfg); this._adTimer = 0; this._adDone = false; }
    Object.defineProperty(Loader.prototype, 'stats', { get: function() { return this._l.stats; } });
    Object.defineProperty(Loader.prototype, 'context', { get: function() { return this._l.context; } });
    Loader.prototype.load = function(ctx, cfg, cb) {
        if (!state.enabled) { this._l.load(ctx, cfg, cb); return; }
        var self = this, orig = cb.onSuccess;
        self._adDone = false;
        function deliver(r, s, c, n) { if (self._adDone) return; self._adDone = true; clearTimeout(self._adTimer); orig.call(null, r, s, c, n); }
        cb.onSuccess = function(resp, stats, c, net) {
            var txt = typeof resp.data === 'string' ? resp.data : '';
            if (!txt.includes('#EXTINF') || !txt.includes('#EXT-X-ENDLIST')) { deliver(resp, stats, c, net); return; }
            state.originalPlaylist = txt;
            state.playlistURL = resp.url || c.url || '';
            self._adTimer = setTimeout(function() { deliver(resp, stats, c, net); }, 250);
            adSkipProcess(txt, state.playlistURL, state).then(function(rewritten) {
                if (rewritten) resp.data = rewritten;
                deliver(resp, stats, c, net);
            }).catch(function() { deliver(resp, stats, c, net); });
        };
        this._l.load(ctx, cfg, cb);
    };
    Loader.prototype.abort = function() { this._adDone = true; clearTimeout(this._adTimer); this._l.abort(); };
    Loader.prototype.destroy = function() { this._adDone = true; clearTimeout(this._adTimer); this._l.destroy(); };
    return Loader;
}

function adSkipHidePrompt() {
    var el = document.getElementById('ad-skip-prompt');
    if (el) el.remove();
}

function adSkipShowPrompt(container, cand, state) {
    adSkipHidePrompt();
    var el = document.createElement('div');
    el.className = 'ad-skip-prompt'; el.id = 'ad-skip-prompt';
    el.innerHTML = '<span class="ad-skip-prompt-text">检测到疑似广告 · ' + Math.round(cand.dur) + ' 秒</span>' +
        '<span class="ad-skip-prompt-btns">' +
        '<button class="ad-skip-prompt-btn confirm">是广告，跳过</button>' +
        '<button class="ad-skip-prompt-btn reject">不是</button>' +
        '<button class="ad-skip-prompt-btn close">×</button></span>';
    el.querySelector('.confirm').onclick = function() {
        state.confirmedLocal[cand.hex] = true;
        state.voted[cand.hex] = true;
        if (state.art) try { state.art.currentTime = cand.end; } catch(e) {}
        fetch('/api/ad-fingerprints/vote', { method: 'POST', headers: {'Content-Type': 'application/json'},
            credentials: 'same-origin', keepalive: true,
            body: JSON.stringify({ fingerprint: cand.hex, vote: 'confirm' }) }).catch(function() {});
        adSkipHidePrompt();
    };
    el.querySelector('.reject').onclick = function() {
        state.voted[cand.hex] = true;
        fetch('/api/ad-fingerprints/vote', { method: 'POST', headers: {'Content-Type': 'application/json'},
            credentials: 'same-origin', keepalive: true,
            body: JSON.stringify({ fingerprint: cand.hex, vote: 'reject' }) }).catch(function() {});
        adSkipHidePrompt();
    };
    el.querySelector('.close').onclick = function() { adSkipHidePrompt(); };
    state.prompted[cand.hex] = true;
    if (getComputedStyle(container).position === 'static') container.style.position = 'relative';
    container.appendChild(el);
}

function adSkipShowToast(container, cand, state) {
    var toastKey = cand.hex + ':' + cand.start + ':' + cand.end;
    if (state.toastShown[toastKey]) return;
    state.toastShown[toastKey] = true;
    var el = document.createElement('div');
    el.className = 'ad-skip-toast';
    el.innerHTML = '已跳过 ' + Math.round(cand.dur) + ' 秒广告　<a class="ad-skip-undo">撤销本次</a>';
    el.querySelector('.ad-skip-undo').onclick = function(e) {
        e.preventDefault();
        state.undone[cand.hex] = true;
        el.remove();
        if (!state.art || !state.originalPlaylist) return;
        try {
            var playURL = state.art.option ? state.art.option.url : '';
            if (!playURL) return;
            state.art.once('video:canplay', function() {
                try { state.art.currentTime = cand.start; } catch(e) {}
            });
            state.art.switchUrl(playURL);
        } catch(e) {
            if (state.art && state.art.notice) state.art.notice.show = '撤销失败，继续播放';
        }
    };
    if (getComputedStyle(container).position === 'static') container.style.position = 'relative';
    container.appendChild(el);
    setTimeout(function() { if (el.parentNode) el.remove(); }, 4000);
}

function adSkipMonitor(state) {
    if (!state.art || !state.enabled) return;
    var container = document.getElementById('artplayer-app');
    if (!container) return;
    state.art.on('video:timeupdate', function() {
        if (!state.enabled || !state.candidates.length) return;
        var t = state.art.currentTime;
        for (var i = 0; i < state.candidates.length; i++) {
            var c = state.candidates[i];
            if (!c.hex) continue;
            var toastKey = c.hex + ':' + c.start + ':' + c.end;
            if (c.status === 'confirmed' && !state.undone[c.hex] && !state.toastShown[toastKey]) {
                if (t >= c.start && t <= c.end + 3) adSkipShowToast(container, c, state);
            } else if (c.status === 'unknown' || c.status === 'pending') {
                if (t >= c.start && t < c.end) {
                    if (state.confirmedLocal[c.hex]) {
                        try { state.art.currentTime = c.end; } catch(e) {}
                    } else if (!state.prompted[c.hex] && !state.voted[c.hex]) {
                        adSkipShowPrompt(container, c, state);
                    }
                }
            }
        }
    });
    // Hide prompt when toggle is turned off via htmx
    document.addEventListener('htmx:afterSwap', function(e) {
        var tgt = e.detail && e.detail.target;
        if (tgt && tgt.id === 'ad-skip-toggle') {
            var cb = tgt.querySelector('input[name="ad_skip_enabled"]');
            if (!cb || !cb.checked) { state.enabled = false; adSkipHidePrompt(); }
        }
    });
}

// ---- End ad-skip ----

// 初始化播放器
function initPlayer(containerId, url, options) {
    options = options || {};
    options._current_url = url;

    var adState = options.adSkipEnabled ? {
        enabled: true, playlistURL: '', originalPlaylist: '',
        candidates: [], prompted: {}, voted: {}, confirmedLocal: {},
        undone: {}, toastShown: {}, art: null
    } : null;

    // 自动播放下一集
    var autoPlayState = null;
    if (options.episodes && options.episodes.length > 1) {
        autoPlayState = createAutoPlayManager(options);
    }

    console.log('[Player] 初始化播放器');
    console.log('[Player] 容器:', containerId);
    console.log('[Player] 播放地址:', url);

    // 检查容器是否存在
    var container = document.getElementById(containerId) || document.querySelector(containerId);
    if (!container) {
        console.error('[Player] 容器不存在:', containerId);
        return null;
    }

    // 检查 URL
    if (!url) {
        console.error('[Player] 播放地址为空');
        container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#fff;">暂无可用播放链接</div>';
        return null;
    }

    // 检查 Artplayer 是否加载
    if (typeof Artplayer === 'undefined') {
        console.error('[Player] Artplayer 库未加载');
        container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#fff;">播放器加载失败</div>';
        return null;
    }

    var videoType = detectVideoType(url);
    console.log('[Player] 视频类型:', videoType);
    
    // 加载速度统计
    const startTime = Date.now();
    options._attempt_started_at = startTime;
    if (options.media_unit_id) {
        var existingFailoverState = readFailoverState(options);
        options.candidate_session_id = existingFailoverState.candidate_session_id || options.candidate_session_id || createPlaybackAttemptId();
        existingFailoverState.candidate_session_id = options.candidate_session_id;
        writeFailoverState(options, existingFailoverState);
    }
    options.attempt_id = options.attempt_id || createPlaybackAttemptId();
    var firstFrameLoadTime = 0;
    var effectivePlaybackMs = 0;
    var lastPlaybackTick = 0;
    var terminalAttempt = false;
    reportPlaybackEvent('attempt_started', 0, '', options);

    function recoverAfterFatal(reason, elapsedMs, fallbackMessage) {
        if (options._recovery_requested) return;
        options._recovery_requested = true;
        clearTimeout(timeoutTimer);
        terminalAttempt = true;
        reportPlaybackEvent('fatal_error', elapsedMs, reason, options);
        if (typeof options.onPlaybackError === 'function') {
            try { options.onPlaybackError({ reason: reason, elapsedMs: elapsedMs }); } catch (e) {}
        }
        recoverPlaybackOrShowAlternatives(options).then(function(shown) {
            if (!shown && currentArt && currentArt.notice) {
                currentArt.notice.show = fallbackMessage;
            }
        });
    }

    // 默认 30 秒超时；直播页可按外部源特性缩短等待时间。
    const loadTimeoutMs = Math.max(5000, Number(options.loadTimeoutMs) || 30000);
    const timeoutTimer = setTimeout(() => {
        recoverAfterFatal('timeout', loadTimeoutMs, '加载超时，请刷新页面或切换其他线路');
    }, loadTimeoutMs);
    // Artplayer 配置
    var config = {
        container: container,
        url: url,
        title: options.title || '',
        poster: options.poster || '',
        volume: 1,
        isLive: options.isLive || false,
        muted: false,
        autoplay: true,
        autoSize: false,
        autoMini: true,
        loop: false,
        flip: true,
        playbackRate: true,
        aspectRatio: true,
        screenshot: true,
        setting: true,
        hotkey: true,
        pip: true,
        fullscreen: true,
        fullscreenWeb: true,
        subtitleOffset: true,
        miniProgressBar: true,
        mutex: true,
        backdrop: true,
        playsInline: true,
        autoPlayback: true,
        fastForward: true,
        lock: true,
        autoOrientation: true,
        airplay: true,
        theme: '#f60c3e',
        lang: 'zh-cn',
        moreVideoAttr: {
            crossOrigin: 'anonymous',
            webkitPlaysinline: true,
            playsinline: true,
            x5Playsinline: true,
            preload: 'auto'
        },
        customType: {
            m3u8: function(video, url, art) {
                const forceMSE = isIOS();
                if (typeof Hls !== 'undefined' && (Hls.isSupported() || forceMSE)) {
                    if (art.hls) {
                        art.hls.destroy();
                        art.hls = null;
                    };
                    var hlsConfig = {
                        overrideNative: true,
                        maxBufferLength: forceMSE ? 60 : 15,
                        maxMaxBufferLength: forceMSE ? 120 : 60,
                        maxBufferSize: 60 * 1000 * 1000,
                        maxBufferHole: 2.5,
                        backBufferLength: 10,
                        startFragPrefetch: true,
                        startLevel: -1,
                        autoStartLoad: true,
                        enableWorker: true,
                        fragLoadingMaxRetry: 3,
                        skipBacktrack: true,
                        manifestLoadingMaxRetry: 3,
                        maxLoadingDelay: 4,
                        fragLoadingTimeOut: 8000,
                        manifestLoadingTimeOut: 15000,
                        maxFragLookUpTolerance: 0.2,
                    };
                    if (adState) hlsConfig.pLoader = adSkipCreateLoader(adState);
                    var hls = new Hls(hlsConfig);
                    hls.loadSource(url);
                    hls.attachMedia(video);
                    art.hls = hls;
                    art.on('destroy', function() {
                        if (hls) {
                            if (hls._bufferTimer) clearTimeout(hls._bufferTimer);
                            hls.destroy();
                            hls = null;
                        }
                    });
                    let errorCount = 0;
                    hls.on(Hls.Events.ERROR, function(event, data) {
                        if (data.fatal) {
                            switch(data.type) {
                                case Hls.ErrorTypes.NETWORK_ERROR:
                                    errorCount++;
                                    console.error(`网络错误, 累计次数: ${errorCount}`);
                                    if (errorCount >= 3) {
                                        console.error('连续错误超过阈值，尝试强制刷新 URL');
                                        if (art && art.notice) {
                                            art.notice.show = '视频片段加载失败，请尝试刷新页面或切换其他视频源';
                                        }
                                        hls.destroy();
                                        recoverAfterFatal('hls_network_error', Date.now() - startTime, '视频片段加载失败，请切换其他线路');
                                    } else {
                                        hls.startLoad();
                                    }
                                    break;
                                case Hls.ErrorTypes.MEDIA_ERROR:
                                    errorCount++;
                                    if (art && art.notice) {
                                        art.notice.show = '媒体错误,正在尝试自动恢复...';
                                    }
                                    hls.recoverMediaError();
                                    break;
                                default:
                                    console.error('无法恢复的错误');
                                    if (art && art.notice) {
                                        art.notice.show = '视频片段加载失败，请尝试刷新页面或切换其他视频源';
                                    }
                                    hls.destroy();
                                    recoverAfterFatal('hls_fatal_error', Date.now() - startTime, '视频片段加载失败，请切换其他线路');
                                    break;
                            }
                        }
                    });
                    hls.on(Hls.Events.FRAG_LOADED, () => {
                        errorCount = 0;
                    });
                    hls.on(Hls.Events.MANIFEST_PARSED, () => {
                        console.log('[Player] HLS manifest 解析完成');
                        reportPlaybackEvent('manifest_loaded', Date.now() - startTime, '', options);
                        hls._bufferTimer = setTimeout(() => {
                            hls.config.maxBufferLength = 40;
                            hls.config.maxMaxBufferLength = 90;
                        }, 8000);
                    });
                    
                    // LEVEL_LOADED 仅表示播放列表就绪；首帧出现前继续保留超时保护。
                    hls.on(Hls.Events.LEVEL_LOADED, (event, data) => {
                        console.log('[Player] HLS level loaded');
                        const loadTime = Date.now() - startTime;
                        console.log('视频加载成功，耗时:', loadTime, '毫秒');
                    });
                } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
                    video.src = url;
                } else {
                    container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#fff;">当前浏览器不支持播放此视频</div>';
                }
            },
            flv: function(video, url, art) {
                if (typeof flvjs !== 'undefined' && flvjs.isSupported()) {
                    console.log('[Player] 使用 FLV.js 播放');
                    if (art.flv) art.flv.destroy();
                    var flvPlayer = flvjs.createPlayer({
                        type: 'flv',
                        url: url
                    });
                    flvPlayer.attachMediaElement(video);
                    flvPlayer.load();
                    art.flv = flvPlayer;
                    art.on('destroy', function() {
                        flvPlayer.destroy();
                    });
                    
                    // 监听FLV加载完成事件
                    flvPlayer.on(flvjs.Events.LOADING_COMPLETE, () => {
                        console.log('[Player] FLV 加载完成');
                        const loadTime = Date.now() - startTime;
                        console.log('视频加载成功，耗时:', loadTime, '毫秒');
                        reportPlaybackEvent('manifest_loaded', loadTime, '', options);
                    });
                } else {
                    console.error('[Player] 不支持 FLV 播放');
                }
            }
        },
        type: videoType
    };

    // 弹幕插件（可选，加载不到就当没有）
    var danmakuPlugin = buildDanmakuPlugin(options);
    if (danmakuPlugin) {
        config.plugins = [danmakuPlugin];
    }

    try {
        var art = new Artplayer(config);
        currentArt = art;

        if (adState) { adState.art = art; adSkipMonitor(adState); }

        if (danmakuPlugin) {
            art.on('artplayerPluginDanmuku:loaded', function(queue) {
                if (queue && queue.length) {
                    console.log('[Player] 弹幕加载完成，共', queue.length, '条');
                }
            });
            art.on('artplayerPluginDanmuku:error', function(e) {
                console.warn('[Player] 弹幕插件异常', e);
            });
            // 记住用户的弹幕开关偏好
            art.on('artplayerPluginDanmuku:show', function() {
                localStorage.setItem(DANMAKU_VISIBLE_KEY, 'true');
            });
            art.on('artplayerPluginDanmuku:hide', function() {
                localStorage.setItem(DANMAKU_VISIBLE_KEY, 'false');
            });
        }

        art.on('ready', function() {
            const video = art.video;
            if (video) {
                video.preservesPitch = true;
                video.mozPreservesPitch = true;
                video.webkitPreservesPitch = true;
            }

            // 保存引用供 destroy 时清理
            var recoverTimer = null;
            var waitingHandler = function() {
                reportPlaybackEvent('rebuffer', 0, 'waiting', options);
                lastPlaybackTick = 0;
                if (recoverTimer) return;
                recoverTimer = setTimeout(function() {
                    console.log('iOS卡顿自动恢复');
                    if (art.hls) {
                        art.hls.startLoad();
                    }
                    video.play().catch(function(){});
                    recoverTimer = null;
                    art._recoverTimer = null;
                }, 1200);
                art._recoverTimer = recoverTimer;
            };
            video.addEventListener('waiting', waitingHandler);
            art._waitingHandler = waitingHandler;

            // 获取上次播放进度并自动跳转
            var playState = Storage.find(options.sourceKey + options.vodId);
            var sameStoredEpisode = playState && (!options.episode || options.episode === playState.episode);
            var lastTime = sameStoredEpisode ? (playState.lastTime || 0) : 0;
            if (lastTime > 5) {
                art.currentTime = lastTime;
                art.notice.show = `已为您定位到上次播放位置: ${formatTime(lastTime)}`;
            }
        });
        art.once('video:canplay', () => {
            clearTimeout(timeoutTimer);
            const loadTime = Date.now() - startTime;
            firstFrameLoadTime = loadTime;
            console.log('视频加载成功，耗时:', loadTime, '毫秒');
            reportPlaybackEvent('first_frame', loadTime, '', options);
            if (typeof options.onPlaybackReady === 'function') {
                try { options.onPlaybackReady({ elapsedMs: loadTime }); } catch (e) {}
            }
        });

        art.on('play', () => {
            lastPlaybackTick = Date.now();
            art.notice.show = '不要相信视频中出现的任何广告！！！';
            showMsg('不要相信视频中出现的任何广告！！！', 'warning');
        });

        art.on('error', function(error, reconnectTime) {
            console.error('[Player] 播放错误:', error);
            clearTimeout(timeoutTimer);
            recoverAfterFatal(String(error || 'error'), Date.now() - startTime, '当前线路播放失败，请稍后重试或切换其他线路');
        });

        art.on('video:timeupdate', () => {
            var playbackNow = Date.now();
            var playbackVideo = art.video;
            if (playbackVideo && !playbackVideo.paused && lastPlaybackTick > 0) {
                effectivePlaybackMs += Math.min(playbackNow - lastPlaybackTick, 2000);
            }
            lastPlaybackTick = playbackNow;
            if (effectivePlaybackMs >= 10000 && !options._played_10s_reported) {
                options._played_10s_reported = true;
                reportPlaybackEvent('played_10s', effectivePlaybackMs, '', options);
                if (options.media_unit_id) {
                    try { sessionStorage.removeItem('moovie_failover:unit:' + options.media_unit_id); } catch (e) {}
                }
            }
            // “手动播放”或 iptv 不需要记录历史也不需要同步服务器
            if (options.title === '手动播放' || options.sourceKey === 'iptv') return;

            // 每隔 3 秒本地保存一次
            if (Math.floor(art.currentTime) % 3 === 0) {
                Storage.upsert({
                    id: options.sourceKey + options.vodId,
                    douban_id: options.douban_id, // 统一使用下划线
                    media_id: options.media_id || 0,
                    media_unit_id: options.media_unit_id || 0,
					season_number: options.season_number || 1,
					episode_key: options.episode_key || options.episode || '',
					entry_page: options.entryPage === 'watch' ? 'watch' : 'play',
					title: options.title,
                    source_key: options.sourceKey,
                    vod_id: options.vodId,
                    play: btoa64(options._current_url || url),
                    lastTime: art.currentTime,
                    duration: art.duration,
                    img: options.poster,
                    episode: options.episode,
                    updatedAt: Date.now()
                });

                // 调度同步 (受 app.js 中 SYNC_INTERVAL 限制)
                if (typeof scheduleSync === 'function') {
                    scheduleSync();
                }
            }
        });

        // 自动播放下一集：视频结束时触发
        art.on('video:ended', function() {
            terminalAttempt = true;
            reportPlaybackEvent('ended', effectivePlaybackMs, '', options);
            if (autoPlayState) {
                autoPlayState.trigger();
            }
        });

        // 用户拖回进度条或重新播放时取消倒计时
        art.on('video:play', function() {
            if (autoPlayState) {
                autoPlayState.cancel();
            }
        });

        // 播放器销毁时清理
        art.on('destroy', function() {
            if (!terminalAttempt) reportPlaybackEvent('abandoned', effectivePlaybackMs, '', options);
            clearTimeout(timeoutTimer);
            if (art._recoverTimer) clearTimeout(art._recoverTimer);
            if (art._waitingHandler && art.video) {
                art.video.removeEventListener('waiting', art._waitingHandler);
            }
            if (autoPlayState) {
                autoPlayState.destroy();
            }
            if (currentArt === art) {
                currentArt = null;
            }
        });

        console.log('[Player] Artplayer 初始化成功');
        return art;
    } catch (e) {
        clearTimeout(timeoutTimer);
        console.error('[Player] Artplayer 初始化失败:', e);
        container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#fff;">播放器初始化失败</div>';
        return null;
    }
}

// 暴露全局函数
window.initPlayer = initPlayer;
