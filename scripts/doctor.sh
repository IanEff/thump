#!/usr/bin/env bash
set -uo pipefail

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD="\033[1m"
  GREEN="\033[32m"
  YELLOW="\033[33m"
  RED="\033[31m"
  DIM="\033[2m"
  RESET="\033[0m"
else
  BOLD=""
  GREEN=""
  YELLOW=""
  RED=""
  DIM=""
  RESET=""
fi

has_cmd() {
  command -v "$1" >/dev/null 2>&1
}

CORE_ERRORS=0
OPTIONAL_WARNS=0

echo -e "${BOLD}thump environment doctor${RESET}"
echo -e "${DIM}Checking prerequisites for hermetic development and live cluster rig...${RESET}\n"

echo -e "${BOLD}1. Core Hermetic Development (task ci, unit & integration tests)${RESET}"

# Go
if has_cmd go; then
  GO_VER=$(go version | awk '{print $3}')
  echo -e "  [${GREEN}OK${RESET}] go ($GO_VER)"
else
  echo -e "  [${RED}MISSING${RESET}] go — install Go >= 1.25 (https://go.dev/dl/)"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

# gofmt
if has_cmd gofmt; then
  echo -e "  [${GREEN}OK${RESET}] gofmt"
else
  echo -e "  [${RED}MISSING${RESET}] gofmt (bundled with Go)"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

# golangci-lint
if has_cmd golangci-lint; then
  LINT_VER=$(golangci-lint --version 2>/dev/null | awk '{print $4}' || echo "found")
  echo -e "  [${GREEN}OK${RESET}] golangci-lint ($LINT_VER)"
else
  echo -e "  [${RED}MISSING${RESET}] golangci-lint — brew install golangci-lint"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

# helm
if has_cmd helm; then
  HELM_VER=$(helm version --short 2>/dev/null || echo "found")
  echo -e "  [${GREEN}OK${RESET}] helm ($HELM_VER)"
else
  echo -e "  [${RED}MISSING${RESET}] helm — brew install helm"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

# promtool
if has_cmd promtool; then
  echo -e "  [${GREEN}OK${RESET}] promtool"
else
  echo -e "  [${RED}MISSING${RESET}] promtool — brew install prometheus (needed for SLO rule tests)"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

# yq
if has_cmd yq; then
  YQ_VER=$(yq --version 2>/dev/null | awk '{print $NF}' || echo "found")
  echo -e "  [${GREEN}OK${RESET}] yq ($YQ_VER)"
else
  echo -e "  [${RED}MISSING${RESET}] yq — brew install yq (needed for promql test runner)"
  CORE_ERRORS=$((CORE_ERRORS + 1))
fi

echo ""
echo -e "${BOLD}2. Live Rig & Tilt Substrate (task dev:up, chaos tests)${RESET}"

# k3d
if has_cmd k3d; then
  K3D_VER=$(k3d version 2>/dev/null | head -n1 | awk '{print $3}' || echo "found")
  echo -e "  [${GREEN}OK${RESET}] k3d ($K3D_VER)"
else
  echo -e "  [${YELLOW}WARN${RESET}] k3d missing — brew install k3d (optional for hermetic dev)"
  OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
fi

# tilt
if has_cmd tilt; then
  TILT_VER=$(tilt version 2>/dev/null | head -n1 || echo "found")
  echo -e "  [${GREEN}OK${RESET}] tilt ($TILT_VER)"
else
  echo -e "  [${YELLOW}WARN${RESET}] tilt missing — brew install tilt-dev/tap/tilt (optional for hermetic dev)"
  OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
fi

# kubectl
if has_cmd kubectl; then
  echo -e "  [${GREEN}OK${RESET}] kubectl"
else
  echo -e "  [${YELLOW}WARN${RESET}] kubectl missing — brew install kubectl (optional for hermetic dev)"
  OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
fi

# Docker / Container runtime
if has_cmd docker; then
  if docker info >/dev/null 2>&1; then
    MEM_TOTAL_BYTES=$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0)
    NCPU=$(docker info --format '{{.NCPU}}' 2>/dev/null || echo 0)
    MEM_GB=$(( MEM_TOTAL_BYTES / 1024 / 1024 / 1024 ))
    if [ "$MEM_GB" -ge 12 ]; then
      echo -e "  [${GREEN}OK${RESET}] docker daemon running (${MEM_GB} GB RAM, ${NCPU} CPUs)"
    else
      echo -e "  [${YELLOW}WARN${RESET}] docker daemon running with ${MEM_GB} GB RAM / ${NCPU} CPUs (recommend >= 12 GB / 6 CPUs for dev substrate)"
      OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
    fi
  else
    echo -e "  [${YELLOW}WARN${RESET}] docker command exists but daemon is not running"
    OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
  fi
else
  echo -e "  [${YELLOW}WARN${RESET}] docker missing — install Docker Desktop or OrbStack (optional for hermetic dev)"
  OPTIONAL_WARNS=$((OPTIONAL_WARNS + 1))
fi

echo ""
echo -e "${BOLD}3. Secrets & API Keys (.env)${RESET}"

if [ -f .env ]; then
  echo -e "  [${GREEN}OK${RESET}] .env file found"
  if grep -q "^ANTHROPIC_API_KEY=" .env && ! grep -q "^ANTHROPIC_API_KEY=.*your.*key" .env && ! grep -q "^ANTHROPIC_API_KEY=\"\"" .env && ! grep -q "^ANTHROPIC_API_KEY=''" .env && [ -n "$(grep "^ANTHROPIC_API_KEY=" .env | cut -d= -f2-)" ]; then
    echo -e "  [${GREEN}OK${RESET}] ANTHROPIC_API_KEY configured (enables task eval and live clank)"
  else
    echo -e "  [${DIM}INFO${RESET}] ANTHROPIC_API_KEY unset (task eval / live reasoning will skip)"
  fi
  if grep -q "^THUMP_SEAL_KEY=" .env && ! grep -q "^THUMP_SEAL_KEY=\"\"" .env && ! grep -q "^THUMP_SEAL_KEY=''" .env && [ -n "$(grep "^THUMP_SEAL_KEY=" .env | cut -d= -f2-)" ]; then
    echo -e "  [${GREEN}OK${RESET}] THUMP_SEAL_KEY configured (enables WAL unseal and corpus mining)"
  else
    echo -e "  [${DIM}INFO${RESET}] THUMP_SEAL_KEY unset (auto-generated on first tilt up or needed for unseal)"
  fi
  if grep -q "^THUMP_NATS_JS_KEY=" .env && ! grep -q "^THUMP_NATS_JS_KEY=\"\"" .env && ! grep -q "^THUMP_NATS_JS_KEY=''" .env && [ -n "$(grep "^THUMP_NATS_JS_KEY=" .env | cut -d= -f2-)" ]; then
    echo -e "  [${GREEN}OK${RESET}] THUMP_NATS_JS_KEY configured (enables persistent NATS storage encryption)"
  else
    echo -e "  [${DIM}INFO${RESET}] THUMP_NATS_JS_KEY unset (auto-generated on first tilt up)"
  fi
else
  echo -e "  [${DIM}INFO${RESET}] .env file not present (cp .env.example .env when testing live mode)"
fi

echo ""
echo -e "${BOLD}Summary:${RESET}"
if [ $CORE_ERRORS -eq 0 ]; then
  echo -e "  Hermetic Development (task ci): ${GREEN}READY${RESET}"
else
  echo -e "  Hermetic Development (task ci): ${RED}INCOMPLETE${RESET} ($CORE_ERRORS required tool(s) missing)"
fi

if [ $OPTIONAL_WARNS -eq 0 ]; then
  echo -e "  Live Dev Rig (task dev:up):     ${GREEN}READY${RESET}"
else
  echo -e "  Live Dev Rig (task dev:up):     ${YELLOW}OPTIONAL STEPS PENDING${RESET}"
fi

echo ""
if [ $CORE_ERRORS -gt 0 ]; then
  echo -e "${RED}Run the install commands listed above to unblock task ci.${RESET}"
  exit 1
fi
exit 0
