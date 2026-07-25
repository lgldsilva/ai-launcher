#!/usr/bin/env bash
# publish-sonar-pr-artifacts.sh — comment + downloadable PR attachments for Sonar reports.
#
# Source of truth: ai-standards (scripts/sonar/).
#
# Publishes sonar-out/* to the open PR via Gitea API:
#   1) markdown summary comment
#   2) issue assets + comment assets (JSON/MD/TXT/PDF/ZIP)
#
# Required:
#   GITEA_TOKEN | GITHUB_TOKEN
#   GITEA_SERVER_URL | GITHUB_SERVER_URL
#   GITEA_REPOSITORY | GITHUB_REPOSITORY   (owner/repo)
#   PR_NUMBER
# Optional:
#   REPORT_DIR [./sonar-out]
#   SHORT_SHA  [unknown]
#   COMMENT_MARKER [ai-standards-sonar-report]
set -euo pipefail

TOKEN="${GITEA_TOKEN:-${GITHUB_TOKEN:-}}"
SERVER="${GITEA_SERVER_URL:-${GITHUB_SERVER_URL:-}}"
REPO="${GITEA_REPOSITORY:-${GITHUB_REPOSITORY:-}}"
PR_NUMBER="${PR_NUMBER:-}"
REPORT_DIR="${REPORT_DIR:-$PWD/sonar-out}"
SHORT_SHA="${SHORT_SHA:-unknown}"
COMMENT_MARKER="${COMMENT_MARKER:-ai-standards-sonar-report}"

if [ -z "$TOKEN" ] || [ -z "$SERVER" ] || [ -z "$REPO" ] || [ -z "$PR_NUMBER" ]; then
  echo "→ skip PR publish (missing TOKEN/SERVER/REPO/PR_NUMBER)"
  exit 0
fi
if [ ! -d "$REPORT_DIR" ]; then
  echo "→ skip PR publish (no $REPORT_DIR)"
  exit 0
fi

export REPORT_DIR PR_NUMBER SHORT_SHA SERVER REPO TOKEN COMMENT_MARKER

ZIP_NAME="sonar-report-pr${PR_NUMBER}-${SHORT_SHA:0:8}.zip"
ZIP_PATH="$REPORT_DIR/$ZIP_NAME"
(
  cd "$REPORT_DIR"
  if command -v zip >/dev/null 2>&1; then
    zip -q -r "$ZIP_NAME" \
      sonar-report.json sonar-report.md sonar-report.txt sonar-report.pdf \
      2>/dev/null || zip -q -r "$ZIP_NAME" sonar-report.json sonar-report.md sonar-report.txt
  else
    ZIP_NAME="sonar-report-pr${PR_NUMBER}-${SHORT_SHA:0:8}.tar.gz"
    ZIP_PATH="$REPORT_DIR/$ZIP_NAME"
    # shellcheck disable=SC2046
    tar -czf "$ZIP_NAME" $(ls sonar-report.json sonar-report.md sonar-report.txt sonar-report.pdf 2>/dev/null)
  fi
)
echo "→ bundle: $ZIP_PATH ($(wc -c <"$ZIP_PATH" | tr -d ' ') bytes)"

PAYLOAD="$(
  python3 - <<'PY'
import json, os
from pathlib import Path

report_dir = Path(os.environ["REPORT_DIR"])
marker = os.environ.get("COMMENT_MARKER", "ai-standards-sonar-report")
md = report_dir / "sonar-report.md"
text = md.read_text(encoding="utf-8") if md.exists() else "_No markdown report generated._"
lines = text.splitlines()
head = "\n".join(lines[:100])
if len(lines) > 100:
    head += f"\n\n---\n_Truncated ({len(lines)} lines). Download full reports from attachments._\n"

names = []
for n in ("sonar-report.json", "sonar-report.md", "sonar-report.txt", "sonar-report.pdf"):
    p = report_dir / n
    if p.exists():
        names.append(f"- `{n}` ({p.stat().st_size} bytes)")
for p in sorted(report_dir.glob("sonar-report-pr*.zip")) + sorted(report_dir.glob("sonar-report-pr*.tar.gz")):
    names.append(f"- `{p.name}` ({p.stat().st_size} bytes) — **bundle**")
files_block = "\n".join(names) if names else "_No report files found._"

body = (
    f"<!-- {marker} -->\n"
    "## SonarQube report (persisted + artifacts)\n\n"
    "The ephemeral Sonar project is deleted after analysis. "
    "Findings are exported and **attached to this PR** as downloadable artifacts.\n\n"
    "### Attachments\n\n"
    f"{files_block}\n\n"
    "### Summary\n\n"
    + head
)
print(json.dumps({"body": body}))
PY
)"

API_BASE="${SERVER%/}/api/v1/repos/${REPO}"
COMMENT_API="${API_BASE}/issues/${PR_NUMBER}/comments"
echo "→ posting Sonar report comment to PR #${PR_NUMBER}"
HTTP_CODE="$(
  curl -sk -o "$REPORT_DIR/_comment-response.json" -w "%{http_code}" \
    -X POST "$COMMENT_API" \
    -H "Authorization: token ${TOKEN}" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD"
)"

COMMENT_ID=""
if [ "$HTTP_CODE" -ge 200 ] && [ "$HTTP_CODE" -lt 300 ]; then
  COMMENT_ID="$(
    python3 -c '
import json, os, sys
from pathlib import Path
d = json.loads(Path(os.environ["REPORT_DIR"], "_comment-response.json").read_text())
print(d.get("id") or "")
print("  comment id:", d.get("id"), "url:", d.get("html_url") or d.get("url"), file=sys.stderr)
'
  )"
  echo "  comment posted (id=${COMMENT_ID:-?})"
else
  echo "  (PR comment post failed HTTP $HTTP_CODE — still attaching files to the PR)"
  head -c 400 "$REPORT_DIR/_comment-response.json" 2>/dev/null || true
  echo
fi

upload_asset() {
  local target="$1" file="$2" url http out
  [ -f "$file" ] || return 0
  case "$target" in
    issue) url="${API_BASE}/issues/${PR_NUMBER}/assets" ;;
    comment)
      [ -n "$COMMENT_ID" ] || return 0
      url="${API_BASE}/issues/comments/${COMMENT_ID}/assets"
      ;;
    *) return 1 ;;
  esac
  out="$REPORT_DIR/_upload-$(basename "$file")-${target}.json"
  http="$(
    curl -sk -o "$out" -w "%{http_code}" \
      -X POST "$url" \
      -H "Authorization: token ${TOKEN}" \
      -F "attachment=@${file}"
  )"
  if [ "$http" -ge 200 ] && [ "$http" -lt 300 ]; then
    python3 -c '
import json, sys
from pathlib import Path
d = json.loads(Path(sys.argv[1]).read_text())
print("  ✓", sys.argv[2], "→", d.get("browser_download_url") or d.get("download_url") or d.get("uuid") or d.get("id"))
' "$out" "$(basename "$file")"
  else
    echo "  ✗ attach $(basename "$file") to $target failed HTTP $http"
    head -c 300 "$out" 2>/dev/null || true
    echo
  fi
}

echo "→ uploading report artifacts to PR #${PR_NUMBER}"
for f in \
  "$REPORT_DIR/sonar-report.json" \
  "$REPORT_DIR/sonar-report.md" \
  "$REPORT_DIR/sonar-report.txt" \
  "$REPORT_DIR/sonar-report.pdf" \
  "$ZIP_PATH"
do
  [ -f "$f" ] || continue
  upload_asset issue "$f"
  upload_asset comment "$f"
done
echo "→ done publishing Sonar artifacts on PR #${PR_NUMBER}"
