#!/usr/bin/env bash
# 手工验证 IMDb 映射回填的两个上游是否可用。
# 用法：./scripts/diagnose_imdb_upstream.sh 1292052 35267224 ...
# 不传参数时用一批已知有 IMDb ID 的豆瓣 ID 做基准测试。
set -uo pipefail

IDS=("$@")
if [ ${#IDS[@]} -eq 0 ]; then
    # 这三部在 Wikidata 上都挂着 P4529 + P345，是干净的基准样本。
    IDS=(1292052 1291546 1292720)
fi

UA="MoovieBot/1.0 (https://github.com/TwoThreeWang/Moovie)"
ENDPOINT="${WIKIDATA_SPARQL_URL:-https://query.wikidata.org/sparql}"

echo "=== 上游 1：Wikidata SPARQL（批量源）==="
VALUES=""
for id in "${IDS[@]}"; do
    if [[ "$id" =~ ^[0-9]{6,9}$ ]]; then
        VALUES+="\"$id\" "
    else
        echo "  [跳过] $id 不满足 validDoubanID（6-9 位纯数字），代码里会被静默丢弃"
    fi
done

if [ -z "$VALUES" ]; then
    echo "  全部 ID 都不合规 -> wikidataQuery 返回空串 -> Resolve 直接返回空 map 且不报错"
    echo "  这正是日志里 resolved=0 却看不出原因的情况之一"
else
    QUERY="SELECT ?douban ?imdb WHERE { VALUES ?douban { $VALUES} ?item wdt:P4529 ?douban; wdt:P345 ?imdb. }"
    echo "  查询：$QUERY"
    HTTP=$(curl -s -o /tmp/wikidata_out.json -w '%{http_code}' \
        -X POST "$ENDPOINT" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -H "Accept: application/sparql-results+json" \
        -H "User-Agent: $UA" \
        --data-urlencode "query=$QUERY" \
        --data-urlencode "format=json" \
        --max-time 60)
    echo "  HTTP $HTTP"
    if [ "$HTTP" = "200" ]; then
        HITS=$(python3 -c "
import json,sys
d=json.load(open('/tmp/wikidata_out.json'))
b=d['results']['bindings']
print(len(b))
for r in b: print('    命中', r['douban']['value'], '->', r['imdb']['value'])
" 2>/dev/null)
        echo "  命中数：$HITS"
        [ "${HITS%%$'\n'*}" = "0" ] && echo "  >>> 200 但零命中：这些条目在 Wikidata 上没有 P4529+P345 的连通路径"
    else
        echo "  非 200 -> 代码会 return err，本轮不会走兜底源，也不会打这条 Info 日志"
        head -c 400 /tmp/wikidata_out.json; echo
    fi
fi

echo
echo "=== 上游 2：wmdb（逐条兜底源）==="
for id in "${IDS[@]}"; do
    OUT=$(curl -s -o /tmp/wmdb_out.json -w '%{http_code}' \
        -H "Accept: application/json" \
        --max-time 30 "https://api.wmdb.tv/movie/api?id=$id")
    IMDB=$(python3 -c "
import json
try: print(json.load(open('/tmp/wmdb_out.json')).get('imdbId') or '(空)')
except Exception: print('(解析失败)')
" 2>/dev/null)
    printf '  %-10s HTTP %-3s imdbId=%s\n' "$id" "$OUT" "$IMDB"
    [ "$OUT" = "429" ] && echo "    >>> 被限流：limiter.Pause 会让整个进程冷却，下一轮 Allow() 直接返回 false，settled 就是 0"
    sleep 1.2   # 与 defaultIMDbLookupInterval 保持一致
done

echo
echo "=== 结论提示 ==="
echo "  Wikidata 零命中 + wmdb 正常  -> 是覆盖率问题，且 Wikidata 的「查不到」结论被代码丢掉了"
echo "  Wikidata 零命中 + wmdb 429   -> settled=0 的直接原因，队列会原地打转"
echo "  Wikidata 非 200              -> 任务会 return err，根本走不到这条日志"
