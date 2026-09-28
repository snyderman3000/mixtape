#!/bin/sh
# Explore the RomHacking.net SQL export: list tables, schemas and sample rows.
set -e
mkdir -p work out
cd work
curl -sSL -o sql.zip "https://archive.org/download/romhacking.net-20240801/romhacking.sql.zip"
unzip -o -q sql.zip
ls -la > ../out/files.txt
F=$(ls *.sql | head -1)
grep -n "CREATE TABLE" "$F" > ../out/tables.txt || true
awk '/CREATE TABLE/{p=1} p{print} /;$/{if(p){print "";p=0}}' "$F" > ../out/schemas.sql
# first 3 INSERT statements per table, truncated
python3 - "$F" > ../out/samples.txt <<'PY'
import sys,re,collections
seen=collections.Counter()
with open(sys.argv[1],encoding='utf-8',errors='replace') as f:
    for line in f:
        m=re.match(r"INSERT INTO `?(\w+)`?",line)
        if m and seen[m.group(1)]<2:
            seen[m.group(1)]+=1
            print(line[:3000]); print()
PY
# a peek at the big file archive listing
curl -sSL "https://archive.org/download/romhacking.net-20240801/rhdn_20240808.zip/" | head -c 20000 > ../out/zip_index_head.html || true
