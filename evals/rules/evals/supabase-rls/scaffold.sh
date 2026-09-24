#!/bin/bash
set -euo pipefail
python3 "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../fixtures" && pwd)/scaffold.py" supabase-rls
