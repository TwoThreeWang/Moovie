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

// 本地存储管理
var Storage = {
    key: 'moovie_play_state',
    get: function() {
        try {
            return JSON.parse(localStorage.getItem(this.key) || '{}');
        } catch (e) {
            return {};
        }
    },
    upsert: function(item) {
        var data = this.get();
        data[item.id] = item;
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

// 初始化播放器
function initPlayer(containerId, url, options) {
    options = options || {};

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

    // Artplayer 配置
    var config = {
        container: container,
        url: url,
        title: options.title || '',
        poster: options.poster || '',
        volume: 1,
        isLive: false,
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
            preload: 'auto'
        },
        customType: {
            m3u8: function(video, url, art) {
                if (typeof Hls !== 'undefined' && Hls.isSupported()) {
                    if (art.hls) art.hls.destroy();
                    var hls = new Hls({
                        maxBufferLength: 15,
                        maxMaxBufferLength: 60,
                        maxBufferSize: 60 * 1000 * 1000,
                        maxBufferHole: 1.5,
                        backBufferLength: 10,
                        startFragPrefetch: true,
                        startLevel: -1,
                        autoStartLoad: true,
                        enableWorker: true,
                        fragLoadingMaxRetry: 5,
                        manifestLoadingMaxRetry: 5
                    });
                    hls.loadSource(url);
                    hls.attachMedia(video);
                    art.hls = hls;
                    art.on('destroy', function() {
                        hls.destroy();
                    });
                    hls.on(Hls.Events.ERROR, function(event, data) {
                        if (data.fatal) {
                            switch(data.type) {
                                case Hls.ErrorTypes.NETWORK_ERROR:
                                    console.error('网络错误,尝试恢复...');
                                    hls.startLoad();
                                    break;
                                case Hls.ErrorTypes.MEDIA_ERROR:
                                    console.error('媒体错误,尝试恢复...');
                                    hls.recoverMediaError();
                                    break;
                                default:
                                    console.error('无法恢复的错误');
                                    showMsg('播放出错,请刷新重试', 'error');
                                    break;
                            }
                        }
                    });
                    hls.on(Hls.Events.MANIFEST_PARSED, () => {
                        setTimeout(() => {
                            hls.config.maxBufferLength = 40;
                            hls.config.maxMaxBufferLength = 90;
                        }, 8000);
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
                } else {
                    console.error('[Player] 不支持 FLV 播放');
                }
            }
        },
        type: videoType
    };

    try {
        var art = new Artplayer(config);

        art.on('ready', function() {
            const video = art.video;
            if (video) {
                video.preservesPitch = true;
                video.mozPreservesPitch = true;
                video.webkitPreservesPitch = true;
            }

            // 获取上次播放进度并自动跳转
            var playState = Storage.find(options.sourceKey + options.vodId);
            var lastTime = playState ? playState.lastTime : 0;
            if (lastTime > 5) {
                // 延迟一小会儿执行跳转，确保播放器状态稳定
                art.currentTime = lastTime;
                art.notice.show = `已为您定位到上次播放位置: ${formatTime(lastTime)}`;
            }
        });

        art.on('play', () => {
            art.notice.show = '不要相信视频中出现的任何广告！！！';
            showMsg('不要相信视频中出现的任何广告！！！', 'warning');
        });

        art.on('error', function(error, reconnectTime) {
            console.error('[Player] 播放错误:', error);
        });

        art.on('video:timeupdate', () => {
            // “手动播放”不需要记录历史也不需要同步服务器
            if (options.title === '手动播放') return;

            // 每隔 3 秒本地保存一次
            if (Math.floor(art.currentTime) % 3 === 0) {
                Storage.upsert({
                    id: options.sourceKey + options.vodId,
                    doubanId: options.doubanId,
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
