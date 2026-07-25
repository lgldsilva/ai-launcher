#!/usr/bin/env bash
# sonar-ephemeral.sh — shared Sonar Community Edition analysis + durable reports.
#
# Source of truth: gitea-workflows (scripts/sonar/). Consumed via reusable workflows.
#
# CE has no real PR/branch analysis. Pattern:
#   EPHEMERAL=1 (PR): temp projectKey → analyze → export reports → delete project
#   EPHEMERAL=0 (main): permanent projectKey → analyze → export reports
#
# Why export before delete?
#   After the temp project is removed, issues vanish from the Sonar UI.
#   JSON/MD/TXT/(optional PDF) keep findings reviewable.
#
# Required env:
#   SONAR_TOKEN
#   PROJECT_KEY
#
# Common env (defaults in brackets):
#   SONAR_HOST_URL   [https://sonar.raspberrypi.lan]
#   PROJECT_NAME     [$PROJECT_KEY]
#   EPHEMERAL        [1]
#   REPORT_DIR       [$PWD/sonar-out]
#   SONAR_SOURCES    [src/]
#   SONAR_TESTS      [tests/]
#   SONAR_TEST_INCLUSIONS   [empty]
#   SONAR_COVERAGE_EXCLUSIONS [empty]
#   COVERAGE_FILE    [empty]  — if set, must exist (or COVERAGE_GENERATE_CMD runs)
#   COVERAGE_PROPERTY [auto from extension: .xml → python path if pytest-ish else generic]
#   EXTRA_SONAR_ARGS  [empty]  — extra -D flags, space-separated string
#   SCANNER           [auto]   — sonar-scanner | npx
#   GENERATE_PDF      [0]
#   PDF_CMD           [empty]  — e.g. 'python -m md_converter "$REPORT_DIR/sonar-report.md" -f pdf -o "$REPORT_DIR/sonar-report.pdf"'
#   NODE_EXTRA_CA_CERTS
#
# Local quality floors (fail-closed; Sonar CE ephemeral often returns QG=OK with
# empty conditions even when bugs/smells/coverage are bad):
#   GATE_STRICT                    [1]  — 0 = only trust official Sonar QG (legacy, insecure on CE ephemeral)
#   GATE_FAIL_ON_EMPTY_CONDITIONS  [1]  — apply floors when QG has no conditions
#   GATE_MAX_BUGS                  [0]
#   GATE_MAX_VULNERABILITIES       [0]
#   GATE_MAX_SECURITY_HOTSPOTS     [0]
#   GATE_MAX_CODE_SMELLS           [0]
#   GATE_MIN_COVERAGE              [80] — percent; 0 disables
#   GATE_MAX_DUPLICATED_LINES_DENSITY [3] — empty string disables
#   GATE_MAX_RELIABILITY_RATING    [1]  — 1=A … 5=E
#   GATE_MAX_SECURITY_RATING       [1]
#   GATE_MAX_SQALE_RATING          [1]  — maintainability
set -euo pipefail

SONAR_HOST_URL="${SONAR_HOST_URL:-${SONAR_URL:-https://sonar.raspberrypi.lan}}"
SONAR_TOKEN="${SONAR_TOKEN:-}"
PROJECT_KEY="${PROJECT_KEY:-}"
PROJECT_NAME="${PROJECT_NAME:-${PROJECT_KEY}}"
EPHEMERAL="${EPHEMERAL:-1}"
REPORT_DIR="${REPORT_DIR:-$PWD/sonar-out}"
REPORT_FILE="${REPORT_FILE:-$REPORT_DIR/sonar-report.txt}"
SONAR_SOURCES="${SONAR_SOURCES:-src/}"
SONAR_TESTS="${SONAR_TESTS:-tests/}"
SONAR_TEST_INCLUSIONS="${SONAR_TEST_INCLUSIONS:-}"
SONAR_COVERAGE_EXCLUSIONS="${SONAR_COVERAGE_EXCLUSIONS:-}"
COVERAGE_FILE="${COVERAGE_FILE:-}"
COVERAGE_GENERATE_CMD="${COVERAGE_GENERATE_CMD:-}"
COVERAGE_PROPERTY="${COVERAGE_PROPERTY:-}"
EXTRA_SONAR_ARGS="${EXTRA_SONAR_ARGS:-}"
SCANNER="${SCANNER:-auto}"
GENERATE_PDF="${GENERATE_PDF:-0}"
PDF_CMD="${PDF_CMD:-}"
# When 1, only pass host/token/projectKey/name; rely on sonar-project.properties
# for sources/tests/exclusions (CLI -D would override those keys).
USE_PROJECT_PROPERTIES="${USE_PROJECT_PROPERTIES:-0}"
NODE_EXTRA_CA_CERTS="${NODE_EXTRA_CA_CERTS:-/usr/local/share/ca-certificates/gitea-ca.crt}"
if [ -z "$SONAR_TOKEN" ]; then
  echo "✘ SONAR_TOKEN not set"
  exit 1
fi
if [ -z "$PROJECT_KEY" ]; then
  echo "✘ PROJECT_KEY not set"
  exit 1
fi

mkdir -p "$REPORT_DIR"
export SONAR_HOST_URL SONAR_TOKEN PROJECT_KEY PROJECT_NAME EPHEMERAL REPORT_DIR REPORT_FILE

api() {
  local method="$1" path="$2"
  shift 2
  curl -sk -X "$method" -H "Authorization: Bearer ${SONAR_TOKEN}" "${SONAR_HOST_URL}${path}" "$@"
}

cleanup() {
  local rc=$?
  if [ "$EPHEMERAL" = "1" ]; then
    echo "→ deleting temporary Sonar project: $PROJECT_KEY"
    api POST "/api/projects/delete" --data-urlencode "project=${PROJECT_KEY}" >/dev/null 2>&1 \
      || echo "  (delete non-zero — may already be gone)"
  fi
  exit "$rc"
}
trap cleanup EXIT

# Coverage file optional; when requested ensure it exists.
if [ -n "$COVERAGE_FILE" ]; then
  if [ ! -s "$COVERAGE_FILE" ] && [ -n "$COVERAGE_GENERATE_CMD" ]; then
    echo "→ generating $COVERAGE_FILE"
    bash -c "$COVERAGE_GENERATE_CMD"
  fi
  if [ ! -s "$COVERAGE_FILE" ]; then
    echo "✘ COVERAGE_FILE=$COVERAGE_FILE missing or empty"
    exit 1
  fi
  if [ -z "$COVERAGE_PROPERTY" ]; then
    # Sensible defaults by stack hint
    case "$COVERAGE_FILE" in
      *.out|coverage.out) COVERAGE_PROPERTY="sonar.go.coverage.reportPaths=${COVERAGE_FILE}" ;;
      *.xml)              COVERAGE_PROPERTY="sonar.python.coverage.reportPaths=${COVERAGE_FILE}" ;;
      lcov.info|*.lcov)   COVERAGE_PROPERTY="sonar.javascript.lcov.reportPaths=${COVERAGE_FILE}" ;;
      *)                  COVERAGE_PROPERTY="sonar.coverageReportPaths=${COVERAGE_FILE}" ;;
    esac
  fi
fi

if [ -f "$NODE_EXTRA_CA_CERTS" ]; then
  export NODE_EXTRA_CA_CERTS
fi

echo "→ SonarScanner projectKey=$PROJECT_KEY ephemeral=$EPHEMERAL url=$SONAR_HOST_URL"

# Build scanner args
ARGS=(
  "-Dsonar.host.url=${SONAR_HOST_URL}"
  "-Dsonar.token=${SONAR_TOKEN}"
  "-Dsonar.projectKey=${PROJECT_KEY}"
  "-Dsonar.projectName=${PROJECT_NAME}"
  "-Dsonar.qualitygate.wait=true"
)
if [ "$USE_PROJECT_PROPERTIES" = "1" ]; then
  echo "→ USE_PROJECT_PROPERTIES=1 (sources/tests/exclusions from sonar-project.properties)"
  if [ -n "$COVERAGE_PROPERTY" ]; then
    ARGS+=("-D${COVERAGE_PROPERTY#-D}")
  fi
else
  ARGS+=(
    "-Dsonar.sources=${SONAR_SOURCES}"
    "-Dsonar.sourceEncoding=UTF-8"
  )
  if [ -n "$SONAR_TESTS" ]; then
    ARGS+=("-Dsonar.tests=${SONAR_TESTS}")
  fi
  if [ -n "$SONAR_TEST_INCLUSIONS" ]; then
    ARGS+=("-Dsonar.test.inclusions=${SONAR_TEST_INCLUSIONS}")
  fi
  if [ -n "$SONAR_COVERAGE_EXCLUSIONS" ]; then
    ARGS+=("-Dsonar.coverage.exclusions=${SONAR_COVERAGE_EXCLUSIONS}")
  fi
  if [ -n "$COVERAGE_PROPERTY" ]; then
    ARGS+=("-D${COVERAGE_PROPERTY#-D}")
  fi
fi
if [ -n "$EXTRA_SONAR_ARGS" ]; then
  # shellcheck disable=SC2206
  EXTRA=( $EXTRA_SONAR_ARGS )
  ARGS+=("${EXTRA[@]}")
fi

run_scanner() {
  case "$SCANNER" in
    sonar-scanner)
      command -v sonar-scanner >/dev/null 2>&1 || { echo "✘ sonar-scanner not found"; return 127; }
      sonar-scanner "${ARGS[@]}"
      ;;
    npx)
      command -v npx >/dev/null 2>&1 || { echo "✘ npx not found"; return 127; }
      npx -y sonarqube-scanner "${ARGS[@]}"
      ;;
    auto|*)
      if command -v sonar-scanner >/dev/null 2>&1; then
        sonar-scanner "${ARGS[@]}"
      elif command -v npx >/dev/null 2>&1; then
        npx -y sonarqube-scanner "${ARGS[@]}"
      else
        echo "✘ neither sonar-scanner nor npx available"
        return 127
      fi
      ;;
  esac
}

set +e
run_scanner 2>&1 | tee "$REPORT_DIR/sonar-scan.log"
SCAN_RC=${PIPESTATUS[0]}
set -e
export SCAN_RC

echo "→ fetching quality gate, measures, and issues (before delete)"
api GET "/api/qualitygates/project_status?projectKey=${PROJECT_KEY}" \
  >"$REPORT_DIR/_qg.json" || echo '{}' >"$REPORT_DIR/_qg.json"
api GET "/api/measures/component?component=${PROJECT_KEY}&metricKeys=bugs,vulnerabilities,code_smells,coverage,duplicated_lines_density,ncloc,security_hotspots,reliability_rating,security_rating,sqale_rating" \
  >"$REPORT_DIR/_measures.json" || echo '{}' >"$REPORT_DIR/_measures.json"

python3 - <<'PY'
import json, os, ssl, urllib.parse, urllib.request
from pathlib import Path

host = os.environ["SONAR_HOST_URL"].rstrip("/")
token = os.environ["SONAR_TOKEN"]
key = os.environ["PROJECT_KEY"]
report_dir = Path(os.environ["REPORT_DIR"])
ctx = ssl._create_unverified_context()

page, ps = 1, 100
all_issues, components, rules = [], [], []
total, error, data = 0, None, {}

while True:
    q = urllib.parse.urlencode({
        "componentKeys": key, "ps": ps, "p": page, "additionalFields": "_all",
    })
    req = urllib.request.Request(
        f"{host}/api/issues/search?{q}",
        headers={"Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=60) as resp:
            data = json.load(resp)
    except Exception as exc:  # noqa: BLE001
        error = str(exc)
        break
    batch = data.get("issues") or []
    all_issues.extend(batch)
    total = int(data.get("total") or len(all_issues))
    if data.get("components"):
        components = data["components"]
    if data.get("rules"):
        rules = data["rules"]
    if page * ps >= total or not batch:
        break
    page += 1

payload = {
    "total": total if not error else len(all_issues),
    "issues": all_issues,
    "components": components,
    "rules": rules,
}
if error:
    payload["error"] = error
(report_dir / "_issues.json").write_text(
    json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
)
print(f"→ fetched {len(all_issues)} issue(s) (total={payload['total']})")
if error:
    print(f"  (issues API warning: {error})")
PY

python3 - <<'PY'
import datetime, json, os
from pathlib import Path

report_dir = Path(os.environ["REPORT_DIR"])
report_file = Path(os.environ.get("REPORT_FILE") or (report_dir / "sonar-report.txt"))

def load(name: str) -> dict:
    path = report_dir / name
    if not path.exists() or path.stat().st_size == 0:
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001
        return {"_parse_error": str(exc), "_raw": path.read_text(encoding="utf-8")[:2000]}

def env_num(name: str, default: str):
    """Return float threshold, or None if disabled (empty env)."""
    raw = os.environ.get(name, default)
    if raw is None:
        return None
    raw = str(raw).strip()
    if raw == "":
        return None
    return float(raw)

qg, meas, issues_payload = load("_qg.json"), load("_measures.json"), load("_issues.json")
issues = issues_payload.get("issues") or []

measures = {
    m.get("metric"): m.get("value")
    for m in (meas.get("component") or {}).get("measures") or []
    if m.get("metric")
}
ps = qg.get("projectStatus") or {}
qg_status = ps.get("status") or "UNKNOWN"
conditions = ps.get("conditions") or []

# ── Local floors (fail-closed on CE ephemeral: QG OK + empty conditions is common) ──
gate_strict = os.environ.get("GATE_STRICT", "1").strip() != "0"
fail_empty = os.environ.get("GATE_FAIL_ON_EMPTY_CONDITIONS", "1").strip() != "0"
apply_floors = gate_strict and (fail_empty or bool(conditions) or qg_status in ("ERROR", "FAILED", "OK", "UNKNOWN"))
# Always apply floors when GATE_STRICT=1 (org rule). GATE_FAIL_ON_EMPTY_CONDITIONS
# kept for docs; strict mode never trusts empty-condition OK alone.
if gate_strict:
    apply_floors = True

def mfloat(key: str):
    v = measures.get(key)
    if v is None or v == "":
        return None
    try:
        return float(v)
    except (TypeError, ValueError):
        return None

failures = []
floor_cfg = {}
if apply_floors:
    max_bugs = env_num("GATE_MAX_BUGS", "0")
    max_vuln = env_num("GATE_MAX_VULNERABILITIES", "0")
    max_hot = env_num("GATE_MAX_SECURITY_HOTSPOTS", "0")
    max_smells = env_num("GATE_MAX_CODE_SMELLS", "0")
    min_cov = env_num("GATE_MIN_COVERAGE", "80")
    max_dup = env_num("GATE_MAX_DUPLICATED_LINES_DENSITY", "3")
    max_rel = env_num("GATE_MAX_RELIABILITY_RATING", "1")
    max_sec = env_num("GATE_MAX_SECURITY_RATING", "1")
    max_sqale = env_num("GATE_MAX_SQALE_RATING", "1")
    floor_cfg = {
        "GATE_MAX_BUGS": max_bugs,
        "GATE_MAX_VULNERABILITIES": max_vuln,
        "GATE_MAX_SECURITY_HOTSPOTS": max_hot,
        "GATE_MAX_CODE_SMELLS": max_smells,
        "GATE_MIN_COVERAGE": min_cov,
        "GATE_MAX_DUPLICATED_LINES_DENSITY": max_dup,
        "GATE_MAX_RELIABILITY_RATING": max_rel,
        "GATE_MAX_SECURITY_RATING": max_sec,
        "GATE_MAX_SQALE_RATING": max_sqale,
    }

    def fail_max(metric: str, thr, label: str):
        if thr is None:
            return
        actual = mfloat(metric)
        if actual is None:
            failures.append(f"{metric}: missing measure (fail-closed; {label}={thr:g})")
            return
        if actual > thr:
            failures.append(f"{metric}: {actual:g} > {label}={thr:g}")

    def fail_min(metric: str, thr, label: str):
        if thr is None or thr == 0:
            return
        actual = mfloat(metric)
        if actual is None:
            failures.append(f"{metric}: missing measure (fail-closed; {label}={thr:g})")
            return
        if actual < thr:
            failures.append(f"{metric}: {actual:g} < {label}={thr:g}")

    fail_max("bugs", max_bugs, "GATE_MAX_BUGS")
    fail_max("vulnerabilities", max_vuln, "GATE_MAX_VULNERABILITIES")
    fail_max("security_hotspots", max_hot, "GATE_MAX_SECURITY_HOTSPOTS")
    fail_max("code_smells", max_smells, "GATE_MAX_CODE_SMELLS")
    fail_min("coverage", min_cov, "GATE_MIN_COVERAGE")
    fail_max("duplicated_lines_density", max_dup, "GATE_MAX_DUPLICATED_LINES_DENSITY")
    fail_max("reliability_rating", max_rel, "GATE_MAX_RELIABILITY_RATING")
    fail_max("security_rating", max_sec, "GATE_MAX_SECURITY_RATING")
    fail_max("sqale_rating", max_sqale, "GATE_MAX_SQALE_RATING")

    if not conditions and fail_empty:
        # Informational line when Sonar QG is a no-op (common on CE ephemeral).
        floor_cfg["note_empty_qg_conditions"] = True

gate_result = "FAIL" if failures else "PASS"
local_gate = {
    "strict": gate_strict,
    "applied": apply_floors,
    "result": gate_result,
    "failures": failures,
    "thresholds": floor_cfg,
    "qg_status": qg_status,
    "qg_conditions_count": len(conditions),
}

bundle = {
    "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "project_key": os.environ.get("PROJECT_KEY"),
    "project_name": os.environ.get("PROJECT_NAME"),
    "ephemeral": os.environ.get("EPHEMERAL"),
    "sonar_host": os.environ.get("SONAR_HOST_URL"),
    "scanner_exit": int(os.environ.get("SCAN_RC") or 0),
    "quality_gate": qg,
    "local_gate": local_gate,
    "measures": meas,
    "issues_total": issues_payload.get("total", len(issues)),
    "issues": issues,
    "rules": issues_payload.get("rules") or [],
    "components": issues_payload.get("components") or [],
    "issues_fetch_error": issues_payload.get("error"),
    "schema": "ai-standards/sonar-report@2",
}
json_path = report_dir / "sonar-report.json"
json_path.write_text(json.dumps(bundle, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

sev_count, type_count = {}, {}
for issue in issues:
    sev, typ = issue.get("severity") or "?", issue.get("type") or "?"
    sev_count[sev] = sev_count.get(sev, 0) + 1
    type_count[typ] = type_count.get(typ, 0) + 1

def file_of(issue: dict) -> str:
    comp = issue.get("component") or ""
    return comp.split(":", 1)[1] if ":" in comp else comp

lines = [
    "# SonarQube report", "",
    f"- **Generated:** {bundle['generated_at']}",
    f"- **Project key:** `{bundle['project_key']}`",
    f"- **Project name:** {bundle['project_name']}",
    f"- **Ephemeral:** {bundle['ephemeral']}",
    f"- **Scanner exit:** {bundle['scanner_exit']}",
    f"- **Quality gate (Sonar):** **{qg_status}**",
    f"- **Local floors:** **{gate_result}**",
    f"- **Issues total:** {bundle['issues_total']}",
    "", "## Quality gate conditions (Sonar API)", "",
]
if conditions:
    lines += ["| Metric | Status | Actual | Threshold |", "|--------|--------|--------|-----------|"]
    for c in conditions:
        lines.append(
            f"| {c.get('metricKey')} | {c.get('status')} | {c.get('actualValue')} | {c.get('errorThreshold')} |"
        )
else:
    lines.append("_(no conditions returned — common on CE ephemeral projects; local floors apply)_")

lines += ["", "## Local quality floors", ""]
if not apply_floors:
    lines.append("_Local floors disabled (`GATE_STRICT=0`)._")
elif not failures:
    lines.append(f"**PASS** (thresholds: `{json.dumps(floor_cfg, ensure_ascii=False)}`)")
else:
    lines.append("**FAIL**")
    lines.append("")
    for f in failures:
        lines.append(f"- {f}")

lines += ["", "## Measures", ""]
if measures:
    lines += ["| Metric | Value |", "|--------|-------|"]
    for key in sorted(measures):
        lines.append(f"| {key} | {measures[key]} |")
else:
    lines.append("_(no measures returned)_")
lines += ["", "## Issue summary", "", "### By severity", ""]
if sev_count:
    for key, value in sorted(sev_count.items(), key=lambda kv: (-kv[1], kv[0])):
        lines.append(f"- **{key}:** {value}")
else:
    lines.append("_No issues._")
lines += ["", "### By type", ""]
if type_count:
    for key, value in sorted(type_count.items(), key=lambda kv: (-kv[1], kv[0])):
        lines.append(f"- **{key}:** {value}")
else:
    lines.append("_No issues._")
lines += ["", "## Issues", ""]
if not issues:
    lines.append("_No open issues reported for this analysis._")
else:
    order = {"BLOCKER": 0, "CRITICAL": 1, "MAJOR": 2, "MINOR": 3, "INFO": 4}
    for idx, issue in enumerate(
        sorted(issues, key=lambda i: (order.get(i.get("severity") or "", 9), file_of(i), i.get("line") or 0)),
        1,
    ):
        lines += [
            f"### {idx}. [{issue.get('severity') or '?'}] {issue.get('type') or '?'} — `{file_of(issue)}:{issue.get('line') or '-'}`",
            "",
            f"- **Rule:** `{issue.get('rule') or '?'}`",
            f"- **Message:** {(issue.get('message') or '').replace(chr(10), ' ')}",
        ]
        if issue.get("effort"):
            lines.append(f"- **Effort:** {issue.get('effort')}")
        lines.append("")

md_path = report_dir / "sonar-report.md"
md_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
txt = [
    "======== Sonar report (persisted) ========",
    f"projectKey: {bundle['project_key']}",
    f"quality_gate: {qg_status}",
    f"local_floors: {gate_result}",
    f"issues_total: {bundle['issues_total']}",
    f"scanner_exit: {bundle['scanner_exit']}",
    f"json: {json_path}",
    f"markdown: {md_path}",
    "",
]
for f in failures:
    txt.append(f"FLOOR_FAIL: {f}")
for issue in issues[:200]:
    txt.append(
        f"[{issue.get('severity')}] {issue.get('type')} "
        f"{file_of(issue)}:{issue.get('line') or '-'} — {issue.get('message')}"
    )
if len(issues) > 200:
    txt.append(f"... and {len(issues) - 200} more (see JSON/MD)")
txt.append("========================================")
report_file.write_text("\n".join(txt) + "\n", encoding="utf-8")
(report_dir / ".qg_status").write_text(qg_status + "\n", encoding="utf-8")
(report_dir / ".gate_result").write_text(gate_result + "\n", encoding="utf-8")
(report_dir / ".gate_failures").write_text(
    ("\n".join(failures) + "\n") if failures else "",
    encoding="utf-8",
)
print(f"→ wrote {json_path}")
print(f"→ wrote {md_path}")
print(f"→ wrote {report_file}")
print(f"quality_gate_status={qg_status}")
print(f"local_gate_result={gate_result}")
print(f"issues_total={bundle['issues_total']}")
if failures:
    print("✘ Local quality floors failed:")
    for f in failures:
        print(f"  - {f}")
PY

if [ "$GENERATE_PDF" = "1" ] && [ -f "$REPORT_DIR/sonar-report.md" ]; then
  if [ -n "$PDF_CMD" ]; then
    echo "→ generating PDF via PDF_CMD"
    set +e
    bash -c "$PDF_CMD" 2>"$REPORT_DIR/sonar-pdf.log"
    PDF_RC=$?
    set -e
    if [ "$PDF_RC" -eq 0 ] && [ -f "$REPORT_DIR/sonar-report.pdf" ]; then
      echo "→ wrote $REPORT_DIR/sonar-report.pdf"
    else
      echo "  (PDF skipped — see $REPORT_DIR/sonar-pdf.log)"
    fi
  else
    echo "  (GENERATE_PDF=1 but PDF_CMD empty — skip)"
  fi
fi

echo "→ report files in $REPORT_DIR:"
ls -la "$REPORT_DIR" || true
if [ -f "$REPORT_DIR/sonar-report.md" ]; then
  echo ""
  echo "======== sonar-report.md (full) ========"
  cat "$REPORT_DIR/sonar-report.md"
  echo "======== end sonar-report.md ========"
fi

QG_STATUS="$(cat "$REPORT_DIR/.qg_status" 2>/dev/null || echo UNKNOWN)"
GATE_RESULT="$(cat "$REPORT_DIR/.gate_result" 2>/dev/null || echo PASS)"
echo "quality_gate_status=$QG_STATUS"
echo "local_gate_result=$GATE_RESULT"

FAIL=0
if [ "$QG_STATUS" = "ERROR" ] || [ "$QG_STATUS" = "FAILED" ]; then
  echo "✘ Sonar quality gate FAILED (status=$QG_STATUS)"
  FAIL=1
fi
if [ "${SCAN_RC}" -ne 0 ]; then
  echo "✘ Scanner exit code ${SCAN_RC} (reports kept under $REPORT_DIR)"
  FAIL=1
fi
if [ "$GATE_RESULT" = "FAIL" ]; then
  echo "✘ Local quality floors FAILED (bugs/vulns/smells/coverage/ratings — see report)"
  if [ -s "$REPORT_DIR/.gate_failures" ]; then
    sed 's/^/  - /' "$REPORT_DIR/.gate_failures"
  fi
  FAIL=1
fi
if [ "$FAIL" -ne 0 ]; then
  echo "✘ Gate blocked — reports persisted under $REPORT_DIR before project delete"
  exit 1
fi
echo "✓ Quality gates passed (Sonar=$QG_STATUS, floors=$GATE_RESULT); reports under $REPORT_DIR"
exit 0
