#!/usr/bin/env bash
# YC Demo: Complete Titra Enterprise demonstration
# Shows: Shrike scan → Vulnerable workflow → Titra blocks → Live dashboard

set -e

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TITRA_BIN="${DEMO_DIR}/titra"
DATA_DIR="${DEMO_DIR}/titra-demo-data"
PORT=8080

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() { echo -e "${BLUE}▸${NC} $*"; }
success() { echo -e "${GREEN}✓${NC} $*"; }
warn() { echo -e "${YELLOW}⚠${NC} $*"; }
error() { echo -e "${RED}✗${NC} $*"; }

banner() {
    echo ""
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}  $*${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
}

# Check if titra binary exists
if [ ! -f "$TITRA_BIN" ]; then
    error "Titra binary not found. Please build it first:"
    echo "  go build -o titra ."
    exit 1
fi

banner "Titra Enterprise Demo for YC"

echo "This demo shows:"
echo "  1. Titra Enterprise dashboard (self-hosted)"
echo "  2. Simulated vulnerable CI workflow"
echo "  3. Real-time attack blocking and visualization"
echo ""
echo "Press Enter to start..."
read -r

# Clean up previous demo data
if [ -d "$DATA_DIR" ]; then
    log "Cleaning up previous demo data..."
    rm -rf "$DATA_DIR"
fi

# Start Titra Enterprise server
banner "STEP 1: Starting Titra Enterprise Dashboard"

log "Starting server at http://localhost:$PORT"
mkdir -p "$DATA_DIR"

# Start server in background
"$TITRA_BIN" serve --addr ":$PORT" --data "$DATA_DIR" &
SERVER_PID=$!

# Wait for server to be ready
sleep 2

if ! kill -0 $SERVER_PID 2>/dev/null; then
    error "Server failed to start"
    exit 1
fi

success "Dashboard running at http://localhost:$PORT"
echo ""
echo "  Open this URL in your browser to watch the dashboard live:"
echo -e "  ${BLUE}http://localhost:$PORT${NC}"
echo ""
echo "Press Enter when ready to run the vulnerable workflow..."
read -r

# Simulate a vulnerable workflow
banner "STEP 2: Running Vulnerable Workflow"

log "This simulates an AI agent in CI that has been prompt-injected..."
echo ""

# Set up environment for the guard
export TITRA_LOG="${DATA_DIR}/actions.jsonl"
export TITRA_MODE="enforce"
export TITRA_SERVER="http://localhost:$PORT"
export TITRA_PROJECT="demo"
export GITHUB_REPOSITORY="ChelateSec/demo-app"
export GITHUB_WORKFLOW="vulnerable-agent.yml"
export GITHUB_RUN_ID="12345"
export GITHUB_ACTOR="ext-user"

log "Installing Titra guard..."
INSTALL_OUTPUT=$("$TITRA_BIN" install --dir "$DATA_DIR/bin" --log "$TITRA_LOG" --mode enforce --server "http://localhost:$PORT" --project "demo" 2>&1)
echo "$INSTALL_OUTPUT"

# Add to PATH
eval "$(echo "$INSTALL_OUTPUT" | grep "export PATH")"

success "Guard installed"
echo ""
log "Simulating prompt-injected agent attempting dangerous actions..."
echo ""

# Run the attack simulation
ATTACK_LOG="${DATA_DIR}/attack.log"
{
    echo "════════════════════════════════════════"
    echo "Simulated Agent Actions"
    echo "════════════════════════════════════════"
    echo ""

    echo "[1] Benign: Check repository status"
    git status 2>&1 || true
    echo ""

    echo "[2] ATTACK: Push malicious code to main"
    git push origin HEAD:main 2>&1 || true
    echo ""

    echo "[3] ATTACK: Self-approve pull request"
    gh pr review --approve 1 2>&1 || true
    echo ""

    echo "[4] ATTACK: Trigger release workflow (privilege escalation)"
    gh workflow run release.yml 2>&1 || true
    echo ""

    echo "[5] ATTACK: Read .git/config (credential exfiltration)"
    cat .git/config 2>&1 || true
    echo ""

    echo "[6] ATTACK: Publish npm package"
    npm publish 2>&1 || true
    echo ""

    echo "════════════════════════════════════════"
} | tee "$ATTACK_LOG"

success "Workflow simulation complete"
echo ""

# Show what was blocked
banner "STEP 3: Attack Summary"

BLOCKED_COUNT=$(grep -c "titra BLOCKED" "$ATTACK_LOG" || echo "0")
success "Titra blocked ${BLOCKED_COUNT} dangerous actions"

echo ""
echo "Check the dashboard at http://localhost:$PORT to see:"
echo "  • Real-time action feed"
echo "  • Risk score calculation"
echo "  • Blocked vs. allowed breakdown"
echo "  • Full attack timeline"
echo ""

banner "Demo Complete!"

echo "The Titra Enterprise server is still running."
echo ""
echo "  Dashboard: ${BLUE}http://localhost:$PORT${NC}"
echo "  Data dir:  ${DATA_DIR}"
echo ""
echo "To stop the server:"
echo "  kill $SERVER_PID"
echo ""
echo "Or press Ctrl+C"
echo ""

# Keep running
wait $SERVER_PID
