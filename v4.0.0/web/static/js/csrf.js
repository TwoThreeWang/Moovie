(function () {
    'use strict';

    const cookieName = 'csrf_token';
    const headerName = 'X-CSRF-Token';
    const unsafeMethods = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

    function token() {
        const prefix = cookieName + '=';
        const item = document.cookie.split('; ').find(function (part) {
            return part.indexOf(prefix) === 0;
        });
        return item ? decodeURIComponent(item.slice(prefix.length)) : '';
    }

    document.addEventListener('submit', function (event) {
        const form = event.target;
        if (!(form instanceof HTMLFormElement) || !unsafeMethods.has((form.method || 'GET').toUpperCase())) return;
        let input = form.querySelector('input[name="' + cookieName + '"]');
        if (!input) {
            input = document.createElement('input');
            input.type = 'hidden';
            input.name = cookieName;
            form.appendChild(input);
        }
        input.value = token();
    }, true);

    document.addEventListener('htmx:configRequest', function (event) {
        const value = token();
        if (value) event.detail.headers[headerName] = value;
    });

    if (typeof window.fetch === 'function') {
        const originalFetch = window.fetch.bind(window);
        window.fetch = function (input, init) {
            const options = Object.assign({}, init || {});
            const requestMethod = (options.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
            const requestURL = new URL(input instanceof Request ? input.url : input, window.location.href);
            if (unsafeMethods.has(requestMethod) && requestURL.origin === window.location.origin) {
                const headers = new Headers(options.headers || (input instanceof Request ? input.headers : undefined));
                const value = token();
                if (value) headers.set(headerName, value);
                options.headers = headers;
            }
            return originalFetch(input, options);
        };
    }
})();
