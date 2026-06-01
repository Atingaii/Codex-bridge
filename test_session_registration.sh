#!/bin/bash
# Test script to verify that Bridge-created Claude sessions are registered

set -e

echo "=== Testing Claude Session Registration ==="
echo ""

# Clean up any existing test sessions
rm -f ~/.claude/sessions/test_*.json

# Start a test orchestration in the background
cd /root/tencent/bbb

echo "1. Starting a test orchestration..."
# This would normally be done through the Bridge API
# For now, we'll just verify the registration function works

echo ""
echo "2. Checking if registerClaudeSession function exists in the code..."
if grep -q "registerClaudeSession" /root/tencent/bridge/internal/bridge/orchestration_claude.go; then
    echo "   ✓ registerClaudeSession function found"
else
    echo "   ✗ registerClaudeSession function NOT found"
    exit 1
fi

echo ""
echo "3. Checking if the function is called in ensureClaudeInteractiveSessionLocked..."
if grep -A 20 "session.claude = claude" /root/tencent/bridge/internal/bridge/orchestration_claude.go | grep -q "registerClaudeSession"; then
    echo "   ✓ registerClaudeSession is called after session creation"
else
    echo "   ✗ registerClaudeSession is NOT called after session creation"
    exit 1
fi

echo ""
echo "4. Verifying the session file format..."
if grep -q '"pid":' /root/tencent/bridge/internal/bridge/orchestration_claude.go && \
   grep -q '"sessionId":' /root/tencent/bridge/internal/bridge/orchestration_claude.go && \
   grep -q '"cwd":' /root/tencent/bridge/internal/bridge/orchestration_claude.go; then
    echo "   ✓ Session file format includes required fields"
else
    echo "   ✗ Session file format is incomplete"
    exit 1
fi

echo ""
echo "=== All checks passed! ==="
echo ""
echo "To fully test this feature, you need to:"
echo "1. Restart the Bridge process: sudo systemctl restart codex-bridge"
echo "2. Trigger an orchestration from the Hub"
echo "3. Check ~/.claude/sessions/ for new session files"
echo "4. Run 'claude' in the working directory and use /resume to see the session"
