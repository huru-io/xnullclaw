#!/usr/bin/env bash
# webhook_poc_test.sh — Manual end-to-end test for nullclaw gateway webhook auth.
#
# Prerequisites:
#   1. An agent must be running with the gateway listening on a mapped port.
#   2. The agent's config.json must have gateway.paired_tokens with a hash.
#   3. The plaintext token must be available in the agent's data/.auth_token.
#
# Usage:
#   ./scripts/webhook_poc_test.sh <agent-name> [xnc-home]
#
# Example:
#   ./scripts/webhook_poc_test.sh alice
#   ./scripts/webhook_poc_test.sh alice ~/.xnc

set -euo pipefail

AGENT="${1:?Usage: $0 <agent-name> [xnc-home]}"
XNC_HOME="${2:-$HOME/.xnc}"
AGENT_DIR="$XNC_HOME/agents/$AGENT"
TOKEN_FILE="$AGENT_DIR/data/.auth_token"
CONFIG_FILE="$AGENT_DIR/config.json"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}PASS${NC} $1"; }
fail() { echo -e "${RED}FAIL${NC} $1"; FAILURES=$((FAILURES + 1)); }
info() { echo -e "${YELLOW}INFO${NC} $1"; }

FAILURES=0

echo "=== Webhook PoC Test ==="
echo "Agent:     $AGENT"
echo "Home:      $XNC_HOME"
echo ""

# --- Check prerequisites ---

if [ ! -d "$AGENT_DIR" ]; then
    echo "ERROR: Agent directory not found: $AGENT_DIR"
    exit 1
fi

if [ ! -f "$CONFIG_FILE" ]; then
    echo "ERROR: Config file not found: $CONFIG_FILE"
    exit 1
fi

# --- Determine port ---

# Find the container's mapped port for 3000/tcp.
INSTANCE_ID=$(cat "$XNC_HOME/.instance_id" 2>/dev/null || echo "")
CONTAINER="xnc-${INSTANCE_ID}-${AGENT}"

PORT=$(docker port "$CONTAINER" 3000/tcp 2>/dev/null | head -1 | cut -d: -f2 || true)
if [ -z "$PORT" ]; then
    echo "ERROR: No port mapping found for container $CONTAINER port 3000."
    echo "       The agent may not have been started with port mapping enabled."
    echo ""
    echo "  To test manually without port mapping, use docker exec:"
    echo "    docker exec $CONTAINER curl -s http://localhost:3000/health"
    exit 1
fi

BASE_URL="http://localhost:$PORT"
info "Gateway URL: $BASE_URL"
echo ""

# --- Test 1: Health check ---

echo "--- Test 1: GET /health ---"
HTTP_CODE=$(curl -s -o /tmp/webhook_poc_health.json -w "%{http_code}" "$BASE_URL/health" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "200" ]; then
    STATUS=$(jq -r '.status' /tmp/webhook_poc_health.json 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "ok" ] || [ "$STATUS" = "degraded" ]; then
        pass "Health check returned $HTTP_CODE, status=$STATUS"
    else
        fail "Health check returned $HTTP_CODE but unexpected status: $STATUS"
    fi
else
    fail "Health check returned HTTP $HTTP_CODE (expected 200)"
    cat /tmp/webhook_poc_health.json 2>/dev/null || true
    echo ""
fi
echo ""

# --- Test 2: Webhook without auth (should be rejected if pairing required) ---

echo "--- Test 2: POST /webhook without auth ---"
HTTP_CODE=$(curl -s -o /tmp/webhook_poc_noauth.txt -w "%{http_code}" \
    -X POST "$BASE_URL/webhook" \
    -H "Content-Type: application/json" \
    -d '{"message":"test without auth"}' 2>/dev/null || echo "000")

# Check if pairing is required.
REQUIRE_PAIRING=$(jq -r '.gateway.require_pairing // false' "$CONFIG_FILE" 2>/dev/null || echo "false")

if [ "$REQUIRE_PAIRING" = "true" ]; then
    if [ "$HTTP_CODE" = "401" ]; then
        pass "Unauthenticated request rejected with 401 (pairing required)"
    else
        fail "Expected 401 for unauthenticated request, got $HTTP_CODE"
    fi
else
    info "Pairing not required — unauthenticated request returned $HTTP_CODE (expected)"
fi
echo ""

# --- Test 3: Webhook with valid token ---

echo "--- Test 3: POST /webhook with valid token ---"
if [ ! -f "$TOKEN_FILE" ]; then
    info "No token file at $TOKEN_FILE — skipping authenticated tests"
    info "Run 'xnc config $AGENT webhook_auth setup' to generate a token"
else
    TOKEN=$(cat "$TOKEN_FILE")
    REDACTED="${TOKEN:0:7}...${TOKEN: -4}"
    info "Using token: $REDACTED"

    HTTP_CODE=$(curl -s -o /tmp/webhook_poc_auth.json -w "%{http_code}" \
        -X POST "$BASE_URL/webhook" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TOKEN" \
        -d '{"message":"Hello from webhook PoC test"}' \
        --max-time 35 2>/dev/null || echo "000")

    if [ "$HTTP_CODE" = "200" ]; then
        pass "Authenticated webhook request accepted ($HTTP_CODE)"
        RESPONSE=$(jq -r '.response // .status // "?"' /tmp/webhook_poc_auth.json 2>/dev/null || echo "?")
        info "Response: $(echo "$RESPONSE" | head -c 200)"
    else
        fail "Authenticated webhook request returned HTTP $HTTP_CODE (expected 200)"
        cat /tmp/webhook_poc_auth.json 2>/dev/null || true
        echo ""
    fi
fi
echo ""

# --- Test 4: Webhook with invalid token ---

echo "--- Test 4: POST /webhook with invalid token ---"
if [ "$REQUIRE_PAIRING" = "true" ]; then
    HTTP_CODE=$(curl -s -o /tmp/webhook_poc_badauth.txt -w "%{http_code}" \
        -X POST "$BASE_URL/webhook" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer zc_0000000000000000000000000000000000000000000000000000000000000000" \
        -d '{"message":"should be rejected"}' 2>/dev/null || echo "000")

    if [ "$HTTP_CODE" = "401" ]; then
        pass "Invalid token rejected with 401"
    else
        fail "Expected 401 for invalid token, got $HTTP_CODE"
    fi
else
    info "Pairing not required — skipping invalid token test"
fi
echo ""

# --- Test 5: Verify token hash in config ---

echo "--- Test 5: Config verification ---"
if [ -f "$TOKEN_FILE" ]; then
    TOKEN=$(cat "$TOKEN_FILE")
    EXPECTED_HASH=$(echo -n "$TOKEN" | sha256sum | awk '{print $1}')
    CONFIG_HASH=$(jq -r '.gateway.paired_tokens[0] // ""' "$CONFIG_FILE" 2>/dev/null || echo "")

    if [ "$CONFIG_HASH" = "$EXPECTED_HASH" ]; then
        pass "Token hash in config matches SHA-256 of stored token"
    elif [ -z "$CONFIG_HASH" ]; then
        fail "No paired_tokens found in config.json"
    else
        fail "Token hash mismatch: config=$CONFIG_HASH expected=$EXPECTED_HASH"
    fi
else
    info "No token file — skipping hash verification"
fi
echo ""

# --- Test 6: Wrong HTTP method ---

echo "--- Test 6: GET /webhook (wrong method) ---"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/webhook" 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "405" ]; then
    pass "GET /webhook rejected with 405 Method Not Allowed"
else
    info "GET /webhook returned $HTTP_CODE (expected 405)"
fi
echo ""

# --- Summary ---

echo "=== Summary ==="
if [ $FAILURES -eq 0 ]; then
    echo -e "${GREEN}All tests passed${NC}"
else
    echo -e "${RED}$FAILURES test(s) failed${NC}"
fi

# Cleanup.
rm -f /tmp/webhook_poc_health.json /tmp/webhook_poc_noauth.txt \
      /tmp/webhook_poc_auth.json /tmp/webhook_poc_badauth.txt

exit $FAILURES
