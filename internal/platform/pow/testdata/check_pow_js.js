// 用 Python hashlib 算出的固定向量校验前端 pow.js，由 pow_js_test.go 调用：
//   node check_pow_js.js <pow.js 路径> <vectors.json 路径>
// 全部通过时打印 ok 并以 0 退出，否则打印第一处不一致并以 1 退出。
'use strict';
const fs = require('fs');
const path = require('path');

const pow = require(path.resolve(process.argv[2]));
const vectors = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

function fail(message) {
    console.error(message);
    process.exit(1);
}

for (const [input, want] of Object.entries(vectors.hashes)) {
    const got = pow.sha256Hex(input);
    if (got !== want) fail(`sha256(${JSON.stringify(input.slice(0, 40))}) = ${got}, want ${want}`);
}

for (const solution of vectors.solutions) {
    const nonce = pow.solveRange(vectors.salt, solution.bits, 0, 0x7fffffff);
    if (String(nonce) !== solution.nonce) fail(`solve ${solution.bits} bits = ${nonce}, want ${solution.nonce}`);
    if (pow.sha256Hex(`${vectors.salt}:${nonce}`) !== solution.hash) fail(`hash of solution ${nonce} mismatch`);
}

// 超长前缀会走通用（多块）实现，结果必须和单块快速路径一致。
const longSalt = 'f'.repeat(60);
const fast = pow.solveRange(longSalt, 8, 0, 100000);
let expected = -1;
for (let nonce = 0; nonce < 100000; nonce++) {
    if (pow.sha256Hex(`${longSalt}:${nonce}`).startsWith('00')) { expected = nonce; break; }
}
if (fast !== expected) fail(`long salt solve = ${fast}, want ${expected}`);

const parsed = pow.parseToken('login.1731300000000.20.abcdef.sig');
if (!parsed || parsed.salt !== 'abcdef' || parsed.bits !== 20) fail('parseToken failed');
if (pow.parseToken('a.b.c') !== null || pow.parseToken('login.1.99.salt.sig') !== null) fail('parseToken accepted bad token');

console.log('ok');
