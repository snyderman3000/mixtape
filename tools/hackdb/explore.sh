#!/bin/sh
set +e
UA="Mozilla/5.0 mixtape-hackdb"
B="https://archive.org/download/rhdn-20210914/RHDN-20210914.zip"
enc() { python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$1"; }
for p in "hacks/GB/Pokémon_ Blue Version/Pokemon Blue - 151___1_2_1/scraped_info.txt" \
         "hacks/GB/Pokémon_ Blue Version/Pokemon Blue - 151___1_2_1/Pokemon Blue - 151 Patch.zip"; do
  echo "=== $p"; curl -sSL -A "$UA" -w "\nhttp=%{http_code} size=%{size_download} t=%{time_total}\n" -o out/sample.bin "$B/$(enc "$p")"
  case "$p" in *.txt) cat out/sample.bin;; *.zip) unzip -l out/sample.bin;; esac
done
# full central directory via range requests to measure speed & sizes
curl -sSI -A "$UA" "$B" | head -20
