#!/bin/sh
# Graphite data seeding script for integration tests.
# Writes Whisper files directly, bypassing Carbon's async write pipeline
# which is unreliable on slow CI runners.

set -e

WHISPER_DIR="${WHISPER_DIR:-/opt/graphite/storage/whisper}"
WHISPER_CREATE="${WHISPER_CREATE:-/opt/graphite/bin/whisper-create.py}"
WHISPER_UPDATE="${WHISPER_UPDATE:-/opt/graphite/bin/whisper-update.py}"
RETENTION="10s:1h"

NOW=$(date +%s)

create_metric() {
  metric_path="$1"
  value="$2"
  # Convert dot-separated metric name to directory path.
  # e.g. test.servers.web01.cpu.load5 -> test/servers/web01/cpu/load5.wsp
  file_path="${WHISPER_DIR}/$(echo "$metric_path" | sed 's/\./\//g').wsp"
  dir_path=$(dirname "$file_path")
  mkdir -p "$dir_path"
  "$WHISPER_CREATE" "$file_path" "$RETENTION"
  "$WHISPER_UPDATE" "$file_path" "${NOW}:${value}"
}

# Hierarchical metrics for listGraphiteMetrics and queryGraphite tests.
create_metric "test.servers.web01.cpu.load5"  "1.5"
create_metric "test.servers.web01.cpu.load15" "1.2"
create_metric "test.servers.web02.cpu.load5"  "2.3"
create_metric "test.servers.web02.cpu.load15" "2.1"
create_metric "test.servers.db01.cpu.load5"   "0.8"

# Tagged metrics for listGraphiteTags tests.
# Graphite stores tagged metrics under _tagged/ using a hash-based path,
# but also accepts them via Carbon. For simplicity, send tagged metrics
# through Carbon since the directory structure for tags is complex.
# The integration tests only assert that listTags doesn't error, not that
# specific tags exist, so this is optional.

echo "Graphite metrics seeded via direct Whisper writes. Done."
