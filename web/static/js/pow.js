/*
 * 表单人机校验：工作量证明（PoW）求解器。
 *
 * 规则与服务端 internal/platform/pow 一致：找一个十进制 nonce，
 * 使 SHA-256(salt + ":" + nonce) 的前 bits 位为 0。
 *
 * 同一个文件有三种运行方式：
 *   - 页面里：接管带 pow_token 字段的表单，页面加载就开始算；
 *   - Web Worker 里：页面用本文件地址启动 Worker，在后台线程求解，不卡界面；
 *   - Node 里：导出纯函数，供 go test 用固定向量校验算法。
 *
 * 不用 crypto.subtle：它是逐个异步哈希，一百万次的 Promise 开销远大于计算本身，
 * 而且只在 HTTPS 下可用，本地开发和内网访问会直接失效。
 */
(function () {
    'use strict';

    // ------------------------------------------------------------ SHA-256
    var K = new Uint32Array([
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
        0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
        0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
        0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
        0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
        0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
    ]);
    var IV = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
    var W = new Int32Array(64);

    // compress 用 64 字节的块更新 state（8 个 32 位字）。block 是 16 个大端字。
    function compress(state, block) {
        var i, s0, s1, t1, t2;
        for (i = 0; i < 16; i++) W[i] = block[i];
        for (i = 16; i < 64; i++) {
            var w15 = W[i - 15], w2 = W[i - 2];
            s0 = ((w15 >>> 7) | (w15 << 25)) ^ ((w15 >>> 18) | (w15 << 14)) ^ (w15 >>> 3);
            s1 = ((w2 >>> 17) | (w2 << 15)) ^ ((w2 >>> 19) | (w2 << 13)) ^ (w2 >>> 10);
            W[i] = (W[i - 16] + s0 + W[i - 7] + s1) | 0;
        }
        var a = state[0], b = state[1], c = state[2], d = state[3];
        var e = state[4], f = state[5], g = state[6], h = state[7];
        for (i = 0; i < 64; i++) {
            s1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
            t1 = (h + s1 + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
            s0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
            t2 = (s0 + ((a & b) ^ (a & c) ^ (b & c))) | 0;
            h = g; g = f; f = e; e = (d + t1) | 0;
            d = c; c = b; b = a; a = (t1 + t2) | 0;
        }
        state[0] = (state[0] + a) | 0; state[1] = (state[1] + b) | 0;
        state[2] = (state[2] + c) | 0; state[3] = (state[3] + d) | 0;
        state[4] = (state[4] + e) | 0; state[5] = (state[5] + f) | 0;
        state[6] = (state[6] + g) | 0; state[7] = (state[7] + h) | 0;
    }

    // utf8 把字符串编码成字节数组。
    function utf8(text) {
        if (typeof TextEncoder !== 'undefined') return new TextEncoder().encode(text);
        var encoded = unescape(encodeURIComponent(text)), bytes = new Uint8Array(encoded.length);
        for (var i = 0; i < encoded.length; i++) bytes[i] = encoded.charCodeAt(i);
        return bytes;
    }

    // digest 计算任意长度字节数组的 SHA-256，返回 8 个 32 位字。
    function digest(bytes) {
        var length = bytes.length;
        var blocks = Math.ceil((length + 9) / 64);
        var padded = new Uint8Array(blocks * 64);
        padded.set(bytes);
        padded[length] = 0x80;
        var bitLength = length * 8;
        // 输入远小于 2^32 位，高 32 位长度恒为 0。
        padded[padded.length - 4] = bitLength >>> 24;
        padded[padded.length - 3] = bitLength >>> 16;
        padded[padded.length - 2] = bitLength >>> 8;
        padded[padded.length - 1] = bitLength;
        var state = new Int32Array(IV), block = new Int32Array(16);
        for (var offset = 0; offset < padded.length; offset += 64) {
            for (var j = 0; j < 16; j++) {
                var p = offset + j * 4;
                block[j] = (padded[p] << 24) | (padded[p + 1] << 16) | (padded[p + 2] << 8) | padded[p + 3];
            }
            compress(state, block);
        }
        return state;
    }

    function sha256Hex(text) {
        var state = digest(utf8(text)), hex = '';
        for (var i = 0; i < 8; i++) hex += ('00000000' + (state[i] >>> 0).toString(16)).slice(-8);
        return hex;
    }

    // leadingZeroBits 返回摘要开头连续 0 位的个数。
    function leadingZeroBits(state) {
        var count = 0;
        for (var i = 0; i < 8; i++) {
            if (state[i] === 0) { count += 32; continue; }
            return count + Math.clz32(state[i]);
        }
        return count;
    }

    // ------------------------------------------------------------ 求解
    // solveRange 在 [start, end) 内找第一个满足难度的 nonce，找不到返回 -1。
    // salt + ":" + nonce 不超过 55 字节时只需压缩一个块，这里把前缀写进一个复用的块里，
    // 每次只改 nonce 的几个数字，省掉逐次分配和填充的开销；更长的输入退回通用实现。
    function solveRange(salt, bits, start, end) {
        var prefix = utf8(salt + ':');
        var nonce, digits;
        if (prefix.length + 16 > 55) {
            for (nonce = start; nonce < end; nonce++) {
                var bytes = new Uint8Array(prefix.length + String(nonce).length);
                bytes.set(prefix);
                digits = String(nonce);
                for (var k = 0; k < digits.length; k++) bytes[prefix.length + k] = digits.charCodeAt(k);
                if (leadingZeroBits(digest(bytes)) >= bits) return nonce;
            }
            return -1;
        }
        var buffer = new Uint8Array(64), block = new Int32Array(16), state = new Int32Array(8);
        buffer.set(prefix);
        for (nonce = start; nonce < end; nonce++) {
            digits = String(nonce);
            var length = prefix.length + digits.length, i;
            for (i = 0; i < digits.length; i++) buffer[prefix.length + i] = digits.charCodeAt(i);
            buffer[length] = 0x80;
            for (i = length + 1; i < 60; i++) buffer[i] = 0;
            var bitLength = length * 8;
            buffer[60] = 0; buffer[61] = 0; buffer[62] = bitLength >>> 8; buffer[63] = bitLength;
            for (i = 0; i < 16; i++) {
                var p = i * 4;
                block[i] = (buffer[p] << 24) | (buffer[p + 1] << 16) | (buffer[p + 2] << 8) | buffer[p + 3];
            }
            for (i = 0; i < 8; i++) state[i] = IV[i];
            compress(state, block);
            if (leadingZeroBits(state) >= bits) return nonce;
        }
        return -1;
    }

    // parseToken 从 scope.签发毫秒.bits.salt.签名 中取出 salt 和 bits。
    function parseToken(token) {
        var parts = String(token || '').split('.');
        if (parts.length !== 5) return null;
        var bits = parseInt(parts[2], 10);
        if (!(bits >= 1 && bits <= 28) || !parts[3]) return null;
        return { salt: parts[3], bits: bits };
    }

    var MAX_NONCE = 0x7fffffff;
    var api = { sha256Hex: sha256Hex, solveRange: solveRange, parseToken: parseToken, leadingZeroBits: leadingZeroBits };

    // ------------------------------------------------------------ Node
    if (typeof module === 'object' && module.exports) {
        module.exports = api;
        return;
    }

    // ------------------------------------------------------------ Worker
    if (typeof document === 'undefined' && typeof importScripts === 'function') {
        self.onmessage = function (event) {
            var data = event.data || {};
            var started = Date.now();
            var nonce = solveRange(data.salt, data.bits, 0, MAX_NONCE);
            self.postMessage({ id: data.id, nonce: nonce, elapsed: Date.now() - started });
        };
        return;
    }

    // ------------------------------------------------------------ 页面
    if (window.MooviePow) return; // 防止脚本被重复引入时重复接管提交
    window.MooviePow = api;

    var scriptURL = document.currentScript && document.currentScript.src;
    // 签发到提交至少间隔 2 秒，服务端更快的提交会被当成脚本；前端多留 100ms 余量。
    var MIN_WAIT_MS = 2100;
    var CHECKING_TEXT = '正在进行安全校验…';
    var states = typeof WeakMap === 'function' ? new WeakMap() : null;
    var nextID = 1;

    function solveInWorker(challenge, done) {
        var worker;
        try {
            if (typeof Worker !== 'function' || !scriptURL) throw new Error('no worker');
            worker = new Worker(scriptURL);
        } catch (error) {
            solveOnMainThread(challenge, done);
            return;
        }
        var id = nextID++;
        worker.onmessage = function (event) {
            if (!event.data || event.data.id !== id) return;
            worker.terminate();
            done(event.data.nonce);
        };
        worker.onerror = function (event) {
            if (event && event.preventDefault) event.preventDefault();
            worker.terminate();
            solveOnMainThread(challenge, done);
        };
        worker.postMessage({ id: id, salt: challenge.salt, bits: challenge.bits });
    }

    // solveOnMainThread 是不支持 Worker 时的兜底：分批计算，每批之间让出主线程。
    function solveOnMainThread(challenge, done) {
        var batch = 20000, start = 0;
        function step() {
            var nonce = solveRange(challenge.salt, challenge.bits, start, Math.min(start + batch, MAX_NONCE));
            if (nonce >= 0) { done(nonce); return; }
            start += batch;
            if (start >= MAX_NONCE) { done(-1); return; }
            setTimeout(step, 0);
        }
        setTimeout(step, 0);
    }

    function submitButton(form, submitter) {
        return submitter || form.querySelector('button[type="submit"], input[type="submit"], button:not([type])');
    }

    function setChecking(state, button) {
        if (!button || state.button) return;
        state.button = button;
        state.buttonText = button.tagName === 'INPUT' ? button.value : button.textContent;
        state.buttonDisabled = button.disabled;
        if (button.tagName === 'INPUT') button.value = CHECKING_TEXT; else button.textContent = CHECKING_TEXT;
        button.disabled = true;
    }

    function restoreButton(state) {
        var button = state.button;
        if (!button) return;
        if (button.tagName === 'INPUT') button.value = state.buttonText; else button.textContent = state.buttonText;
        button.disabled = state.buttonDisabled;
        state.button = null;
    }

    function ready(state) {
        return state.nonce !== null && Date.now() >= state.readyAt;
    }

    // resubmit 在条件满足后替用户重新提交。requestSubmit 会再次触发 submit 事件，
    // csrf.js 和 htmx 都靠这个事件工作；老浏览器没有它时手动补发事件再 submit。
    function resubmit(form, state) {
        restoreButton(state);
        state.passThrough = true;
        try {
            if (typeof form.requestSubmit === 'function') {
                form.requestSubmit(state.submitter && state.submitter.form === form ? state.submitter : undefined);
            } else {
                var event = document.createEvent('Event');
                event.initEvent('submit', true, true);
                if (form.dispatchEvent(event)) form.submit();
            }
        } finally {
            state.passThrough = false;
            state.submitter = null;
        }
    }

    function finish(form, state) {
        state.pending = false;
        if (state.nonce === -1) {
            // 理论上不会发生；放行让服务端给出明确的错误并换一个新挑战。
            restoreButton(state);
            return;
        }
        resubmit(form, state);
    }

    function waitAndSubmit(form, state) {
        if (state.timer) return;
        (function check() {
            state.timer = null;
            if (form.isConnected === false) return; // 表单已被 htmx 换掉
            if (state.nonce === null) return; // 求解完成时会再次调用
            var remaining = state.readyAt - Date.now();
            if (remaining > 0) { state.timer = setTimeout(check, remaining); return; }
            finish(form, state);
        })();
    }

    function stateOf(form) {
        return states ? states.get(form) : form.__mooviePow;
    }

    function init(form) {
        if (!(form instanceof HTMLFormElement) || stateOf(form)) return;
        var tokenInput = form.querySelector('input[name="pow_token"]');
        var nonceInput = form.querySelector('input[name="pow_nonce"]');
        if (!tokenInput || !nonceInput) return;
        var challenge = parseToken(tokenInput.value);
        var state = {
            nonce: null, readyAt: Date.now() + MIN_WAIT_MS, pending: false, passThrough: false,
            button: null, submitter: null, timer: null, nonceInput: nonceInput
        };
        if (states) states.set(form, state); else form.__mooviePow = state;
        if (!challenge) { state.nonce = -1; return; }
        solveInWorker(challenge, function (nonce) {
            state.nonce = nonce;
            if (nonce >= 0) nonceInput.value = String(nonce);
            if (state.pending) waitAndSubmit(form, state);
        });
    }

    function scan(root) {
        if (!root) return;
        if (root instanceof HTMLFormElement) { init(root); return; }
        if (!root.querySelectorAll) return;
        var forms = root.querySelectorAll('form');
        for (var i = 0; i < forms.length; i++) init(forms[i]);
    }

    // 在捕获阶段拦截提交：校验没完成或页面打开不足 2.1 秒（比如密码管理器自动填充后
    // 立刻回车）时先拦下，按钮显示「正在进行安全校验…」，条件满足后自动提交。
    // stopImmediatePropagation 让 htmx 等后续监听器这一轮完全看不到这次提交。
    document.addEventListener('submit', function (event) {
        var form = event.target;
        var state = form instanceof HTMLFormElement ? stateOf(form) : null;
        if (!state) return;
        if (state.used && !state.passThrough) {
            // 一个挑战只能用一次：重复点击提交只会换来「安全校验失败」，直接忽略。
            event.preventDefault();
            event.stopImmediatePropagation();
            return;
        }
        if (state.passThrough || (ready(state) && !state.pending)) {
            if (state.nonce >= 0) state.nonceInput.value = String(state.nonce);
            state.used = state.nonce >= 0;
            return;
        }
        event.preventDefault();
        event.stopImmediatePropagation();
        if (state.pending) return;
        state.pending = true;
        state.submitter = event.submitter || null;
        setChecking(state, submitButton(form, event.submitter));
        waitAndSubmit(form, state);
    }, true);

    // htmx 换进来的新表单（例如反馈提交失败后带着新挑战重新渲染）也要接管。
    document.addEventListener('htmx:load', function (event) { scan(event.detail && event.detail.elt); });

    // 从往返缓存（后退/前进）恢复的页面里，挑战可能已经用掉或过期，重新加载拿新挑战。
    window.addEventListener('pageshow', function (event) {
        if (event.persisted && document.querySelector('input[name="pow_token"]')) window.location.reload();
    });

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function () { scan(document); });
    } else {
        scan(document);
    }
})();
