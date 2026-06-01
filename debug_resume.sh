#!/bin/bash
# 诊断 /resume 看不到会话的问题

echo "=== 诊断 Claude /resume 问题 ==="
echo ""

echo "1. 检查所有会话文件："
for f in ~/.claude/sessions/*.json; do
    if [ -f "$f" ]; then
        echo "---"
        echo "文件: $(basename $f)"
        cat "$f" | jq -c '{pid, cwd, entrypoint, kind, status, name: (.name // "N/A")}'
    fi
done
echo ""

echo "2. 检查 /root/tencent/bbb 目录下的会话："
echo "当前目录的会话应该显示在 /resume 的顶部"
for f in ~/.claude/sessions/*.json; do
    if [ -f "$f" ]; then
        CWD=$(cat "$f" | jq -r '.cwd')
        if [ "$CWD" = "/root/tencent/bbb" ]; then
            echo "✓ 找到匹配的会话: $(basename $f)"
            cat "$f" | jq .
        fi
    fi
done
echo ""

echo "3. 检查进程是否还在运行："
for f in ~/.claude/sessions/*.json; do
    if [ -f "$f" ]; then
        PID=$(basename "$f" .json)
        CWD=$(cat "$f" | jq -r '.cwd')
        if [ "$CWD" = "/root/tencent/bbb" ]; then
            if ps -p "$PID" > /dev/null 2>&1; then
                echo "✓ 进程 $PID 正在运行"
            else
                echo "✗ 进程 $PID 已结束（会话文件应该被清理）"
            fi
        fi
    fi
done
echo ""

echo "4. 可能的问题："
echo "  - 如果 entrypoint 是 'sdk-cli'，/resume 可能会过滤掉"
echo "  - 如果进程已结束，会话不会显示"
echo "  - 如果 kind 不是 'interactive'，可能不会显示"
echo "  - 如果没有 status 字段，可能不会显示"
