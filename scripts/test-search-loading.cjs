// Run with Node and Playwright available (NODE_PATH may point to bundled packages).
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const root = path.join(__dirname, '..');
const read = file => fs.readFileSync(path.join(root, file), 'utf8');
// Exercise the actual search markup, inline controller, stylesheet and HTMX bundle.
const content = read('web/templates/pages/search.html').split('<br>')[0]
    .replace('{{ define "content" }}', '')
    .replace('{{ if .Bypass }}&bypass=1{{ end }}', '&bypass=1')
    .replaceAll('{{ urlquery .Keyword }}', encodeURIComponent('沙丘'))
    .replaceAll('{{ .Keyword }}', '沙丘');
assert(!content.includes('{{'), 'fixture must resolve every template action');
const html = `<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/static/css/style.css">
<script defer src="/static/js/htmx.min.js"></script></head><body>${content}</body></html>`;

(async function () {
    const browser = await chromium.launch({ headless: true });
    try {
        for (const scenario of ['slow-script', 'missing-script', 'success', 'empty', 'offline', '503', 'timeout']) {
            const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
            const page = await context.newPage();
            const errors = [];
            page.on('pageerror', error => errors.push(error.message));
            page.setDefaultTimeout(10000);
            let releaseScript;
            const scriptGate = new Promise(resolve => { releaseScript = resolve; });
            let requests = 0;
            let navigations = 0;
            await page.route('http://moovie.test/**', async route => {
                const url = new URL(route.request().url());
                if (url.pathname === '/search') {
                    navigations++;
                    assert(html.includes('}, 30000);'), 'script startup has a bounded wait');
                    return route.fulfill({ contentType: 'text/html', body: scenario === 'missing-script'
                        ? html.replace('}, 30000);', '}, 1000);') : html });
                }
                if (url.pathname === '/static/css/style.css') {
                    return route.fulfill({ contentType: 'text/css', body: read('web/static/css/style.css') });
                }
                if (url.pathname === '/static/js/htmx.min.js') {
                    if (scenario === 'missing-script') return route.abort('failed');
                    if (scenario === 'slow-script' || scenario === 'timeout') await scriptGate;
                    return route.fulfill({ contentType: 'text/javascript', body: read('web/static/js/htmx.min.js') });
                }
                if (url.pathname === '/api/htmx/search') {
                    requests++;
                    assert.equal(url.searchParams.get('q'), '沙丘');
                    assert.equal(url.searchParams.get('bypass'), '1');
                    if (requests === 1) {
                        if (scenario === 'offline') return route.abort('internetdisconnected');
                        if (scenario === '503') return route.fulfill({ status: 503, body: 'unavailable' });
                        if (scenario === 'timeout') await new Promise(resolve => setTimeout(resolve, 500));
                    }
                    return route.fulfill({ contentType: 'text/html', body: scenario === 'empty'
                        ? '<div class="empty-state">未找到相关资源</div>'
                        : '<div id="search-container">沙丘搜索结果</div>' });
                }
                return route.abort();
            });
            await page.goto('http://moovie.test/search?kw=沙丘&bypass=1', { waitUntil: 'commit' });
            const retry = page.getByRole('button', { name: '重试', exact: true });
            if (scenario === 'slow-script' || scenario === 'timeout') {
                await page.locator('.search-loading-spinner').waitFor({ state: 'visible' });
                assert.equal(requests, 0);
                assert.equal(await retry.isVisible(), false);
                assert.equal(await page.locator('#search-results-container').getAttribute('hx-request'), '{"timeout":45000}');
                if (scenario === 'timeout') {
                    // Keep production's timeout assertion; shorten only this browser fixture's wait.
                    await page.locator('#search-results-container').evaluate(el => el.setAttribute('hx-request', '{"timeout":100}'));
                }
                releaseScript();
            }
            if (scenario === 'missing-script') {
                await page.waitForLoadState('domcontentloaded');
                await page.locator('.search-loading-spinner').waitFor({ state: 'visible' });
                await retry.waitFor({ state: 'visible' });
                assert.equal(requests, 0);
                await Promise.all([page.waitForEvent('framenavigated'), retry.click()]);
                assert.equal(navigations, 2, 'missing HTMX must retry by reloading the page');
            } else {
                if (['offline', '503', 'timeout'].includes(scenario)) {
                    await retry.waitFor({ state: 'visible' });
                    assert.equal(await page.locator('.search-loading-spinner').isVisible(), false);
                    assert.match(await page.getByRole('status').innerText(), scenario === 'timeout' ? /超时/ : /加载失败/);
                    await retry.click();
                }
                await page.locator(scenario === 'empty' ? '.empty-state' : '#search-container').waitFor();
                assert.equal(await page.locator('#search-loading').count(), 0);
                assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
            }
            assert.deepEqual(errors, [], `${scenario}: no uncaught script errors`);
            console.log(`PASS ${scenario}`);
            await context.close();
        }
    } finally {
        await browser.close();
    }
})().catch(error => { console.error(error); process.exitCode = 1; });
