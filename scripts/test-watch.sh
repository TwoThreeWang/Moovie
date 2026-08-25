#!/bin/sh
# 测试守护脚本：让协作方（无法在本机执行命令）也能触发测试。
#
# 用法：在 v4.0.0 目录下运行 ./scripts/test-watch.sh，然后一直开着。
#
# 机制：检测到 .run-tests 文件就把它的内容当作 go test 的包参数执行一次，
# 结果写入 .test-out.txt，末尾追加 ===DONE=== 作为完成标记。
# .run-tests 内容为空时跑 ./...
set -u
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
echo "test-watch 已启动，监视 $(pwd)/.run-tests（Ctrl-C 退出）"

while true; do
    if [ -f .run-tests ]; then
        packages=$(cat .run-tests 2>/dev/null)
        rm -f .run-tests
        [ -z "$packages" ] && packages="./..."
        echo "$(date '+%H:%M:%S') 运行 go test -count=1 -p 1 $packages"
        {
            echo "=== go test -count=1 -p 1 $packages ==="
            go test -count=1 -p 1 $packages 2>&1
            echo "===DONE==="
        } > .test-out.txt
        echo "$(date '+%H:%M:%S') 完成，输出见 .test-out.txt"
    fi
    sleep 2
done
