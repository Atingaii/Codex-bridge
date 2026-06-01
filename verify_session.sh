#!/bin/bash
# 验证会话注册功能

echo "=== 会话注册功能验证 ==="
echo ""

echo "1. 检查新创建的会话文件："
ls -lht ~/.claude/sessions/ | head -5
echo ""

echo "2. 查看最新会话的详细信息："
LATEST_SESSION=$(ls -t ~/.claude/sessions/*.json | head -1)
echo "文件: $LATEST_SESSION"
cat "$LATEST_SESSION" | jq .
echo ""

echo "3. 检查会话对应的 Claude 进程："
SESSION_PID=$(basename "$LATEST_SESSION" .json)
if ps -p "$SESSION_PID" > /dev/null 2>&1; then
    echo "✓ 进程 $SESSION_PID 正在运行"
    ps -p "$SESSION_PID" -o pid,cmd | tail -1
else
    echo "✗ 进程 $SESSION_PID 已结束"
fi
echo ""

echo "4. 会话信息摘要："
cat "$LATEST_SESSION" | jq -r '"  - Session ID: \(.sessionId)\n  - 工作目录: \(.cwd)\n  - 进程 PID: \(.pid)\n  - 会话名称: \(.name // "N/A")"'
echo ""

echo "=== 如何在 Claude Code 中查看这个会话 ==="
echo ""
echo "方法 1: 在工作目录下使用 /resume"
echo "  cd /root/tencent/bbb"
echo "  claude"
echo "  然后输入: /resume"
echo ""
echo "方法 2: 在任何目录下使用 /resume 并搜索"
echo "  claude"
echo "  然后输入: /resume"
echo "  搜索: bbb 或 测试会话"
echo ""
echo "你应该能看到标题为 'Codex Bridge claude orc_...' 的会话"
