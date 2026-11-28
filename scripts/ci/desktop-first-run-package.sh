#!/bin/bash
# Build the unsigned, unpacked Linux development artifact before its AppArmor profile is loaded.
# Linux is supplementary first-run evidence, not a shipping desktop target.
set -euo pipefail
source "$(dirname "$0")/common.sh"
export PERSONA_DESKTOP_APP_ID=com.example.persona.desktop
export CSC_IDENTITY_AUTO_DISCOVERY=false
ci_run 'Desktop unsigned Linux unpacked first-run package' 1500 npm --prefix desktop run dist:unsigned -- --linux dir --x64 --publish never
