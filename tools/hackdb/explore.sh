#!/bin/sh
# Which archive.org copies of RHDN / patch archives are publicly downloadable?
set +e
UA="Mozilla/5.0 mixtape-hackdb"
for item in romhackingbackup08102024 rhdn-20210914 rom-hack-patch-archive; do
  echo "===== $item"
  curl -sSL -A "$UA" "https://archive.org/metadata/$item/files" | python3 -c "
import json,sys
d=json.load(sys.stdin).get('result',[])
for f in d[:80]: print(f.get('name'), f.get('size'), 'PRIVATE' if f.get('private') else '')
print('files:',len(d))"
done
echo "===== inner file tests"
t() { echo "--- $1"; curl -sSL -A "$UA" -r 0-400 -w "\nhttp=%{http_code} type=%{content_type} size=%{size_download}\n" "$1" | tr -c "[:print:]\n" "." | head -c 900; echo; }
t "https://archive.org/download/romhackingbackup08102024/www.romhacking.net.zip/www.romhacking.net/hacks/1/index.html"
t "https://archive.org/download/romhackingbackup08102024/www.romhacking.net.zip/www.romhacking.net/hacks/1/"
t "https://archive.org/download/romhackingbackup08102024/www.romhacking.net.zip/"
