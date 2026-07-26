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
let hasReported = false;
// 加载速度上报函数
function reportLoad(status, loadTime, reason, sourceKey, vodId) {
    if (hasReported) return;
    hasReported = true;
    console.log('视频加载', status, '，耗时:', loadTime, '毫秒');
    fetch('/api/report/load-speed', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({source_key: sourceKey, vod_id: vodId, load_time: loadTime, status: status, reason: reason})
    }).catch(() => {}); // 静默处理错误
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
        speed: 6,
        opacity: 1,
        fontSize: 22,
        margin: [10, '30%'],
        mode: 0,
        modes: [0, 1, 2],
        antiOverlap: true,
        synchronousPlayback: true,   // 倍速播放时弹幕同步变速
        heatmap: true,               // 进度条上的弹幕密度热力图
        emitter: false,              // 关闭发射器：弹幕来自外部源，站内暂不支持发送
        visible: localStorage.getItem(DANMAKU_VISIBLE_KEY) !== 'false',
        filter: function(danmu) {
            return danmu.text && danmu.text.length <= 60;
        }
    });
}

// 初始化播放器
function initPlayer(containerId, url, options) {
    options = options || {};
    hasReported = false; // 重置上报标记，确保每次初始化都能正常上报

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
    // 30秒超时
    const timeoutTimer = setTimeout(() => reportLoad('failed', 30000, 'timeout', options.sourceKey, options.vodId), 30000);
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
                    var hls = new Hls({
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
                    });
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
                                    break;
                            }
                        }
                    });
                    hls.on(Hls.Events.FRAG_LOADED, () => {
                        errorCount = 0;
                    });
                    hls.on(Hls.Events.MANIFEST_PARSED, () => {
                        console.log('[Player] HLS manifest 解析完成');
                        hls._bufferTimer = setTimeout(() => {
                            hls.config.maxBufferLength = 40;
                            hls.config.maxMaxBufferLength = 90;
                        }, 8000);
                    });
                    
                    // 监听HLS的LEVEL_LOADED事件，表示数据加载完成
                    hls.on(Hls.Events.LEVEL_LOADED, (event, data) => {
                        console.log('[Player] HLS level loaded, 可以开始播放');
                        clearTimeout(timeoutTimer);
                        const loadTime = Date.now() - startTime;
                        console.log('视频加载成功，耗时:', loadTime, '毫秒');
                        reportLoad('success', loadTime, null, options.sourceKey, options.vodId);
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
                        clearTimeout(timeoutTimer);
                        const loadTime = Date.now() - startTime;
                        console.log('视频加载成功，耗时:', loadTime, '毫秒');
                        reportLoad('success', loadTime, null, options.sourceKey, options.vodId);
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
            if(options.episode && options.episode == playState.episode){
                var lastTime = playState ? playState.lastTime : 0;
                if (lastTime > 5) {
                    // 延迟一小会儿执行跳转，确保播放器状态稳定
                    art.currentTime = lastTime;
                    art.notice.show = `已为您定位到上次播放位置: ${formatTime(lastTime)}`;
                }
            }
        });
        art.once('video:canplay', () => {
            clearTimeout(timeoutTimer);
            const loadTime = Date.now() - startTime;
            console.log('视频加载成功，耗时:', loadTime, '毫秒');
            reportLoad('success', loadTime, null, options.sourceKey, options.vodId);
        });

        art.on('play', () => {
            art.notice.show = '不要相信视频中出现的任何广告！！！';
            showMsg('不要相信视频中出现的任何广告！！！', 'warning');
        });

        art.on('error', function(error, reconnectTime) {
            console.error('[Player] 播放错误:', error);
            clearTimeout(timeoutTimer);
            reportLoad('failed', Date.now() - startTime, 'error', options.sourceKey, options.vodId);
        });

        art.on('video:timeupdate', () => {
            // “手动播放”或 iptv 不需要记录历史也不需要同步服务器
            if (options.title === '手动播放' || options.sourceKey === 'iptv') return;

            // 每隔 3 秒本地保存一次
            if (Math.floor(art.currentTime) % 3 === 0) {
                Storage.upsert({
                    id: options.sourceKey + options.vodId,
                    douban_id: options.douban_id, // 统一使用下划线
                    title: options.title,
                    source_key: options.sourceKey,
                    vod_id: options.vodId,
                    play: btoa64(url),
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
            clearTimeout(timeoutTimer);
            if (art._recoverTimer) clearTimeout(art._recoverTimer);
            if (art._waitingHandler && art.video) {
                art.video.removeEventListener('waiting', art._waitingHandler);
            }
            if (autoPlayState) {
                autoPlayState.destroy();
            }
        });

        console.log('[Player] Artplayer 初始化成功');
        return art;
    } catch (e) {
        console.error('[Player] Artplayer 初始化失败:', e);
        container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#fff;">播放器初始化失败</div>';
        return null;
    }
}

// 暴露全局函数
window.initPlayer = initPlayer;
